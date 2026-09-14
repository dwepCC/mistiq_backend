package service

import (
	"testing"
	"time"

	"tukifac/pkg/database"
)

// Corrección P0: una venta a crédito sin cobrar NO debe aparecer como ingreso — antes,
// GetSessionReport la clasificaba como "electrónica" (por el fallback que usa sale.PaymentMethod
// cuando no hay TenantSalePayment, y "credito" no se traduce a "efectivo" en
// normalizeReportMethod, así que caía directo en Electronic.TotalSales). Ahora se excluye
// explícitamente y se contabiliza aparte en CreditGenerated, igual que ya se hace con la
// detracción SPOT.
//
// IMPORTANTE (hallazgo de la revisión con MySQL real): sale_service.Create() SÍ crea una fila en
// tenant_sale_payments con Method="credito" y Amount=sale.Total para toda venta 100% a crédito sin
// adelanto — el fixture original de este test la omitía (construía la venta directo por SQL, sin
// ninguna fila de pago), por lo que nunca ejercitó el camino real y no detectó que ese marcador
// hacía que la venta se contara DOS veces (una en el lazo de pagos, otra en el fallback). Ahora el
// fixture crea el marcador exactamente como sale_service.Create() lo hace.
func TestGetSessionReport_ventaACredito_noApareceComoElectronico(t *testing.T) {
	db := setupCashReportPhysicalBalanceTestDB(t)
	svc := NewCashBankService(db)
	seedReportPaymentMethods(t, db)

	session := &database.TenantCashSession{BranchID: 1, UserID: 1, OpenedBy: 1, Status: "open", OpenedAt: time.Now()}
	if err := db.Create(session).Error; err != nil {
		t.Fatal(err)
	}
	// Venta al contado en efectivo, normal.
	createReportSale(t, db, svc, 1, session.ID, map[string]float64{"cash": 200})

	// Venta a crédito sin adelanto, EXACTAMENTE como la crea sale_service.Create(): PaymentMethod
	// vacío en la venta, PaymentConditionCode="credit", y una fila en tenant_sale_payments con
	// Method="credito" por el total completo.
	creditSale := &database.TenantSale{
		ID: 2, BranchID: 1, UserID: 1, CashSessionID: &session.ID,
		SeriesID: 1, DocType: "boleta", Series: "B001", Correlative: 2,
		Number: "B001-2", IssueDate: time.Now(),
		Total: 3000, PaymentMethod: "", PaymentConditionCode: "credit", Status: "credit", CreatedAt: time.Now(),
	}
	if err := db.Create(creditSale).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.TenantSalePayment{
		SaleID: 2, Method: "credito", Amount: 3000, CreatedAt: time.Now(),
	}).Error; err != nil {
		t.Fatal(err)
	}

	report, err := svc.GetSessionReport(session.ID)
	if err != nil {
		t.Fatal(err)
	}

	// Antes de la corrección esto daba 6000 (contado dos veces: una por el marcador "credito" en
	// el lazo de pagos, otra por el fallback al no ver incrementado paidBySale).
	if report.CreditGenerated.Total != 3000 {
		t.Errorf("CreditGenerated.Total = %v, want 3000 (sin duplicar)", report.CreditGenerated.Total)
	}
	if len(report.CreditGenerated.Sales) != 1 || report.CreditGenerated.Sales[0].Amount != 3000 {
		t.Errorf("CreditGenerated.Sales inesperado: %+v", report.CreditGenerated.Sales)
	}

	// La venta a crédito NO debe inflar ninguno de estos totales — deben reflejar solo la venta
	// en efectivo (200).
	if report.Totals.TotalSales != 200 {
		t.Errorf("Totals.TotalSales = %v, want 200 (la venta a crédito no debe sumarse)", report.Totals.TotalSales)
	}
	if report.Totals.TotalSalesDirect != 200 {
		t.Errorf("Totals.TotalSalesDirect = %v, want 200", report.Totals.TotalSalesDirect)
	}
	if report.Totals.TotalSalesCommercial != 200 {
		t.Errorf("Totals.TotalSalesCommercial = %v, want 200", report.Totals.TotalSalesCommercial)
	}
	if report.Electronic.TotalSales != 0 {
		t.Errorf("Electronic.TotalSales = %v, want 0 (la venta a crédito NO es electrónica)", report.Electronic.TotalSales)
	}
	// Ninguna fila de IncomeDetail debe corresponder a la venta a crédito.
	for _, row := range report.IncomeDetail {
		if row.Amount == 3000 {
			t.Errorf("la venta a crédito no debe aparecer en IncomeDetail: %+v", row)
		}
	}
	// El efectivo físico solo refleja la venta en efectivo — sin cambios.
	if report.CashPhysical.PhysicalBalance != 200 {
		t.Errorf("PhysicalBalance = %v, want 200", report.CashPhysical.PhysicalBalance)
	}
}

