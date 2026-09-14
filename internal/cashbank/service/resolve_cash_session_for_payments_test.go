package service

import (
	"fmt"
	"testing"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func setupResolveCashSessionForPaymentsTestDB(t *testing.T) *gorm.DB {
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

func nonCashPaymentLines() []PaymentLineInput {
	return []PaymentLineInput{{Method: "transferencia", Amount: 100}}
}

// Corrección de la brecha de validación: un pago que NO requiere efectivo (needsCash == false)
// pero que igual trae un cash_session_id (p. ej. enviado por el frontend para trazabilidad) debe
// validarse con la misma regla que ya se aplicaba a los pagos en efectivo — no puede pasar
// silenciosamente una sesión inexistente, cerrada o ajena.

func TestResolveCashSessionForPayments_nonCash_nonexistentSession_rejected(t *testing.T) {
	db := setupResolveCashSessionForPaymentsTestDB(t)
	svc := NewCashBankService(db)

	bogus := uint(999)
	_, err := svc.ResolveCashSessionForPayments(1, 5, &bogus, nonCashPaymentLines())
	if err == nil {
		t.Fatal("se esperaba error por sesión inexistente, no hubo error")
	}
}

func TestResolveCashSessionForPayments_nonCash_closedSession_rejected(t *testing.T) {
	db := setupResolveCashSessionForPaymentsTestDB(t)
	svc := NewCashBankService(db)

	session := &database.TenantCashSession{BranchID: 1, UserID: 5, OpenedBy: 5, Status: "closed"}
	if err := db.Create(session).Error; err != nil {
		t.Fatal(err)
	}

	_, err := svc.ResolveCashSessionForPayments(1, 5, &session.ID, nonCashPaymentLines())
	if err == nil {
		t.Fatal("se esperaba error por sesión cerrada, no hubo error")
	}
}

func TestResolveCashSessionForPayments_nonCash_otherUsersSession_rejected(t *testing.T) {
	db := setupResolveCashSessionForPaymentsTestDB(t)
	svc := NewCashBankService(db)

	// Sesión abierta, pero pertenece al usuario 7, no al 5 que hace la solicitud.
	session := &database.TenantCashSession{BranchID: 1, UserID: 7, OpenedBy: 7, Status: "open"}
	if err := db.Create(session).Error; err != nil {
		t.Fatal(err)
	}

	_, err := svc.ResolveCashSessionForPayments(1, 5, &session.ID, nonCashPaymentLines())
	if err == nil {
		t.Fatal("se esperaba error por sesión de otro usuario, no hubo error")
	}
}

func TestResolveCashSessionForPayments_nonCash_otherBranchSession_rejected(t *testing.T) {
	db := setupResolveCashSessionForPaymentsTestDB(t)
	svc := NewCashBankService(db)

	// Sesión abierta y del mismo usuario, pero de otra sucursal.
	session := &database.TenantCashSession{BranchID: 2, UserID: 5, OpenedBy: 5, Status: "open"}
	if err := db.Create(session).Error; err != nil {
		t.Fatal(err)
	}

	_, err := svc.ResolveCashSessionForPayments(1, 5, &session.ID, nonCashPaymentLines())
	if err == nil {
		t.Fatal("se esperaba error por sesión de otra sucursal, no hubo error")
	}
}

func TestResolveCashSessionForPayments_nonCash_validSession_accepted(t *testing.T) {
	db := setupResolveCashSessionForPaymentsTestDB(t)
	svc := NewCashBankService(db)

	session := &database.TenantCashSession{BranchID: 1, UserID: 5, OpenedBy: 5, Status: "open"}
	if err := db.Create(session).Error; err != nil {
		t.Fatal(err)
	}

	resolved, err := svc.ResolveCashSessionForPayments(1, 5, &session.ID, nonCashPaymentLines())
	if err != nil {
		t.Fatalf("no se esperaba error con sesión válida: %v", err)
	}
	if resolved == nil || *resolved != session.ID {
		t.Fatalf("se esperaba la sesión %d, se obtuvo %v", session.ID, resolved)
	}
}

// Sin cash_session_id (nil) y sin necesidad de efectivo, el comportamiento previo se mantiene:
// no se exige ni se valida ninguna sesión (p. ej. compras por transferencia, que nunca envían
// cash_session_id).
func TestResolveCashSessionForPayments_nonCash_nilSession_staysNil(t *testing.T) {
	db := setupResolveCashSessionForPaymentsTestDB(t)
	svc := NewCashBankService(db)

	resolved, err := svc.ResolveCashSessionForPayments(1, 5, nil, nonCashPaymentLines())
	if err != nil {
		t.Fatalf("no se esperaba error: %v", err)
	}
	if resolved != nil {
		t.Fatalf("se esperaba nil, se obtuvo %v", resolved)
	}
}
