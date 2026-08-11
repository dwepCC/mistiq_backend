package service

import (
	"errors"
	"fmt"

	"tukifac/pkg/database"
	"tukifac/pkg/saas"
)

type PaymentService struct{}

func NewPaymentService() *PaymentService { return &PaymentService{} }

type PaymentDetail struct {
	database.SaasPayment
	TenantName string `json:"tenant_name"`
	TenantSlug string `json:"tenant_slug"`
}

func (s *PaymentService) List(status string) ([]PaymentDetail, error) {
	result := make([]PaymentDetail, 0)
	query := database.CentralDB.Model(&database.SaasPayment{}).Order("created_at desc")
	if status == "pending" {
		query = query.Where("status IN ?", []string{database.SaasPayPending, database.SaasPayPendingReview})
	} else if status != "" {
		query = query.Where("status = ?", status)
	}
	var payments []database.SaasPayment
	if err := query.Find(&payments).Error; err != nil {
		return nil, err
	}
	for _, p := range payments {
		d := PaymentDetail{SaasPayment: p}
		var tenant database.Tenant
		database.CentralDB.First(&tenant, p.TenantID)
		d.TenantName = tenant.Name
		d.TenantSlug = tenant.Slug
		result = append(result, d)
	}
	return result, nil
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
	ReviewerID uint
}

func (s *PaymentService) Approve(paymentID uint, input ApproveInput) error {
	return saas.ApprovePayment(paymentID, input.PlanID, 0, input.AdminNotes, input.ReviewerID)
}

func (s *PaymentService) Reject(paymentID uint, adminNotes string, reviewerID uint) error {
	return saas.RejectPayment(paymentID, adminNotes, reviewerID)
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
