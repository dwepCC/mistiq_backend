package service

import (
	"strings"
	"testing"

	"tukifac/pkg/database"
)

func TestPurchase_PresentationStockInAndVoid(t *testing.T) {
	db := setupPurchaseSaleUnitDB(t)
	if err := db.AutoMigrate(&database.TenantProductPresentation{}, &database.TenantProductPresentationStock{}); err != nil {
		t.Fatal(err)
	}
	p := database.TenantProduct{
		Code: "CAM", Name: "Camisa", Type: "product", Unit: "NIU", SalePrice: 0, ManageStock: true,
		IgvAffectationType: "10", Active: true, HasVariants: true,
	}
	db.Create(&p)
	m := database.TenantProductPresentation{ProductID: p.ID, Name: "Talla M", SalePrice: 55, Active: true}
	l := database.TenantProductPresentation{ProductID: p.ID, Name: "Talla L", SalePrice: 60, Active: true}
	db.Create(&m)
	db.Create(&l)
	pid, mid := p.ID, m.ID

	// Sin presentación: se rechaza antes de escribir nada.
	_, err := createPurchaseSU(t, db, PurchaseItemInput{ProductID: &pid, Description: "Camisa", Unit: "NIU", Quantity: 4, UnitCost: 20, IgvAffectationType: "10"})
	if err == nil || !strings.Contains(err.Error(), "seleccione una presentación") {
		t.Fatalf("sin presentación debía rechazarse, got %v", err)
	}

	// Con presentación: el stock entra a ESA presentación y el precio de venta nuevo también.
	purchase, err := createPurchaseSU(t, db, PurchaseItemInput{
		ProductID: &pid, PresentationID: &mid, Description: "Camisa - Talla M", Unit: "NIU", Quantity: 4, UnitCost: 20,
		IgvAffectationType: "10", UpdateSalePrice: true, NewSalePrice: 70,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	var st database.TenantProductPresentationStock
	db.Where("presentation_id = ? AND branch_id = ?", m.ID, 1).First(&st)
	if st.Quantity != 4 {
		t.Fatalf("stock Talla M = %g, want 4", st.Quantity)
	}
	var lst database.TenantProductPresentationStock
	if db.Where("presentation_id = ?", l.ID).First(&lst).Error == nil && lst.Quantity != 0 {
		t.Fatalf("Talla L no debía recibir stock: %g", lst.Quantity)
	}
	var pm database.TenantProductPresentation
	db.First(&pm, m.ID)
	if pm.SalePrice != 70 {
		t.Fatalf("precio de la presentación = %g, want 70", pm.SalePrice)
	}
	var reloaded database.TenantProduct
	db.First(&reloaded, p.ID)
	if reloaded.SalePrice != 0 {
		t.Fatalf("el precio base del producto no debía cambiar: %g", reloaded.SalePrice)
	}

	// Anular revierte el stock de esa presentación.
	if err := NewPurchaseService(db).Void(purchase.ID, 1); err != nil {
		t.Fatalf("Void: %v", err)
	}
	db.Where("presentation_id = ? AND branch_id = ?", m.ID, 1).First(&st)
	if st.Quantity != 0 {
		t.Fatalf("tras anular, stock Talla M = %g, want 0", st.Quantity)
	}
}
