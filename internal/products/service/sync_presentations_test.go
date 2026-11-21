package service

import (
	"strings"
	"testing"

	"tukifac/pkg/database"
)

// Editar un producto con presentaciones NO debe perder su stock. Tukichef reenviaba las
// presentaciones sin ID: el backend las trataba como nuevas, daba de baja las anteriores y el
// stock (y su historial) quedaba huérfano — el producto pasaba a total 0 tras cualquier edición.

func seedPresentationsWithStock(t *testing.T) (*ProductService, uint, database.TenantProductPresentation, database.TenantProductPresentation) {
	t.Helper()
	db := setupProductServiceTestDB(t)
	if err := db.AutoMigrate(&database.TenantProductPresentationStock{}); err != nil {
		t.Fatal(err)
	}
	product := database.TenantProduct{Code: "PZ", Name: "Pizza", Type: "product", Unit: "NIU", ManageStock: true, HasVariants: true, SalePrice: 20, Active: true}
	db.Create(&product)
	personal := database.TenantProductPresentation{ProductID: product.ID, Name: "Personal", SalePrice: 20, Active: true}
	familiar := database.TenantProductPresentation{ProductID: product.ID, Name: "Familiar", SalePrice: 40, SortOrder: 1, Active: true}
	db.Create(&personal)
	db.Create(&familiar)
	db.Create(&database.TenantProductPresentationStock{PresentationID: personal.ID, BranchID: 1, Quantity: 10})
	db.Create(&database.TenantProductPresentationStock{PresentationID: familiar.ID, BranchID: 1, Quantity: 5})
	return NewProductService(db), product.ID, personal, familiar
}

func presentationStockOf(t *testing.T, svc *ProductService, presentationID uint) float64 {
	t.Helper()
	var q float64
	svc.db.Model(&database.TenantProductPresentationStock{}).Where("presentation_id = ?", presentationID).Select("COALESCE(SUM(quantity),0)").Scan(&q)
	return q
}

func TestSyncPresentations_sinID_seEmparejaPorNombreYConservaElStock(t *testing.T) {
	svc, productID, personal, familiar := seedPresentationsWithStock(t)

	// Mismo payload que enviaba Tukichef al editar: nombre y precio, sin ID.
	res, err := svc.syncPresentations(productID, []ProductPresentationInput{
		{Name: "personal", SalePrice: 22}, // distinto uso de mayúsculas y precio nuevo
		{Name: "Familiar", SalePrice: 40},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 2 || res[0].IsNew || res[1].IsNew {
		t.Fatalf("ninguna debe ser nueva: %+v", res)
	}
	if res[0].Presentation.ID != personal.ID || res[1].Presentation.ID != familiar.ID {
		t.Errorf("los IDs deben conservarse: got %d/%d want %d/%d", res[0].Presentation.ID, res[1].Presentation.ID, personal.ID, familiar.ID)
	}
	if res[0].Presentation.SalePrice != 22 || res[0].Presentation.Name != "personal" {
		t.Errorf("el precio/nombre sí se actualizan: %+v", res[0].Presentation)
	}
	if presentationStockOf(t, svc, personal.ID) != 10 || presentationStockOf(t, svc, familiar.ID) != 5 {
		t.Error("el stock de cada presentación debe conservarse tras editar")
	}
	var n int64
	svc.db.Unscoped().Model(&database.TenantProductPresentation{}).Where("product_id = ?", productID).Count(&n)
	if n != 2 {
		t.Errorf("no deben crearse filas nuevas (ni quedar eliminadas): %d filas", n)
	}
}

func TestSyncPresentations_noPermiteQuitarUnaPresentacionConStock(t *testing.T) {
	svc, productID, personal, familiar := seedPresentationsWithStock(t)

	_, err := svc.syncPresentations(productID, []ProductPresentationInput{{ID: &personal.ID, Name: "Personal", SalePrice: 20}})
	if err == nil || !strings.Contains(err.Error(), "Familiar") || !strings.Contains(err.Error(), "5 en stock") {
		t.Fatalf("esperaba rechazo por stock de «Familiar», got %v", err)
	}
	// Nada se escribió: la presentación sigue viva y con su stock.
	var n int64
	svc.db.Model(&database.TenantProductPresentation{}).Where("id = ?", familiar.ID).Count(&n)
	if n != 1 || presentationStockOf(t, svc, familiar.ID) != 5 {
		t.Error("al rechazar no debe modificarse nada")
	}

	// Con stock 0 sí se puede quitar.
	svc.db.Model(&database.TenantProductPresentationStock{}).Where("presentation_id = ?", familiar.ID).Update("quantity", 0)
	if _, err := svc.syncPresentations(productID, []ProductPresentationInput{{ID: &personal.ID, Name: "Personal", SalePrice: 20}}); err != nil {
		t.Fatalf("sin stock debe poder quitarse: %v", err)
	}
	svc.db.Model(&database.TenantProductPresentation{}).Where("id = ?", familiar.ID).Count(&n)
	if n != 0 {
		t.Error("la presentación sin stock debió eliminarse")
	}
}

func TestUpdateWithoutPresentationsKeepsHasVariants(t *testing.T) {
	db := setupProductServiceTestDB(t)
	svc := NewProductService(db)
	p, _, err := svc.Create(ProductInput{
		Code: "PV-1", Name: "Pizza", Type: "product", Unit: "NIU", SalePrice: 0, TaxRate: 18, IgvAffectationType: "10", Active: true,
		HasVariants:   true,
		Presentations: &[]ProductPresentationInput{{Name: "Personal", SalePrice: 20}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Update(p.ID, ProductInput{
		Code: "PV-1", Name: "Pizza", Type: "product", Unit: "NIU", SalePrice: 0, TaxRate: 18, IgvAffectationType: "10", Active: true,
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	var got database.TenantProduct
	db.First(&got, p.ID)
	if !got.HasVariants {
		t.Fatal("has_variants se apagó al editar sin enviar presentaciones")
	}
}

func TestCreateSaleUnitRejectedWhenProductHasPresentations(t *testing.T) {
	db := setupProductServiceTestDB(t)
	svc := NewProductService(db)
	p, _, err := svc.Create(ProductInput{
		Code: "PV-2", Name: "Pizza", Type: "product", Unit: "NIU", SalePrice: 0, TaxRate: 18, IgvAffectationType: "10", Active: true,
		Presentations: &[]ProductPresentationInput{{Name: "Personal", SalePrice: 20}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateSaleUnit(p.ID, SaleUnitInput{Name: "Caja", ConversionFactor: 12}); err == nil {
		t.Fatal("se esperaba rechazo al combinar unidades de venta con presentaciones")
	}
}
