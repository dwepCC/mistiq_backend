package service

import (
	"fmt"
	"math"
	"testing"
	"time"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// Una nota de crédito solo devuelve en dinero lo que el cliente ya pagó por encima de lo que aún debe tras la nota.
// Caso real: AAR SERVICIOS, F001-123 a crédito (nada pagado) con una nota de valor de S/ 90 → no hay nada que devolver.

func refundDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&database.TenantSale{}, &database.TenantSalePayment{}, &database.TenantSaleDetraccion{}, &database.TenantCashMovement{}); err != nil {
		t.Fatal(err)
	}
	return db
}

func refundSale(t *testing.T, db *gorm.DB, number string, total float64, paid ...float64) database.TenantSale {
	t.Helper()
	s := database.TenantSale{DocType: "FACTURA", Number: number, Total: total, Subtotal: total, Status: "credit", BillingStatus: "accepted", BranchID: 1, IssueDate: time.Now()}
	if err := db.Create(&s).Error; err != nil {
		t.Fatal(err)
	}
	for _, p := range paid {
		db.Create(&database.TenantSalePayment{SaleID: s.ID, Method: "efectivo", Amount: p})
	}
	return s
}

func refundNote(t *testing.T, db *gorm.DB, orig uint, number string, total float64) database.TenantSale {
	t.Helper()
	oid := orig
	n := database.TenantSale{DocType: "NOTA_CREDITO", Number: number, Total: total, Subtotal: total, Status: "paid", BillingStatus: "accepted",
		NoteReasonCode: "09", OriginalSaleID: &oid, BranchID: 1, IssueDate: time.Now(), PaymentMethod: "efectivo"}
	if err := db.Create(&n).Error; err != nil {
		t.Fatal(err)
	}
	return n
}

func refundOf(t *testing.T, db *gorm.DB, n database.TenantSale) float64 {
	t.Helper()
	v, err := NoteRefundableAmount(db, &n)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func close2(a, b float64) bool { return math.Abs(a-b) < 0.005 }

func TestNoteRefundable_VentaACreditoSinPagarNoDevuelveNada(t *testing.T) {
	db := refundDB(t)
	f := refundSale(t, db, "F001-123", 5521.22) // sin pagos
	if got := refundOf(t, db, refundNote(t, db, f.ID, "FC01-18", 90)); got != 0 {
		t.Fatalf("venta a crédito sin pagar: devolución %.2f, quería 0", got)
	}
}

func TestNoteRefundable_VentaCobradaCompletaDevuelveLaNota(t *testing.T) {
	db := refundDB(t)
	f := refundSale(t, db, "B001-1", 5521.22, 5521.22)
	if got := refundOf(t, db, refundNote(t, db, f.ID, "BC01-1", 90)); !close2(got, 90) {
		t.Fatalf("venta cobrada: devolución %.2f, quería 90 (igual que antes)", got)
	}
}

func TestNoteRefundable_CobradaEnParteSoloDevuelveElExceso(t *testing.T) {
	db := refundDB(t)
	f := refundSale(t, db, "F001-9", 5521.22, 5500)
	// Debe 21.22; tras la nota de 90 debe 0 y pagó 5431.22... no: nuevo total 5431.22, pagó 5500 → exceso 68.78.
	if got := refundOf(t, db, refundNote(t, db, f.ID, "FC01-9", 90)); !close2(got, 68.78) {
		t.Fatalf("devolución %.2f, quería 68.78", got)
	}
}

func TestNoteRefundable_NotasEnOrdenCadaUnaDevuelveLoQueLeToca(t *testing.T) {
	db := refundDB(t)
	f := refundSale(t, db, "F001-5", 100, 80) // pagó 80 de 100
	n1 := refundNote(t, db, f.ID, "FC01-1", 10) // debe 90, pagó 80: sin exceso
	n2 := refundNote(t, db, f.ID, "FC01-2", 30) // total 60, pagó 80: exceso 20
	if got := refundOf(t, db, n1); got != 0 {
		t.Errorf("nota 1: %.2f, quería 0", got)
	}
	if got := refundOf(t, db, n2); !close2(got, 20) {
		t.Errorf("nota 2: %.2f, quería 20", got)
	}
}

func TestNoteRefundable_FacturaEmitidaDesdeNotaDeVentaMideLoCobradoEnLaNota(t *testing.T) {
	db := refundDB(t)
	nv := refundSale(t, db, "NV01-1", 200, 200) // el dinero entró en la nota de venta
	f := refundSale(t, db, "F001-7", 200)       // la factura emitida desde ella no tiene pagos propios
	db.Model(&f).Update("issued_from_nota_sale_id", nv.ID)
	if got := refundOf(t, db, refundNote(t, db, f.ID, "FC01-7", 50)); !close2(got, 50) {
		t.Fatalf("devolución %.2f, quería 50", got)
	}
}

func TestNoteRefundable_ConDetraccionMideContraLoQueSePagaDirecto(t *testing.T) {
	db := refundDB(t)
	f := refundSale(t, db, "F001-123", 5521.22)
	db.Create(&database.TenantSaleDetraccion{SaleID: f.ID, NetPayablePen: 4858.68, DetractionAmountPen: 662.54, BnConfirmationStatus: "pending"})
	db.Create(&database.TenantSalePayment{SaleID: f.ID, Method: "detraccion_bn", Amount: 662.54}) // el depósito SPOT no es cobro directo
	if got := refundOf(t, db, refundNote(t, db, f.ID, "FC01-18", 90)); got != 0 {
		t.Fatalf("con detracción y nada pagado directo: devolución %.2f, quería 0", got)
	}
}

// Por el flujo real: la lista de devoluciones pendientes de caja ya no ofrece una nota sobre una venta sin cobrar.
func TestPendingRefunds_NoOfreceNotasSobreVentasSinCobrar(t *testing.T) {
	db := refundDB(t)
	credito := refundSale(t, db, "F001-123", 5521.22)
	cobrada := refundSale(t, db, "F001-124", 1000, 1000)
	refundNote(t, db, credito.ID, "FC01-18", 90)
	refundNote(t, db, cobrada.ID, "FC01-19", 40)

	rows, err := NewSaleService(db).PendingSaleRefunds(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].NoteNumber != "FC01-19" || !close2(rows[0].Amount, 40) {
		t.Fatalf("pendientes = %+v, quería solo FC01-19 por 40", rows)
	}
}
