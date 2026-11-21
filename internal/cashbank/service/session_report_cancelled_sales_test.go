package service

import (
	"testing"
	"time"

	"tukifac/pkg/database"
)

// Una venta ANULADA no es una venta: no debe sumar a "Ventas", a los totales por método ni al
// detalle de ingresos del reporte de sesión. Antes GetSessionReport leía tenant_sale_payments sin
// mirar el estado de la venta, así que una venta anulada seguía contando como ingreso (y como
// venta por método) aunque el dashboard y la lista de ventas ya la excluyeran.
//
// El efectivo se retira JUNTO con su egreso de reversión: el front calcula "Efectivo en caja" como
// ingresos-por-venta menos egresos, así que quitar solo uno de los dos descuadraría el arqueo.

func cancelReportSale(t *testing.T, svc *CashBankService, saleID, sessionID uint, cashRefund float64) {
	t.Helper()
	if err := svc.db.Model(&database.TenantSale{}).Where("id = ?", saleID).Update("status", "cancelled").Error; err != nil {
		t.Fatal(err)
	}
	if cashRefund <= 0 {
		return
	}
	if err := svc.db.Create(&database.TenantCashMovement{
		CashSessionID: sessionID, Type: "expense", Amount: cashRefund, PaymentMethod: "cash",
		Category: "Anulación venta", Reference: "ANULACION VENTA", SaleID: &saleID, UserID: 1, CreatedAt: time.Now(),
	}).Error; err != nil {
		t.Fatal(err)
	}
}

func methodTotals(rows []MethodTotal) map[string]float64 {
	out := map[string]float64{}
	for _, r := range rows {
		out[r.Method] = r.Total
	}
	return out
}

func TestGetSessionReport_ventaAnulada_noSumaAVentasPeroElSaldoFisicoNoCambia(t *testing.T) {
	db := setupCashReportPhysicalBalanceTestDB(t)
	svc := NewCashBankService(db)
	seedReportPaymentMethods(t, db)

	session := &database.TenantCashSession{BranchID: 1, UserID: 1, OpenedBy: 1, Status: "open", OpeningBalance: 50, OpenedAt: time.Now()}
	if err := db.Create(session).Error; err != nil {
		t.Fatal(err)
	}
	createReportSale(t, db, svc, 1, session.ID, map[string]float64{"cash": 200})
	// Venta mixta (efectivo + Yape) que luego se anula; el efectivo se devuelve en esta misma sesión.
	createReportSale(t, db, svc, 2, session.ID, map[string]float64{"cash": 30, "yape": 20})
	cancelReportSale(t, svc, 2, session.ID, 30)

	report, err := svc.GetSessionReport(session.ID)
	if err != nil {
		t.Fatal(err)
	}

	sales := methodTotals(report.TotalsByMethod.Sales)
	if sales["efectivo"] != 200 {
		t.Errorf("Ventas[efectivo] = %v, want 200 (la venta anulada no cuenta)", sales["efectivo"])
	}
	if v, ok := sales["yape"]; ok {
		t.Errorf("Ventas[yape] = %v, la venta anulada no debe aparecer", v)
	}
	if report.Totals.TotalSales != 200 || report.Totals.TotalSalesDirect != 200 {
		t.Errorf("TotalSales/Direct = %v/%v, want 200/200", report.Totals.TotalSales, report.Totals.TotalSalesDirect)
	}
	if report.CashPhysical.SalesTotal != 200 {
		t.Errorf("CashPhysical.SalesTotal = %v, want 200", report.CashPhysical.SalesTotal)
	}
	if report.Electronic.TotalSales != 0 {
		t.Errorf("Electronic.TotalSales = %v, want 0", report.Electronic.TotalSales)
	}

	// El saldo físico no cambia: 50 de apertura + 200 de ventas vigentes. La venta anulada y su
	// devolución se compensan, así que ambas se retiran del reporte.
	if want := svc.getExpectedBalance(session.ID); report.CashPhysical.PhysicalBalance != want || want != 250 {
		t.Errorf("PhysicalBalance = %v, esperado de cierre = %v, want 250", report.CashPhysical.PhysicalBalance, want)
	}
	for _, row := range report.ExpenseDetail {
		if row.Type == "egreso_manual" {
			t.Errorf("la devolución por anulación no debe figurar como egreso: %+v", row)
		}
	}
	// Front: Efectivo en caja = ingresos efectivo por venta - egresos efectivo.
	var ing, egr float64
	for _, row := range report.IncomeDetail {
		if row.Type == "venta" && row.PaymentMethod == "efectivo" {
			ing += row.Amount
		}
	}
	for _, row := range report.ExpenseDetail {
		if row.PaymentMethod == "efectivo" {
			egr += row.Amount
		}
	}
	if ing-egr != 200 {
		t.Errorf("saldo efectivo (front) = %v, want 200", ing-egr)
	}

	// La anulación sigue visible en su propia sección.
	if len(report.CancelledSalesDetail) == 0 {
		t.Error("cancelled_sales_detail debe seguir listando la venta anulada")
	}
}

// Si la devolución del efectivo NO cayó en esta sesión (p. ej. se hizo en otra Caja o sigue
// pendiente), el efectivo original sigue en el cajón de esta sesión: se mantiene contado. Lo
// no-efectivo sí se excluye siempre.
func TestGetSessionReport_ventaAnulada_sinDevolucionEnEstaSesion_mantieneElEfectivo(t *testing.T) {
	db := setupCashReportPhysicalBalanceTestDB(t)
	svc := NewCashBankService(db)
	seedReportPaymentMethods(t, db)

	session := &database.TenantCashSession{BranchID: 1, UserID: 1, OpenedBy: 1, Status: "open", OpenedAt: time.Now()}
	if err := db.Create(session).Error; err != nil {
		t.Fatal(err)
	}
	createReportSale(t, db, svc, 1, session.ID, map[string]float64{"cash": 40, "tarjeta": 60})
	cancelReportSale(t, svc, 1, session.ID, 0) // anulada sin egreso de reversión en esta sesión

	report, err := svc.GetSessionReport(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	sales := methodTotals(report.TotalsByMethod.Sales)
	if sales["efectivo"] != 40 {
		t.Errorf("Ventas[efectivo] = %v, want 40 (el efectivo sigue en este cajón)", sales["efectivo"])
	}
	if v, ok := sales["tarjeta"]; ok {
		t.Errorf("Ventas[tarjeta] = %v, lo no-efectivo de una venta anulada no debe contar", v)
	}
	if want := svc.getExpectedBalance(session.ID); report.CashPhysical.PhysicalBalance != want {
		t.Errorf("PhysicalBalance = %v, esperado de cierre = %v", report.CashPhysical.PhysicalBalance, want)
	}
}
