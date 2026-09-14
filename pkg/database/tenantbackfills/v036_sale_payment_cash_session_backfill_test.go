package tenantbackfills

import (
	"fmt"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// Modelos mínimos para el test (evita depender del paquete database completo, mismo patrón que
// v034_product_codes_test.go / v035_merge_duplicate_contacts_test.go).
type tsalePayment struct {
	ID            uint `gorm:"primaryKey"`
	SaleID        uint
	Method        string
	Amount        float64
	CashSessionID *uint
}

func (tsalePayment) TableName() string { return "tenant_sale_payments" }

type tcashMovement struct {
	ID            uint `gorm:"primaryKey"`
	CashSessionID uint
	Type          string
	Amount        float64
	SaleID        *uint
}

func (tcashMovement) TableName() string { return "tenant_cash_movements" }

type tbankMovement struct {
	ID            uint `gorm:"primaryKey"`
	CashSessionID *uint
	Type          string
	Amount        float64
	SaleID        *uint
}

func (tbankMovement) TableName() string { return "tenant_bank_movements" }

func setupSalePaymentBackfillDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&tsalePayment{}, &tcashMovement{}, &tbankMovement{}); err != nil {
		t.Fatal(err)
	}
	return db
}

func saleIDPtr(v uint) *uint { return &v }

// Candidato único (efectivo): el único TenantCashMovement de esa venta/monto/clase resuelve el pago.
func TestV036_CandidatoUnicoEfectivo_Asigna(t *testing.T) {
	db := setupSalePaymentBackfillDB(t)
	db.Create(&tsalePayment{ID: 1, SaleID: 100, Method: "efectivo", Amount: 300})
	db.Create(&tcashMovement{ID: 1, CashSessionID: 30, Type: "income", Amount: 300, SaleID: saleIDPtr(100)})

	if err := (V036SalePaymentCashSessionBackfill{}).Run(db); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var p tsalePayment
	db.First(&p, 1)
	if p.CashSessionID == nil || *p.CashSessionID != 30 {
		t.Fatalf("cash_session_id = %v, want 30", p.CashSessionID)
	}
}

// Candidato único (no efectivo): el único TenantBankMovement resuelve el pago.
func TestV036_CandidatoUnicoNoEfectivo_Asigna(t *testing.T) {
	db := setupSalePaymentBackfillDB(t)
	db.Create(&tsalePayment{ID: 1, SaleID: 100, Method: "yape", Amount: 300})
	db.Create(&tbankMovement{ID: 1, CashSessionID: saleIDPtr(35), Type: "credit", Amount: 300, SaleID: saleIDPtr(100)})

	if err := (V036SalePaymentCashSessionBackfill{}).Run(db); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var p tsalePayment
	db.First(&p, 1)
	if p.CashSessionID == nil || *p.CashSessionID != 35 {
		t.Fatalf("cash_session_id = %v, want 35", p.CashSessionID)
	}
}

// Ningún candidato: no existe movimiento financiero para esa venta/monto — queda NULL.
func TestV036_SinCandidato_QuedaNull(t *testing.T) {
	db := setupSalePaymentBackfillDB(t)
	db.Create(&tsalePayment{ID: 1, SaleID: 100, Method: "efectivo", Amount: 300})

	if err := (V036SalePaymentCashSessionBackfill{}).Run(db); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var p tsalePayment
	db.First(&p, 1)
	if p.CashSessionID != nil {
		t.Fatalf("cash_session_id = %v, want nil (sin candidato)", *p.CashSessionID)
	}
}

// Múltiples candidatos del lado del movimiento: dos TenantCashMovement con el mismo sale_id/monto
// para un único pago pendiente — no hay forma de saber cuál lo generó. Queda NULL.
func TestV036_MultiplesCandidatosMovimiento_QuedaNull(t *testing.T) {
	db := setupSalePaymentBackfillDB(t)
	db.Create(&tsalePayment{ID: 1, SaleID: 100, Method: "efectivo", Amount: 300})
	db.Create(&tcashMovement{ID: 1, CashSessionID: 30, Type: "income", Amount: 300, SaleID: saleIDPtr(100)})
	db.Create(&tcashMovement{ID: 2, CashSessionID: 31, Type: "income", Amount: 300, SaleID: saleIDPtr(100)})

	if err := (V036SalePaymentCashSessionBackfill{}).Run(db); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var p tsalePayment
	db.First(&p, 1)
	if p.CashSessionID != nil {
		t.Fatalf("cash_session_id = %v, want nil (ambiguo: 2 movimientos candidatos)", *p.CashSessionID)
	}
}

