package service

import (
	cashbanksvc "tukifac/internal/cashbank/service"
	"tukifac/pkg/database"

	"gorm.io/gorm"
)

// PendingRefund devolución de dinero que quedó sin registrar al anular una venta.
type PendingRefund struct {
	CashMovementID uint    `json:"cash_movement_id"`
	SaleID         uint    `json:"sale_id"`
	SaleNumber     string  `json:"sale_number"`
	Amount         float64 `json:"amount"`
	PaymentMethod  string  `json:"payment_method"`
	// OriginalSessionID caja donde entró el dinero, ya cerrada.
	OriginalSessionID uint   `json:"original_session_id"`
	CancelledAt       string `json:"cancelled_at,omitempty"`
}

// PendingSaleRefunds devoluciones pendientes de la sucursal.
//
// No hay tabla ni estado que mantener: una devolución pendiente es «cobro de una venta anulada
// que todavía no tiene ningún movimiento apuntándole con reversal_of_id». En cuanto se registra
// la devolución, el vínculo existe y la fila deja de aparecer sola —sin nada que marcar como
// resuelto, que es donde se desincronizan las listas de pendientes.
func (s *SaleService) PendingSaleRefunds(branchID uint) ([]PendingRefund, error) {
	var rows []struct {
		database.TenantCashMovement
		Number string
	}
	q := s.db.Table("tenant_cash_movements AS cm").
		Select("cm.*, s.number AS number").
		Joins("JOIN tenant_sales s ON s.id = cm.sale_id").
		Where("cm.type = ? AND s.status = ?", "income", "cancelled").
		Where("NOT EXISTS (SELECT 1 FROM tenant_cash_movements r WHERE r.reversal_of_id = cm.id)")
	if branchID > 0 {
		q = q.Where("s.branch_id = ?", branchID)
	}
	if err := q.Order("cm.created_at asc").Scan(&rows).Error; err != nil {
		return nil, err
	}

	out := make([]PendingRefund, 0, len(rows))
	for _, r := range rows {
		saleID := uint(0)
		if r.SaleID != nil {
			saleID = *r.SaleID
		}
		out = append(out, PendingRefund{
			CashMovementID:    r.ID,
			SaleID:            saleID,
			SaleNumber:        r.Number,
			Amount:            r.Amount,
			PaymentMethod:     r.PaymentMethod,
			OriginalSessionID: r.CashSessionID,
		})
	}
	return out, nil
}

// ApplyPendingRefund registra en la sesión indicada una devolución que quedó pendiente.
func (s *SaleService) ApplyPendingRefund(cashMovementID, sessionID, userID uint, reason string) error {
	var original database.TenantCashMovement
	if err := s.db.First(&original, cashMovementID).Error; err != nil {
		return errNotFound("movimiento de caja no encontrado")
	}
	var session database.TenantCashSession
	if err := s.db.First(&session, sessionID).Error; err != nil {
		return errNotFound("caja no encontrada")
	}
	if session.Status != "open" {
		return errNotFound("la caja está cerrada; abra una caja para registrar la devolución")
	}
	ref := "ANULACION VENTA"
	if original.SaleID != nil {
		var sale database.TenantSale
		if s.db.First(&sale, *original.SaleID).Error == nil {
			ref = "ANULACION VENTA/" + sale.Number
		}
	}
	return s.db.Transaction(func(tx *gorm.DB) error {
		return createRefundInSessionTx(tx, &original, sessionID, ref, reason, userID)
	})
}

// errNotFound error simple de dominio (evita arrastrar errores de GORM a la API).
func errNotFound(msg string) error { return &domainError{msg} }

type domainError struct{ msg string }

func (e *domainError) Error() string { return e.msg }

// compile-time: el servicio de caja se usa en la reversión.
var _ = cashbanksvc.NewCashBankService
