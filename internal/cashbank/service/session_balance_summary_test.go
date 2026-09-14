package service

import (
	"fmt"
	"testing"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func setupSessionBalanceSummaryTestDB(t *testing.T) *gorm.DB {
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

// Caso del reporte del usuario: venta mixta S/300 = 100 efectivo + 100 Yape + 100 transferencia
// en la misma sesión. El resumen debe traer los tres métodos por separado y el total correcto —
// y CashExpected (arqueo) debe coincidir exactamente con getExpectedBalance (solo el efectivo).
func TestGetSessionBalanceSummary_mixedPaymentSale(t *testing.T) {
	db := setupSessionBalanceSummaryTestDB(t)
	svc := NewCashBankService(db)

	session := &database.TenantCashSession{BranchID: 1, UserID: 1, OpenedBy: 1, Status: "open", OpeningBalance: 50}
	if err := db.Create(session).Error; err != nil {
		t.Fatal(err)
	}

	yapeAcc := &database.TenantBankAccount{Name: "Yape", Type: "wallet", Active: true}
	transfAcc := &database.TenantBankAccount{Name: "BCP", Type: "bank", Active: true}
	db.Create(yapeAcc)
	db.Create(transfAcc)
	db.Create(&database.TenantPaymentMethod{Code: "yape", Name: "Yape", BankAccountID: &yapeAcc.ID, Active: true, DestinationType: "bank_account"})
	db.Create(&database.TenantPaymentMethod{Code: "transferencia", Name: "Transferencia", BankAccountID: &transfAcc.ID, Active: true, DestinationType: "bank_account"})
	db.Create(&database.TenantPaymentMethod{Code: "cash", Name: "Efectivo", IsSystem: true, Active: true, DestinationType: "cash"})

	saleID := uint(1)
	if err := svc.RecordPayment(db, "cash", 100, &session.ID, "F001-1", "Venta F001-1", &saleID, 1); err != nil {
		t.Fatal(err)
	}
	if err := svc.RecordPayment(db, "yape", 100, &session.ID, "F001-1", "Venta F001-1", &saleID, 1); err != nil {
		t.Fatal(err)
	}
	if err := svc.RecordPayment(db, "transferencia", 100, &session.ID, "F001-1", "Venta F001-1", &saleID, 1); err != nil {
		t.Fatal(err)
	}

	summary, err := svc.GetSessionBalanceSummary(session.ID)
	if err != nil {
		t.Fatal(err)
	}

	byMethod := map[string]MethodBalance{}
	for _, mb := range summary.ByMethod {
		byMethod[mb.Method] = mb
	}

	if got := byMethod["efectivo"].Net; got != 150 { // 50 apertura + 100 venta
		t.Errorf("efectivo = %v, want 150", got)
	}
	if !byMethod["efectivo"].IsCash {
		t.Error("efectivo debe marcarse IsCash=true")
	}
	if got := byMethod["yape"].Net; got != 100 {
		t.Errorf("yape = %v, want 100", got)
	}
	if byMethod["yape"].IsCash {
		t.Error("yape NO debe marcarse IsCash")
	}
	if got := byMethod["transferencia"].Net; got != 100 {
		t.Errorf("transferencia = %v, want 100", got)
	}

	wantTotal := 150.0 + 100.0 + 100.0
	if summary.Total != wantTotal {
		t.Errorf("total = %v, want %v", summary.Total, wantTotal)
	}

	// El corazón del bug original: CashExpected (lo que compara el arqueo) debe ser SOLO
	// efectivo — igual a getExpectedBalance — nunca el total de todos los métodos.
	wantExpected := svc.getExpectedBalance(session.ID)
	if summary.CashExpected != wantExpected {
		t.Errorf("cash_expected = %v, want %v (getExpectedBalance)", summary.CashExpected, wantExpected)
	}
	if summary.CashExpected != 150 {
		t.Errorf("cash_expected = %v, want 150 (NO debe incluir yape/transferencia)", summary.CashExpected)
	}
	if summary.CashExpected == summary.Total {
		t.Error("cash_expected no debería coincidir con el total cuando hay métodos no efectivo — si coinciden, algo está sumando de más o de menos")
	}
}

// Sesión sin ningún movimiento: debe seguir mostrando "efectivo" con la apertura, sin reventar.
func TestGetSessionBalanceSummary_emptySessionShowsOpeningCash(t *testing.T) {
	db := setupSessionBalanceSummaryTestDB(t)
	svc := NewCashBankService(db)

	session := &database.TenantCashSession{BranchID: 1, UserID: 1, OpenedBy: 1, Status: "open", OpeningBalance: 200}
	if err := db.Create(session).Error; err != nil {
		t.Fatal(err)
	}

	summary, err := svc.GetSessionBalanceSummary(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(summary.ByMethod) != 1 || summary.ByMethod[0].Method != "efectivo" {
		t.Fatalf("esperaba solo 'efectivo', got %+v", summary.ByMethod)
	}
	if summary.ByMethod[0].Net != 200 || summary.Total != 200 || summary.CashExpected != 200 {
		t.Errorf("esperaba 200 en todos los totales, got net=%v total=%v expected=%v",
			summary.ByMethod[0].Net, summary.Total, summary.CashExpected)
	}
	// La apertura solo debe sumarse al Net, nunca al Income — si un consumidor (p. ej. un modal
	// que muestre "Ingresos: S/ X") lee Income esperando "solo lo que entró en la sesión", no
	// debe encontrarse la apertura mezclada ahí (sin movimientos, Income debe ser 0).
	if summary.ByMethod[0].Income != 0 {
		t.Errorf("Income = %v, want 0 (la apertura va solo en Net, no en Income)", summary.ByMethod[0].Income)
	}
}

// Egreso manual por transferencia: no debe afectar cash_expected, sí debe restar del total y
// del saldo de "transferencia" — replica el bug histórico de getExpectedBalance con otro punto
// de entrada (AddMovement en vez de RecordPayment).
func TestGetSessionBalanceSummary_manualTransferExpenseDoesNotTouchCashExpected(t *testing.T) {
	db := setupSessionBalanceSummaryTestDB(t)
	svc := NewCashBankService(db)

	session := &database.TenantCashSession{BranchID: 1, UserID: 1, OpenedBy: 1, Status: "open", OpeningBalance: 665}
	if err := db.Create(session).Error; err != nil {
		t.Fatal(err)
	}
	acc := &database.TenantBankAccount{Name: "Cta transferencias", Type: "bank", PaymentMethod: "transferencia", Active: true}
	db.Create(acc)

	if err := svc.AddMovement(session.ID, 1, "expense", "Pago proveedor", "", "transferencia", 700, "", nil); err != nil {
		t.Fatal(err)
	}

	summary, err := svc.GetSessionBalanceSummary(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if summary.CashExpected != 665 {
		t.Errorf("cash_expected = %v, want 665 (el egreso por transferencia no debe tocar el efectivo físico)", summary.CashExpected)
	}
	if summary.Total != -35 { // 665 efectivo - 700 egreso transferencia
		t.Errorf("total = %v, want -35", summary.Total)
	}
}
