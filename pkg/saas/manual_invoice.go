package saas

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"tukifac/pkg/database"
	"tukifac/pkg/saas/docusage"
)

// RenewalInvoiceInput cobro de renovación emitido por el superadmin.
//
// No lleva fechas ni plan: un cobro SIEMPRE corresponde a la suscripción vigente del
// tenant y a su plan. El período se deriva del vencimiento actual, de modo que el cliente
// pague el día que se le vence y el cobro ya indique hasta cuándo quedará cubierto.
type RenewalInvoiceInput struct {
	TenantID uint
	// Months duración a cobrar. 0 = los meses del ciclo del plan.
	Months int
	// Amount importe a cobrar. 0 = precio del plan por los meses cobrados.
	Amount  float64
	Notes   string
	ActorID *uint
}

// RenewalPreview lo que se cobrará, para confirmarlo antes de emitir.
type RenewalPreview struct {
	TenantID   uint   `json:"tenant_id"`
	PlanID     uint   `json:"plan_id"`
	PlanName   string `json:"plan_name"`
	CurrentEnd string `json:"current_end"` // vence hoy la suscripción
	// CoveredUntil hasta dónde llega lo ya cobrado (suscripción + cobros por adelantado).
	// Si va por delante de CurrentEnd, ya hay renovaciones emitidas sin pagar.
	CoveredUntil string  `json:"covered_until"`
	PeriodStart  string  `json:"period_start"` // arranca el período cobrado
	PeriodEnd    string  `json:"period_end"`   // nuevo vencimiento si paga
	DueDate      string  `json:"due_date"`     // fecha límite de pago
	Months       int     `json:"months"`
	Amount       float64 `json:"amount"` // lo que se cobra: bruto menos descuento
	// Desglose, para que el panel muestre de dónde sale el importe.
	GrossAmount   float64 `json:"gross_amount"`
	DiscountType  string  `json:"discount_type,omitempty"`
	DiscountValue float64 `json:"discount_value,omitempty"`
	Currency      string  `json:"currency"`
	AlreadyIssued bool    `json:"already_issued"` // ya existe un cobro para ese período
}

var (
	// ErrNoActiveSubscription el tenant no tiene suscripción que renovar.
	ErrNoActiveSubscription = errors.New("el tenant no tiene una suscripción vigente; cree primero la suscripción")
	// ErrRenewalAlreadyIssued ya hay un cobro emitido para el próximo período.
	ErrRenewalAlreadyIssued = errors.New("ya existe un cobro pendiente para el próximo período de este tenant")
)

const maxRenewalMonths = 120

