package service

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"tukifac/pkg/database"
	"tukifac/pkg/money"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Estados del pago del pedido, derivados de los abonos validados (se guardan en
// TenantEcommerceOrder.PaymentStatus para poder filtrar en la lista).
const (
	PaymentStatusNone    = "NO_APLICA" // sin abonos registrados
	PaymentStatusPartial = "ABONADO"
	PaymentStatusPaid    = "PAGADO"
)

// OrderPaymentSummary cuánto lleva pagado un pedido.
type OrderPaymentSummary struct {
	Total float64 `json:"total"`
	// Paid suma de abonos VALIDADOS (los pendientes u observados no cuentan).
	Paid float64 `json:"paid"`
	// Pending suma de abonos esperando validación u observados.
	Pending float64 `json:"pending"`
	// Balance lo que falta: total − pagado validado (nunca negativo).
	Balance float64 `json:"balance"`
	// NeedsReview hay abonos por validar u observados.
	NeedsReview bool   `json:"needs_review"`
	Status      string `json:"payment_status"`
}

func summarize(order *database.TenantEcommerceOrder, payments []database.TenantEcommerceOrderPayment) OrderPaymentSummary {
	var paid, pending float64
	review := false
	for _, p := range payments {
		switch p.Status {
		case database.OrderPaymentValidated:
			paid += p.Amount
		default:
			pending += p.Amount
			review = true
		}
	}
	paid = money.RoundDisplay(paid)
	balance := money.RoundDisplay(order.Total - paid)
	if balance < 0 {
		balance = 0
	}
	status := PaymentStatusNone
	switch {
	case paid <= 0 && len(payments) == 0:
		status = PaymentStatusNone
	case balance < money.PaymentTolerance && order.Total > 0:
		status = PaymentStatusPaid
	case paid > 0:
		status = PaymentStatusPartial
	}
	return OrderPaymentSummary{
		Total: order.Total, Paid: paid, Pending: money.RoundDisplay(pending),
		Balance: balance, NeedsReview: review, Status: status,
	}
}

func (s *EcommerceService) listPaymentsTx(db *gorm.DB, orderID uint) ([]database.TenantEcommerceOrderPayment, error) {
	var rows []database.TenantEcommerceOrderPayment
	err := db.Where("order_id = ?", orderID).Order("paid_at ASC, id ASC").Find(&rows).Error
	return rows, err
}

// ListOrderPayments abonos del pedido con su resumen.
func (s *EcommerceService) ListOrderPayments(orderID uint) ([]database.TenantEcommerceOrderPayment, OrderPaymentSummary, error) {
	var order database.TenantEcommerceOrder
	if err := s.db.First(&order, orderID).Error; err != nil {
		return nil, OrderPaymentSummary{}, errors.New("pedido no encontrado")
	}
	rows, err := s.listPaymentsTx(s.db, orderID)
	if err != nil {
		return nil, OrderPaymentSummary{}, err
	}
	return rows, summarize(&order, rows), nil
}

// syncPaymentStatus guarda en el pedido el estado de pago derivado de sus abonos.
func (s *EcommerceService) syncPaymentStatus(tx *gorm.DB, orderID uint) error {
	var order database.TenantEcommerceOrder
	if err := tx.First(&order, orderID).Error; err != nil {
		return err
	}
	rows, err := s.listPaymentsTx(tx, orderID)
	if err != nil {
		return err
	}
	sum := summarize(&order, rows)
	return tx.Model(&database.TenantEcommerceOrder{}).Where("id = ?", orderID).Update("payment_status", sum.Status).Error
}

// AddPaymentInput datos de un abono.
type AddPaymentInput struct {
	Amount     float64
	Method     string
	Reference  string
	Note       string
	VoucherURL string
	PaidAt     *time.Time
	UserID     uint
}

var paymentBlockedStatuses = map[string]bool{
	OrderStatusCancelado: true, OrderStatusRechazado: true, OrderStatusDevuelto: true,
}

// validMethod comprueba que el medio de pago exista y esté activo en el negocio. Los medios de
// crédito y detracción no son abonos.
func (s *EcommerceService) validMethod(code string) error {
	code = strings.TrimSpace(code)
	if code == "" {
		return errors.New("elige cómo pagó el cliente")
	}
	lower := strings.ToLower(code)
	if lower == "credit" || lower == "credito" || strings.Contains(lower, "detrac") {
		return errors.New("elige un medio de pago directo (efectivo, Yape, transferencia…)")
	}
	if !s.db.Migrator().HasTable(&database.TenantPaymentMethod{}) {
		return nil
	}
	var n int64
	if err := s.db.Model(&database.TenantPaymentMethod{}).Where("code = ? AND active = ?", code, true).Count(&n).Error; err != nil {
		return err
	}
	if n == 0 {
		return errors.New("ese medio de pago no está disponible")
	}
	return nil
}

// AddOrderPayment registra un abono. Con «validar pagos» activado nace PENDIENTE (alguien debe revisar
// el voucher); si no, nace VALIDADO. El abono no puede superar lo que falta por pagar.
func (s *EcommerceService) AddOrderPayment(orderID uint, in AddPaymentInput) (*database.TenantEcommerceOrderPayment, error) {
	if math.IsNaN(in.Amount) || math.IsInf(in.Amount, 0) || in.Amount <= 0 {
		return nil, errors.New("el monto del abono debe ser mayor a cero")
	}
	amount := money.RoundDisplay(in.Amount)
	if err := s.validMethod(in.Method); err != nil {
		return nil, err
	}
	settings, err := s.GetSettings()
	if err != nil {
		return nil, err
	}

	var created database.TenantEcommerceOrderPayment
	err = s.db.Transaction(func(tx *gorm.DB) error {
		var order database.TenantEcommerceOrder
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&order, orderID).Error; err != nil {
			return errors.New("pedido no encontrado")
		}
		if paymentBlockedStatuses[order.Status] {
			return fmt.Errorf("no se pueden registrar abonos en un pedido %s", strings.ToLower(order.Status))
		}
		rows, err := s.listPaymentsTx(tx, orderID)
		if err != nil {
			return err
		}
		sum := summarize(&order, rows)
		// Contra el saldo cuentan también los abonos aún sin validar, para no registrar de más.
		remaining := money.RoundDisplay(order.Total - sum.Paid - sum.Pending)
		if amount-remaining > money.PaymentTolerance {
			return fmt.Errorf("el abono (S/ %.2f) supera lo que falta por pagar (S/ %.2f)", amount, math.Max(remaining, 0))
		}

		now := time.Now()
		paidAt := now
		if in.PaidAt != nil {
			paidAt = *in.PaidAt
			if paidAt.After(now.Add(24 * time.Hour)) {
				return errors.New("la fecha del pago no puede ser futura")
			}
		}
		created = database.TenantEcommerceOrderPayment{
			OrderID: orderID, Amount: amount, Method: strings.TrimSpace(in.Method),
			Reference: strings.TrimSpace(in.Reference), Note: strings.TrimSpace(in.Note),
			VoucherURL: strings.TrimSpace(in.VoucherURL), PaidAt: paidAt,
			Status: database.OrderPaymentValidated,
		}
		var uid *uint
		if in.UserID > 0 {
			uid = &in.UserID
			created.CreatedBy = uid
		}
		if settings.RequirePaymentValidation {
			created.Status = database.OrderPaymentPending
		} else {
			created.ReviewedBy = uid
			created.ReviewedAt = &now
		}
		if err := tx.Create(&created).Error; err != nil {
			return err
		}
		return s.syncPaymentStatus(tx, orderID)
	})
	if err != nil {
		return nil, err
	}
	return &created, nil
}

