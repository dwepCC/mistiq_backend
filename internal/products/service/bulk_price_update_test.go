package service

import (
	"testing"

	"tukifac/pkg/database"
)

func TestBulkUpdatePrices_MatchesByCodeAndUpdatesOnlyGivenFields(t *testing.T) {
	db := setupProductServiceTestDB(t)

	p1 := database.TenantProduct{Code: "B001", Name: "Producto 1", SalePrice: 10, PurchasePrice: 5, Unit: "NIU"}
	if err := db.Create(&p1).Error; err != nil {
		t.Fatal(err)
	}
	p2 := database.TenantProduct{Code: "B002", Name: "Producto 2", SalePrice: 20, PurchasePrice: 8, Unit: "NIU"}
	if err := db.Create(&p2).Error; err != nil {
		t.Fatal(err)
	}

	svc := NewProductService(db)
	sale := 15.5
	purchase := 7.0
	res, err := svc.BulkUpdatePrices([]BulkPriceUpdateRow{
		{RowNumber: 2, Code: "B001", SalePrice: &sale, PurchasePrice: &purchase},
		// Solo precio de venta: precio de compra no debe tocarse.
		{RowNumber: 3, Code: "B002", SalePrice: floatPtr(25)},
	})
	if err != nil {
		t.Fatalf("BulkUpdatePrices: %v", err)
	}
	if res.Updated != 2 || res.Errors != 0 {
		t.Fatalf("updated=%d errors=%d, want updated=2 errors=0 (%+v)", res.Updated, res.Errors, res.Rows)
	}

	var got1, got2 database.TenantProduct
	if err := db.First(&got1, p1.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got1.SalePrice != 15.5 || got1.PurchasePrice != 7.0 {
		t.Errorf("p1: sale=%.2f purchase=%.2f, want sale=15.50 purchase=7.00", got1.SalePrice, got1.PurchasePrice)
	}
	if err := db.First(&got2, p2.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got2.SalePrice != 25 {
		t.Errorf("p2: sale=%.2f, want 25.00", got2.SalePrice)
	}
	if got2.PurchasePrice != 8 {
		t.Errorf("p2: purchase=%.2f no debía cambiar (columna vacía en la fila), want 8.00", got2.PurchasePrice)
	}
}

func TestBulkUpdatePrices_UnknownAndAmbiguousCodesReportErrors(t *testing.T) {
	db := setupProductServiceTestDB(t)

	// Dos productos con el mismo código (code no es unique en BD) → ambiguo, no se actualiza.
	dup1 := database.TenantProduct{Code: "DUP1", Name: "Dup A", SalePrice: 1, Unit: "NIU"}
	dup2 := database.TenantProduct{Code: "DUP1", Name: "Dup B", SalePrice: 2, Unit: "NIU"}
	if err := db.Create(&dup1).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&dup2).Error; err != nil {
		t.Fatal(err)
	}

	svc := NewProductService(db)
	sale := 99.0
	res, err := svc.BulkUpdatePrices([]BulkPriceUpdateRow{
		{RowNumber: 2, Code: "NOEXISTE", SalePrice: &sale},
		{RowNumber: 3, Code: "DUP1", SalePrice: &sale},
		{RowNumber: 4, Code: "", SalePrice: &sale},
		{RowNumber: 5, Code: "DUP1"}, // sin ningún precio
	})
	if err != nil {
		t.Fatalf("BulkUpdatePrices: %v", err)
	}
	if res.Updated != 0 || res.Errors != 4 {
		t.Fatalf("updated=%d errors=%d, want updated=0 errors=4 (%+v)", res.Updated, res.Errors, res.Rows)
	}
	for _, r := range res.Rows {
		if r.Status != "error" || r.Error == "" {
			t.Errorf("fila %d: se esperaba status=error con mensaje, got %+v", r.RowNumber, r)
		}
	}

	var check1, check2 database.TenantProduct
	if err := db.First(&check1, dup1.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.First(&check2, dup2.ID).Error; err != nil {
		t.Fatal(err)
	}
	if check1.SalePrice != 1 || check2.SalePrice != 2 {
		t.Errorf("productos con código ambiguo no debían cambiar: got %.2f / %.2f", check1.SalePrice, check2.SalePrice)
	}
}

func floatPtr(f float64) *float64 { return &f }
