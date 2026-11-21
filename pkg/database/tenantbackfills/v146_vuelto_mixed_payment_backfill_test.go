package tenantbackfills

import (
	"fmt"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"tukifac/pkg/money"
)

// Modelos mínimos para el test — mismo patrón que v036_sale_payment_cash_session_backfill_test.go.
type v146Sale struct {
	ID            uint `gorm:"primaryKey"`
	Total         float64
	CashSessionID *uint
}

func (v146Sale) TableName() string { return "tenant_sales" }

type v146CashSession struct {
	ID     uint `gorm:"primaryKey"`
	Status string
}

func (v146CashSession) TableName() string { return "tenant_cash_sessions" }

type v146BankAccount struct {
	ID      uint `gorm:"primaryKey"`
	Balance float64
}

func (v146BankAccount) TableName() string { return "tenant_bank_accounts" }

func setupVueltoBackfillDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&v146Sale{}, &v146CashSession{}, &v146BankAccount{}, &tsalePayment{}, &tcashMovement{}, &tbankMovement{}); err != nil {
		t.Fatal(err)
	}
	return db
}

// Escenario real de producción (tenant doriconta, venta NV001-00000746): cash+plin mixto con
// vuelto, sesión abierta. El movimiento de caja debe corregirse a 200 (300 - 100 de vuelto); el
// de plin vuelve a su monto íntegro (400), tal cual se ingresó.
func TestV146_MixedCashPlin_OpenSession_CorrectsCashOnly(t *testing.T) {
	db := setupVueltoBackfillDB(t)
	sess := uint(1)
	db.Create(&v146CashSession{ID: sess, Status: "open"})
	db.Create(&v146Sale{ID: 1, Total: 600, CashSessionID: &sess})
	db.Create(&tsalePayment{ID: 1, SaleID: 1, Method: "plin", Amount: 400})
	db.Create(&tsalePayment{ID: 2, SaleID: 1, Method: "cash", Amount: 300})
	// Saldo cacheado de la billetera Plin, ya incrementado con el monto viejo (342.86) al
	// registrar la venta — mismo que hace CashBankService.RecordPaymentToAccount.
	db.Create(&v146BankAccount{ID: 9, Balance: 1000})
	// Montos actuales = lo que produjo la fórmula ANTERIOR (proporcional entre las 2 líneas).
	db.Create(&tcashMovement{ID: 1, CashSessionID: sess, Type: "income", Amount: 257.14, SaleID: saleIDPtr(1)})
	db.Create(&tbankMovement{ID: 1, CashSessionID: &sess, Type: "credit", Amount: 342.86, SaleID: saleIDPtr(1), BankAccountID: 9})

	bf := V146VueltoMixedPaymentBackfill{}
	result, err := bf.Diagnose(db)
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	if result.SalesAnalyzed != 1 {
		t.Fatalf("expected 1 venta analizada, got %d", result.SalesAnalyzed)
	}
	// Regresión: Diagnose no escribe nada, pero debe reportar cuántos movimientos CORREGIRÍA
	// Run — no dejar esto en 0 solo porque Diagnose nunca llega a escribir.
	if result.MovementsFixed != 2 {
		t.Fatalf("Diagnose debe previsualizar 2 movimientos a corregir (cash+plin), got %d", result.MovementsFixed)
	}

	if err := bf.Run(db); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var cm tcashMovement
	db.First(&cm, 1)
	if cm.Amount != 200 {
		t.Fatalf("efectivo: expected 200, got %v", cm.Amount)
	}
	var bm tbankMovement
	db.First(&bm, 1)
	if bm.Amount != 400 {
		t.Fatalf("plin: expected 400 (intacto), got %v", bm.Amount)
	}
	// El saldo cacheado de la cuenta debe moverse por el mismo delta (+57.14) que el UPDATE del
	// movimiento — de 1000 a 1057.14 — o la cuenta quedaría desalineada con su propio historial.
	var acc v146BankAccount
	db.First(&acc, 9)
	if money.RoundDisplay(acc.Balance) != 1057.14 {
		t.Fatalf("saldo cuenta bancaria: expected 1057.14, got %v", acc.Balance)
	}
}

// Sesión ya CERRADA: no se toca nada, aunque el patrón sea idéntico al caso anterior.
func TestV146_ClosedSession_NeverTouched(t *testing.T) {
	db := setupVueltoBackfillDB(t)
	sess := uint(2)
	db.Create(&v146CashSession{ID: sess, Status: "closed"})
	db.Create(&v146Sale{ID: 2, Total: 600, CashSessionID: &sess})
	db.Create(&tsalePayment{ID: 3, SaleID: 2, Method: "plin", Amount: 400})
	db.Create(&tsalePayment{ID: 4, SaleID: 2, Method: "cash", Amount: 300})
	db.Create(&tcashMovement{ID: 2, CashSessionID: sess, Type: "income", Amount: 257.14, SaleID: saleIDPtr(2)})
	db.Create(&tbankMovement{ID: 2, CashSessionID: &sess, Type: "credit", Amount: 342.86, SaleID: saleIDPtr(2)})

	bf := V146VueltoMixedPaymentBackfill{}
	result, err := bf.Diagnose(db)
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	if result.SalesAnalyzed != 0 {
		t.Fatalf("expected 0 ventas analizadas (sesión cerrada), got %d", result.SalesAnalyzed)
	}

	if err := bf.Run(db); err != nil {
		t.Fatalf("Run: %v", err)
	}
	var cm tcashMovement
	db.First(&cm, 2)
	if cm.Amount != 257.14 {
		t.Fatalf("no debía tocarse, got %v", cm.Amount)
	}
}

