package service

import (
	"testing"

	"tukifac/pkg/database"
)

// Fase 7G — corrección de asimetría: la nota "de todo" (buildFullNoteItems) debe copiar
// PresentationID igual que ya copiaba SaleUnitID, simétrico con buildPartialNoteItems.

func TestBuildFullNoteItems_PreservesPresentationID(t *testing.T) {
	presID := uint(9)
	orig := database.TenantSaleItem{
		ID: 1, SaleID: 100, ProductID: uintPtr(7), PresentationID: &presID,
		Code: "TEC-ROJO", Description: "Teclado rojo", Unit: "NIU",
		Quantity: 3, UnitPrice: 20, Subtotal: 50.85, TaxAmount: 9.15, Total: 60,
	}
	items := buildFullNoteItems(200, []database.TenantSaleItem{orig})
	if len(items) != 1 {
		t.Fatalf("esperaba 1 ítem, got %d", len(items))
	}
	if items[0].PresentationID == nil || *items[0].PresentationID != presID {
		t.Errorf("PresentationID = %v, want %d — la nota completa debe conservar la Presentation de la venta original", items[0].PresentationID, presID)
	}
	if items[0].SaleID != 200 {
		t.Errorf("SaleID = %v, want 200 (la nota de crédito)", items[0].SaleID)
	}
}

func TestBuildFullNoteItems_PreservesSaleUnitID(t *testing.T) {
	suid := uint(45)
	orig := database.TenantSaleItem{
		ID: 1, SaleID: 100, ProductID: uintPtr(7), SaleUnitID: &suid,
		Code: "TEC-1", Description: "Teclado", Unit: "BX",
		Quantity: 1, UnitPrice: 45, Subtotal: 38.14, TaxAmount: 6.86, Total: 45,
	}
	items := buildFullNoteItems(200, []database.TenantSaleItem{orig})
	if items[0].SaleUnitID == nil || *items[0].SaleUnitID != suid {
		t.Errorf("SaleUnitID = %v, want %d", items[0].SaleUnitID, suid)
	}
	if items[0].Unit != "BX" {
		t.Errorf("Unit = %q, want BX", items[0].Unit)
	}
}

// Unidad base (sin SaleUnit ni Presentation) — no debe inventarse ninguna de las dos.
func TestBuildFullNoteItems_BaseUnit_NoSaleUnitNoPresentation(t *testing.T) {
	orig := database.TenantSaleItem{
		ID: 1, SaleID: 100, ProductID: uintPtr(7),
		Code: "TEC-1", Description: "Teclado", Unit: "NIU",
		Quantity: 1, UnitPrice: 4, Subtotal: 3.39, TaxAmount: 0.61, Total: 4,
	}
	items := buildFullNoteItems(200, []database.TenantSaleItem{orig})
	if items[0].SaleUnitID != nil {
		t.Errorf("SaleUnitID debería ser nil para una línea de unidad base, got %v", items[0].SaleUnitID)
	}
	if items[0].PresentationID != nil {
		t.Errorf("PresentationID debería ser nil para una línea de unidad base, got %v", items[0].PresentationID)
	}
	if items[0].Unit != "NIU" {
		t.Errorf("Unit = %q, want NIU", items[0].Unit)
	}
}
