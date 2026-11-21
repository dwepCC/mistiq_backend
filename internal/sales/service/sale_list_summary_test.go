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

// Reproduce el reporte del usuario (2026-09-15, SEDE KEIKO): "Efectivo" mostraba S/139.20 sobre
// un "Total no anuladas" de S/122.00 del mismo filtro. Causa: tenant_sale_payments.amount guarda
// el monto ENTREGADO por el cliente, no el neto (el vuelto se calcula después en
// print_data.change_amount) — si alguien paga con un billete mayor al total, sumar tsp.amount tal
// cual infla "Efectivo" por encima de lo realmente vendido. El total por método debe acotarse al
// total de la venta (AllocateSalePaymentReportAmounts), igual que ya hace el reporte de caja.
func TestSaleService_List_Summary_PaymentTotals_CapsChangeGivenBack(t *testing.T) {
	db := setupSaleListSummaryDB(t)
	now := time.Now()

	sale := database.TenantSale{
		Number: "NV002-1", DocType: "NOTA_VENTA", BranchID: 1, UserID: 1,
		IssueDate: now, Subtotal: 8.05, TaxAmount: 1.45, Total: 9.50,
		Status: "paid", PaymentMethod: "efectivo",
	}
	if err := db.Create(&sale).Error; err != nil {
		t.Fatal(err)
	}
	// Cliente paga con un billete de S/20 (vuelto S/10.50, calculado en la impresión, no acá) — el
	// pago se guarda con el monto entregado completo, tal como lo hace el flujo real de venta.
	if err := db.Create(&database.TenantSalePayment{
		SaleID: sale.ID, Method: "efectivo", Amount: 20.00,
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
		if pt.Method == "efectivo" {
			cashTotal = pt.Total
			found = true
		}
	}
	if !found {
		t.Fatalf("no se encontró el total del método 'efectivo' en payment_totals: %+v", summary.PaymentTotals)
	}
	if cashTotal != 9.50 {
		t.Errorf("payment_totals[efectivo] = %.2f, se esperaba 9.50 (acotado al total de la venta, sin el vuelto)", cashTotal)
	}
	if summary.SumActive != 9.50 {
		t.Errorf("sum_active = %.2f, se esperaba 9.50", summary.SumActive)
	}
	// El vuelto (S/20 - S/9.50 = S/10.50) debe reportarse aparte, no perderse.
	if summary.SumChangeAmount != 10.50 {
		t.Errorf("sum_change_amount = %.2f, se esperaba 10.50", summary.SumChangeAmount)
	}
}

// List() debe exponer el vuelto por venta (para la columna del reporte), no solo el agregado.
func TestSaleService_List_ChangeAmount_PerSale(t *testing.T) {
	db := setupSaleListSummaryDB(t)
	now := time.Now()

	sale := database.TenantSale{
		Number: "NV002-1", DocType: "NOTA_VENTA", BranchID: 1, UserID: 1,
		IssueDate: now, Subtotal: 8.05, TaxAmount: 1.45, Total: 9.50,
		Status: "paid", PaymentMethod: "efectivo",
	}
	if err := db.Create(&sale).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.TenantSalePayment{
		SaleID: sale.ID, Method: "efectivo", Amount: 20.00,
	}).Error; err != nil {
		t.Fatal(err)
	}
	// Venta sin vuelto, para confirmar que no queda contaminada.
	exact := database.TenantSale{
		Number: "NV002-2", DocType: "NOTA_VENTA", BranchID: 1, UserID: 1,
		IssueDate: now, Subtotal: 5, TaxAmount: 0.90, Total: 5.90,
		Status: "paid", PaymentMethod: "efectivo",
	}
	if err := db.Create(&exact).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.TenantSalePayment{
		SaleID: exact.ID, Method: "efectivo", Amount: 5.90,
	}).Error; err != nil {
		t.Fatal(err)
	}

	svc := NewSaleService(db)
	list, _, _, err := svc.List(SaleListParams{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	byID := make(map[uint]database.TenantSale, len(list))
	for _, s := range list {
		byID[s.ID] = s
	}
	if byID[sale.ID].ChangeAmount != 10.50 {
		t.Errorf("change_amount de la venta con vuelto = %.2f, se esperaba 10.50", byID[sale.ID].ChangeAmount)
	}
	if byID[exact.ID].ChangeAmount != 0 {
		t.Errorf("change_amount de la venta exacta = %.2f, se esperaba 0", byID[exact.ID].ChangeAmount)
	}
}

// Reporte del usuario: "todo sale en soles". Las ventas en USD se sumaban con las de PEN en un
// solo número (sum_total) y los totales por método tampoco distinguían moneda. by_currency y la
// moneda en payment_totals las separan; sum_* plano conserva el comportamiento legado.
func TestSaleService_List_Summary_SeparaMonedas(t *testing.T) {
	db := setupSaleListSummaryDB(t)
	now := time.Now()
	rate := 3.75
	for _, s := range []database.TenantSale{
		{Number: "F001-1", DocType: "FACTURA", BranchID: 1, UserID: 1, IssueDate: now, Subtotal: 100, TaxAmount: 18, Total: 118, Status: "paid", PaymentMethod: "cash", Currency: "PEN"},
		{Number: "F001-2", DocType: "FACTURA", BranchID: 1, UserID: 1, IssueDate: now, Subtotal: 84.75, TaxAmount: 15.25, Total: 100, Status: "paid", PaymentMethod: "cash", Currency: "USD", ExchangeRate: &rate},
		{Number: "F001-3", DocType: "FACTURA", BranchID: 1, UserID: 1, IssueDate: now, Subtotal: 42.37, TaxAmount: 7.63, Total: 50, Status: "cancelled", PaymentMethod: "cash", Currency: "USD", ExchangeRate: &rate},
		{Number: "B001-1", DocType: "BOLETA", BranchID: 1, UserID: 1, IssueDate: now, Subtotal: 10, TaxAmount: 1.8, Total: 11.8, Status: "paid", PaymentMethod: "cash"},
	} {
		s := s
		if err := db.Create(&s).Error; err != nil {
			t.Fatal(err)
		}
	}
	_, _, summary, err := NewSaleService(db).List(SaleListParams{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if summary.SumActive != 229.8 { // legado: PEN + USD mezclados
		t.Errorf("sum_active plano = %.2f (legado mezclado), se esperaba 229.80", summary.SumActive)
	}
	by := map[string]SaleListCurrencySummary{}
	for _, c := range summary.ByCurrency {
		by[c.Currency] = c
	}
	if len(by) != 2 {
		t.Fatalf("by_currency = %+v, se esperaban PEN y USD", summary.ByCurrency)
	}
	if got := by["PEN"]; got.SumActive != 129.8 || got.CountActive != 2 || got.SumCancelled != 0 {
		t.Errorf("PEN = %+v, se esperaba sum_active 129.80 y 2 ventas (la sin moneda cuenta como PEN)", got)
	}
	if got := by["USD"]; got.SumActive != 100 || got.SumCancelled != 50 || got.CountActive != 1 || got.CountCancelled != 1 {
		t.Errorf("USD = %+v, se esperaba sum_active 100, sum_cancelled 50", got)
	}
	cash := map[string]float64{}
	for _, pt := range summary.PaymentTotals {
		if pt.Method == "cash" {
			cash[pt.Currency] = pt.Total
		}
	}
	if cash["PEN"] != 129.8 || cash["USD"] != 100 {
		t.Errorf("payment_totals[cash] por moneda = %+v, se esperaba PEN 129.80 y USD 100", cash)
	}
}
