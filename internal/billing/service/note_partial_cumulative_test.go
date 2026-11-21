package service

import (
	"strings"
	"testing"

	"tukifac/pkg/database"
)

// Fase 7G — validación acumulada: buildPartialNoteItems debe rechazar cuando "solicitado + ya
// devuelto" excede lo vendido, y permitir devoluciones parciales sucesivas mientras no lo excedan.
// Casos del encargo: Venta 2 Cajas, primera devolución 1 Caja (permitido), segunda 1 Caja
// (permitido, total 2 = vendido), tercera cualquier cantidad adicional (rechazado).

func twoCajasSaleItem() database.TenantSaleItem {
	suid := uint(45)
	return database.TenantSaleItem{
		ID: 1, SaleID: 100, ProductID: uintPtr(7), SaleUnitID: &suid,
		Code: "TEC-1", Description: "Teclado", Unit: "BX",
		Quantity: 2, UnitPrice: 45, Subtotal: 76.28, TaxAmount: 13.72, Total: 90,
	}
}

func TestBuildPartialNoteItems_FirstPartialReturn_Allowed(t *testing.T) {
	orig := twoCajasSaleItem()
	items, _, _, _, err := buildPartialNoteItems(100, []database.TenantSaleItem{orig}, []NoteItemSelection{
		{OriginalItemID: 1, Quantity: 1},
	}, nil) // sin devoluciones previas
	if err != nil {
		t.Fatalf("primera devolución de 1 de 2 Cajas debería permitirse: %v", err)
	}
	if len(items) != 1 || items[0].Quantity != 1 {
		t.Fatalf("esperaba 1 ítem con cantidad 1, got %+v", items)
	}
}

func TestBuildPartialNoteItems_SecondPartialReturn_CompletesExactly_Allowed(t *testing.T) {
	orig := twoCajasSaleItem()
	// Ya se devolvió 1 Caja antes (alreadyReturned) — la segunda pide la 1 restante: 1+1=2, exacto.
	alreadyReturned := map[uint]float64{1: 1}
	items, _, _, _, err := buildPartialNoteItems(100, []database.TenantSaleItem{orig}, []NoteItemSelection{
		{OriginalItemID: 1, Quantity: 1},
	}, alreadyReturned)
	if err != nil {
		t.Fatalf("segunda devolución que completa exactamente lo vendido debería permitirse: %v", err)
	}
	if len(items) != 1 || items[0].Quantity != 1 {
		t.Fatalf("esperaba 1 ítem con cantidad 1, got %+v", items)
	}
}

func TestBuildPartialNoteItems_OverReturn_Rejected(t *testing.T) {
	orig := twoCajasSaleItem()
	// Ya se devolvieron las 2 Cajas completas — cualquier solicitud adicional debe rechazarse.
	alreadyReturned := map[uint]float64{1: 2}
	_, _, _, _, err := buildPartialNoteItems(100, []database.TenantSaleItem{orig}, []NoteItemSelection{
		{OriginalItemID: 1, Quantity: 1},
	}, alreadyReturned)
	if err == nil {
		t.Fatal("una tercera devolución sobre una línea ya devuelta al 100% debería rechazarse")
	}
	if !strings.Contains(err.Error(), "ya se devolvieron") {
		t.Fatalf("mensaje de error inesperado: %v", err)
	}
}

func TestBuildPartialNoteItems_PartialOverReturn_Rejected(t *testing.T) {
	orig := twoCajasSaleItem()
	// Ya se devolvió 1.5 de 2 — pedir 1 más (total 2.5) excede lo vendido.
	alreadyReturned := map[uint]float64{1: 1.5}
	_, _, _, _, err := buildPartialNoteItems(100, []database.TenantSaleItem{orig}, []NoteItemSelection{
		{OriginalItemID: 1, Quantity: 1},
	}, alreadyReturned)
	if err == nil {
		t.Fatal("1.5 ya devuelto + 1 solicitado > 2 vendido debería rechazarse")
	}
}

func TestBuildPartialNoteItems_ValidationIsCommercialQuantity_NotBaseUnits(t *testing.T) {
	// La venta es "2 Cajas" (comercial) con factor histórico 12 = 24 unidades base — la
	// validación acumulada debe operar sobre 2 (comercial), nunca sobre 24 (base). Confirmamos
	// que devolver 1 Caja (no 12) es la unidad correcta de comparación contra orig.Quantity=2.
	orig := twoCajasSaleItem() // Quantity: 2 (comercial), factor histórico vive en TenantStockMovement, no aquí.
	if orig.Quantity != 2 {
		t.Fatalf("fixture inválido: Quantity debería ser 2 (comercial), got %v", orig.Quantity)
	}
	items, _, _, _, err := buildPartialNoteItems(100, []database.TenantSaleItem{orig}, []NoteItemSelection{
		{OriginalItemID: 1, Quantity: 1}, // 1 Caja, NO 12 unidades base
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if items[0].Quantity != 1 {
		t.Fatalf("Quantity de la nota debe seguir siendo comercial (1), got %v", items[0].Quantity)
	}
}
