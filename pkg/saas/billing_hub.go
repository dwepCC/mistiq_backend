package saas

import (
	"fmt"
	"strings"
	"time"

	"tukifac/pkg/database"
)

// BillingHub respuesta única para /subscription (tenant).
type BillingHub struct {
	Subscription     TenantSubscriptionView  `json:"subscription"`
	BillingContext   BillingContextView      `json:"billing_context"`
	PaymentConfig    PaymentConfigView       `json:"payment_config"`
	Support          SupportConfig           `json:"support"`
	StatusBanner     StatusBannerView        `json:"status_banner"`
	Documents        DocumentUsageHubView    `json:"documents"`
	DocumentPackages []CatalogPackageHubView `json:"document_packages,omitempty"`
	Invoices         []InvoiceView           `json:"invoices"`
	Payments         []PaymentView           `json:"payments"`
	Events           []TimelineEventView     `json:"events"`
}

type StatusBannerView struct {
	Variant string `json:"variant"` // info, warning, danger, success
	Message string `json:"message"`
}

type InvoiceView struct {
	ID              uint    `json:"id"`
	Amount          float64 `json:"amount"`
	ReconnectionFee float64 `json:"reconnection_fee"`
	Currency        string  `json:"currency"`
	Status          string  `json:"status"`
	DueDate         string  `json:"due_date"`
	PeriodStart     string  `json:"period_start"`
	PeriodEnd       string  `json:"period_end"`
	ProvisionalUsed bool    `json:"provisional_used"`
	// FiscalDocURL boleta/factura del pago que saldó este período (ver saas_billing_cycles.
	// payment_id); vacío si el ciclo no está pagado o el admin todavía no la adjuntó. Vive acá
	// —no en el historial de pagos— porque conceptualmente es el comprobante DEL PERÍODO, y un
	// período puede haber tenido más de un intento de pago (rechazados, anulados) antes del que
	// finalmente lo saldó.
	FiscalDocURL string `json:"fiscal_doc_url,omitempty"`
	// ReceiptURL comprobante que el TENANT subió (voucher/captura de yape, transferencia, etc.)
	// del pago más reciente ligado a este ciclo — sea cual sea su estado (pending_review,
	// approved, rejected). Distinto de FiscalDocURL: ese es la boleta/factura que el admin le
	// emite AL tenant; este es lo que el tenant mismo adjuntó como prueba de pago.
	ReceiptURL string `json:"receipt_url,omitempty"`
}

type PaymentView struct {
	ID            uint    `json:"id"`
	Amount        float64 `json:"amount"`
	PaymentMethod string  `json:"payment_method"`
	Status        string  `json:"status"`
	Reference     string  `json:"reference"`
	PaymentDate   string  `json:"payment_date,omitempty"`
	RejectReason  string  `json:"reject_reason,omitempty"`
	CreatedAt     string  `json:"created_at"`
	// FiscalDocURL boleta/factura que le emitieron por este pago; vacío mientras no se
	// haya adjuntado desde el panel central.
	FiscalDocURL string `json:"fiscal_doc_url,omitempty"`
}

// DocumentUsageHubView resumen documentos (rellenado desde docusage en handler o AttachDocumentsToHub).
type DocumentUsageHubView struct {
	IsUnlimited      bool   `json:"is_unlimited"`
	PlanLimit        int    `json:"plan_limit"`
	PlanUsed         int    `json:"plan_used"`
	PlanRemaining    int    `json:"plan_remaining"`
	PackageBonus     int    `json:"package_bonus"`
	PackageUsed      int    `json:"package_used"`
	PackageRemaining int    `json:"package_remaining"`
	TotalAvailable   int    `json:"total_available"`
	TotalConsumed    int    `json:"total_consumed"`
	UsagePercent     int    `json:"usage_percent"`
	WarningLevel     string `json:"warning_level"`
	WarningMessage   string `json:"warning_message,omitempty"`
	CanEmit          bool   `json:"can_emit"`
	// Fin de la suscripción pagada: hasta esta fecha valen los paquetes comprados.
	BillingCycleEnd string `json:"billing_cycle_end,omitempty"`
	// Fecha en que el cupo mensual del plan vuelve a cero. No coincide con
	// BillingCycleEnd cuando el cliente paga varios meses por adelantado.
	QuotaPeriodEnd   string `json:"quota_period_end,omitempty"`
	QuotaPeriodIndex int    `json:"quota_period_index,omitempty"`
	QuotaPeriodTotal int    `json:"quota_period_total,omitempty"`
}

type CatalogPackageHubView struct {
	ID           uint    `json:"id"`
	Name         string  `json:"name"`
	Description  string  `json:"description"`
	DocumentsQty int     `json:"documents_qty"`
	Price        float64 `json:"price"`
	Currency     string  `json:"currency"`
}

type TimelineEventView struct {
	ID        uint   `json:"id"`
	EventType string `json:"event_type"`
	Label     string `json:"label"`
	Reason    string `json:"reason"`
	CreatedAt string `json:"created_at"`
}

