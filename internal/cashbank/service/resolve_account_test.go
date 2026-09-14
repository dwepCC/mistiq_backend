package service

import (
	"fmt"
	"testing"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func setupResolveAccountTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []interface{}{
		&database.TenantPaymentMethod{}, &database.TenantBankAccount{}, &database.TenantBankMovement{},
		&database.TenantCashSession{}, &database.TenantCashMovement{},
	} {
		if err := db.AutoMigrate(m); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

// Fase 4: cuando ambas vías apuntan a cuentas DISTINTAS (configuración inconsistente real), el
// FK (tenant_payment_methods.bank_account_id) debe ganar — es la fuente principal.
func TestResolveAccountForPaymentMethod_prefersFKOverLegacyText(t *testing.T) {
	db := setupResolveAccountTestDB(t)
	svc := NewCashBankService(db)

	legacyAcc := &database.TenantBankAccount{Name: "Cuenta vieja (texto)", Type: "bank", PaymentMethod: "transferencia", Active: true}
	fkAcc := &database.TenantBankAccount{Name: "Cuenta nueva (FK)", Type: "bank", Active: true}
	db.Create(legacyAcc)
	db.Create(fkAcc)
	db.Create(&database.TenantPaymentMethod{Code: "transferencia", Name: "Transferencia", BankAccountID: &fkAcc.ID, Active: true, DestinationType: "bank_account"})

	acc, err := svc.resolveAccountForPaymentMethod("transferencia")
	if err != nil {
		t.Fatal(err)
	}
	if acc == nil || acc.ID != fkAcc.ID {
		t.Fatalf("esperaba la cuenta del FK (%d), got %+v", fkAcc.ID, acc)
	}
}

// Sin FK configurado (tenant viejo que solo usó el texto), debe seguir funcionando por el
// fallback legado — no romper configuraciones existentes.
func TestResolveAccountForPaymentMethod_fallsBackToLegacyTextWhenNoFK(t *testing.T) {
	db := setupResolveAccountTestDB(t)
	svc := NewCashBankService(db)

	legacyAcc := &database.TenantBankAccount{Name: "Solo texto legado", Type: "wallet", PaymentMethod: "yape", Active: true}
	db.Create(legacyAcc)
	// Método existe pero sin bank_account_id (configuración vieja, o alguien lo creó sin
	// terminar de vincular la cuenta).
	db.Create(&database.TenantPaymentMethod{Code: "yape", Name: "Yape", Active: true, DestinationType: "bank_account"})

	acc, err := svc.resolveAccountForPaymentMethod("yape")
	if err != nil {
		t.Fatal(err)
	}
	if acc == nil || acc.ID != legacyAcc.ID {
		t.Fatalf("esperaba caer al texto legado (%d), got %+v", legacyAcc.ID, acc)
	}
}

// Sin ningún TenantPaymentMethod registrado en absoluto (tenant muy viejo) — debe seguir
// resolviendo por el texto legado, comportamiento sin cambios respecto a antes de la Fase 4.
func TestResolveAccountForPaymentMethod_worksWithoutAnyPaymentMethodRecord(t *testing.T) {
	db := setupResolveAccountTestDB(t)
	svc := NewCashBankService(db)

	legacyAcc := &database.TenantBankAccount{Name: "Efectivo legado", Type: "cash", PaymentMethod: "efectivo", Active: true}
	db.Create(legacyAcc)

	acc, err := svc.resolveAccountForPaymentMethod("efectivo")
	if err != nil {
		t.Fatal(err)
	}
	if acc == nil || acc.ID != legacyAcc.ID {
		t.Fatalf("esperaba la cuenta legada (%d), got %+v", legacyAcc.ID, acc)
	}
}

// Integración: un ingreso manual (AddMovement) por un método cuyo FK y texto legado apuntan a
// cuentas distintas debe actualizar el saldo de la cuenta del FK, no la del texto.
func TestAddMovement_usesFKAccountOverLegacyText(t *testing.T) {
	db := setupResolveAccountTestDB(t)
	svc := NewCashBankService(db)

	session := &database.TenantCashSession{BranchID: 1, UserID: 1, OpenedBy: 1, Status: "open"}
	db.Create(session)

	legacyAcc := &database.TenantBankAccount{Name: "Vieja", Type: "wallet", PaymentMethod: "plin", Balance: 0, Active: true}
	fkAcc := &database.TenantBankAccount{Name: "Nueva", Type: "wallet", Balance: 0, Active: true}
	db.Create(legacyAcc)
	db.Create(fkAcc)
	db.Create(&database.TenantPaymentMethod{Code: "plin", Name: "Plin", BankAccountID: &fkAcc.ID, Active: true, DestinationType: "bank_account"})

	if err := svc.AddMovement(session.ID, 1, "income", "Aporte", "", "plin", 300, "", nil); err != nil {
		t.Fatal(err)
	}

	var loadedFK, loadedLegacy database.TenantBankAccount
	db.First(&loadedFK, fkAcc.ID)
	db.First(&loadedLegacy, legacyAcc.ID)
	if loadedFK.Balance != 300 {
		t.Errorf("cuenta del FK: balance = %v, want 300", loadedFK.Balance)
	}
	if loadedLegacy.Balance != 0 {
		t.Errorf("cuenta del texto legado no debió tocarse: balance = %v, want 0", loadedLegacy.Balance)
	}
}
