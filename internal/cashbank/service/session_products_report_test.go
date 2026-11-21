package service

import (
	"testing"
	"time"

	"tukifac/pkg/database"
)

func addReportItem(t *testing.T, svc *CashBankService, saleID uint, code, desc, unit string, qty, total float64) {
	t.Helper()
	if err := svc.db.Create(&database.TenantSaleItem{SaleID: saleID, Code: code, Description: desc, Unit: unit, Quantity: qty, Total: total}).Error; err != nil {
		t.Fatal(err)
	}
}

// El reporte de productos de la sesión cuenta lo vendido con TODOS los métodos de pago, excluye anuladas,
// otras sesiones y notas, y no mezcla unidades distintas del mismo producto.
func TestGetSessionProductsReport_todosLosMetodosSinAnuladasNiOtraSesion(t *testing.T) {
	db := setupCashReportPhysicalBalanceTestDB(t)
	svc := NewCashBankService(db)
	seedReportPaymentMethods(t, db)
	if err := db.AutoMigrate(&database.TenantSaleItem{}); err != nil {
		t.Fatal(err)
	}

	s1 := &database.TenantCashSession{BranchID: 1, UserID: 1, OpenedBy: 1, Status: "open", OpenedAt: time.Now()}
	s2 := &database.TenantCashSession{BranchID: 1, UserID: 1, OpenedBy: 1, Status: "open", OpenedAt: time.Now()}
	if err := db.Create(s1).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(s2).Error; err != nil {
		t.Fatal(err)
	}

	createReportSale(t, db, svc, 1, s1.ID, map[string]float64{"cash": 20})
	createReportSale(t, db, svc, 2, s1.ID, map[string]float64{"yape": 30})
	createReportSale(t, db, svc, 3, s1.ID, map[string]float64{"cash": 50}) // se anula
	createReportSale(t, db, svc, 4, s2.ID, map[string]float64{"cash": 99}) // otra sesión
	addReportItem(t, svc, 1, "P1", "Gaseosa", "NIU", 2, 20)
	addReportItem(t, svc, 2, "P1", "Gaseosa", "NIU", 3, 30)
	addReportItem(t, svc, 2, "P1", "Gaseosa", "CJA", 1, 40)
	addReportItem(t, svc, 3, "P2", "Galleta", "NIU", 5, 50)
	addReportItem(t, svc, 4, "P1", "Gaseosa", "NIU", 9, 99)
	if err := db.Model(&database.TenantSale{}).Where("id = ?", 3).Update("status", "cancelled").Error; err != nil {
		t.Fatal(err)
	}

	rows, err := svc.GetSessionProductsReport(s1.ID)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]SessionProductSoldRow{}
	for _, r := range rows {
		got[r.Code+"/"+r.Unit] = r
	}
	if len(rows) != 2 {
		t.Fatalf("filas = %d, want 2 (Gaseosa NIU y Gaseosa CJA): %+v", len(rows), rows)
	}
	if r := got["P1/NIU"]; r.Quantity != 5 || r.Total != 50 {
		t.Errorf("Gaseosa NIU = %+v, want qty 5 total 50 (efectivo + yape, sin otra sesión)", r)
	}
	if r := got["P1/CJA"]; r.Quantity != 1 || r.Total != 40 {
		t.Errorf("Gaseosa CJA = %+v", r)
	}
	if _, ok := got["P2/NIU"]; ok {
		t.Error("el producto de la venta anulada no debe aparecer")
	}
}
