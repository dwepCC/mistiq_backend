package service

import (
	"testing"
	"time"

	"tukifac/pkg/database"
)

// Hallazgo #1.3 (revisión con MySQL real): buildSalePaymentMovementRows (usado por
// ListMovementsReport, el reporte multisesión) no filtraba el marcador "credito" — se contaba
// como venta electrónica real. Confirmado en producción: el bloque "electronic" del reporte
// mostraba `{"method":"credito","total":295}` sumado a sum_income, para una venta a crédito que
// nunca recibió ese dinero. Esta prueba verifica que el marcador queda excluido tanto del canal
// "electronic" como de cualquier total, y que un cobro real posterior de esa misma venta sí
// aparece con su método real.
func TestListMovementsReport_marcadorCredito_excluidoDeElectronico(t *testing.T) {
	db := setupMovementsReportPurchaseTestDB(t)
	svc := NewCashBankService(db)

	session := &database.TenantCashSession{BranchID: 1, UserID: 1, OpenedBy: 1, Status: "open", OpenedAt: time.Now()}
	if err := db.Create(session).Error; err != nil {
		t.Fatal(err)
	}
	sale := &database.TenantSale{
		ID: 1, BranchID: 1, UserID: 1, CashSessionID: &session.ID,
		SeriesID: 1, DocType: "boleta", Series: "NV001", Correlative: 1,
		Number: "NV001-1", IssueDate: time.Now(),
		Total: 590, PaymentMethod: "", PaymentConditionCode: "credit", Status: "credit", CreatedAt: time.Now(),
	}
	if err := db.Create(sale).Error; err != nil {
		t.Fatal(err)
	}
	// Marcador "credito" — no debe aparecer en ningún canal del reporte de movimientos.
	if err := db.Create(&database.TenantSalePayment{
		SaleID: 1, Method: "credito", Amount: 590, CreatedAt: time.Now(),
	}).Error; err != nil {
		t.Fatal(err)
	}

	split, err := svc.ListMovementsReport(MovementReportFilters{SessionID: session.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(split.Electronic.Data) != 0 {
		t.Errorf("Electronic.Data trae %d filas, want 0 (el marcador credito no es dinero): %+v", len(split.Electronic.Data), split.Electronic.Data)
	}
	if split.Electronic.Summary.SumIncome != 0 {
		t.Errorf("Electronic.Summary.SumIncome = %v, want 0", split.Electronic.Summary.SumIncome)
	}
	if len(split.Cash.Data) != 0 {
		t.Errorf("Cash.Data trae %d filas, want 0", len(split.Cash.Data))
	}

	// Un cobro real posterior (Yape) de esa misma venta sí debe aparecer, con su método real.
	if err := db.Create(&database.TenantSalePayment{
		SaleID: 1, Method: "yape", Amount: 590, CashSessionID: &session.ID, CreatedAt: time.Now().Add(time.Minute),
	}).Error; err != nil {
		t.Fatal(err)
	}
	split2, err := svc.ListMovementsReport(MovementReportFilters{SessionID: session.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(split2.Electronic.Data) != 1 {
		t.Fatalf("tras el cobro real, Electronic.Data trae %d filas, want 1: %+v", len(split2.Electronic.Data), split2.Electronic.Data)
	}
	if split2.Electronic.Data[0].PaymentMethod != "yape" || split2.Electronic.Data[0].Amount != 590 {
		t.Errorf("fila inesperada: %+v", split2.Electronic.Data[0])
	}
	if split2.Electronic.Summary.SumIncome != 590 {
		t.Errorf("Electronic.Summary.SumIncome = %v, want 590 (solo el cobro real)", split2.Electronic.Summary.SumIncome)
	}
}

// Venta legacy a crédito SIN ninguna fila en tenant_sale_payments (ni siquiera el marcador) —
// camino de "ventas legacy sin líneas" de buildSalePaymentMovementRows. Debe quedar excluida
// igual que la que sí tiene marcador.
func TestListMovementsReport_ventaLegacyACredito_sinFilasDePago_excluida(t *testing.T) {
	db := setupMovementsReportPurchaseTestDB(t)
	svc := NewCashBankService(db)

	session := &database.TenantCashSession{BranchID: 1, UserID: 1, OpenedBy: 1, Status: "open", OpenedAt: time.Now()}
	if err := db.Create(session).Error; err != nil {
		t.Fatal(err)
	}
	sale := &database.TenantSale{
		ID: 1, BranchID: 1, UserID: 1, CashSessionID: &session.ID,
		SeriesID: 1, DocType: "boleta", Series: "NV001", Correlative: 1,
		Number: "NV001-1", IssueDate: time.Now(),
		Total: 590, PaymentMethod: "credito", PaymentConditionCode: "", Status: "credit", CreatedAt: time.Now(),
	}
	if err := db.Create(sale).Error; err != nil {
		t.Fatal(err)
	}

	split, err := svc.ListMovementsReport(MovementReportFilters{SessionID: session.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(split.Electronic.Data) != 0 || len(split.Cash.Data) != 0 {
		t.Errorf("venta legacy a crédito sin pagos no debe aparecer en ningún canal: electronic=%+v cash=%+v",
			split.Electronic.Data, split.Cash.Data)
	}
}