// Múltiples pagos pendientes del mismo (sale_id, clase, monto): aunque exista igual cantidad de
// movimientos candidatos, no se puede saber cuál pago corresponde a cuál sesión sin inventar un
// criterio de orden — ambos quedan NULL.
func TestV036_MultiplesPagosMismoGrupo_QuedanNull(t *testing.T) {
	db := setupSalePaymentBackfillDB(t)
	db.Create(&tsalePayment{ID: 1, SaleID: 100, Method: "efectivo", Amount: 300})
	db.Create(&tsalePayment{ID: 2, SaleID: 100, Method: "efectivo", Amount: 300})
	db.Create(&tcashMovement{ID: 1, CashSessionID: 30, Type: "income", Amount: 300, SaleID: saleIDPtr(100)})
	db.Create(&tcashMovement{ID: 2, CashSessionID: 31, Type: "income", Amount: 300, SaleID: saleIDPtr(100)})

	if err := (V036SalePaymentCashSessionBackfill{}).Run(db); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var p1, p2 tsalePayment
	db.First(&p1, 1)
	db.First(&p2, 2)
	if p1.CashSessionID != nil || p2.CashSessionID != nil {
		t.Fatalf("ambos pagos debían quedar ambiguos: p1=%v p2=%v", p1.CashSessionID, p2.CashSessionID)
	}
}

// Un pago que YA tiene cash_session_id (poblado por el flujo nuevo, post-V123) no se toca, aunque
// exista un movimiento candidato que "encajaría".
func TestV036_PagoYaResuelto_NoSeModifica(t *testing.T) {
	db := setupSalePaymentBackfillDB(t)
	existing := uint(99)
	db.Create(&tsalePayment{ID: 1, SaleID: 100, Method: "efectivo", Amount: 300, CashSessionID: &existing})
	db.Create(&tcashMovement{ID: 1, CashSessionID: 30, Type: "income", Amount: 300, SaleID: saleIDPtr(100)})

	if err := (V036SalePaymentCashSessionBackfill{}).Run(db); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var p tsalePayment
	db.First(&p, 1)
	if p.CashSessionID == nil || *p.CashSessionID != 99 {
		t.Fatalf("cash_session_id = %v, want 99 (no debía tocarse)", p.CashSessionID)
	}
}

// Ejecución repetida: no duplica ni altera resultados ya asignados, y no cambia los que quedaron
// ambiguos/sin candidato si no aparece evidencia nueva.
func TestV036_EjecucionRepetida_Idempotente(t *testing.T) {
	db := setupSalePaymentBackfillDB(t)
	db.Create(&tsalePayment{ID: 1, SaleID: 100, Method: "efectivo", Amount: 300})
	db.Create(&tsalePayment{ID: 2, SaleID: 200, Method: "yape", Amount: 500})
	db.Create(&tcashMovement{ID: 1, CashSessionID: 30, Type: "income", Amount: 300, SaleID: saleIDPtr(100)})
	// Pago 2 queda deliberadamente sin candidato.

	if err := (V036SalePaymentCashSessionBackfill{}).Run(db); err != nil {
		t.Fatalf("primera pasada: %v", err)
	}
	if err := (V036SalePaymentCashSessionBackfill{}).Run(db); err != nil {
		t.Fatalf("segunda pasada: %v", err)
	}

	var p1, p2 tsalePayment
	db.First(&p1, 1)
	db.First(&p2, 2)
	if p1.CashSessionID == nil || *p1.CashSessionID != 30 {
		t.Fatalf("pago 1 tras dos pasadas: %v, want 30", p1.CashSessionID)
	}
	if p2.CashSessionID != nil {
		t.Fatalf("pago 2 tras dos pasadas: %v, want nil", p2.CashSessionID)
	}
}

// El backfill nunca modifica los movimientos financieros en sí (solo lee de ahí) — ni su
// cash_session_id ni ningún otro campo cambian al correrlo.
func TestV036_NoModificaMovimientosFinancieros(t *testing.T) {
	db := setupSalePaymentBackfillDB(t)
	db.Create(&tsalePayment{ID: 1, SaleID: 100, Method: "efectivo", Amount: 300})
	db.Create(&tcashMovement{ID: 1, CashSessionID: 30, Type: "income", Amount: 300, SaleID: saleIDPtr(100)})

	if err := (V036SalePaymentCashSessionBackfill{}).Run(db); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var mv tcashMovement
	db.First(&mv, 1)
	if mv.CashSessionID != 30 || mv.Amount != 300 || mv.Type != "income" {
		t.Fatalf("el movimiento financiero no debía cambiar: %+v", mv)
	}
}

