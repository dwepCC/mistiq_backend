package service

import (
	"fmt"
	"testing"
	"time"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func setupFinancialReversalTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []interface{}{&database.TenantBankAccount{}, &database.TenantBankMovement{}} {
		if err := db.AutoMigrate(m); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func TestCreateBankReversal_CompensatesDebit(t *testing.T) {
	db := setupFinancialReversalTestDB(t)
	svc := NewCashBankService(db)

	acc := &database.TenantBankAccount{Name: "Test", PaymentMethod: "efectivo", Balance: 900, Active: true}
	if err := db.Create(acc).Error; err != nil {
		t.Fatal(err)
	}
	orig := database.TenantBankMovement{
		BankAccountID: acc.ID,
		Type:          "debit",
		Amount:        100,
		Description:   "Compra F001-1",
		Reference:     "F001-1",
		Date:          time.Now(),
		UserID:        1,
	}
	if err := db.Create(&orig).Error; err != nil {
		t.Fatal(err)
	}

	if err := svc.CreateBankReversal(db, orig, "Reversión por anulación de compra", "ANUL/1", 2); err != nil {
		t.Fatal(err)
	}

	var rev database.TenantBankMovement
	if err := db.Where("reversal_of_id = ?", orig.ID).First(&rev).Error; err != nil {
		t.Fatal(err)
	}
	if rev.Type != "credit" || rev.Amount != 100 {
		t.Fatalf("reversal: type=%s amount=%v", rev.Type, rev.Amount)
	}

	var loaded database.TenantBankAccount
	if err := db.First(&loaded, acc.ID).Error; err != nil {
		t.Fatal(err)
	}
	if loaded.Balance != 1000 {
		t.Fatalf("balance: got %.2f want 1000", loaded.Balance)
	}

	// Idempotente: segunda reversión no duplica.
	if err := svc.CreateBankReversal(db, orig, "Reversión por anulación de compra", "ANUL/1", 2); err != nil {
		t.Fatal(err)
	}
	var cnt int64
	db.Model(&database.TenantBankMovement{}).Where("reversal_of_id = ?", orig.ID).Count(&cnt)
	if cnt != 1 {
		t.Fatalf("expected 1 reversal, got %d", cnt)
	}
}