// Escenario real (tenant juanpedro, venta NV001-00000003): DOS líneas de efectivo + una
// electrónica, vuelto igual al efectivo total disponible -> ambas líneas de efectivo deben quedar
// en 0; la electrónica intacta.
func TestV146_MultipleCashLines_BothZeroed(t *testing.T) {
	db := setupVueltoBackfillDB(t)
	sess := uint(3)
	db.Create(&v146CashSession{ID: sess, Status: "open"})
	db.Create(&v146Sale{ID: 3, Total: 16, CashSessionID: &sess})
	db.Create(&tsalePayment{ID: 5, SaleID: 3, Method: "yape", Amount: 16})
	db.Create(&tsalePayment{ID: 6, SaleID: 3, Method: "cash", Amount: 12})
	db.Create(&tsalePayment{ID: 7, SaleID: 3, Method: "cash", Amount: 25})
	// Montos actuales = fórmula anterior (proporcional entre las 3 líneas, sum=53, payable=16).
	// yape no generó movimiento bancario (método sin cuenta configurada) — caso real observado.
	db.Create(&tcashMovement{ID: 3, CashSessionID: sess, Type: "income", Amount: 3.62, SaleID: saleIDPtr(3)})
	db.Create(&tcashMovement{ID: 4, CashSessionID: sess, Type: "income", Amount: 7.55, SaleID: saleIDPtr(3)})

	bf := V146VueltoMixedPaymentBackfill{}
	if err := bf.Run(db); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var m3, m4 tcashMovement
	db.First(&m3, 3)
	db.First(&m4, 4)
	if m3.Amount != 0 || m4.Amount != 0 {
		t.Fatalf("ambas lineas de efectivo deben quedar en 0, got %v y %v", m3.Amount, m4.Amount)
	}
}

// Anomalía (vuelto > efectivo disponible, tipo guillenbegazo real): nunca se corrige a ciegas,
// se cuenta como "requiere revisión manual".
func TestV146_ChangeExceedsCash_NeedsReview(t *testing.T) {
	db := setupVueltoBackfillDB(t)
	sess := uint(4)
	db.Create(&v146CashSession{ID: sess, Status: "open"})
	db.Create(&v146Sale{ID: 4, Total: 0.01, CashSessionID: &sess})
	db.Create(&tsalePayment{ID: 8, SaleID: 4, Method: "tarjeta", Amount: 2588})
	db.Create(&tsalePayment{ID: 9, SaleID: 4, Method: "cash", Amount: 0})

	bf := V146VueltoMixedPaymentBackfill{}
	result, err := bf.Diagnose(db)
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	if result.SalesNeedsReview != 1 {
		t.Fatalf("expected 1 venta que requiere revision, got %d (analizadas=%d)", result.SalesNeedsReview, result.SalesAnalyzed)
	}
}

// Idempotencia: correrlo dos veces produce el mismo resultado final, sin doble-corrección.
func TestV146_Idempotent(t *testing.T) {
	db := setupVueltoBackfillDB(t)
	sess := uint(5)
	db.Create(&v146CashSession{ID: sess, Status: "open"})
	db.Create(&v146Sale{ID: 5, Total: 120, CashSessionID: &sess})
	db.Create(&tsalePayment{ID: 10, SaleID: 5, Method: "cash", Amount: 100})
	db.Create(&tsalePayment{ID: 11, SaleID: 5, Method: "yape", Amount: 50})
	db.Create(&v146BankAccount{ID: 7, Balance: 500})
	// Fórmula anterior: sum=150, payable=120 -> cash=80, yape=40.
	db.Create(&tcashMovement{ID: 5, CashSessionID: sess, Type: "income", Amount: 80, SaleID: saleIDPtr(5)})
	db.Create(&tbankMovement{ID: 3, CashSessionID: &sess, Type: "credit", Amount: 40, SaleID: saleIDPtr(5), BankAccountID: 7})

	bf := V146VueltoMixedPaymentBackfill{}
	if err := bf.Run(db); err != nil {
		t.Fatalf("Run 1: %v", err)
	}
	var cm tcashMovement
	db.First(&cm, 5)
	if cm.Amount != 70 {
		t.Fatalf("efectivo: expected 70, got %v", cm.Amount)
	}
	var bm tbankMovement
	db.First(&bm, 3)
	if bm.Amount != 50 {
		t.Fatalf("yape: expected 50, got %v", bm.Amount)
	}
	var acc v146BankAccount
	db.First(&acc, 7)
	if money.RoundDisplay(acc.Balance) != 510 {
		t.Fatalf("saldo cuenta bancaria: expected 510 (500 + 10 de delta), got %v", acc.Balance)
	}

	// Segunda corrida: ya corregido, no debe volver a cambiar nada (ni fallar) — el saldo de la
	// cuenta tampoco debe moverse una segunda vez.
	result, err := bf.Diagnose(db)
	if err != nil {
		t.Fatalf("Diagnose 2: %v", err)
	}
	if result.SalesAnalyzed != 1 {
		t.Fatalf("la venta sigue siendo mixta con vuelto, expected 1 analizada, got %d", result.SalesAnalyzed)
	}
	if err := bf.Run(db); err != nil {
		t.Fatalf("Run 2: %v", err)
	}
	db.First(&cm, 5)
	db.First(&bm, 3)
	if cm.Amount != 70 || bm.Amount != 50 {
		t.Fatalf("no debia cambiar en la segunda corrida, got cash=%v yape=%v", cm.Amount, bm.Amount)
	}
	db.First(&acc, 7)
	if money.RoundDisplay(acc.Balance) != 510 {
		t.Fatalf("saldo cuenta bancaria no debia moverse una segunda vez, expected 510, got %v", acc.Balance)
	}
}
