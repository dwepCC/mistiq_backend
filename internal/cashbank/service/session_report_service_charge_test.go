package service

import (
	"testing"
	"time"

	"tukifac/pkg/database"
)

// El reporte de caja informa aparte el recargo al consumo de las ventas de la sesión (no es ingreso del negocio: es lo que se reparte a los
// trabajadores). El efectivo esperado no cambia: el dinero ya está dentro de lo cobrado.
func TestGetSessionReport_InformaElRecargoAparte(t *testing.T) {
	db := setupCashReportPhysicalBalanceTestDB(t)
	svc := NewCashBankService(db)
	seedReportPaymentMethods(t, db)
	session := &database.TenantCashSession{BranchID: 1, UserID: 1, OpenedBy: 1, Status: "open", OpeningBalance: 0, OpenedAt: time.Now()}
	if err := db.Create(session).Error; err != nil {
		t.Fatal(err)
	}
	createReportSale(t, db, svc, 1, session.ID, map[string]float64{"cash": 128})
	createReportSale(t, db, svc, 2, session.ID, map[string]float64{"cash": 100}) // sin recargo
	if err := db.Model(&database.TenantSale{}).Where("id = 1").Updates(map[string]interface{}{"service_charge_amount": 10, "service_charge_rate": 10}).Error; err != nil {
		t.Fatal(err)
	}

	report, err := svc.GetSessionReport(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if report.ServiceCharge.Total != 10 || report.ServiceCharge.Sales != 1 {
		t.Errorf("recargo de la sesión: %+v", report.ServiceCharge)
	}
	if report.CashPhysical.PhysicalBalance != 228 {
		t.Errorf("el efectivo esperado no cambia: %v", report.CashPhysical.PhysicalBalance)
	}
}