// Venta a crédito CON adelanto: sale_service.Create() en este caso NO crea el marcador "credito"
// — solo una fila de pago real por el adelanto (Method=método real, Amount=adelanto). Antes de la
// corrección, en cuanto ese pago real existía, paidBySale dejaba de ser cero y el fallback
// (la única rama que sabía calcular CreditGenerated) dejaba de correr — el saldo pendiente
// simplemente desaparecía del reporte, sin aparecer ni como ingreso ni como crédito generado.
func TestGetSessionReport_ventaACreditoConAdelanto_saldoPendienteEnCreditoGenerado(t *testing.T) {
	db := setupCashReportPhysicalBalanceTestDB(t)
	svc := NewCashBankService(db)
	seedReportPaymentMethods(t, db)

	session := &database.TenantCashSession{BranchID: 1, UserID: 1, OpenedBy: 1, Status: "open", OpeningBalance: 0, OpenedAt: time.Now()}
	if err := db.Create(session).Error; err != nil {
		t.Fatal(err)
	}

	sale := &database.TenantSale{
		ID: 1, BranchID: 1, UserID: 1, CashSessionID: &session.ID,
		SeriesID: 1, DocType: "boleta", Series: "B001", Correlative: 1,
		Number: "B001-1", IssueDate: time.Now(),
		// PaymentMethod = "cash": es el método del ADELANTO, no "credito" — así lo deja
		// sale_service.Create para una venta a crédito con adelanto.
		Total: 354, PaymentMethod: "cash", PaymentConditionCode: "credit", Status: "credit", CreatedAt: time.Now(),
	}
	if err := db.Create(sale).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.TenantSalePayment{
		SaleID: 1, Method: "cash", Amount: 100, CashSessionID: &session.ID, CreatedAt: time.Now(),
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.RecordPayment(db, "cash", 100, &session.ID, sale.Number, "Venta "+sale.Number, &sale.ID, 1); err != nil {
		t.Fatal(err)
	}

	report, err := svc.GetSessionReport(session.ID)
	if err != nil {
		t.Fatal(err)
	}

	// Ingreso efectivo/electrónico = exactamente el adelanto (100), según su método.
	if report.Totals.TotalSalesDirect != 100 {
		t.Errorf("Totals.TotalSalesDirect = %v, want 100 (solo el adelanto)", report.Totals.TotalSalesDirect)
	}
	if report.CashPhysical.PhysicalBalance != 100 {
		t.Errorf("PhysicalBalance = %v, want 100", report.CashPhysical.PhysicalBalance)
	}
	// Crédito generado = saldo pendiente real (354 - 100 = 254), ni 0 ni 354.
	if report.CreditGenerated.Total != 254 {
		t.Errorf("CreditGenerated.Total = %v, want 254 (saldo pendiente tras el adelanto)", report.CreditGenerated.Total)
	}
	if len(report.CreditGenerated.Sales) != 1 || report.CreditGenerated.Sales[0].Amount != 254 {
		t.Errorf("CreditGenerated.Sales inesperado: %+v", report.CreditGenerated.Sales)
	}
}

// Una venta a crédito sin adelanto (con su marcador "credito" de 590) recibe DOS cobros reales
// EN LA MISMA SESIÓN — 200 en efectivo y 390 por Yape. Verifica que buildSalePaymentReportAmountsFromPayments
// ya no incluye el marcador "credito" en la base del prorrateo (antes: la suma de las 3 líneas,
// 1180, superaba el total de la venta, 590, disparando la rama de "vuelto" y repartiendo el total
// entre las tres en vez de asignarle a cada pago real su propio monto — los cobros aparecían "a
// mitad", 100 y 195 en vez de 200 y 390) y que el marcador no se duplica en CreditGenerated.
func TestGetSessionReport_creditoConDosCobrosReales_sinPrratearNiDuplicar(t *testing.T) {
	db := setupCashReportPhysicalBalanceTestDB(t)
	svc := NewCashBankService(db)
	seedReportPaymentMethods(t, db)

	session := &database.TenantCashSession{BranchID: 1, UserID: 1, OpenedBy: 1, Status: "open", OpenedAt: time.Now()}
	if err := db.Create(session).Error; err != nil {
		t.Fatal(err)
	}

	sale := &database.TenantSale{
		ID: 1, BranchID: 1, UserID: 1, CashSessionID: &session.ID,
		SeriesID: 1, DocType: "boleta", Series: "NV001", Correlative: 1,
		Number: "NV001-1", IssueDate: time.Now(),
		Total: 590, PaymentMethod: "", PaymentConditionCode: "credit", Status: "paid", CreatedAt: time.Now(),
	}
	if err := db.Create(sale).Error; err != nil {
		t.Fatal(err)
	}
	// Marcador "credito" al registrar.
	if err := db.Create(&database.TenantSalePayment{
		SaleID: 1, Method: "credito", Amount: 590, CashSessionID: &session.ID, CreatedAt: time.Now(),
	}).Error; err != nil {
		t.Fatal(err)
	}
	// Cobro 1: efectivo, 200, misma sesión.
	if err := db.Create(&database.TenantSalePayment{
		SaleID: 1, Method: "cash", Amount: 200, CashSessionID: &session.ID, CreatedAt: time.Now().Add(time.Minute),
	}).Error; err != nil {
		t.Fatal(err)
	}
	// Cobro 2: Yape, 390, misma sesión.
	if err := db.Create(&database.TenantSalePayment{
		SaleID: 1, Method: "yape", Amount: 390, CashSessionID: &session.ID, CreatedAt: time.Now().Add(2 * time.Minute),
	}).Error; err != nil {
		t.Fatal(err)
	}

	report, err := svc.GetSessionReport(session.ID)
	if err != nil {
		t.Fatal(err)
	}

	// Montos reales completos, no prorrateados (antes: 100 y 195).
	wantByMethod := map[string]float64{"efectivo": 200, "yape": 390}
	gotByMethod := map[string]float64{}
	for _, row := range report.IncomeDetail {
		gotByMethod[row.PaymentMethod] += row.Amount
	}
	for method, want := range wantByMethod {
		if gotByMethod[method] != want {
			t.Errorf("IncomeDetail[%s] = %v, want %v (sin prorratear)", method, gotByMethod[method], want)
		}
	}
	if report.Totals.TotalSalesDirect != 590 {
		t.Errorf("Totals.TotalSalesDirect = %v, want 590 (200+390 completos)", report.Totals.TotalSalesDirect)
	}
	if report.Electronic.TotalSales != 390 {
		t.Errorf("Electronic.TotalSales = %v, want 390 (solo el cobro Yape)", report.Electronic.TotalSales)
	}
	// Crédito generado neto = 0 (590 - 590 ya cobrado) — nunca 590 duplicado por el marcador.
	if report.CreditGenerated.Total != 0 {
		t.Errorf("CreditGenerated.Total = %v, want 0 (ya se cobró todo, sin duplicar el marcador)", report.CreditGenerated.Total)
	}

	// Sale.CashSessionID nunca cambia (no lo toca este flujo).
	var reloaded database.TenantSale
	if err := db.First(&reloaded, sale.ID).Error; err != nil {
		t.Fatal(err)
	}
	if reloaded.CashSessionID == nil || *reloaded.CashSessionID != session.ID {
		t.Errorf("Sale.CashSessionID = %v, want %d (nunca debe cambiar)", reloaded.CashSessionID, session.ID)
	}
}

// Escenario real íntegro (idéntico al verificado contra MySQL: venta #52 en saas_tenant_demo) —
// Caja 3 registra una venta a crédito de S/590; Caja 4 cobra S/200 en efectivo; Caja 5 cobra
// S/390 por Yape. Verifica la separación estricta documento/pago:
//   - Caja 3 (registro): NO debe mostrar nada de esos cobros como "dinero recibido" — su propio
//     dinero recibido es 0 para esta venta, y crédito generado = 0 porque ya está pagada.
//   - Caja 4: dinero recibido = 200, tipo "cobro_cxc", con SaleCashSessionID apuntando a Caja 3.
//   - Caja 5: dinero recibido = 390 (electrónico), mismo tratamiento.
//   - Sale.CashSessionID permanece en Caja 3 durante todo el proceso.
func TestGetSessionReport_cobroCxCEnOtraCaja_apareceEnLaCajaDelCobroNoEnLaDeRegistro(t *testing.T) {
	db := setupCashReportPhysicalBalanceTestDB(t)
	svc := NewCashBankService(db)
	seedReportPaymentMethods(t, db)

	caja3 := &database.TenantCashSession{BranchID: 1, UserID: 1, OpenedBy: 1, Status: "open", OpenedAt: time.Now()}
	caja4 := &database.TenantCashSession{BranchID: 1, UserID: 1, OpenedBy: 1, Status: "open", OpenedAt: time.Now()}
	caja5 := &database.TenantCashSession{BranchID: 1, UserID: 1, OpenedBy: 1, Status: "open", OpenedAt: time.Now()}
	for _, s := range []*database.TenantCashSession{caja3, caja4, caja5} {
		if err := db.Create(s).Error; err != nil {
			t.Fatal(err)
		}
	}

	sale := &database.TenantSale{
		ID: 52, BranchID: 1, UserID: 1, CashSessionID: &caja3.ID,
		SeriesID: 1, DocType: "boleta", Series: "NV001", Correlative: 27,
		Number: "NV001-00000027", IssueDate: time.Now(),
		Total: 590, PaymentMethod: "", PaymentConditionCode: "credit", Status: "paid", CreatedAt: time.Now(),
	}
	if err := db.Create(sale).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.TenantSalePayment{
		SaleID: 52, Method: "credito", Amount: 590, CashSessionID: &caja3.ID, CreatedAt: time.Now(),
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.TenantSalePayment{
		SaleID: 52, Method: "cash", Amount: 200, CashSessionID: &caja4.ID, CreatedAt: time.Now().Add(time.Minute),
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.TenantSalePayment{
		SaleID: 52, Method: "yape", Amount: 390, CashSessionID: &caja5.ID, CreatedAt: time.Now().Add(2 * time.Minute),
	}).Error; err != nil {
		t.Fatal(err)
	}

	reportCaja3, err := svc.GetSessionReport(caja3.ID)
	if err != nil {
		t.Fatal(err)
	}
	reportCaja4, err := svc.GetSessionReport(caja4.ID)
	if err != nil {
		t.Fatal(err)
	}
	reportCaja5, err := svc.GetSessionReport(caja5.ID)
	if err != nil {
		t.Fatal(err)
	}

	// Caja 3: ni un centavo de "dinero recibido" por esta venta, y crédito generado neto = 0
	// (ya está totalmente cobrada, en otras Cajas).
	if reportCaja3.Totals.TotalSalesDirect != 0 {
		t.Errorf("Caja3.Totals.TotalSalesDirect = %v, want 0", reportCaja3.Totals.TotalSalesDirect)
	}
	if reportCaja3.CreditGenerated.Total != 0 {
		t.Errorf("Caja3.CreditGenerated.Total = %v, want 0 (ya cobrada por completo)", reportCaja3.CreditGenerated.Total)
	}
	for _, row := range reportCaja3.IncomeDetail {
		if row.DocNumber == "NV001-00000027" {
			t.Errorf("Caja3.IncomeDetail no debe traer nada de esta venta: %+v", row)
		}
	}

	// Caja 4: dinero recibido = 200, cobro CxC en efectivo, con Caja de registro = 3.
	if reportCaja4.Totals.TotalSalesDirect != 200 {
		t.Errorf("Caja4.Totals.TotalSalesDirect = %v, want 200", reportCaja4.Totals.TotalSalesDirect)
	}
	if len(reportCaja4.IncomeDetail) != 1 {
		t.Fatalf("Caja4.IncomeDetail = %+v, want 1 fila", reportCaja4.IncomeDetail)
	}
	row4 := reportCaja4.IncomeDetail[0]
	if row4.Type != "cobro_cxc" || row4.Amount != 200 || row4.PaymentMethod != "efectivo" {
		t.Errorf("Caja4.IncomeDetail[0] inesperado: %+v", row4)
	}
	if row4.SaleCashSessionID == nil || *row4.SaleCashSessionID != caja3.ID {
		t.Errorf("Caja4.IncomeDetail[0].SaleCashSessionID = %v, want %d", row4.SaleCashSessionID, caja3.ID)
	}
	if reportCaja4.CashPhysical.SalesTotal != 200 {
		t.Errorf("Caja4.CashPhysical.SalesTotal = %v, want 200", reportCaja4.CashPhysical.SalesTotal)
	}

	// Caja 5: dinero recibido = 390, cobro CxC por Yape (electrónico), con Caja de registro = 3.
	if reportCaja5.Totals.TotalSalesDirect != 390 {
		t.Errorf("Caja5.Totals.TotalSalesDirect = %v, want 390", reportCaja5.Totals.TotalSalesDirect)
	}
	if reportCaja5.Electronic.TotalSales != 390 {
		t.Errorf("Caja5.Electronic.TotalSales = %v, want 390", reportCaja5.Electronic.TotalSales)
	}
	if len(reportCaja5.IncomeDetail) != 1 {
		t.Fatalf("Caja5.IncomeDetail = %+v, want 1 fila", reportCaja5.IncomeDetail)
	}
	row5 := reportCaja5.IncomeDetail[0]
	if row5.Type != "cobro_cxc" || row5.Amount != 390 || row5.PaymentMethod != "yape" {
		t.Errorf("Caja5.IncomeDetail[0] inesperado: %+v", row5)
	}
	if row5.SaleCashSessionID == nil || *row5.SaleCashSessionID != caja3.ID {
		t.Errorf("Caja5.IncomeDetail[0].SaleCashSessionID = %v, want %d", row5.SaleCashSessionID, caja3.ID)
	}

	// Sale.CashSessionID permanece en Caja 3 durante todo el proceso.
	var reloaded database.TenantSale
	if err := db.First(&reloaded, sale.ID).Error; err != nil {
		t.Fatal(err)
	}
	if reloaded.CashSessionID == nil || *reloaded.CashSessionID != caja3.ID {
		t.Errorf("Sale.CashSessionID = %v, want %d (nunca debe cambiar)", reloaded.CashSessionID, caja3.ID)
	}
}