// Diagnose (modo preview) reporta los mismos contadores que Run resolvería, pero NO escribe nada
// — es el modo de solo lectura pensado para inspeccionar antes de decidir ejecutar el backfill
// real sobre datos de producción.
func TestV036_Diagnose_ReportaSinEscribir(t *testing.T) {
	db := setupSalePaymentBackfillDB(t)
	db.Create(&tsalePayment{ID: 1, SaleID: 100, Method: "efectivo", Amount: 300}) // resoluble
	db.Create(&tsalePayment{ID: 2, SaleID: 200, Method: "yape", Amount: 500})     // sin candidato
	db.Create(&tsalePayment{ID: 3, SaleID: 300, Method: "efectivo", Amount: 700}) // ambiguo (2 movimientos)
	db.Create(&tcashMovement{ID: 1, CashSessionID: 30, Type: "income", Amount: 300, SaleID: saleIDPtr(100)})
	db.Create(&tcashMovement{ID: 2, CashSessionID: 40, Type: "income", Amount: 700, SaleID: saleIDPtr(300)})
	db.Create(&tcashMovement{ID: 3, CashSessionID: 41, Type: "income", Amount: 700, SaleID: saleIDPtr(300)})

	result, err := (V036SalePaymentCashSessionBackfill{}).Diagnose(db)
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	if result.Analyzed != 3 || result.Resolved != 1 || result.NoCandidate != 1 || result.Ambiguous != 1 || result.Errors != 0 {
		t.Fatalf("resultado inesperado: %+v", result)
	}

	// Nada debe haberse escrito: los 3 pagos siguen exactamente igual que antes de diagnosticar.
	var payments []tsalePayment
	db.Order("id").Find(&payments)
	for _, p := range payments {
		if p.CashSessionID != nil {
			t.Fatalf("Diagnose no debe escribir — pago %d quedó con cash_session_id=%v", p.ID, *p.CashSessionID)
		}
	}
}

// Después de Diagnose, Run debe seguir resolviendo exactamente lo que Diagnose reportó — confirma
// que ambos comparten el mismo análisis y que el modo preview no deja ningún efecto secundario
// que altere la corrida real posterior.
func TestV036_DiagnoseLuegoRun_MismoResultado(t *testing.T) {
	db := setupSalePaymentBackfillDB(t)
	db.Create(&tsalePayment{ID: 1, SaleID: 100, Method: "efectivo", Amount: 300})
	db.Create(&tcashMovement{ID: 1, CashSessionID: 30, Type: "income", Amount: 300, SaleID: saleIDPtr(100)})

	diag, err := (V036SalePaymentCashSessionBackfill{}).Diagnose(db)
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	if diag.Resolved != 1 {
		t.Fatalf("Diagnose.Resolved = %d, want 1", diag.Resolved)
	}

	if err := (V036SalePaymentCashSessionBackfill{}).Run(db); err != nil {
		t.Fatalf("Run: %v", err)
	}
	var p tsalePayment
	db.First(&p, 1)
	if p.CashSessionID == nil || *p.CashSessionID != 30 {
		t.Fatalf("tras Run: cash_session_id = %v, want 30", p.CashSessionID)
	}
}

// Distintas ventas con el mismo monto/método no se confunden entre sí: cada una resuelve contra
// su propio movimiento.
func TestV036_DistintasVentas_NoSeConfunden(t *testing.T) {
	db := setupSalePaymentBackfillDB(t)
	db.Create(&tsalePayment{ID: 1, SaleID: 100, Method: "efectivo", Amount: 300})
	db.Create(&tsalePayment{ID: 2, SaleID: 200, Method: "efectivo", Amount: 300})
	db.Create(&tcashMovement{ID: 1, CashSessionID: 30, Type: "income", Amount: 300, SaleID: saleIDPtr(100)})
	db.Create(&tcashMovement{ID: 2, CashSessionID: 40, Type: "income", Amount: 300, SaleID: saleIDPtr(200)})

	if err := (V036SalePaymentCashSessionBackfill{}).Run(db); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var p1, p2 tsalePayment
	db.First(&p1, 1)
	db.First(&p2, 2)
	if p1.CashSessionID == nil || *p1.CashSessionID != 30 {
		t.Fatalf("pago 1: %v, want 30", p1.CashSessionID)
	}
	if p2.CashSessionID == nil || *p2.CashSessionID != 40 {
		t.Fatalf("pago 2: %v, want 40", p2.CashSessionID)
	}
}
