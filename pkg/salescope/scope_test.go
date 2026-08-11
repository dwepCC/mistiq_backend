package salescope

import (
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

type testSale struct {
	ID                   uint `gorm:"primaryKey"`
	DocType              string
	Total                float64
	Status               string
	SaleOrigin           string
	IssuedFromNotaSaleID *uint
}

func (testSale) TableName() string { return "tenant_sales" }

func openScopeTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&testSale{}); err != nil {
		t.Fatal(err)
	}
	return db
}

func commercialTotal(db *gorm.DB) float64 {
	var total float64
	if err := CommercialSales(db.Model(&testSale{})).Where("status != ?", "cancelled").
		Select("COALESCE(SUM(total), 0)").Scan(&total).Error; err != nil {
		panic(err)
	}
	return total
}

func TestCommercialSales_excludesConvertedChild(t *testing.T) {
	db := openScopeTestDB(t)
	nvID := uint(1)
	db.Create(&testSale{ID: 1, Total: 100, Status: "paid", SaleOrigin: SaleOriginDirect})
	db.Create(&testSale{ID: 2, Total: 100, Status: "paid", SaleOrigin: SaleOriginConvertedFromNota, IssuedFromNotaSaleID: &nvID})

	if total := commercialTotal(db); total != 100 {
		t.Fatalf("commercial total=%v want 100", total)
	}
}

func TestConvertedSales_onlyChild(t *testing.T) {
	db := openScopeTestDB(t)
	nvID := uint(1)
	db.Create(&testSale{ID: 1, Total: 100, SaleOrigin: SaleOriginDirect})
	db.Create(&testSale{ID: 2, Total: 100, SaleOrigin: SaleOriginConvertedFromNota, IssuedFromNotaSaleID: &nvID})

	var n int64
	ConvertedSales(db.Model(&testSale{})).Count(&n)
	if n != 1 {
		t.Fatalf("converted count=%d want 1", n)
	}
}

func TestCommercialSales_ScenarioA_NVDirect(t *testing.T) {
	db := openScopeTestDB(t)
	db.Create(&testSale{ID: 1, DocType: "NV", Total: 100, Status: "paid", SaleOrigin: SaleOriginDirect})
	if total := commercialTotal(db); total != 100 {
		t.Fatalf("scenario A: total=%v want 100", total)
	}
}

func TestCommercialSales_ScenarioB_BoletaDirect(t *testing.T) {
	db := openScopeTestDB(t)
	db.Create(&testSale{ID: 1, DocType: "BOLETA", Total: 150, Status: "paid", SaleOrigin: SaleOriginDirect})
	if total := commercialTotal(db); total != 150 {
		t.Fatalf("scenario B: total=%v want 150", total)
	}
}

func TestCommercialSales_ScenarioC_FacturaDirect(t *testing.T) {
	db := openScopeTestDB(t)
	db.Create(&testSale{ID: 1, DocType: "FACTURA", Total: 200, Status: "paid", SaleOrigin: SaleOriginDirect})
	if total := commercialTotal(db); total != 200 {
		t.Fatalf("scenario C: total=%v want 200", total)
	}
}

func TestCommercialSales_ScenarioD_NVConvertedToBoleta(t *testing.T) {
	db := openScopeTestDB(t)
	nvID := uint(1)
	db.Create(&testSale{ID: 1, DocType: "NV", Total: 300, Status: "paid", SaleOrigin: SaleOriginDirect})
	db.Create(&testSale{ID: 2, DocType: "BOLETA", Total: 300, Status: "paid", SaleOrigin: SaleOriginConvertedFromNota, IssuedFromNotaSaleID: &nvID})
	if total := commercialTotal(db); total != 300 {
		t.Fatalf("scenario D: total=%v want 300 (never 600)", total)
	}
}

