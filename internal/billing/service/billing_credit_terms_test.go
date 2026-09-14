package service

import (
	"os"
	"testing"
	"time"

	"tukifac/pkg/database"
	"tukifac/pkg/facturador"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func setupCreditTermsDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := "file:credit_terms_test.db?_journal_mode=WAL&_busy_timeout=15000"
	t.Cleanup(func() { os.Remove("credit_terms_test.db") })
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&database.TenantSale{}, &database.TenantSaleCreditInstallment{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

// El bug real: FormaPago.Monto nunca se seteaba, y como el JSON usa `omitempty` en un float64,
// el zero-value desaparecía del payload entero — SUNAT rechazaba con el código 3251 ("Si el tipo
// de transacción es al Crédito debe consignarse el Monto neto pendiente de pago") en el 100% de
// las ventas a crédito, no solo casos raros.
func TestApplyCreditTermsToInvoicePayload_setsMontoFromCuotas(t *testing.T) {
	db := setupCreditTermsDB(t)

	sale := database.TenantSale{
		BranchID: 1, UserID: 1, SeriesID: 1, DocType: "01", Series: "F001", Correlative: 1,
		Number: "F001-1", IssueDate: time.Now(), Total: 300, Currency: "PEN",
		PaymentConditionCode: "credit", Status: "credit",
	}
	db.Create(&sale)
	db.Create(&database.TenantSaleCreditInstallment{
		SaleID: sale.ID, InstallmentNo: 1, DueDate: time.Now().AddDate(0, 1, 0), Amount: 100, Currency: "PEN",
	})
	db.Create(&database.TenantSaleCreditInstallment{
		SaleID: sale.ID, InstallmentNo: 2, DueDate: time.Now().AddDate(0, 2, 0), Amount: 200, Currency: "PEN",
	})

	payload := &facturador.InvoicePayload{TipoMoneda: "PEN"}
	applyCreditTermsToInvoicePayload(db, &sale, payload)

	if payload.FormaPago == nil {
		t.Fatal("FormaPago no debería ser nil para una venta a crédito")
	}
	if payload.FormaPago.Tipo != "Credito" {
		t.Errorf("Tipo = %q, want Credito", payload.FormaPago.Tipo)
	}
	if payload.FormaPago.Monto != 300 {
		t.Errorf("Monto = %v, want 300 (suma de las cuotas: 100+200) — este es exactamente el bug del 3251", payload.FormaPago.Monto)
	}
	if len(payload.Cuotas) != 2 {
		t.Fatalf("esperaba 2 cuotas, obtuve %d", len(payload.Cuotas))
	}
}

// Venta a crédito sin filas de cuotas (legacy/migrada): igual debe declarar un monto pendiente
// (sale.Total), nunca dejar el campo en 0/ausente.
func TestApplyCreditTermsToInvoicePayload_fallsBackToSaleTotalWithoutInstallments(t *testing.T) {
	db := setupCreditTermsDB(t)

	sale := database.TenantSale{
		BranchID: 1, UserID: 1, SeriesID: 1, DocType: "01", Series: "F001", Correlative: 2,
		Number: "F001-2", IssueDate: time.Now(), Total: 450.50, Currency: "PEN",
		PaymentConditionCode: "credit", Status: "credit",
	}
	db.Create(&sale)
	// Sin filas en TenantSaleCreditInstallment.

	payload := &facturador.InvoicePayload{TipoMoneda: "PEN"}
	applyCreditTermsToInvoicePayload(db, &sale, payload)

	if payload.FormaPago == nil || payload.FormaPago.Monto != 450.50 {
		t.Fatalf("Monto = %+v, want 450.50 (sale.Total como fallback conservador)", payload.FormaPago)
	}
}

// Venta al contado: no debe tocar el payload en absoluto.
func TestApplyCreditTermsToInvoicePayload_noopForCashSale(t *testing.T) {
	db := setupCreditTermsDB(t)

	sale := database.TenantSale{
		BranchID: 1, UserID: 1, SeriesID: 1, DocType: "01", Series: "F001", Correlative: 3,
		Number: "F001-3", IssueDate: time.Now(), Total: 100, Currency: "PEN",
		PaymentConditionCode: "cash", Status: "paid",
	}
	db.Create(&sale)

	payload := &facturador.InvoicePayload{TipoMoneda: "PEN"}
	applyCreditTermsToInvoicePayload(db, &sale, payload)

	if payload.FormaPago != nil {
		t.Errorf("FormaPago debería quedar sin tocar (nil) para una venta al contado, obtuve %+v", payload.FormaPago)
	}
}
