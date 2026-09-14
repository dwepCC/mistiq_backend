package service

import (
	"strings"
	"time"

	cashbanksvc "tukifac/internal/cashbank/service"
	"tukifac/pkg/database"

	"gorm.io/gorm"
)

// PendingRefund devolución de dinero que quedó sin registrar al anular una venta, o el monto
// de una nota de crédito parcial (Fase 2) aceptada por SUNAT que aún no se devolvió en caja.
type PendingRefund struct {
	// Source "sale" (venta anulada por completo) o "credit_note" (nota parcial) — decide qué
	// endpoint de "aplicar" corresponde: ApplyPendingRefund vs ApplyPendingNoteRefund.
	Source         string `json:"source"`
	CashMovementID uint   `json:"cash_movement_id,omitempty"`
	// NoteSaleID: solo cuando Source="credit_note" — la nota de crédito que origina la devolución.
	NoteSaleID    uint    `json:"note_sale_id,omitempty"`
	NoteNumber    string  `json:"note_number,omitempty"`
	SaleID        uint    `json:"sale_id"`
	SaleNumber    string  `json:"sale_number"`
	Amount        float64 `json:"amount"`
	PaymentMethod string  `json:"payment_method"`
	// OriginalSessionID caja donde entró el dinero, ya cerrada.
	OriginalSessionID uint   `json:"original_session_id,omitempty"`
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
			Source:            "sale",
			CashMovementID:    r.ID,
			SaleID:            saleID,
			SaleNumber:        r.Number,
			Amount:            r.Amount,
			PaymentMethod:     r.PaymentMethod,
			OriginalSessionID: r.CashSessionID,
		})
	}

	noteRows, err := s.pendingCreditNoteRefunds(branchID)
	if err != nil {
		return nil, err
	}
	out = append(out, noteRows...)
	return out, nil
}

// pendingCreditNoteRefunds notas de crédito parciales (Fase 2) aceptadas por SUNAT cuyo
// monto todavía no se devolvió en caja — a diferencia de una venta anulada por completo, acá
// no hay un cobro original que revertir 1:1 (la nota puede ser solo una fracción de lo
// cobrado), así que el dedup es por referencia ("NC/{numero}") en vez de reversal_of_id.
func (s *SaleService) pendingCreditNoteRefunds(branchID uint) ([]PendingRefund, error) {
	var notes []database.TenantSale
	q := s.db.
		Where("doc_type = ? AND billing_status = ?", "NOTA_CREDITO", "accepted").
		Where("note_reason_code IN ?", partialCreditNoteReasonCodesList())
	if branchID > 0 {
		q = q.Where("branch_id = ?", branchID)
	}
	if err := q.Order("issue_date asc").Find(&notes).Error; err != nil {
		return nil, err
	}
	if len(notes) == 0 {
		return nil, nil
	}

	// Dedup por referencia en vez de un NOT EXISTS con CONCAT: CONCAT no es portable entre
	// MySQL (producción) y SQLite (tests) — armar la lista de referencias en Go y filtrar sí.
	refs := make([]string, len(notes))
	for i, nc := range notes {
		refs[i] = "NC/" + nc.Number
	}
	var refunded []string
	if err := s.db.Model(&database.TenantCashMovement{}).
		Where("reference IN ?", refs).
		Pluck("reference", &refunded).Error; err != nil {
		return nil, err
	}
	refundedSet := make(map[string]bool, len(refunded))
	for _, r := range refunded {
		refundedSet[r] = true
	}

	out := make([]PendingRefund, 0, len(notes))
	for _, nc := range notes {
		if refundedSet["NC/"+nc.Number] {
			continue
		}
		saleID := uint(0)
		if nc.OriginalSaleID != nil {
			saleID = *nc.OriginalSaleID
		}
		out = append(out, PendingRefund{
			Source:        "credit_note",
			NoteSaleID:    nc.ID,
			NoteNumber:    nc.Number,
			SaleID:        saleID,
			SaleNumber:    nc.Number,
			Amount:        nc.Total,
			PaymentMethod: nc.PaymentMethod,
			CancelledAt:   nc.IssueDate.Format("2006-01-02"),
		})
	}
	return out, nil
}

// partialCreditNoteReasonCodesList espejo de billing/service.partialCreditNoteReasonCodes —
// vive acá también porque este paquete no importa internal/billing/service (importaría al
// revés, billing sí importa sales). Mantener sincronizado con pkg/sunatnote catálogo 09.
func partialCreditNoteReasonCodesList() []string {
	return []string{"04", "05", "06", "07", "08", "09", "10"}
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

// ApplyPendingNoteRefund registra en la sesión indicada la devolución del monto de una nota
// de crédito parcial (Fase 2) — a diferencia de ApplyPendingRefund, no hay un cobro original
// (tenant_cash_movements) que revertir con reversal_of_id: se crea el egreso directo por el
// monto de la nota, con la misma referencia ("NC/{numero}") que pendingCreditNoteRefunds usa
// para no volver a ofrecerla como pendiente una vez aplicada.
func (s *SaleService) ApplyPendingNoteRefund(noteSaleID, sessionID, userID uint, reason string) error {
	var nc database.TenantSale
	if err := s.db.First(&nc, noteSaleID).Error; err != nil {
		return errNotFound("nota de crédito no encontrada")
	}
	if nc.DocType != "NOTA_CREDITO" {
		return errNotFound("el documento indicado no es una nota de crédito")
	}
	var session database.TenantCashSession
	if err := s.db.First(&session, sessionID).Error; err != nil {
		return errNotFound("caja no encontrada")
	}
	if session.Status != "open" {
		return errNotFound("la caja está cerrada; abra una caja para registrar la devolución")
	}
	ref := "NC/" + nc.Number
	var already int64
	if err := s.db.Model(&database.TenantCashMovement{}).Where("reference = ?", ref).Count(&already).Error; err != nil {
		return err
	}
	if already > 0 {
		return nil
	}
	paymentMethod := strings.TrimSpace(nc.PaymentMethod)
	if paymentMethod == "" {
		paymentMethod = "efectivo"
	}
	return s.db.Create(&database.TenantCashMovement{
		CashSessionID: sessionID,
		Type:          "expense",
		Amount:        nc.Total,
		PaymentMethod: paymentMethod,
		Category:      "Devolución por nota de crédito",
		Reference:     ref,
		SaleID:        nc.OriginalSaleID,
		Notes:         reason,
		UserID:        userID,
		CreatedAt:     time.Now(),
	}).Error
}

// errNotFound error simple de dominio (evita arrastrar errores de GORM a la API).
func errNotFound(msg string) error { return &domainError{msg} }

type domainError struct{ msg string }

func (e *domainError) Error() string { return e.msg }

// compile-time: el servicio de caja se usa en la reversión.
var _ = cashbanksvc.NewCashBankService
