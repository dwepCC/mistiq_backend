package service

import (
	"errors"
	"time"

	"tukifac/pkg/database"

	"gorm.io/gorm"
)

// RestorePartialSaleBankRefundTx registra en la cuenta bancaria el egreso correspondiente a una
// nota de crédito parcial (Fase 2), cuando la venta original se cobró por un método de pago
// vinculado a cuenta bancaria (transferencia, Yape/Plin con cuenta configurada, POS de tarjeta).
//
// Antes de esto, una nota de crédito parcial solo reponía stock: la devolución de dinero pendiente
// (sale_pending_refunds.go) solo sabía escribir en tenant_cash_movements (Caja), así que si la
// venta se cobró a una cuenta bancaria el ingreso original quedaba para siempre sin contraparte —
// se seguía viendo como ingreso vigente en Cuentas/Bancos.
//
// A diferencia de la anulación total (reverseSaleCashTx + CreateBankReversal, que revierte 1:1 el
// cobro original vía reversal_of_id), acá NO se revierte el movimiento original completo — la nota
// puede ser solo una fracción de lo cobrado, y puede haber varias notas parciales contra la misma
// venta. Se crea un nuevo egreso por el monto exacto de la nota, referenciado como "NC/{numero}"
// (idempotente: si ya existe un movimiento con esa referencia, no se duplica).
//
// Limitación conocida: si la venta original se cobró con más de un método de pago (pago mixto,
// parte efectivo + parte banco), esta función toma la cuenta del primer cobro bancario que
// encuentra y le descuenta el monto completo de la nota — no reparte proporcionalmente entre los
// métodos de pago originales. Caso poco frecuente (venta con pago mixto + nota de crédito
// parcial); documentado para un ajuste futuro si se reporta.
func RestorePartialSaleBankRefundTx(tx *gorm.DB, noteSale *database.TenantSale, origSaleID uint, origSaleNumber string, userID uint) error {
	if origSaleID == 0 && origSaleNumber == "" {
		return nil
	}
	ref := "NC/" + noteSale.Number
	var already int64
	if err := tx.Model(&database.TenantBankMovement{}).Where("reference = ?", ref).Count(&already).Error; err != nil {
		return err
	}
	if already > 0 {
		return nil
	}

	var origCredit database.TenantBankMovement
	err := tx.Where("(sale_id = ? OR reference = ?) AND type = ?", origSaleID, origSaleNumber, "credit").
		Order("created_at ASC").First(&origCredit).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil // la venta original no se cobró a cuenta bancaria; nada que hacer acá
		}
		return err
	}

	uid := userID
	if uid == 0 {
		uid = noteSale.UserID
	}
	now := time.Now()
	if err := tx.Create(&database.TenantBankMovement{
		BankAccountID: origCredit.BankAccountID,
		Type:          "debit",
		Amount:        noteSale.Total,
		Description:   "Devolución por nota de crédito " + noteSale.Number,
		Reference:     ref,
		Date:          now,
		UserID:        uid,
		SaleID:        &origSaleID,
		CreatedAt:     now,
	}).Error; err != nil {
		return err
	}
	return tx.Model(&database.TenantBankAccount{}).
		Where("id = ?", origCredit.BankAccountID).
		Update("balance", gorm.Expr("balance - ?", noteSale.Total)).Error
}
