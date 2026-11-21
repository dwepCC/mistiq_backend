package service

import (
	"fmt"
	"testing"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// Fase 7G — validación acumulada de devoluciones: sumReturnedQuantitiesByOriginalItem debe sumar
// solo lo ya devuelto en notas NO rechazadas, para que buildPartialNoteItems pueda comparar
// "solicitado + ya devuelto" contra lo vendido (ver note_partial.go).

func setupReturnedQuantityTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&database.TenantSale{}, &database.TenantSaleItem{}); err != nil {
		t.Fatal(err)
	}
	return db
}

// createReturnedQtyNote inserta una venta NOTA_CREDITO con billing_status dado y una única línea
// ligada a originalItemID, con la cantidad comercial indicada.
func createReturnedQtyNote(t *testing.T, db *gorm.DB, billingStatus string, originalItemID uint, quantity float64) {
	t.Helper()
	note := database.TenantSale{
		DocType: "NOTA_CREDITO", Series: "FC01", Number: fmt.Sprintf("FC01-%d", originalItemID*1000+uint(quantity*10)),
		Status: "paid", BillingStatus: billingStatus, Subtotal: 1, Total: 1,
	}
	if err := db.Create(&note).Error; err != nil {
		t.Fatal(err)
	}
	item := database.TenantSaleItem{
		SaleID: note.ID, Description: "Caja", Unit: "BX", Quantity: quantity, UnitPrice: 45,
		Subtotal: quantity * 45, Total: quantity * 45, OriginalSaleItemID: &originalItemID,
	}
	if err := db.Create(&item).Error; err != nil {
		t.Fatal(err)
	}
}

func TestSumReturnedQuantities_NoPriorNotes_ReturnsEmptyMap(t *testing.T) {
	db := setupReturnedQuantityTestDB(t)
	got, err := sumReturnedQuantitiesByOriginalItem(db, []uint{1, 2, 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("esperaba mapa vacío sin notas previas, got %v", got)
	}
}

func TestSumReturnedQuantities_EmptyIDs_NoQuery(t *testing.T) {
	db := setupReturnedQuantityTestDB(t)
	got, err := sumReturnedQuantitiesByOriginalItem(db, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("esperaba mapa vacío para slice de IDs vacío, got %v", got)
	}
}

func TestSumReturnedQuantities_SumsAcceptedAndPending_ExcludesRejected(t *testing.T) {
	db := setupReturnedQuantityTestDB(t)
	// Línea 1: una NC aceptada (1) + una NC pendiente (0.5) = 1.5 ya devuelto.
	createReturnedQtyNote(t, db, "accepted", 1, 1)
	createReturnedQtyNote(t, db, "pending", 1, 0.5)
	// Una NC rechazada NO debe sumar — nunca llegó a tener validez legal ni movió stock.
	createReturnedQtyNote(t, db, "rejected", 1, 10)
	// Línea 2: sin ninguna nota previa.
	got, err := sumReturnedQuantitiesByOriginalItem(db, []uint{1, 2})
	if err != nil {
		t.Fatal(err)
	}
	if got[1] != 1.5 {
		t.Errorf("línea 1: got %.3f, want 1.5 (acepta accepted+pending, excluye rejected)", got[1])
	}
	if _, ok := got[2]; ok {
		t.Errorf("línea 2 no debería aparecer en el mapa (sin notas previas), got %v", got[2])
	}
}
