package combos

import (
	"testing"

	"tukifac/pkg/database"
)

func fixedGroup(name string) database.TenantComboGroup {
	return database.TenantComboGroup{
		ID: 1, Name: name, SelectionType: database.ComboSelectionFixed,
	}
}

func catalogItem(productID uint, qty float64, name string) CatalogItem {
	return CatalogItem{
		Item: database.TenantComboGroupItem{
			GroupID: 1, ProductID: productID, DefaultQuantity: qty,
		},
		Product: database.TenantProduct{ID: productID, Name: name},
	}
}

// Un grupo fijo entrega TODOS sus componentes.
//
// Antes solo entraba el primero, mientras el precio de referencia sumaba todos: el combo
// se cobraba por más de lo que entregaba, y el stock de los demás nunca se descontaba.
func TestResolveGroupSelection_fixedIncludesEveryComponent(t *testing.T) {
	g := fixedGroup("Promoción verano")
	catalog := []CatalogItem{
		catalogItem(10, 1, "Polera"),
		catalogItem(11, 1, "Pantalón"),
	}

	got, err := ResolveGroupSelection(g, catalog, nil)
	if err != nil {
		t.Fatalf("ResolveGroupSelection: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("componentes = %d want 2 (polera y pantalón)", len(got))
	}

	seen := map[uint]bool{}
	for _, c := range got {
		seen[c.Product.ID] = true
		if c.Quantity != 1 {
			t.Errorf("producto %d: cantidad = %v want 1", c.Product.ID, c.Quantity)
		}
	}
	if !seen[10] || !seen[11] {
		t.Fatalf("faltan componentes: %+v", seen)
	}
}

// La cantidad por defecto de cada componente se respeta.
func TestResolveGroupSelection_fixedKeepsDefaultQuantities(t *testing.T) {
	g := fixedGroup("Pack")
	catalog := []CatalogItem{
		catalogItem(10, 2, "Polera"),
		catalogItem(11, 3, "Medias"),
	}

	got, err := ResolveGroupSelection(g, catalog, nil)
	if err != nil {
		t.Fatal(err)
	}
	qty := map[uint]float64{}
	for _, c := range got {
		qty[c.Product.ID] = c.Quantity
	}
	if qty[10] != 2 || qty[11] != 3 {
		t.Fatalf("cantidades = %v want {10:2, 11:3}", qty)
	}
}

// Un grupo fijo sin componentes es un combo mal configurado: debe fallar, no vender vacío.
func TestResolveGroupSelection_fixedRejectsEmptyGroup(t *testing.T) {
	if _, err := ResolveGroupSelection(fixedGroup("Vacío"), nil, nil); err == nil {
		t.Fatal("se esperaba error por grupo sin componente")
	}
}
