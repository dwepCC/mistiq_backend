package service

import (
	"testing"

	"tukifac/pkg/database"

	"gorm.io/gorm"
)

func newCashMovement(t *testing.T, db *gorm.DB, sessionID uint, movType, paymentMethod string, amount float64) {
	t.Helper()
	m := &database.TenantCashMovement{
		CashSessionID: sessionID, Type: movType, Amount: amount,
		PaymentMethod: paymentMethod, Category: "test", UserID: 7,
	}
	if err := db.Create(m).Error; err != nil {
		t.Fatal(err)
	}
}

// El bug real: getExpectedBalance sumaba/restaba CUALQUIER movimiento de la sesión sin mirar el
// método de pago — un egreso pagado por transferencia (plata que salió del banco, no del cajón)
// restaba igual que uno en efectivo. Con una caja que también registra pagos a proveedores por
// transferencia, el "esperado" podía dar negativo, algo imposible con billetes reales.
func TestGetExpectedBalance_transferExpenseDoesNotReduceCash(t *testing.T) {
	db := setupSessionAdminDB(t)
	svc := NewCashBankService(db)
	session := newSession(t, db, 665.00)

	newCashMovement(t, db, session.ID, "expense", "transferencia", 700.00)

	got := svc.getExpectedBalance(session.ID)
	if got != 665.00 {
		t.Errorf("esperado = %v, want 665.00 (el egreso por transferencia no debe tocar el efectivo físico)", got)
	}
}

// Egreso en efectivo sí debe restar — confirma que el fix no terminó ignorando TODOS los egresos,
// solo los que no son efectivo.
func TestGetExpectedBalance_cashExpenseReducesCash(t *testing.T) {
	db := setupSessionAdminDB(t)
	svc := NewCashBankService(db)
	session := newSession(t, db, 665.00)

	newCashMovement(t, db, session.ID, "expense", "efectivo", 100.00)

	got := svc.getExpectedBalance(session.ID)
	if got != 565.00 {
		t.Errorf("esperado = %v, want 565.00 (665 - 100 de egreso en efectivo)", got)
	}
}

// Caso real de producción que motivó el fix (tenant pizarromendiguri, sesión #4): ingresos en
// efectivo suman, egresos en efectivo restan, egresos por transferencia NO restan — mezclando
// los tres tipos en la misma sesión, como pasa en la práctica.
func TestGetExpectedBalance_mixedCashAndTransferMovements(t *testing.T) {
	db := setupSessionAdminDB(t)
	svc := NewCashBankService(db)
	session := newSession(t, db, 665.00)

	newCashMovement(t, db, session.ID, "income", "cash", 500.00) // "cash" y "efectivo" son el mismo canal
	newCashMovement(t, db, session.ID, "expense", "efectivo", 706.00)
	newCashMovement(t, db, session.ID, "expense", "transferencia", 700.00)

	got := svc.getExpectedBalance(session.ID)
	want := 665.00 + 500.00 - 706.00 // el egreso por transferencia queda afuera
	if got != want {
		t.Errorf("esperado = %v, want %v (665 + 500 ingreso efectivo - 706 egreso efectivo, sin restar los 700 de transferencia)", got, want)
	}
	if got < 0 {
		t.Errorf("un saldo esperado de caja física nunca debería dar negativo, got %v", got)
	}
}

// sessionMovementTotals (usada por ListOpenSessionsInBranch) debe respetar el mismo criterio —
// antes tenía la misma fórmula duplicada sin el fix.
func TestSessionMovementTotals_ignoresTransferMovements(t *testing.T) {
	db := setupSessionAdminDB(t)
	svc := NewCashBankService(db)
	session := newSession(t, db, 0)

	newCashMovement(t, db, session.ID, "income", "efectivo", 200.00)
	newCashMovement(t, db, session.ID, "expense", "transferencia", 150.00)

	income, expense := svc.sessionMovementTotals(session.ID)
	if income != 200.00 || expense != 0 {
		t.Errorf("income=%v expense=%v, want income=200 expense=0 (transferencia no debe restar)", income, expense)
	}
}
