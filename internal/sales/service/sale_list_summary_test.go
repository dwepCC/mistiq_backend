package service

import (
	"fmt"
	"testing"
	"time"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func setupSaleListSummaryDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	models := []interface{}{
		&database.TenantSale{}, &database.TenantSalePayment{}, &database.TenantSaleDetraccion{},
	}
	for _, m := range models {
		if err := db.AutoMigrate(m); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

// Anular una venta con nota de crédito no debe contar el mismo monto dos veces: una en
// sum_cancelled (la original) y otra en sum_active (su propia NC, que queda status='paid').
// El neto activo debe reflejar solo lo que sigue siendo venta real.
func TestSaleService_List_Summary_CreditNoteNetsOutCancelledSale(t *testing.T) {
	db := setupSaleListSummaryDB(t)
	now := time.Now()

	orig := database.TenantSale{
		Number: "F001-1", DocType: "FACTURA", BranchID: 1, UserID: 1,
		IssueDate: now, Subtotal: 100, TaxAmount: 18, Total: 118,
		Status: "cancelled", PaymentMethod: "cash",
	}
	if err := db.Create(&orig).Error; err != nil {
		t.Fatal(err)
	}
	origID := orig.ID
	nc := database.TenantSale{
		Number: "FC01-1", DocType: "NOTA_CREDITO", BranchID: 1, UserID: 1,
		IssueDate: now, Subtotal: 100, TaxAmount: 18, Total: 118,
		Status: "paid", PaymentMethod: "cash", OriginalSaleID: &origID,
	}
	if err := db.Create(&nc).Error; err != nil {
		t.Fatal(err)
	}
	// Otra venta activa normal, no relacionada, para confirmar que sigue sumando en positivo.
	other := database.TenantSale{
		Number: "F001-2", DocType: "FACTURA", BranchID: 1, UserID: 1,
		IssueDate: now, Subtotal: 50, TaxAmount: 9, Total: 59,
		Status: "paid", PaymentMethod: "cash",
	}
	if err := db.Create(&other).Error; err != nil {
		t.Fatal(err)
	}

	svc := NewSaleService(db)
	_, _, summary, err := svc.List(SaleListParams{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	if summary.SumCancelled != 118 {
		t.Errorf("sum_cancelled = %.2f, se esperaba 118 (la original anulada)", summary.SumCancelled)
	}
	// La original ya queda fuera de sum_active (status='cancelled'); la NC no debe restar de
	// nuevo — solo se excluye (aporta 0), así que sum_active = 59 (solo la otra venta).
	if summary.SumActive != 59 {
		t.Errorf("sum_active = %.2f, se esperaba 59 (la NC no debe restar: su reversión ya está reflejada al excluir la original)", summary.SumActive)
	}
	// sum_total = sum_active + sum_cancelled = 59 + 118 (la NC no suma ni resta, solo se excluye).
	if summary.SumTotal != 177 {
		t.Errorf("sum_total = %.2f, se esperaba 177 (= sum_active + sum_cancelled)", summary.SumTotal)
	}
	// Invariante que debe sostenerse siempre, con o sin notas de crédito de por medio.
	if summary.SumTotal != summary.SumActive+summary.SumCancelled {
		t.Errorf("se rompió el invariante sum_total (%.2f) = sum_active (%.2f) + sum_cancelled (%.2f)",
			summary.SumTotal, summary.SumActive, summary.SumCancelled)
	}
}

// Sin nota de crédito de por medio, el comportamiento de siempre no cambia.
func TestSaleService_List_Summary_NoCreditNote_Unaffected(t *testing.T) {
	db := setupSaleListSummaryDB(t)
	now := time.Now()

	if err := db.Create(&database.TenantSale{
		Number: "F001-1", DocType: "FACTURA", BranchID: 1, UserID: 1,
		IssueDate: now, Subtotal: 100, TaxAmount: 18, Total: 118,
		Status: "paid", PaymentMethod: "cash",
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.TenantSale{
		Number: "F001-2", DocType: "FACTURA", BranchID: 1, UserID: 1,
		IssueDate: now, Subtotal: 100, TaxAmount: 18, Total: 118,
		Status: "cancelled", PaymentMethod: "cash",
	}).Error; err != nil {
		t.Fatal(err)
	}

	svc := NewSaleService(db)
	_, _, summary, err := svc.List(SaleListParams{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if summary.SumActive != 118 {
		t.Errorf("sum_active = %.2f, se esperaba 118", summary.SumActive)
	}
	if summary.SumCancelled != 118 {
		t.Errorf("sum_cancelled = %.2f, se esperaba 118", summary.SumCancelled)
	}
	if summary.SumTotal != 236 {
		t.Errorf("sum_total = %.2f, se esperaba 236", summary.SumTotal)
	}
}

// La tarjeta "por método de pago" tampoco debe contar la NC como venta positiva ni restarla de más.
func TestSaleService_List_Summary_PaymentTotals_CreditNoteExcluded(t *testing.T) {
	db := setupSaleListSummaryDB(t)
	now := time.Now()

	orig := database.TenantSale{
		Number: "F001-1", DocType: "FACTURA", BranchID: 1, UserID: 1,
		IssueDate: now, Subtotal: 100, TaxAmount: 18, Total: 118,
		Status: "cancelled", PaymentMethod: "cash",
	}
	if err := db.Create(&orig).Error; err != nil {
		t.Fatal(err)
	}
	origID := orig.ID
	if err := db.Create(&database.TenantSale{
		Number: "FC01-1", DocType: "NOTA_CREDITO", BranchID: 1, UserID: 1,
		IssueDate: now, Subtotal: 100, TaxAmount: 18, Total: 118,
		Status: "paid", PaymentMethod: "cash", OriginalSaleID: &origID,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.TenantSale{
		Number: "F001-2", DocType: "FACTURA", BranchID: 1, UserID: 1,
		IssueDate: now, Subtotal: 50, TaxAmount: 9, Total: 59,
		Status: "paid", PaymentMethod: "cash",
	}).Error; err != nil {
		t.Fatal(err)
	}

	svc := NewSaleService(db)
	_, _, summary, err := svc.List(SaleListParams{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var cashTotal float64
	found := false
	for _, pt := range summary.PaymentTotals {
		if pt.Method == "cash" {
			cashTotal = pt.Total
			found = true
		}
	}
	if !found {
		t.Fatalf("no se encontró el total del método 'cash' en payment_totals: %+v", summary.PaymentTotals)
	}
	if cashTotal != 59 {
		t.Errorf("payment_totals[cash] = %.2f, se esperaba 59 (la NC no debe sumar ni restar, solo excluirse)", cashTotal)
	}
}
