package service

import (
	"testing"

	"tukifac/pkg/database"
)

// Caso 6 de la corrección "Unidad Comercial Fiscal": una devolución/nota de crédito parcial de una
// línea vendida como "1 Caja" (Unit="BX", SaleUnitID=<Caja>) debe conservar ambos datos en su
// propia fila — antes de esta corrección, ni buildPartialNoteItems ni las rutas de nota completa
// (billing_service.go) copiaban SaleUnitID a la fila de la nota (Unit sí se copiaba, pero el
// vínculo directo con la SaleUnit se perdía). No se toca la lógica de conversión de stock: esto es
// puramente sobre qué campos trae la fila de TenantSaleItem de la nota misma.
func TestBuildPartialNoteItems_PreservesSaleUnitAndUnitCode(t *testing.T) {
	suid := uint(45)
	orig := database.TenantSaleItem{
		ID: 1, SaleID: 100, ProductID: uintPtr(7), SaleUnitID: &suid,
		Code: "TEC-1", Description: "Teclado", Unit: "BX",
		Quantity: 2, UnitPrice: 45, Subtotal: 76.28, TaxAmount: 13.72, Total: 90,
	}

	items, subtotal, taxAmount, total, err := buildPartialNoteItems(100, []database.TenantSaleItem{orig}, []NoteItemSelection{
		{OriginalItemID: 1, Quantity: 1},
	}, nil)
	if err != nil {
		t.Fatalf("buildPartialNoteItems: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("esperaba 1 ítem, got %d", len(items))
	}
	item := items[0]
	if item.Unit != "BX" {
		t.Errorf("Unit = %q, want BX — la nota debe seguir identificando la línea como Caja, no NIU", item.Unit)
	}
	if item.SaleUnitID == nil || *item.SaleUnitID != suid {
		t.Errorf("SaleUnitID = %v, want %d — la nota debe conservar el vínculo con la SaleUnit original", item.SaleUnitID, suid)
	}
	if item.OriginalSaleItemID == nil || *item.OriginalSaleItemID != orig.ID {
		t.Errorf("OriginalSaleItemID = %v, want %d", item.OriginalSaleItemID, orig.ID)
	}
	// Totales proporcionales (1 de 2 devueltas) — no es el foco de este test, pero confirma que no
	// se rompió nada de la lógica ya existente al agregar SaleUnitID.
	if subtotal <= 0 || taxAmount <= 0 || total <= 0 {
		t.Errorf("totales inesperados: subtotal=%v tax=%v total=%v", subtotal, taxAmount, total)
	}
}

func uintPtr(v uint) *uint { return &v }
