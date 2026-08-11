package service

import (
	"time"

	cashbanksvc "tukifac/internal/cashbank/service"
	"tukifac/pkg/database"

	"gorm.io/gorm"
)

// reverseSaleCashTx devuelve el dinero cobrado por una venta que se anula.
//
// La anulación fiscal y la devolución del dinero son dos hechos distintos cuando la nota de
// crédito se acepta días después: el comprobante se anula con fecha de la NC, pero el efectivo
// sale de la caja que esté abierta en ese momento. De ahí las tres reglas:
//
//  1. Sesión original ABIERTA (se anula en el mismo turno): se revierte ahí. El dinero sigue
//     físicamente en esa caja y el arqueo cuadra solo.
//  2. Sesión original CERRADA: el egreso va a la caja abierta actual, porque de ahí sale el
//     dinero de verdad. El arqueo ya cerrado NO se toca: reescribirlo dejaría mintiendo el
//     conteo de billetes que firmó el cajero esa noche.
//  3. SIN caja abierta (la NC se acepta de madrugada): no se inventa un movimiento. La
//     devolución queda pendiente y se recupera con PendingSaleRefunds, que la deriva de los
//     cobros sin reversión asociada.
//
// El vínculo entre el cobro y su devolución es `reversal_of_id`, así que desde el arqueo
// histórico se llega a la devolución de hoy y al revés, aunque estén en cajas distintas.
func reverseSaleCashTx(
	tx *gorm.DB,
	cashSvc *cashbanksvc.CashBankService,
	sale *database.TenantSale,
	ref, reason string,
	userID uint,
) error {
	var incomes []database.TenantCashMovement
	if err := tx.Where("sale_id = ? AND type = ?", sale.ID, "income").Find(&incomes).Error; err != nil {
		return err
	}

	for i := range incomes {
		m := &incomes[i]
		open, err := sessionIsOpenTx(tx, m.CashSessionID)
		if err != nil {
			return err
		}
		if open {
			// Caso 1: misma caja, reversión directa (ya es idempotente por reversal_of_id).
			if err := cashSvc.CreateCashReversal(tx, *m, "Anulación venta", ref, reason, userID); err != nil {
				return err
			}
			continue
		}

		// Caso 2: la caja del cobro ya cerró. Se busca una abierta donde registrar la salida.
		targetSession, err := openSessionForRefundTx(tx, sale.BranchID, userID)
		if err != nil {
			return err
		}
		if targetSession == 0 {
			// Caso 3: sin caja abierta. Se deja pendiente; no se fuerza un movimiento en una
			// sesión cerrada ni se inventa una.
			continue
		}
		if err := createRefundInSessionTx(tx, m, targetSession, ref, reason, userID); err != nil {
			return err
		}
	}

	// Ventas antiguas cuyo movimiento de caja no guardó sale_id: se reconstruye desde los
	// pagos registrados, contra la sesión de la venta si sigue abierta.
	if len(incomes) == 0 && sale.Total > 0 {
		if err := reverseLegacySalePaymentsTx(tx, cashSvc, sale, ref, reason, userID); err != nil {
			return err
		}
	}

	// Los movimientos bancarios no dependen de la caja: se revierten siempre.
	var bankCredits []database.TenantBankMovement
	if err := tx.Where("reference = ? AND type = ?", sale.Number, "credit").Find(&bankCredits).Error; err != nil {
		return err
	}
	for _, bm := range bankCredits {
		if err := cashSvc.CreateBankReversal(tx, bm, "Reversión por anulación de venta", ref, userID); err != nil {
			return err
		}
	}
	return nil
}

// reverseLegacySalePaymentsTx reversión de ventas cuyo cobro no quedó ligado por sale_id.
//
// Solo aplica a los pagos que fueron a caja (los de banco ya se revierten aparte) y respeta la
// misma regla: si la sesión de la venta cerró, la salida va a la caja abierta.
func reverseLegacySalePaymentsTx(
	tx *gorm.DB,
	cashSvc *cashbanksvc.CashBankService,
	sale *database.TenantSale,
	ref, reason string,
	userID uint,
) error {
	var payments []database.TenantSalePayment
	if err := tx.Where("sale_id = ?", sale.ID).Find(&payments).Error; err != nil {
		return err
	}
	uid := userID
	if uid == 0 {
		uid = sale.UserID
	}
	for _, p := range payments {
		if p.Amount <= 0 {
			continue
		}
		pm, _ := cashSvc.GetPaymentMethodByCode(p.Method)
		if pm == nil || pm.DestinationType != "cash" {
			continue
		}
		sessionID := uint(0)
		if sale.CashSessionID != nil {
			sessionID = *sale.CashSessionID
		}
		open, err := sessionIsOpenTx(tx, sessionID)
		if err != nil {
			return err
		}
		if !open {
			if sessionID, err = openSessionForRefundTx(tx, sale.BranchID, userID); err != nil {
				return err
			}
		}
		if sessionID == 0 {
			continue
		}
		saleID := sale.ID
		if err := tx.Create(&database.TenantCashMovement{
			CashSessionID: sessionID,
			Type:          "expense",
			Amount:        p.Amount,
			PaymentMethod: p.Method,
			Category:      "Anulación venta",
			Reference:     ref,
			SaleID:        &saleID,
			Notes:         reason,
			UserID:        uid,
			CreatedAt:     time.Now(),
		}).Error; err != nil {
			return err
		}
	}
	return nil
}

func sessionIsOpenTx(tx *gorm.DB, sessionID uint) (bool, error) {
	if sessionID == 0 {
		return false, nil
	}
	var st database.TenantCashSession
	if err := tx.First(&st, sessionID).Error; err != nil {
		return false, nil
	}
	return st.Status == "open", nil
}

// openSessionForRefundTx caja abierta donde registrar la devolución: la del usuario que anula
// si tiene una, y si no cualquier otra abierta de la sucursal. 0 = no hay ninguna.
func openSessionForRefundTx(tx *gorm.DB, branchID, userID uint) (uint, error) {
	var st database.TenantCashSession
	q := tx.Where("branch_id = ? AND status = ?", branchID, "open")
	if userID > 0 {
		if err := q.Session(&gorm.Session{}).Where("user_id = ?", userID).
			Order("opened_at desc").First(&st).Error; err == nil {
			return st.ID, nil
		}
	}
	if err := tx.Where("branch_id = ? AND status = ?", branchID, "open").
		Order("opened_at desc").First(&st).Error; err != nil {
		return 0, nil
	}
	return st.ID, nil
}

// createRefundInSessionTx salida de efectivo en otra caja, ligada al cobro original.
func createRefundInSessionTx(
	tx *gorm.DB,
	original *database.TenantCashMovement,
	sessionID uint,
	ref, reason string,
	userID uint,
) error {
	var already int64
	if err := tx.Model(&database.TenantCashMovement{}).
		Where("reversal_of_id = ?", original.ID).Count(&already).Error; err != nil {
		return err
	}
	if already > 0 {
		return nil
	}
	uid := userID
	if uid == 0 {
		uid = original.UserID
	}
	origID := original.ID
	return tx.Create(&database.TenantCashMovement{
		CashSessionID: sessionID,
		Type:          "expense",
		Amount:        original.Amount,
		PaymentMethod: original.PaymentMethod,
		Category:      "Devolución por anulación",
		Reference:     ref,
		SaleID:        original.SaleID,
		ReversalOfID:  &origID,
		Notes:         reason,
		UserID:        uid,
		CreatedAt:     time.Now(),
	}).Error
}