// ReviewPaymentInput valida u observa un abono.
type ReviewPaymentInput struct {
	Approve bool
	Reason  string // obligatorio al observar
	UserID  uint
}

// ReviewOrderPayment valida u observa un abono pendiente u observado.
func (s *EcommerceService) ReviewOrderPayment(paymentID uint, in ReviewPaymentInput) (*database.TenantEcommerceOrderPayment, error) {
	if !in.Approve && strings.TrimSpace(in.Reason) == "" {
		return nil, errors.New("indica por qué observas el pago")
	}
	var p database.TenantEcommerceOrderPayment
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&p, paymentID).Error; err != nil {
			return errors.New("abono no encontrado")
		}
		var order database.TenantEcommerceOrder
		if err := tx.First(&order, p.OrderID).Error; err != nil {
			return errors.New("pedido no encontrado")
		}
		if order.ConvertedSaleID != nil {
			return errors.New("el pedido ya se convirtió en venta: sus abonos ya no se pueden revisar")
		}
		now := time.Now()
		upd := map[string]interface{}{"reviewed_at": now}
		if in.UserID > 0 {
			upd["reviewed_by"] = in.UserID
		}
		if in.Approve {
			upd["status"] = database.OrderPaymentValidated
			upd["observed_reason"] = ""
			p.Status = database.OrderPaymentValidated
		} else {
			upd["status"] = database.OrderPaymentObserved
			upd["observed_reason"] = strings.TrimSpace(in.Reason)
			p.Status = database.OrderPaymentObserved
			p.ObservedReason = strings.TrimSpace(in.Reason)
		}
		if err := tx.Model(&database.TenantEcommerceOrderPayment{}).Where("id = ?", paymentID).Updates(upd).Error; err != nil {
			return err
		}
		return s.syncPaymentStatus(tx, p.OrderID)
	})
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// DeleteOrderPayment quita un abono mal registrado mientras el pedido no sea una venta.
func (s *EcommerceService) DeleteOrderPayment(paymentID uint) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		var p database.TenantEcommerceOrderPayment
		if err := tx.First(&p, paymentID).Error; err != nil {
			return errors.New("abono no encontrado")
		}
		var order database.TenantEcommerceOrder
		if err := tx.First(&order, p.OrderID).Error; err != nil {
			return errors.New("pedido no encontrado")
		}
		if order.ConvertedSaleID != nil {
			return errors.New("el pedido ya se convirtió en venta: el abono ya forma parte de sus pagos")
		}
		if err := tx.Delete(&p).Error; err != nil {
			return err
		}
		return s.syncPaymentStatus(tx, p.OrderID)
	})
}