// ResolveNextBillingDate decide qué fecha se muestra como «próximo pago».
//
// Por defecto es el vencimiento del período vigente. El due_date de la factura pendiente
// solo lo reemplaza si esa factura es de la suscripción actual y todavía no venció: una
// factura atrasada —o heredada de una suscripción anterior— es deuda por cobrar, no la
// fecha del siguiente cobro. Antes se usaba siempre, y por eso el panel del tenant podía
// mostrar un «próximo pago» anterior al inicio del período.
func ResolveNextBillingDate(
	endDate string,
	pending *database.SaasBillingCycle,
	currentSubscriptionID uint,
	now time.Time,
) string {
	if pending == nil || currentSubscriptionID == 0 {
		return endDate
	}
	if pending.SubscriptionID != currentSubscriptionID {
		return endDate
	}
	if CalendarDateLima(pending.DueDate).Before(CalendarDateLima(now)) {
		return endDate
	}
	return pending.DueDate.In(lima()).Format(timeRFC3339Lima)
}

// GetBillingHub agrega todo el contexto de cobro para el tenant.
func GetBillingHub(tenantID uint) (BillingHub, error) {
	cfg, _ := LoadSettings()
	sub, err := GetTenantView(tenantID)
	if err != nil {
		return BillingHub{}, err
	}
	sub.PortalURL = strings.TrimSpace(cfg.PortalURLOverride)
	var pendingInvoice *database.SaasBillingCycle
	if sub.PendingInvoiceID > 0 {
		var inv database.SaasBillingCycle
		if database.CentralDB.First(&inv, sub.PendingInvoiceID).Error == nil {
			pendingInvoice = &inv
		}
	}
	sub.NextBillingDate = ResolveNextBillingDate(sub.EndDate, pendingInvoice, sub.SubscriptionID, NowLima())

	ux := BuildBillingContext(sub, cfg, tenantID)
	banner := StatusBannerView{Variant: "success", Message: "Tu suscripción está activa."}
	if ux.ShowStatusBanner {
		banner = StatusBannerView{Variant: ux.StatusBannerVariant, Message: ux.StatusBannerMessage}
	}

	hub := BillingHub{
		Subscription:   sub,
		BillingContext: ux,
		PaymentConfig:  TenantPaymentConfig(cfg),
		Support:        cfg.Support,
		StatusBanner:   banner,
		Invoices:       ListInvoicesView(tenantID),
		Payments:       ListPaymentsView(tenantID),
		Events:         ListTimelineEvents(tenantID),
	}
	return hub, nil
}

// PendingCycleForTenant cobro impago más próximo del tenant (nil si no hay).
func PendingCycleForTenant(tenantID uint) *database.SaasBillingCycle {
	var cycle database.SaasBillingCycle
	if database.CentralDB.Where("tenant_id = ? AND status IN ?", tenantID,
		[]string{database.SaasInvoicePending, database.SaasInvoiceOverdue}).
		Order("due_date asc").First(&cycle).Error != nil {
		return nil
	}
	return &cycle
}

// ListInvoicesView facturas/ciclos del tenant.
func ListInvoicesView(tenantID uint) []InvoiceView {
	var cycles []database.SaasBillingCycle
	database.CentralDB.Where("tenant_id = ?", tenantID).Order("due_date desc").Limit(24).Find(&cycles)

	// Boleta/factura de cada ciclo pagado: sale del pago que lo saldó (cycle.PaymentID), no de
	// "el último pago del tenant" — un período puede haber tenido intentos rechazados/anulados
	// antes del que finalmente lo pagó. Una sola consulta para todos los ciclos de esta página.
	paymentIDs := make([]uint, 0, len(cycles))
	for _, c := range cycles {
		if c.PaymentID != nil {
			paymentIDs = append(paymentIDs, *c.PaymentID)
		}
	}
	fiscalDocByPayment := map[uint]string{}
	if len(paymentIDs) > 0 {
		var payments []database.SaasPayment
		database.CentralDB.Where("id IN ?", paymentIDs).Find(&payments)
		for _, p := range payments {
			if p.FiscalDocURL != "" {
				fiscalDocByPayment[p.ID] = p.FiscalDocURL
			}
		}
	}

	// Comprobante que el TENANT subió, por ciclo: el del intento MÁS RECIENTE, sea cual sea su
	// estado (en revisión, aprobado, rechazado) — a diferencia del bloque de arriba (boleta/
	// factura, que solo existe para el que finalmente pagó). Una sola consulta para todos los
	// ciclos de esta página, quedándose con el primero (más reciente) por billing_cycle_id.
	cycleIDs := make([]uint, 0, len(cycles))
	for _, c := range cycles {
		cycleIDs = append(cycleIDs, c.ID)
	}
	receiptByCycle := map[uint]string{}
	if len(cycleIDs) > 0 {
		var payments []database.SaasPayment
		database.CentralDB.Where("billing_cycle_id IN ?", cycleIDs).
			Order("created_at desc").Find(&payments)
		for _, p := range payments {
			if p.BillingCycleID == nil || p.ReceiptURL == "" {
				continue
			}
			if _, seen := receiptByCycle[*p.BillingCycleID]; !seen {
				receiptByCycle[*p.BillingCycleID] = p.ReceiptURL
			}
		}
	}

	out := make([]InvoiceView, 0, len(cycles))
	for _, c := range cycles {
		v := InvoiceView{
			ID: c.ID, Amount: c.Amount, ReconnectionFee: c.ReconnectionFee, Currency: c.Currency,
			Status: c.Status, ProvisionalUsed: c.ProvisionalUsed,
			DueDate:     c.DueDate.In(lima()).Format(timeRFC3339Lima),
			PeriodStart: c.PeriodStart.In(lima()).Format(timeRFC3339Lima),
			PeriodEnd:   c.PeriodEnd.In(lima()).Format(timeRFC3339Lima),
			ReceiptURL:  receiptByCycle[c.ID],
		}
		if c.PaymentID != nil {
			v.FiscalDocURL = fiscalDocByPayment[*c.PaymentID]
		}
		out = append(out, v)
	}
	return out
}

