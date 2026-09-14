package service

import (
	"fmt"
	"testing"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func setupRecordPaymentSessionTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []interface{}{
		&database.TenantCashSession{}, &database.TenantCashMovement{},
		&database.TenantPaymentMethod{}, &database.TenantBankAccount{}, &database.TenantBankMovement{},
		&database.TenantSale{},
	} {
		if err := db.AutoMigrate(m); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

// Fase 2: un pago no efectivo (Yape/Plin/transferencia/tarjeta) de una venta crea un
// TenantBankMovement — antes de esta fase, ese movimiento no quedaba trazable a ninguna sesión
// de Caja (la columna no existía). Ahora RecordPayment debe propagarle el cash_session_id de la
// venta, igual que ya hacía para el TenantCashMovement de un pago en efectivo.
func TestRecordPayment_bankAccountMovementCarriesCashSessionID(t *testing.T) {
	db := setupRecordPaymentSessionTestDB(t)
	svc := NewCashBankService(db)

	session := &database.TenantCashSession{BranchID: 1, UserID: 5, OpenedBy: 5, Status: "open"}
	if err := db.Create(session).Error; err != nil {
		t.Fatal(err)
	}
	acc := &database.TenantBankAccount{Name: "Yape", Type: "wallet", Active: true}
	if err := db.Create(acc).Error; err != nil {
		t.Fatal(err)
	}
	pm := &database.TenantPaymentMethod{
		Code: "yape", Name: "Yape", BankAccountID: &acc.ID, Active: true, DestinationType: "bank_account",
	}
	if err := db.Create(pm).Error; err != nil {
		t.Fatal(err)
	}

	saleID := uint(42)
	if err := svc.RecordPayment(db, "yape", 100, &session.ID, "F001-1", "Venta F001-1", &saleID, 5); err != nil {
		t.Fatal(err)
	}

	var mov database.TenantBankMovement
	if err := db.Where("bank_account_id = ?", acc.ID).First(&mov).Error; err != nil {
		t.Fatal(err)
	}
	if mov.CashSessionID == nil || *mov.CashSessionID != session.ID {
		t.Fatalf("cash_session_id del movimiento bancario = %v, want %d", mov.CashSessionID, session.ID)
	}
}

// Movimiento manual (ingreso/egreso sin venta) por un método no efectivo: AddMovement debe
// propagar su propia sesión al TenantBankMovement que crea RecordPaymentToAccount.
func TestAddMovement_bankAccountMovementCarriesCashSessionID(t *testing.T) {
	db := setupRecordPaymentSessionTestDB(t)
	svc := NewCashBankService(db)

	session := &database.TenantCashSession{BranchID: 1, UserID: 5, OpenedBy: 5, Status: "open"}
	if err := db.Create(session).Error; err != nil {
		t.Fatal(err)
	}
	acc := &database.TenantBankAccount{Name: "Cta transferencias", Type: "bank", PaymentMethod: "transferencia", Active: true}
	if err := db.Create(acc).Error; err != nil {
		t.Fatal(err)
	}

	if err := svc.AddMovement(session.ID, 5, "income", "Aporte", "", "transferencia", 250, "", nil); err != nil {
		t.Fatal(err)
	}

	var mov database.TenantBankMovement
	if err := db.Where("bank_account_id = ?", acc.ID).First(&mov).Error; err != nil {
		t.Fatal(err)
	}
	if mov.CashSessionID == nil || *mov.CashSessionID != session.ID {
		t.Fatalf("cash_session_id del movimiento manual = %v, want %d", mov.CashSessionID, session.ID)
	}
}