// resolveRenewal calcula el período y el importe del próximo cobro del tenant.
func resolveRenewal(in RenewalInvoiceInput) (*database.SaasSubscription, *database.SaasPlan, RenewalPreview, error) {
	var preview RenewalPreview
	if in.TenantID == 0 {
		return nil, nil, preview, errors.New("tenant es requerido")
	}
	if in.Months < 0 || in.Months > maxRenewalMonths {
		return nil, nil, preview, fmt.Errorf("los meses deben estar entre 1 y %d", maxRenewalMonths)
	}
	if in.Amount < 0 {
		return nil, nil, preview, errors.New("el monto no puede ser negativo")
	}

	var sub database.SaasSubscription
	err := database.CentralDB.Where("tenant_id = ?", in.TenantID).
		Where("status NOT IN ?", []string{database.SaasSubCancelled}).
		Order("created_at desc").First(&sub).Error
	if err != nil {
		return nil, nil, preview, ErrNoActiveSubscription
	}

	var plan database.SaasPlan
	if err := database.CentralDB.First(&plan, sub.PlanID).Error; err != nil {
		return nil, nil, preview, errors.New("el plan de la suscripción no existe en el catálogo")
	}

	months := in.Months
	if months == 0 {
		months = CycleMonthsFromBilling(sub.BillingCycle)
	}

	// Base = hasta dónde está cubierto HOY, contando lo ya cobrado por adelantado.
	//
	// No basta con sub.EndDate: emitir un cobro no mueve la vigencia, así que dos cobros
	// seguidos arrancarían en la misma fecha y con el mismo vencimiento. Tomando también el
	// último período ya emitido, las renovaciones quedan encadenadas: 30 set → 30 oct,
	// 30 oct → 30 nov, y así.
	currentEnd := CalendarDateLima(sub.EndDate)
	base := currentEnd
	var lastCovered database.SaasBillingCycle
	if err := database.CentralDB.
		Where("subscription_id = ? AND status NOT IN ?", sub.ID, []string{database.SaasInvoiceRejected}).
		Order("period_end desc").First(&lastCovered).Error; err == nil {
		if d := CalendarDateLima(lastCovered.PeriodEnd); d.After(base) {
			base = d
		}
	}
	periodEnd := base.AddDate(0, months, 0)

	// Sin monto explícito se calcula igual que los cobros automáticos: precio mensual × meses
	// menos el descuento pactado en la suscripción. Antes solo escalaba por meses, así que un
	// cliente con descuento recibía la renovación manual al precio de lista.
	amounts := ComputeCycleAmounts(plan.Price, months,
		Discount{Type: sub.DiscountType, Value: sub.DiscountValue})
	amount := in.Amount
	if amount <= 0 {
		amount = amounts.Net
	}
	// Un neto en cero por un descuento del 100% es una cortesía válida; el error queda para
	// cuando el plan no tiene precio y tampoco se indicó un monto a mano.
	if amount <= 0 && amounts.Gross <= 0 {
		return nil, nil, preview, errors.New("el monto debe ser mayor a cero; el plan no tiene precio configurado")
	}

	// El plan no maneja moneda: todo el SaaS cobra en soles.
	const currency = "PEN"

	preview = RenewalPreview{
		TenantID: in.TenantID,
		PlanID:   plan.ID,
		PlanName: plan.Name,
		// Vencimiento real de la suscripción, para contrastarlo con lo ya cobrado.
		CurrentEnd:   currentEnd.Format("2006-01-02"),
		CoveredUntil: base.Format("2006-01-02"),
		// Cada cobro se vence al inicio del período que cubre: el cliente paga por adelantado.
		PeriodStart:   base.Format("2006-01-02"),
		PeriodEnd:     periodEnd.Format("2006-01-02"),
		DueDate:       base.Format("2006-01-02"),
		Months:        months,
		Amount:        amount,
		GrossAmount:   amounts.Gross,
		DiscountType:  amounts.Discount.Type,
		DiscountValue: amounts.Discount.Value,
		Currency:      currency,
	}

	var dup int64
	database.CentralDB.Model(&database.SaasBillingCycle{}).
		Where("subscription_id = ? AND period_end = ?", sub.ID, EndOfDayLima(periodEnd)).
		Where("status NOT IN ?", []string{database.SaasInvoiceRejected}).
		Count(&dup)
	preview.AlreadyIssued = dup > 0

	return &sub, &plan, preview, nil
}

// PreviewRenewalInvoice muestra qué se cobraría, sin escribir nada.
func PreviewRenewalInvoice(in RenewalInvoiceInput) (RenewalPreview, error) {
	_, _, preview, err := resolveRenewal(in)
	return preview, err
}

