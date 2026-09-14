package service

import (
	"fmt"
	"testing"
	"time"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// Test directo de CloseSession — antes de esta ronda de deudas técnicas, el cierre de sesión no
// tenía ningún test propio en este repo (solo se ejercitaba indirectamente). Cubre: cierre
// correcto sin arqueo, cálculo del saldo esperado (solo efectivo, igual que getExpectedBalance),
// cierre con arqueo exacto, cierre con diferencia de arqueo, los rechazos existentes (sesión ya
// cerrada, sesión inexistente, dueño distinto sin permiso elevado) y que, tras cerrar, la sesión
// queda cerrada y consultable con los valores correctos. No se cambia la lógica de CloseSession —
// solo se agrega evidencia de que el comportamiento actual es el esperado.

func setupCloseSessionTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []interface{}{
		&database.TenantCashSession{}, &database.TenantCashMovement{},
		&database.TenantPaymentMethod{}, &database.TenantBankAccount{}, &database.TenantBankMovement{},
	} {
		if err := db.AutoMigrate(m); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

// 1. Cierre correcto sin arqueo: el saldo esperado se calcula SOLO con efectivo (un egreso por
// transferencia de por medio no debe alterarlo), closing_balance queda con lo que se pasó, y la
// diferencia es 0 cuando coincide con lo esperado.
func TestCloseSession_correctoSinArqueo_calculaSaldoEsperadoSoloEfectivo(t *testing.T) {
	db := setupCloseSessionTestDB(t)
	svc := NewCashBankService(db)

	session := &database.TenantCashSession{BranchID: 1, UserID: 1, OpenedBy: 1, Status: "open", OpeningBalance: 100, OpenedAt: time.Now()}
	if err := db.Create(session).Error; err != nil {
		t.Fatal(err)
	}
	db.Create(&database.TenantCashMovement{CashSessionID: session.ID, Type: "income", Amount: 300, PaymentMethod: "efectivo"})
	db.Create(&database.TenantCashMovement{CashSessionID: session.ID, Type: "expense", Amount: 200, PaymentMethod: "transferencia"})

	wantExpected := 400.0 // 100 apertura + 300 ingreso efectivo; el egreso por transferencia NO resta
	if got := svc.getExpectedBalance(session.ID); got != wantExpected {
		t.Fatalf("getExpectedBalance = %v, want %v (fixture inconsistente)", got, wantExpected)
	}

	if err := svc.CloseSession(session.ID, 1, wantExpected, "cierre normal", nil, false); err != nil {
		t.Fatalf("no se esperaba error: %v", err)
	}

	var closed database.TenantCashSession
	if err := db.First(&closed, session.ID).Error; err != nil {
		t.Fatal(err)
	}
	if closed.Status != "closed" {
		t.Fatalf("status = %q, want closed", closed.Status)
	}
	if closed.ExpectedBalance == nil || *closed.ExpectedBalance != wantExpected {
		t.Errorf("expected_balance = %v, want %v", closed.ExpectedBalance, wantExpected)
	}
	if closed.ClosingBalance == nil || *closed.ClosingBalance != wantExpected {
		t.Errorf("closing_balance = %v, want %v", closed.ClosingBalance, wantExpected)
	}
	if closed.Difference == nil || *closed.Difference != 0 {
		t.Errorf("difference = %v, want 0", closed.Difference)
	}
	if closed.ClosedBy == nil || *closed.ClosedBy != 1 {
		t.Errorf("closed_by = %v, want 1", closed.ClosedBy)
	}
	if closed.ClosedAt == nil {
		t.Error("closed_at no debe quedar nil")
	}
}

// 2. Cierre con arqueo exacto (denominaciones que suman igual al esperado): closing_balance debe
// ser la suma del arqueo, no el parámetro closingBalance, y la diferencia debe ser 0.
func TestCloseSession_conArqueoExacto(t *testing.T) {
	db := setupCloseSessionTestDB(t)
	svc := NewCashBankService(db)

	session := &database.TenantCashSession{BranchID: 1, UserID: 1, OpenedBy: 1, Status: "open", OpeningBalance: 0, OpenedAt: time.Now()}
	if err := db.Create(session).Error; err != nil {
		t.Fatal(err)
	}
	db.Create(&database.TenantCashMovement{CashSessionID: session.ID, Type: "income", Amount: 250, PaymentMethod: "efectivo"})

	arqueo := map[string]float64{"100": 2, "50": 1} // 200 + 50 = 250
	if err := svc.CloseSession(session.ID, 1, 0, "", arqueo, false); err != nil {
		t.Fatalf("no se esperaba error: %v", err)
	}

	var closed database.TenantCashSession
	if err := db.First(&closed, session.ID).Error; err != nil {
		t.Fatal(err)
	}
	if closed.ClosingBalance == nil || *closed.ClosingBalance != 250 {
		t.Errorf("closing_balance = %v, want 250 (suma del arqueo, no el parámetro)", closed.ClosingBalance)
	}
	if closed.Difference == nil || *closed.Difference != 0 {
		t.Errorf("difference = %v, want 0", closed.Difference)
	}
	if closed.ArqueoJSON == "" {
		t.Error("arqueo_json no debe quedar vacío cuando se envía arqueo")
	}
}

// 3. Cierre con diferencia de arqueo: el arqueo suma menos que lo esperado — difference debe ser
// negativa y exacta (faltante de caja).
func TestCloseSession_conArqueoConDiferencia(t *testing.T) {
	db := setupCloseSessionTestDB(t)
	svc := NewCashBankService(db)

	session := &database.TenantCashSession{BranchID: 1, UserID: 1, OpenedBy: 1, Status: "open", OpeningBalance: 0, OpenedAt: time.Now()}
	if err := db.Create(session).Error; err != nil {
		t.Fatal(err)
	}
	db.Create(&database.TenantCashMovement{CashSessionID: session.ID, Type: "income", Amount: 250, PaymentMethod: "efectivo"})

	arqueo := map[string]float64{"100": 2} // 200, faltan 50
	if err := svc.CloseSession(session.ID, 1, 0, "", arqueo, false); err != nil {
		t.Fatalf("no se esperaba error: %v", err)
	}

	var closed database.TenantCashSession
	if err := db.First(&closed, session.ID).Error; err != nil {
		t.Fatal(err)
	}
	if closed.ClosingBalance == nil || *closed.ClosingBalance != 200 {
		t.Errorf("closing_balance = %v, want 200", closed.ClosingBalance)
	}
	if closed.ExpectedBalance == nil || *closed.ExpectedBalance != 250 {
		t.Errorf("expected_balance = %v, want 250", closed.ExpectedBalance)
	}
	if closed.Difference == nil || *closed.Difference != -50 {
		t.Errorf("difference = %v, want -50 (faltante)", closed.Difference)
	}
}

// 4. Rechazo: no se puede cerrar una sesión ya cerrada.
func TestCloseSession_rechazaSesionYaCerrada(t *testing.T) {
	db := setupCloseSessionTestDB(t)
	svc := NewCashBankService(db)

	session := &database.TenantCashSession{BranchID: 1, UserID: 1, OpenedBy: 1, Status: "closed", OpenedAt: time.Now()}
	if err := db.Create(session).Error; err != nil {
		t.Fatal(err)
	}

	if err := svc.CloseSession(session.ID, 1, 0, "", nil, false); err == nil {
		t.Fatal("se esperaba error por sesión ya cerrada, no hubo error")
	}
}

// 5. Rechazo: sesión inexistente.
func TestCloseSession_rechazaSesionInexistente(t *testing.T) {
	db := setupCloseSessionTestDB(t)
	svc := NewCashBankService(db)

	if err := svc.CloseSession(999, 1, 0, "", nil, false); err == nil {
		t.Fatal("se esperaba error por sesión inexistente, no hubo error")
	}
}

// 6. Rechazo: un usuario distinto al dueño no puede cerrar la sesión sin el permiso elevado
// (allowAnyOwner). Con allowAnyOwner=true, sí puede (comportamiento existente, ya usado por el
// permiso "cerrar cualquier sesión").
func TestCloseSession_duenoDistinto_rechazaSalvoPermisoElevado(t *testing.T) {
	db := setupCloseSessionTestDB(t)
	svc := NewCashBankService(db)

	session := &database.TenantCashSession{BranchID: 1, UserID: 7, OpenedBy: 7, Status: "open", OpenedAt: time.Now()}
	if err := db.Create(session).Error; err != nil {
		t.Fatal(err)
	}

	if err := svc.CloseSession(session.ID, 5, 0, "", nil, false); err == nil {
		t.Fatal("se esperaba error por sesión de otro usuario sin permiso elevado")
	}

	if err := svc.CloseSession(session.ID, 5, 0, "", nil, true); err != nil {
		t.Fatalf("con allowAnyOwner=true no se esperaba error: %v", err)
	}
	var closed database.TenantCashSession
	if err := db.First(&closed, session.ID).Error; err != nil {
		t.Fatal(err)
	}
	if closed.Status != "closed" {
		t.Errorf("status = %q, want closed (permiso elevado debe poder cerrarla)", closed.Status)
	}
}

// 7. Tras cerrar, la sesión queda cerrada y consultable (no se borra ni se oculta), y un segundo
// intento de cierre es rechazado.
func TestCloseSession_sesionCerradaQuedaConsultableYNoSePuedeRecerrar(t *testing.T) {
	db := setupCloseSessionTestDB(t)
	svc := NewCashBankService(db)

	session := &database.TenantCashSession{BranchID: 1, UserID: 1, OpenedBy: 1, Status: "open", OpeningBalance: 80, OpenedAt: time.Now()}
	if err := db.Create(session).Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.CloseSession(session.ID, 1, 80, "", nil, false); err != nil {
		t.Fatal(err)
	}

	var closed database.TenantCashSession
	if err := db.First(&closed, session.ID).Error; err != nil {
		t.Fatalf("la sesión cerrada debe seguir siendo consultable: %v", err)
	}
	if closed.Status != "closed" {
		t.Fatalf("status = %q, want closed", closed.Status)
	}

	if err := svc.CloseSession(session.ID, 1, 80, "", nil, false); err == nil {
		t.Fatal("se esperaba error al intentar cerrar una sesión ya cerrada por segunda vez")
	}
}
