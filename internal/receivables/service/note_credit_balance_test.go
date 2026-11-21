package service

import (
	"fmt"
	"math"
	"testing"
	"time"

	salessvc "tukifac/internal/sales/service"
	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// Una nota de crédito aceptada baja lo que se puede cobrar de la factura (la factura no se edita: la nota la corrige). Caso real:
// AAR SERVICIOS, F001-123 a crédito de 5,521.22 con detracción 12 %, nota de valor de 90.

func near2(a, b float64) bool { return math.Abs(a-b) < 0.005 }

func noteBalanceDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&database.TenantSale{}, &database.TenantSalePayment{}, &database.TenantSaleDetraccion{}, &database.TenantSaleCreditInstallment{}, &database.TenantContact{}); err != nil {
		t.Fatal(err)
	}
	return db
}

func addNote(t *testing.T, db *gorm.DB, orig uint, number, billing string, total float64) {
	t.Helper()
	oid := orig
	if err := db.Create(&database.TenantSale{DocType: "NOTA_CREDITO", Number: number, Series: "FC01", Status: "paid", BillingStatus: billing,
		Total: total, Subtotal: total, OriginalSaleID: &oid, IssueDate: time.Now()}).Error; err != nil {
		t.Fatal(err)
	}
}

func TestSaleBalance_NotaAceptadaBajaElSaldoPorCobrar(t *testing.T) {
	sale := database.TenantSale{Total: 5521.22, Status: "credit", NoteCredited: 90}
	det := &database.TenantSaleDetraccion{NetPayablePen: 4858.68, DetractionAmountPen: 662.54, BnConfirmationStatus: BnStatusPending}
	target, paid, due, _, _, _ := SaleBalance(sale, det, nil)
	if !near2(target, 4768.68) || paid != 0 || !near2(due, 4768.68) {
		t.Fatalf("con detracción: target=%.2f due=%.2f, quería 4768.68 (4858.68 − 90)", target, due)
	}
	// Sin detracción: total − nota.
	_, _, due, _, _, _ = SaleBalance(database.TenantSale{Total: 5521.22, Status: "credit", NoteCredited: 90}, nil, nil)
	if !near2(due, 5431.22) {
		t.Fatalf("sin detracción: due=%.2f, quería 5431.22", due)
	}
	// Sin notas: nada cambia.
	_, _, due, _, _, _ = SaleBalance(database.TenantSale{Total: 5521.22, Status: "credit"}, nil, nil)
	if !near2(due, 5521.22) {
		t.Fatalf("sin notas: due=%.2f", due)
	}
	// Una nota que cubre todo deja la deuda en 0, nunca negativa.
	_, _, due, _, _, _ = SaleBalance(database.TenantSale{Total: 100, NoteCredited: 150}, nil, nil)
	if due != 0 {
		t.Fatalf("deuda negativa: %.2f", due)
	}
}

func TestAttachNoteCredits_SoloNotasAceptadas(t *testing.T) {
	db := noteBalanceDB(t)
	f := database.TenantSale{DocType: "FACTURA", Number: "F001-1", Total: 5521.22, Status: "credit", BillingStatus: "accepted"}
	g := database.TenantSale{DocType: "FACTURA", Number: "F001-2", Total: 100, Status: "credit", BillingStatus: "accepted"}
	db.Create(&f)
	db.Create(&g)
	addNote(t, db, f.ID, "FC01-1", "accepted", 90)
	addNote(t, db, f.ID, "FC01-2", "accepted", 10)
	addNote(t, db, f.ID, "FC01-3", "rejected", 500) // rechazada: no cuenta
	addNote(t, db, f.ID, "FC01-4", "pending", 700)  // aún sin confirmar: no cuenta

	sales := []database.TenantSale{f, g}
	if err := AttachNoteCredits(db, sales); err != nil {
		t.Fatal(err)
	}
	if !near2(sales[0].NoteCredited, 100) || sales[1].NoteCredited != 0 {
		t.Fatalf("acreditado: F001-1=%.2f (quería 100), F001-2=%.2f (quería 0)", sales[0].NoteCredited, sales[1].NoteCredited)
	}
}

// Por el flujo real de Cuentas por Cobrar: el saldo que ve y puede cobrar el cliente baja con la nota.
func TestReceivableList_ElSaldoBajaConLaNotaDeCredito(t *testing.T) {
	db := noteBalanceDB(t)
	cid := uint(1)
	due := time.Now().AddDate(0, 0, 1)
	f := database.TenantSale{DocType: "FACTURA", Number: "F001-123", Series: "F001", Total: 5521.22, Subtotal: 4679, TaxAmount: 842.22,
		Status: "credit", BillingStatus: "accepted", ContactID: &cid, DueDate: &due, IssueDate: time.Now(), PaymentMethod: "credit"}
	db.Create(&f)
	addNote(t, db, f.ID, "FC01-18", "accepted", 90)

	svc := NewReceivableService(db)
	rows, _, err := svc.List(ListFilter{Page: 1, PageSize: 50})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || !near2(rows[0].DirectDue, 5431.22) {
		got := 0.0
		if len(rows) > 0 {
			got = rows[0].DirectDue
		}
		t.Fatalf("filas=%d saldo=%.2f, quería 1 fila con 5431.22", len(rows), got)
	}
	// Cobrar 5,431.22 cierra la venta; cobrar de más se rechaza con el saldo correcto.
	err = svc.Collect(f.ID, CollectPaymentInput{Payments: []salessvc.PaymentInput{{Method: "efectivo", Amount: 5521.22}}})
	if err == nil {
		t.Fatal("cobrar 5521.22 cuando el saldo es 5431.22 debía rechazarse")
	}
}
