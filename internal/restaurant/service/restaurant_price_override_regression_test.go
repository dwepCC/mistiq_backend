package service

import (
	"testing"

	"tukifac/pkg/database"
	"tukifac/pkg/tax"
)

// TestCheckoutDirect_PrecioAcordadoSigueFuncionando es la prueba de no-regresión más importante
// de la Fase 0 de hardening (internal/sales/service): SaleService.Create ahora exige que
// unit_price coincida con el catálogo salvo que el ítem venga marcado PriceAuthorized=true.
// Tukichef fija esa marca en resolveDirectSaleItems precisamente para no romper el "precio
// acordado en caja/mesa" (restaurant_modifiers.go:56) — un mesero puede cobrar un plato a un
// precio distinto del de catálogo, y sale_service ya no debe rechazarlo por eso.
func TestCheckoutDirect_PrecioAcordadoSigueFuncionando(t *testing.T) {
	db, f := setupDirectCheckoutDB(t)
	if err := db.AutoMigrate(&database.TenantSale{}, &database.TenantSaleItem{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.TenantCompanyConfig{SunatEnabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	series := database.TenantDocumentSeries{
		BranchID: 1, DocType: "Boleta", SunatCode: "03", Series: "B001", Correlative: 1, Active: true,
	}
	if err := db.Create(&series).Error; err != nil {
		t.Fatal(err)
	}

	// f.Pollo tiene SalePrice = 20 en el catálogo (ver seedComboFamiliar / TestResolveDirectSaleItems_PlainProducts).
	// El mesero cobra 18 (cortesía/negociación en mesa) — precio distinto del catálogo, a propósito.
	polloID := f.Pollo.ID
	svc := NewRestaurantPOSCheckoutService(db)
	in := POSCheckoutInput{
		BranchID: 1, UserID: 1, OrderType: OrderTypeQuickSale, SeriesID: series.ID, DocType: "03",
		Items:    []NewOrderItem{{ProductID: &polloID, Quantity: 1, UnitPrice: 18}},
		Payments: []PaymentInput{{Method: "cash", Amount: 18}},
	}

	sale, err := svc.Checkout(in, tax.DefaultConfig())
	if err != nil {
		t.Fatalf("el precio acordado en mesa (18, distinto del catálogo 20) no debe rechazarse: %v", err)
	}
	if sale.Total != 18 {
		t.Errorf("total = %.2f, want 18 (precio acordado, no el de catálogo)", sale.Total)
	}
}

// TestCheckoutDirect_SinPrecioAcordado_UsaCatalogo: si el mesero no manda precio (0), se usa el
// del catálogo — comportamiento preexistente, sigue igual con el hardening activo.
func TestCheckoutDirect_SinPrecioAcordado_UsaCatalogo(t *testing.T) {
	db, f := setupDirectCheckoutDB(t)
	if err := db.AutoMigrate(&database.TenantSale{}, &database.TenantSaleItem{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.TenantCompanyConfig{SunatEnabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	series := database.TenantDocumentSeries{
		BranchID: 1, DocType: "Boleta", SunatCode: "03", Series: "B001", Correlative: 1, Active: true,
	}
	if err := db.Create(&series).Error; err != nil {
		t.Fatal(err)
	}

	polloID := f.Pollo.ID
	svc := NewRestaurantPOSCheckoutService(db)
	in := POSCheckoutInput{
		BranchID: 1, UserID: 1, OrderType: OrderTypeQuickSale, SeriesID: series.ID, DocType: "03",
		Items:    []NewOrderItem{{ProductID: &polloID, Quantity: 1}},
		Payments: []PaymentInput{{Method: "cash", Amount: 20}},
	}

	sale, err := svc.Checkout(in, tax.DefaultConfig())
	if err != nil {
		t.Fatalf("Checkout: %v", err)
	}
	if sale.Total != 20 {
		t.Errorf("total = %.2f, want 20 (precio de catálogo por defecto)", sale.Total)
	}
}
