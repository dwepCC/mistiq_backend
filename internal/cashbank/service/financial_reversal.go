package service

import (
	"time"

	"tukifac/pkg/database"

	"gorm.io/gorm"
)

func movementAlreadyReversed(tx *gorm.DB, table string, originalID uint) (bool, error) {
	var cnt int64
	if err := tx.Table(table).Where("reversal_of_id = ?", originalID).Count(&cnt).Error; err != nil {
		return false, err
	}
	return cnt > 0, nil
}

// CreateBankReversal genera un movimiento bancario compensatorio sin modificar el original.
func (s *CashBankService) CreateBankReversal(tx *gorm.DB, original database.TenantBankMovement, description, reference string, userID uint) error {
	exec := s.db
	if tx != nil {
		exec = tx
	}
	done, err := movementAlreadyReversed(exec, "tenant_bank_movements", original.ID)
	if err != nil {
		return err
	}
	if done {
		return nil
	}

	reversalType := "credit"
	delta := original.Amount
	if original.Type == "credit" {
		reversalType = "debit"
		delta = -original.Amount
	} else if original.Type != "debit" {
		return nil
	}

	uid := userID
	if uid == 0 {
		uid = original.UserID
	}
	origID := original.ID
	now := time.Now()
	rev := database.TenantBankMovement{
		BankAccountID: original.BankAccountID,
		Type:          reversalType,
		Amount:        original.Amount,
		Description:   description,
		Reference:     reference,
		Date:          now,
		UserID:        uid,
		ReversalOfID:  &origID,
		CreatedAt:     now,
	}
	if err := exec.Create(&rev).Error; err != nil {
		return err
	}
	return exec.Model(&database.TenantBankAccount{}).
		Where("id = ?", original.BankAccountID).
		Update("balance", gorm.Expr("balance + ?", delta)).Error
}

// ReverseBankMovementsByReference revierte movimientos bancarios del tipo indicado que comparten referencia.
func (s *CashBankService) ReverseBankMovementsByReference(tx *gorm.DB, reference, originalType, description, voidReference string, userID uint) error {
	exec := s.db
	if tx != nil {
		exec = tx
	}
	var originals []database.TenantBankMovement
	if err := exec.Where("reference = ? AND type = ?", reference, originalType).Find(&originals).Error; err != nil {
		return err
	}
	for _, orig := range originals {
		if err := s.CreateBankReversal(tx, orig, description, voidReference, userID); err != nil {
			return err
		}
	}
	return nil
}

// CreateCashReversal genera un movimiento de caja compensatorio sin modificar el original.
func (s *CashBankService) CreateCashReversal(tx *gorm.DB, original database.TenantCashMovement, category, reference, notes string, userID uint) error {
	exec := s.db
	if tx != nil {
		exec = tx
	}
	done, err := movementAlreadyReversed(exec, "tenant_cash_movements", original.ID)
	if err != nil {
		return err
	}
	if done {
		return nil
	}

	reversalType := "expense"
	if original.Type == "expense" {
		reversalType = "income"
	} else if original.Type != "income" {
		return nil
	}

	uid := userID
	if uid == 0 {
		uid = original.UserID
	}
	origID := original.ID
	now := time.Now()
	rev := database.TenantCashMovement{
		CashSessionID: original.CashSessionID,
		Type:          reversalType,
		Amount:        original.Amount,
		PaymentMethod: original.PaymentMethod,
		Category:      category,
		Reference:     reference,
		SaleID:        original.SaleID,
		PurchaseID:    original.PurchaseID,
		ReversalOfID:  &origID,
		Notes:         notes,
		UserID:        uid,
		CreatedAt:     now,
	}
	return exec.Create(&rev).Error
}
