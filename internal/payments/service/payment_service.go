package service

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"tukifac/pkg/database"
	"tukifac/pkg/pagination"
	"tukifac/pkg/saas"
)

type PaymentService struct{}

func NewPaymentService() *PaymentService { return &PaymentService{} }

type PaymentDetail struct {
	database.SaasPayment
	TenantName string `json:"tenant_name"`
	TenantSlug string `json:"tenant_slug"`
	TenantRUC  string `json:"tenant_ruc"`
}

// PaymentListParams filtros de /superadmin/payments — mismo patrón que
// subscriptions.SubscriptionListParams: status exacto, búsqueda por empresa/RUC (join a
// tenants), rango de fechas sobre created_at (cuándo se registró/envió el pago) y paginación.
type PaymentListParams struct {
	Status   string
	Query    string
	DateFrom string // AAAA-MM-DD, inclusive
	DateTo   string // AAAA-MM-DD, inclusive
	Page     int
	PerPage  int
}

func (s *PaymentService) List(params PaymentListParams) ([]PaymentDetail, int64, error) {
	page, perPage := pagination.Normalize(params.Page, params.PerPage)
	q := database.CentralDB.Model(&database.SaasPayment{})
	// Columna calificada: al unirse con tenants (búsqueda por empresa/RUC) "status" queda
	// ambiguo, porque tenants también tiene su propia columna status.
	if params.Status == "pending" {
		q = q.Where("saas_payments.status IN ?", []string{database.SaasPayPending, database.SaasPayPendingReview})
	} else if params.Status != "" {
		q = q.Where("saas_payments.status = ?", params.Status)
	}
	if strings.TrimSpace(params.Query) != "" {
		like := "%" + strings.TrimSpace(params.Query) + "%"
		q = q.Joins("JOIN tenants ON tenants.id = saas_payments.tenant_id").
			Where("tenants.name LIKE ? OR tenants.ruc LIKE ? OR tenants.slug LIKE ?", like, like, like)
	}
	if from, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(params.DateFrom), saas.LimaLocation()); err == nil {
		q = q.Where("saas_payments.created_at >= ?", from)
	}
	if to, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(params.DateTo), saas.LimaLocation()); err == nil {
		q = q.Where("saas_payments.created_at <= ?", saas.EndOfDayLima(to))
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var payments []database.SaasPayment
	if err := q.Order("saas_payments.created_at desc").
		Limit(perPage).Offset(pagination.Offset(page, perPage)).
		Find(&payments).Error; err != nil {
		return nil, 0, err
	}

	tenantIDs := make([]uint, 0, len(payments))
	seen := map[uint]bool{}
	for _, p := range payments {
		if !seen[p.TenantID] {
			seen[p.TenantID] = true
			tenantIDs = append(tenantIDs, p.TenantID)
		}
	}
	tenantsByID := map[uint]database.Tenant{}
	if len(tenantIDs) > 0 {
		var tenants []database.Tenant
		database.CentralDB.Select("id", "name", "slug", "ruc").Where("id IN ?", tenantIDs).Find(&tenants)
		for _, t := range tenants {
			tenantsByID[t.ID] = t
		}
	}

	result := make([]PaymentDetail, 0, len(payments))
	for _, p := range payments {
		d := PaymentDetail{SaasPayment: p}
		if t, ok := tenantsByID[p.TenantID]; ok {
			d.TenantName = t.Name
			d.TenantSlug = t.Slug
			d.TenantRUC = t.RUC
		}
		result = append(result, d)
	}
	return result, total, nil
}

func (s *PaymentService) GetByID(id uint) (*PaymentDetail, error) {
	var payment database.SaasPayment
	if err := database.CentralDB.First(&payment, id).Error; err != nil {
		return nil, errors.New("pago no encontrado")
	}
	d := &PaymentDetail{SaasPayment: payment}
	var tenant database.Tenant
	database.CentralDB.First(&tenant, payment.TenantID)
	d.TenantName = tenant.Name
	d.TenantSlug = tenant.Slug
	return d, nil
}