// pendingReview hay abonos esperando validación u observados (bloquean preparar si «validar pagos» está activo).
func (s *EcommerceService) pendingReview(tx *gorm.DB, orderID uint) (bool, error) {
	var n int64
	err := tx.Model(&database.TenantEcommerceOrderPayment{}).
		Where("order_id = ? AND status <> ?", orderID, database.OrderPaymentValidated).Count(&n).Error
	return n > 0, err
}

// ShippingSettingsView los ajustes de pedidos que la pantalla de pedidos necesita conocer, sin exigir el
// permiso de configurar la tienda.
type ShippingSettingsView struct {
	RequirePaymentValidation bool   `json:"require_payment_validation"`
	EmitSaleOnDispatch       bool   `json:"emit_sale_on_dispatch"`
	EmitSaleTarget           string `json:"emit_sale_target"`
}

func (s *EcommerceService) ShippingSettings() ShippingSettingsView {
	cfg, err := s.GetSettings()
	if err != nil || cfg == nil {
		return ShippingSettingsView{EmitSaleTarget: "nota_venta"}
	}
	target := cfg.EmitSaleTarget
	if target == "" {
		target = "nota_venta"
	}
	return ShippingSettingsView{
		RequirePaymentValidation: cfg.RequirePaymentValidation,
		EmitSaleOnDispatch:       cfg.EmitSaleOnDispatch,
		EmitSaleTarget:           target,
	}
}