// ListPaymentsView historial de pagos del tenant.
func ListPaymentsView(tenantID uint) []PaymentView {
	var rows []database.SaasPayment
	database.CentralDB.Where("tenant_id = ?", tenantID).Order("created_at desc").Limit(50).Find(&rows)
	out := make([]PaymentView, 0, len(rows))
	for _, p := range rows {
		v := PaymentView{
			ID: p.ID, Amount: p.Amount, PaymentMethod: p.PaymentMethod, Status: p.Status,
			Reference: p.Reference, CreatedAt: p.CreatedAt.In(lima()).Format(timeRFC3339Lima),
			FiscalDocURL: p.FiscalDocURL,
		}
		if p.PaymentDate != nil {
			v.PaymentDate = p.PaymentDate.In(lima()).Format("2006-01-02")
		}
		if p.Status == database.SaasPayRejected {
			v.RejectReason = p.AdminNotes
		}
		out = append(out, v)
	}
	return out
}

// ListTimelineEvents auditoría visible para el tenant.
func ListTimelineEvents(tenantID uint) []TimelineEventView {
	var rows []database.SaasSubscriptionEvent
	database.CentralDB.Where("tenant_id = ?", tenantID).Order("created_at desc").Limit(40).Find(&rows)
	out := make([]TimelineEventView, 0, len(rows))
	for _, e := range rows {
		out = append(out, TimelineEventView{
			ID: e.ID, EventType: e.EventType, Label: EventLabel(e.EventType),
			Reason: e.Reason, CreatedAt: e.CreatedAt.In(lima()).Format(timeRFC3339Lima),
		})
	}
	return out
}

// EventLabel etiqueta humana para timeline.
func EventLabel(t string) string {
	switch t {
	case EventProvisionalGranted:
		return "Acceso provisional concedido"
	case EventPaymentRejected:
		return "Pago rechazado"
	case EventTenantBlocked:
		return "Cuenta bloqueada"
	case EventTenantUnblocked:
		return "Cuenta desbloqueada"
	case EventPaymentApproved:
		return "Pago aprobado"
	case EventSuspended:
		return "Servicio suspendido"
	case EventReactivated:
		return "Servicio reactivado"
	case EventInvoiceIssued:
		return "Cobro emitido"
	case EventValidityAdjusted:
		return "Vigencia ajustada"
	case EventInvoiceSuperseded:
		return "Cobro reemplazado"
	case EventPaymentReversed:
		return "Pago anulado"
	default:
		return t
	}
}

// BuildStatusBanner mensaje principal para UI.
func BuildStatusBanner(sub TenantSubscriptionView, cfg PlatformSettings) StatusBannerView {
	if sub.IsBlocked {
		return StatusBannerView{Variant: "danger", Message: sub.SupportMessage}
	}
	if sub.HasPendingPaymentReview {
		return StatusBannerView{Variant: "info", Message: "Tienes un pago en validación. Te avisaremos cuando sea aprobado."}
	}
	if sub.Status == database.SaasSubProvisionalActive && sub.ProvisionalHoursLeft > 0 {
		return StatusBannerView{
			Variant: "success",
			Message: fmt.Sprintf("Acceso provisional activo (%d h restantes aprox.)", sub.ProvisionalHoursLeft),
		}
	}
	if sub.IsSuspended || !sub.CanOperate {
		return StatusBannerView{
			Variant: "warning",
			Message: fmt.Sprintf("Tu cuenta está suspendida. Monto pendiente: S/ %.2f", sub.PendingAmount),
		}
	}
	if sub.InGracePeriod {
		return StatusBannerView{Variant: "warning", Message: "Periodo de gracia: realiza tu pago para evitar la suspensión."}
	}
	if sub.ShowRenewalBanner && sub.DaysUntilExpiry > 0 {
		return StatusBannerView{
			Variant: "info",
			Message: fmt.Sprintf("Tu plan vence en %d día(s)", sub.DaysUntilExpiry),
		}
	}
	if sub.IsOverdue {
		return StatusBannerView{Variant: "warning", Message: "Tu suscripción está vencida. Regulariza tu pago."}
	}
	_ = cfg
	return StatusBannerView{Variant: "success", Message: "Tu suscripción está activa."}
}
