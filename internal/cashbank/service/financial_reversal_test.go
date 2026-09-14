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
	// TenantSale: no la usan los tests de este archivo, pero ListBankMovementsPaged (probado en
	// bank_movements_paged_test.go, que reutiliza este mismo helper) ahora hace un subquery contra
	// ella para excluir movimientos de notas de crédito/débito — sin migrarla, esa consulta falla
	// con "no such table" incluso cuando el resultado esperado no depende de ninguna fila real.
	for _, m := range []interface{}{&database.TenantBankAccount{}, &database.TenantBankMovement{}, &database.TenantSale{}} {
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

	if err := svc.CreateBankReversal(db, orig, "Reversión por anulación de compra", "ANUL/1", "", "", 2); err != nil {
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
	if err := svc.CreateBankReversal(db, orig, "Reversión por anulación de compra", "ANUL/1", "", "", 2); err != nil {
		t.Fatal(err)
	}
	var cnt int64
	db.Model(&database.TenantBankMovement{}).Where("reversal_of_id = ?", orig.ID).Count(&cnt)
	if cnt != 1 {
		t.Fatalf("expected 1 reversal, got %d", cnt)
	}
}

// Corrección P0: la reversión debe conservar el cash_session_id del movimiento original — la
// Caja donde ocurrió el pago que se está revirtiendo, nunca una inventada. Antes de esta
// corrección quedaba NULL, lo que hacía invisible la reversión para
// GetSessionBalanceSummary(sessionID) de esa misma sesión.
func TestCreateBankReversal_PreservesCashSessionID(t *testing.T) {
	db := setupFinancialReversalTestDB(t)
	svc := NewCashBankService(db)

	acc := &database.TenantBankAccount{Name: "Yape", PaymentMethod: "yape", Balance: 500, Active: true}
	if err := db.Create(acc).Error; err != nil {
		t.Fatal(err)
	}
	sessionID := uint(30)
	orig := database.TenantBankMovement{
		BankAccountID: acc.ID,
		Type:          "credit",
		Amount:        500,
		Description:   "Venta F001-1",
		Reference:     "F001-1",
		Date:          time.Now(),
		UserID:        1,
		CashSessionID: &sessionID,
	}
	if err := db.Create(&orig).Error; err != nil {
		t.Fatal(err)
	}

	if err := svc.CreateBankReversal(db, orig, "Reversión por anulación de venta", "ANUL/F001-1", "", "", 2); err != nil {
		t.Fatal(err)
	}

	var rev database.TenantBankMovement
	if err := db.Where("reversal_of_id = ?", orig.ID).First(&rev).Error; err != nil {
		t.Fatal(err)
	}
	if rev.CashSessionID == nil || *rev.CashSessionID != sessionID {
		t.Fatalf("reversal.cash_session_id = %v, want %d (heredado del movimiento original)", rev.CashSessionID, sessionID)
	}
	if rev.ReversalOfID == nil || *rev.ReversalOfID != orig.ID {
		t.Fatalf("reversal_of_id incorrecto: %+v", rev.ReversalOfID)
	}

	// Sigue protegido contra doble reversión con la sesión ya poblada.
	if err := svc.CreateBankReversal(db, orig, "Reversión por anulación de venta", "ANUL/F001-1", "", "", 2); err != nil {
		t.Fatal(err)
	}
	var cnt int64
	db.Model(&database.TenantBankMovement{}).Where("reversal_of_id = ?", orig.ID).Count(&cnt)
	if cnt != 1 {
		t.Fatalf("expected 1 reversal, got %d (no debe duplicarse)", cnt)
	}
}
