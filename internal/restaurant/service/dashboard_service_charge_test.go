package service

import (
	"fmt"
	"testing"
	"time"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// El resumen del dashboard del restaurante suma las ventas netas del recargo al consumo (no es ingreso del negocio).
func TestDashboardSummary_RestaElRecargoAlConsumo(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&database.TenantSale{}, &database.TenantSaleItem{}, &database.TenantTableSession{}, &database.TenantComanda{}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for i, s := range []database.TenantSale{
		{Total: 128, ServiceChargeAmount: 10},
		{Total: 50},
	} {
		s.BranchID, s.UserID, s.DocType, s.Series, s.Number, s.IssueDate, s.Subtotal, s.Status = 1, 1, "BOLETA", "B001", fmt.Sprintf("B001-%d", i+1), now, s.Total, "paid"
		if err := db.Create(&s).Error; err != nil {
			t.Fatal(err)
		}
	}
	out := &DashboardData{}
	if err := (&DashboardService{db: db}).loadSummary(0, now.Add(-time.Hour), now.Add(time.Hour), DashboardFilter{}, out); err != nil {
		t.Fatal(err)
	}
	if out.Summary.TotalSales != 168 { // 118 + 50
		t.Errorf("ventas netas = %v, quiere 168", out.Summary.TotalSales)
	}
}
