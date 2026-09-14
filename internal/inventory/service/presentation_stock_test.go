package service

import (
	"testing"

	"tukifac/pkg/database"
)

// El stock por presentación se mostraba antes colapsado en un único total por sucursal
// (SUM ... GROUP BY solo product_id, branch_id) — sin forma de ver cuánto había de CADA
// presentación. GetPresentationStockByProduct debe conservar esa identidad.
func TestGetPresentationStockByProduct_breaksDownByPresentation(t *testing.T) {
	db := setupKardexTestDB(t)
	branch := database.TenantBranch{Name: "Sucursal 2", Active: true}
	db.Create(&branch)
	var mainBranch database.TenantBranch
	db.Where("is_main = ?", true).First(&mainBranch)

	product := database.TenantProduct{
		Code: "P-VAR", Name: "Producto con presentaciones", Type: "product", Unit: "NIU",
		ManageStock: true, HasVariants: true, SalePrice: 10, Active: true,
	}
	db.Create(&product)

	chica := database.TenantProductPresentation{ProductID: product.ID, Name: "Chica", SalePrice: 10, Active: true}
	db.Create(&chica)
	grande := database.TenantProductPresentation{ProductID: product.ID, Name: "Grande", SalePrice: 15, Active: true}
	db.Create(&grande)

	db.Create(&database.TenantProductPresentationStock{PresentationID: chica.ID, BranchID: mainBranch.ID, Quantity: 5})
	db.Create(&database.TenantProductPresentationStock{PresentationID: grande.ID, BranchID: mainBranch.ID, Quantity: 3})
	db.Create(&database.TenantProductPresentationStock{PresentationID: chica.ID, BranchID: branch.ID, Quantity: 2})

	svc := NewInventoryService(db)

	t.Run("todas las sucursales", func(t *testing.T) {
		rows := svc.GetPresentationStockByProduct(product.ID, 0)
		if len(rows) != 3 {
			t.Fatalf("filas = %d, se esperaban 3 (2 presentaciones x sucursal principal + 1 en la otra)", len(rows))
		}
		byKey := map[string]float64{}
		for _, r := range rows {
			byKey[r.PresentationName] += r.Quantity
			if r.ProductID != product.ID {
				t.Errorf("product_id = %d, se esperaba %d", r.ProductID, product.ID)
			}
		}
		if byKey["Chica"] != 7 { // 5 + 2, en las dos sucursales
			t.Errorf("total Chica = %.0f, se esperaba 7", byKey["Chica"])
		}
		if byKey["Grande"] != 3 {
			t.Errorf("total Grande = %.0f, se esperaba 3", byKey["Grande"])
		}
	})

	t.Run("filtrado por sucursal", func(t *testing.T) {
		rows := svc.GetPresentationStockByProduct(product.ID, mainBranch.ID)
		if len(rows) != 2 {
			t.Fatalf("filas = %d, se esperaban 2 (solo la sucursal principal)", len(rows))
		}
		for _, r := range rows {
			if r.BranchID != mainBranch.ID {
				t.Errorf("branch_id = %d, se esperaba %d (filtro no aplicado)", r.BranchID, mainBranch.ID)
			}
		}
	})
}