func TestCommercialSales_ScenarioE_MixedPortfolio(t *testing.T) {
	db := openScopeTestDB(t)
	nvID := uint(4)
	rows := []testSale{
		{ID: 1, DocType: "NV", Total: 100, Status: "paid", SaleOrigin: SaleOriginDirect},
		{ID: 2, DocType: "BOLETA", Total: 150, Status: "paid", SaleOrigin: SaleOriginDirect},
		{ID: 3, DocType: "FACTURA", Total: 200, Status: "paid", SaleOrigin: SaleOriginDirect},
		{ID: 4, DocType: "NV", Total: 300, Status: "paid", SaleOrigin: SaleOriginDirect},
		{ID: 5, DocType: "BOLETA", Total: 300, Status: "paid", SaleOrigin: SaleOriginConvertedFromNota, IssuedFromNotaSaleID: &nvID},
	}
	for i := range rows {
		db.Create(&rows[i])
	}
	if total := commercialTotal(db); total != 750 {
		t.Fatalf("scenario E: total=%v want 750 (never 1050)", total)
	}
}

func TestCommercialSales_CompatibilityFiscalStillQueryable(t *testing.T) {
	db := openScopeTestDB(t)
	nvID := uint(1)
	db.Create(&testSale{ID: 1, DocType: "NV", Total: 300, Status: "paid", SaleOrigin: SaleOriginDirect})
	db.Create(&testSale{ID: 2, DocType: "BOLETA", Total: 300, Status: "paid", SaleOrigin: SaleOriginConvertedFromNota, IssuedFromNotaSaleID: &nvID})

	var fiscalCount int64
	db.Model(&testSale{}).Where("doc_type IN ?", []string{"BOLETA", "FACTURA"}).Count(&fiscalCount)
	if fiscalCount != 1 {
		t.Fatalf("fiscal list count=%d want 1 (converted boleta visible for SUNAT ops)", fiscalCount)
	}

	var allCount int64
	db.Model(&testSale{}).Count(&allCount)
	if allCount != 2 {
		t.Fatalf("operational list count=%d want 2", allCount)
	}
}

type testPayment struct {
	ID     uint `gorm:"primaryKey"`
	SaleID uint
	Amount float64
}

func (testPayment) TableName() string { return "tenant_sale_payments" }

func seedSalesWithPayments(t *testing.T, db *gorm.DB) {
	t.Helper()
	nvID := uint(2)
	if err := db.AutoMigrate(&testPayment{}); err != nil {
		t.Fatal(err)
	}
	db.Create(&testSale{ID: 1, Total: 100, Status: "paid", SaleOrigin: SaleOriginDirect})
	db.Create(&testSale{ID: 2, Total: 100, Status: "paid", SaleOrigin: SaleOriginConvertedFromNota, IssuedFromNotaSaleID: &nvID})
	db.Create(&testPayment{ID: 1, SaleID: 1, Amount: 100})
	db.Create(&testPayment{ID: 2, SaleID: 2, Amount: 100})
}

func TestScopeCommercialWithAlias(t *testing.T) {
	t.Run("JOIN tenant_sales ts with ScopeCommercial(ts)", func(t *testing.T) {
		db := openScopeTestDB(t)
		seedSalesWithPayments(t, db)

		var total float64
		err := db.Table("tenant_sale_payments tsp").
			Joins("JOIN tenant_sales ts ON ts.id = tsp.sale_id").
			Scopes(ScopeCommercial("ts")).
			Where("ts.status != ?", "cancelled").
			Select("COALESCE(SUM(tsp.amount), 0)").
			Scan(&total).Error
		if err != nil {
			t.Fatalf("alias ts query failed: %v", err)
		}
		if total != 100 {
			t.Fatalf("alias ts total=%v want 100", total)
		}
	})

	t.Run("JOIN tenant_sales s with ScopeCommercial(s)", func(t *testing.T) {
		db := openScopeTestDB(t)
		seedSalesWithPayments(t, db)

		var total float64
		err := db.Table("tenant_sale_payments tsp").
			Joins("JOIN tenant_sales s ON s.id = tsp.sale_id").
			Scopes(ScopeCommercial("s")).
			Where("s.status != ?", "cancelled").
			Select("COALESCE(SUM(tsp.amount), 0)").
			Scan(&total).Error
		if err != nil {
			t.Fatalf("alias s query failed: %v", err)
		}
		if total != 100 {
			t.Fatalf("alias s total=%v want 100", total)
		}
	})

	t.Run("Model TenantSale with CommercialSales", func(t *testing.T) {
		db := openScopeTestDB(t)
		seedSalesWithPayments(t, db)

		if total := commercialTotal(db); total != 100 {
			t.Fatalf("CommercialSales total=%v want 100", total)
		}
	})
}
