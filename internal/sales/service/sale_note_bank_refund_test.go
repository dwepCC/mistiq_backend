package service

import (
	"fmt"
	"testing"
	"time"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func setupBankRefundDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	models := []interface{}{
		&database.TenantSale{}, &database.TenantBankAccount{}, &database.TenantBankMovement{},
	}
	for _, m := range models {
		if err := db.AutoMigrate(m); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func TestRestorePartialSaleBankRefundTx_DebitsOriginatingAccount(t *testing.T) {
	db := setupBankRefundDB(t)

	acc := &database.TenantBankAccount{Name: "BBVA", PaymentMethod: "transferencia", Balance: 500, Active: true}
	if err := db.Create(acc).Error; err != nil {
		t.Fatal(err)
	}
	orig := &database.TenantSale{Number: "F001-1", DocType: "FACTURA", Total: 100, Status: "cancelled"}
	if err := db.Create(orig).Error; err != nil {
		t.Fatal(err)
	}
	origCredit := &database.TenantBankMovement{
		BankAccountID: acc.ID, Type: "credit", Amount: 100, Reference: orig.Number,
		Date: time.Now(), UserID: 1, SaleID: &orig.ID,
	}
	if err := db.Create(origCredit).Error; err != nil {
		t.Fatal(err)
	}

	noteSale := &database.TenantSale{Number: "FC01-1", DocType: "NOTA_CREDITO", Total: 30}
	if err := db.Create(noteSale).Error; err != nil {
		t.Fatal(err)
	}

	if err := RestorePartialSaleBankRefundTx(db, noteSale, orig.ID, orig.Number, 2); err != nil {
		t.Fatal(err)
	}

	var loaded database.TenantBankAccount
	if err := db.First(&loaded, acc.ID).Error; err != nil {
		t.Fatal(err)
	}
	if loaded.Balance != 470 {
		t.Fatalf("balance: got %.2f want 470", loaded.Balance)
	}

	var refund database.TenantBankMovement
	if err := db.Where("reference = ?", "NC/FC01-1").First(&refund).Error; err != nil {
		t.Fatal(err)
	}
	if refund.Type != "debit" || refund.Amount != 30 || refund.BankAccountID != acc.ID {
		t.Fatalf("refund: type=%s amount=%.2f account=%d", refund.Type, refund.Amount, refund.BankAccountID)
	}
	if refund.SaleID == nil || *refund.SaleID != orig.ID {
		t.Fatalf("refund.SaleID not linked to original sale")
	}
}

func TestRestorePartialSaleBankRefundTx_NoOpWhenOriginalWasCash(t *testing.T) {
	db := setupBankRefundDB(t)

	orig := &database.TenantSale{Number: "F001-2", DocType: "FACTURA", Total: 100}
	if err := db.Create(orig).Error; err != nil {
		t.Fatal(err)
	}
	noteSale := &database.TenantSale{Number: "FC01-2", DocType: "NOTA_CREDITO", Total: 30}
	if err := db.Create(noteSale).Error; err != nil {
		t.Fatal(err)
	}

	if err := RestorePartialSaleBankRefundTx(db, noteSale, orig.ID, orig.Number, 2); err != nil {
		t.Fatal(err)
	}

	var count int64
	db.Model(&database.TenantBankMovement{}).Count(&count)
	if count != 0 {
		t.Fatalf("expected no bank movement created, got %d", count)
	}
}

func TestRestorePartialSaleBankRefundTx_IdempotentOnRetry(t *testing.T) {
	db := setupBankRefundDB(t)

	acc := &database.TenantBankAccount{Name: "BCP", PaymentMethod: "transferencia", Balance: 200, Active: true}
	if err := db.Create(acc).Error; err != nil {
		t.Fatal(err)
	}
	orig := &database.TenantSale{Number: "F001-3", DocType: "FACTURA", Total: 100}
	if err := db.Create(orig).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.TenantBankMovement{
		BankAccountID: acc.ID, Type: "credit", Amount: 100, Reference: orig.Number,
		Date: time.Now(), UserID: 1, SaleID: &orig.ID,
	}).Error; err != nil {
		t.Fatal(err)
	}
	noteSale := &database.TenantSale{Number: "FC01-3", DocType: "NOTA_CREDITO", Total: 30}
	if err := db.Create(noteSale).Error; err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 2; i++ {
		if err := RestorePartialSaleBankRefundTx(db, noteSale, orig.ID, orig.Number, 2); err != nil {
			t.Fatal(err)
		}
	}

	var loaded database.TenantBankAccount
	if err := db.First(&loaded, acc.ID).Error; err != nil {
		t.Fatal(err)
	}
	if loaded.Balance != 170 {
		t.Fatalf("balance after retry: got %.2f want 170 (debe descontar una sola vez)", loaded.Balance)
	}
}