type CreatePaymentInput struct {
	TenantID      uint    `json:"tenant_id" form:"tenant_id"`
	Amount        float64 `json:"amount" form:"amount"`
	Currency      string  `json:"currency" form:"currency"`
	PeriodMonths  int     `json:"period_months" form:"period_months"`
	Notes         string  `json:"notes" form:"notes"`
	ReceiptURL    string  `json:"receipt_url"`
	PaymentMethod string  `json:"payment_method" form:"payment_method"`
	// BillingCycleID cobro que este pago cancela. Sin él la factura queda pendiente
	// aunque el pago se aplique.
	BillingCycleID uint `json:"billing_cycle_id" form:"billing_cycle_id"`
	// ReviewedBy superadmin que registra el pago; queda como quien lo aprobó.
	ReviewedBy uint `json:"-"`
}

// Create registra un pago cobrado fuera del sistema (efectivo, transferencia directa) y lo
// aplica en el acto.
//
// No pasa por revisión a propósito: lo está registrando un administrador desde el panel
// central, que es quien tendría que aprobarlo. Dejarlo pendiente obligaba a aprobar el
// propio pago que uno acaba de cargar.
func (s *PaymentService) Create(input CreatePaymentInput) (*database.SaasPayment, error) {
	payment, err := saas.SubmitPayment(saas.SubmitPaymentInput{
		TenantID:       input.TenantID,
		Amount:         input.Amount,
		PaymentMethod:  input.PaymentMethod,
		ReceiptURL:     input.ReceiptURL,
		Notes:          input.Notes,
		FromAdmin:      true,
		PeriodMonths:   input.PeriodMonths,
		BillingCycleID: input.BillingCycleID,
	})
	if err != nil {
		return nil, err
	}

	// Aplica la renovación: extiende la suscripción, marca el cobro pagado y sincroniza
	// módulos. Si fallara, el pago queda registrado como pendiente y se puede aprobar a
	// mano desde la misma pantalla; por eso el error dice dónde quedó.
	if err := saas.ApprovePayment(payment.ID, 0, input.PeriodMonths, input.Notes, input.ReviewedBy); err != nil {
		return nil, fmt.Errorf("el pago se registró pero no pudo aplicarse (queda pendiente de aprobación): %w", err)
	}

	var applied database.SaasPayment
	if err := database.CentralDB.First(&applied, payment.ID).Error; err != nil {
		return payment, nil
	}
	return &applied, nil
}

type ApproveInput struct {
	PlanID     uint   `json:"plan_id"`
	AdminNotes string `json:"admin_notes"`
	// PeriodMonths opcional: 0 deja que ApprovePayment use payment.PeriodMonths (lo que pidió
	// el tenant), no fuerza 1 mes.
	PeriodMonths int
	ReviewerID   uint
}

func (s *PaymentService) Approve(paymentID uint, input ApproveInput) error {
	return saas.ApprovePayment(paymentID, input.PlanID, input.PeriodMonths, input.AdminNotes, input.ReviewerID)
}

func (s *PaymentService) Reject(paymentID uint, adminNotes string, reviewerID uint) error {
	return saas.RejectPayment(paymentID, adminNotes, reviewerID)
}

// Revert anula un pago ya aprobado y deshace la extensión de suscripción/ciclo que produjo, para
// que el tenant pueda repetir la renovación desde cero. Ver saas.RevertApprovedPayment.
func (s *PaymentService) Revert(paymentID uint, reason string, actorID uint) error {
	return saas.RevertApprovedPayment(paymentID, reason, actorID)
}

// PendingCount para dashboard.
func PendingCount() (int64, error) {
	var n int64
	err := database.CentralDB.Model(&database.SaasPayment{}).
		Where("status IN ?", []string{database.SaasPayPendingReview, database.SaasPayPending}).
		Count(&n).Error
	return n, err
}

// SetFiscalDoc guarda la URL de la boleta/factura emitida al cliente por este pago.
func (s *PaymentService) SetFiscalDoc(paymentID uint, url string) error {
	return database.CentralDB.Model(&database.SaasPayment{}).
		Where("id = ?", paymentID).
		Update("fiscal_doc_url", url).Error
}