// IssueRenewalInvoice emite el cobro de la próxima renovación del tenant.
//
// No altera la vigencia: la suscripción se extiende recién cuando el pago se aprueba.
func IssueRenewalInvoice(in RenewalInvoiceInput) (*database.SaasBillingCycle, error) {
	sub, _, preview, err := resolveRenewal(in)
	if err != nil {
		return nil, err
	}
	if preview.AlreadyIssued {
		return nil, ErrRenewalAlreadyIssued
	}

	day := func(s string) time.Time {
		t, _ := time.ParseInLocation("2006-01-02", s, LimaLocation())
		return EndOfDayLima(t)
	}

	cycle := &database.SaasBillingCycle{
		TenantID:       in.TenantID,
		SubscriptionID: sub.ID,
		PlanID:         sub.PlanID,
		PeriodStart:    day(preview.PeriodStart),
		PeriodEnd:      day(preview.PeriodEnd),
		DueDate:        day(preview.DueDate),
		Amount:         preview.Amount,
		GrossAmount:    preview.GrossAmount,
		MonthsCovered:  preview.Months,
		DiscountType:   preview.DiscountType,
		DiscountValue:  preview.DiscountValue,
		// Sin recargo de reconexión: es una renovación al día, no una mora.
		ReconnectionFee: 0,
		Currency:        preview.Currency,
		Status:          database.SaasInvoicePending,
	}
	if err := database.CentralDB.Create(cycle).Error; err != nil {
		if isDuplicateBillingCycleErr(err) {
			return nil, ErrRenewalAlreadyIssued
		}
		return nil, err
	}

	// Cuota de documentos heredada del plan, igual que en los ciclos automáticos.
	_ = docusage.SyncCycleDocumentQuotaFromPlan(cycle, sub.PlanID)
	_ = database.CentralDB.First(cycle, cycle.ID).Error

	subID := sub.ID
	LogEvent(in.TenantID, &subID, EventInvoiceIssued, "admin", in.ActorID, in.Notes, "")
	return cycle, nil
}

// InvoiceRow vista plana de un cobro para el panel central (fechas como día calendario).
type InvoiceRow struct {
	ID          uint
	TenantID    uint
	PeriodStart string
	PeriodEnd   string
	DueDate     string
	Amount      float64
	Currency    string
	Status      string
	PaidAt      string
}

// ToInvoiceRow normaliza fechas a AAAA-MM-DD en hora de Lima.
func ToInvoiceRow(c *database.SaasBillingCycle) InvoiceRow {
	if c == nil {
		return InvoiceRow{}
	}
	day := func(t time.Time) string {
		if t.IsZero() {
			return ""
		}
		return t.In(lima()).Format("2006-01-02")
	}
	row := InvoiceRow{
		ID: c.ID, TenantID: c.TenantID,
		PeriodStart: day(c.PeriodStart), PeriodEnd: day(c.PeriodEnd), DueDate: day(c.DueDate),
		Amount: c.Amount, Currency: c.Currency, Status: c.Status,
	}
	if c.PaidAt != nil {
		row.PaidAt = day(*c.PaidAt)
	}
	return row
}

// InvoiceListRow cobro con el nombre de la empresa, para el listado global del panel.
type InvoiceListRow struct {
	InvoiceRow
	TenantName string
	// CoversActivePeriod el período ya empezó y todavía no termina: anular este cobro deja al
	// cliente con el servicio corriendo y sin deuda. La UI lo advierte antes de confirmar.
	CoversActivePeriod bool
}

// ListInvoices cobros de todas las empresas, del vencimiento más próximo al más lejano.
//
// status vacío = solo los que siguen por cobrar (pendientes y vencidos), que es lo que el
// administrador necesita revisar; "all" trae todo el historial.
func ListInvoices(status string, limit int) ([]InvoiceListRow, error) {
	if limit <= 0 {
		limit = 100
	}
	q := database.CentralDB.Model(&database.SaasBillingCycle{})
	switch strings.TrimSpace(status) {
	case "", "open":
		q = q.Where("status IN ?", []string{database.SaasInvoicePending, database.SaasInvoiceOverdue})
	case "all":
		// sin filtro
	default:
		q = q.Where("status = ?", status)
	}

	var rows []database.SaasBillingCycle
	if err := q.Order("due_date asc").Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}

	// Nombres en una sola consulta: el listado puede traer muchas empresas distintas.
	ids := make([]uint, 0, len(rows))
	for i := range rows {
		ids = append(ids, rows[i].TenantID)
	}
	names := make(map[uint]string, len(ids))
	if len(ids) > 0 {
		var tenants []database.Tenant
		database.CentralDB.Select("id", "name").Where("id IN ?", ids).Find(&tenants)
		for _, t := range tenants {
			names[t.ID] = t.Name
		}
	}

	now := CalendarDateLima(NowLima())
	out := make([]InvoiceListRow, 0, len(rows))
	for i := range rows {
		c := &rows[i]
		out = append(out, InvoiceListRow{
			InvoiceRow: ToInvoiceRow(c),
			TenantName: names[c.TenantID],
			CoversActivePeriod: !CalendarDateLima(c.PeriodEnd).Before(now) &&
				!CalendarDateLima(c.PeriodStart).After(now),
		})
	}
	return out, nil
}

