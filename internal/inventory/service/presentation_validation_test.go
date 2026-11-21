package service

import (
	"strings"
	"testing"

	"tukifac/pkg/database"
)

// Todo movimiento de stock de un producto con presentaciones debe indicar una presentación válida:
// sin ella el stock caía en una fila "agregada" que ningún total ni venta lee, y una presentación
// de OTRO producto se aceptaba y dejaba stock fantasma.

func TestRecordMovementTx_productoConPresentaciones_exigePresentacionValida(t *testing.T) {
	db := setupKardexTestDB(t)
	var branch database.TenantBranch
	db.Where("is_main = ?", true).First(&branch)
	svc := NewInventoryService(db)

	pantalla := database.TenantProduct{Code: "PAN", Name: "Pantalla", Type: "product", Unit: "NIU", ManageStock: true, HasVariants: true, SalePrice: 0, Active: true}
	db.Create(&pantalla)
	grande := database.TenantProductPresentation{ProductID: pantalla.ID, Name: "grande", SalePrice: 300, Active: true}
	db.Create(&grande)
	inactiva := database.TenantProductPresentation{ProductID: pantalla.ID, Name: "vieja", SalePrice: 100, Active: true}
	db.Create(&inactiva)
	db.Model(&inactiva).Update("active", false)

	juego := database.TenantProduct{Code: "JUE", Name: "Juego", Type: "product", Unit: "NIU", ManageStock: true, HasVariants: true, SalePrice: 0, Active: true}
	db.Create(&juego)
	hora := database.TenantProductPresentation{ProductID: juego.ID, Name: "1 hora", SalePrice: 10, Active: true}
	db.Create(&hora)

	simple := database.TenantProduct{Code: "SIM", Name: "Simple", Type: "product", Unit: "NIU", ManageStock: true, SalePrice: 5, Active: true}
	db.Create(&simple)

	move := func(productID uint, presID *uint, typ string, qty float64) error {
		return svc.RecordMovement(MovementInput{
			ProductID: productID, PresentationID: presID, BranchID: branch.ID, Type: typ, Quantity: qty, UserID: 1,
			Reference: "QA", OperationCode: "INITIAL_STOCK",
		})
	}
	ptr := func(v uint) *uint { return &v }

	t.Run("sin presentación se rechaza", func(t *testing.T) {
		err := move(pantalla.ID, nil, "in", 5)
		if err == nil || !strings.Contains(err.Error(), "seleccione una presentación") {
			t.Fatalf("esperaba pedir presentación, got %v", err)
		}
	})
	t.Run("presentación de otro producto se rechaza", func(t *testing.T) {
		err := move(pantalla.ID, ptr(hora.ID), "in", 5)
		if err == nil || !strings.Contains(err.Error(), "no pertenece") {
			t.Fatalf("esperaba rechazo por presentación ajena, got %v", err)
		}
		var n int64
		db.Model(&database.TenantProductPresentationStock{}).Where("presentation_id = ?", hora.ID).Count(&n)
		if n != 0 {
			t.Errorf("no debe quedar stock fantasma en la presentación ajena (%d filas)", n)
		}
	})
	t.Run("presentación inexistente se rechaza", func(t *testing.T) {
		if err := move(pantalla.ID, ptr(99999), "in", 5); err == nil {
			t.Fatal("esperaba rechazo por presentación inexistente")
		}
	})
	t.Run("producto sin presentaciones rechaza una presentación", func(t *testing.T) {
		err := move(simple.ID, ptr(grande.ID), "in", 5)
		if err == nil || !strings.Contains(err.Error(), "no maneja presentaciones") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("entrada válida y salida con mensaje claro", func(t *testing.T) {
		if err := move(pantalla.ID, ptr(grande.ID), "in", 4); err != nil {
			t.Fatalf("entrada válida: %v", err)
		}
		err := move(pantalla.ID, ptr(grande.ID), "out", 5)
		if err == nil {
			t.Fatal("esperaba stock insuficiente")
		}
		for _, want := range []string{"Pantalla", "grande", "requiere 5.00", "hay 4.00"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("el mensaje %q debe contener %q", err.Error(), want)
			}
		}
	})
	t.Run("una presentación inactiva no puede vender pero sí recibir stock", func(t *testing.T) {
		if err := move(pantalla.ID, ptr(inactiva.ID), "in", 3); err != nil {
			t.Fatalf("la entrada a una presentación inactiva es válida: %v", err)
		}
		err := move(pantalla.ID, ptr(inactiva.ID), "out", 1)
		if err == nil || !strings.Contains(err.Error(), "inactiva") {
			t.Fatalf("esperaba rechazo por presentación inactiva, got %v", err)
		}
	})
	t.Run("producto simple sigue funcionando", func(t *testing.T) {
		if err := move(simple.ID, nil, "in", 5); err != nil {
			t.Fatal(err)
		}
	})
}