// ListTenantInvoices cobros del tenant, del más reciente al más antiguo.
func ListTenantInvoices(tenantID uint, limit int) ([]database.SaasBillingCycle, error) {
	if limit <= 0 {
		limit = 50
	}
	var rows []database.SaasBillingCycle
	err := database.CentralDB.Where("tenant_id = ?", tenantID).
		Order("due_date desc").Limit(limit).Find(&rows).Error
	return rows, err
}

// CancelInvoice anula un cobro pendiente. Un cobro ya pagado no se toca.
func CancelInvoice(cycleID uint) error {
	var cycle database.SaasBillingCycle
	if err := database.CentralDB.First(&cycle, cycleID).Error; err != nil {
		return errors.New("cobro no encontrado")
	}
	if cycle.Status == database.SaasInvoicePaid {
		return errors.New("el cobro ya fue pagado y no puede anularse")
	}
	if cycle.Status == database.SaasInvoiceRejected {
		return errors.New("el cobro ya está anulado")
	}

	// Un pago vivo apunta a este cobro: anularlo dejaría el comprobante del cliente sin
	// deuda a la que aplicarse. Primero hay que resolver el pago (aprobarlo o rechazarlo).
	var attached int64
	database.CentralDB.Model(&database.SaasPayment{}).
		Where("billing_cycle_id = ? AND status IN ?", cycleID,
			[]string{database.SaasPayPending, database.SaasPayPendingReview, database.SaasPayApproved}).
		Count(&attached)
	if attached > 0 {
		return errors.New("este cobro tiene un pago asociado; revísalo o recházalo antes de anular el cobro")
	}

	return database.CentralDB.Model(&cycle).Update("status", database.SaasInvoiceRejected).Error
}

// InvoiceCancelWarning advertencia previa a anular (no impide hacerlo, lo explica).
type InvoiceCancelWarning struct {
	// CoversActivePeriod el período que cubre este cobro está en curso: el cliente lo está
	// usando. Anularlo le deja el servicio hasta esa fecha sin deuda registrada.
	CoversActivePeriod bool   `json:"covers_active_period"`
	CoveredUntil       string `json:"covered_until,omitempty"`
	// IsOnlyOpenInvoice es el único cobro por cobrar del tenant; al anularlo deja de figurar
	// en cobranza.
	IsOnlyOpenInvoice bool `json:"is_only_open_invoice"`
}

// InvoiceCancelInfo describe qué implica anular un cobro concreto.
func InvoiceCancelInfo(cycle *database.SaasBillingCycle) InvoiceCancelWarning {
	var out InvoiceCancelWarning
	if cycle == nil {
		return out
	}
	now := CalendarDateLima(NowLima())
	if !CalendarDateLima(cycle.PeriodEnd).Before(now) && !CalendarDateLima(cycle.PeriodStart).After(now) {
		out.CoversActivePeriod = true
		out.CoveredUntil = CalendarDateLima(cycle.PeriodEnd).Format("2006-01-02")
	}
	var open int64
	database.CentralDB.Model(&database.SaasBillingCycle{}).
		Where("tenant_id = ? AND status IN ?", cycle.TenantID,
			[]string{database.SaasInvoicePending, database.SaasInvoiceOverdue}).
		Count(&open)
	out.IsOnlyOpenInvoice = open <= 1
	return out
}
