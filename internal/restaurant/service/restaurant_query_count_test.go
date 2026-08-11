package service

import (
	"strings"
	"sync"
	"testing"

	"gorm.io/gorm"
)

// countQueries cuenta las consultas que pasan por GORM, para poder afirmar el costo real
// de resolver un ítem en vez de suponerlo.
func countQueries(t *testing.T, db *gorm.DB, fn func(tx *gorm.DB)) (int, []string) {
	t.Helper()
	var mu sync.Mutex
	count := 0
	sqls := []string{}

	session := db.Session(&gorm.Session{NewDB: true})
	session.Callback().Query().After("*").Register("count:query", func(tx *gorm.DB) {
		mu.Lock()
		count++
		sqls = append(sqls, tx.Statement.SQL.String())
		mu.Unlock()
	})
	fn(session)
	return count, sqls
}

// TestResolveRestaurantOrderItem_PlainProductIsOneQuery fija el ahorro: un producto sin
// variantes ni extras (el caso mayoritario) se resuelve leyendo su fila y nada más.
// Antes se consultaban además presentaciones y grupos de modificadores: 3 consultas por
// ítem que no podían cambiar el resultado.
func TestResolveRestaurantOrderItem_PlainProductIsOneQuery(t *testing.T) {
	db, _ := setupComboOrderTestDB(t)
	f := seedComboFamiliar(t, db)

	aguaID := f.Agua.ID
	item := &NewOrderItem{ProductID: &aguaID, Quantity: 1}

	n, sqls := countQueries(t, db, func(tx *gorm.DB) {
		if _, err := resolveRestaurantOrderItem(tx, item); err != nil {
			t.Fatal(err)
		}
	})

	if n != 1 {
		t.Errorf("esperaba 1 consulta para un producto llano, got %d:\n%s", n, strings.Join(sqls, "\n"))
	}
	if item.UnitPrice != 2.50 {
		t.Errorf("el precio debe seguir saliendo del catálogo: got %.2f, want 2.50", item.UnitPrice)
	}
	for _, q := range sqls {
		if strings.Contains(q, "tenant_product_presentations") {
			t.Error("un producto sin variantes no debe consultar presentaciones")
		}
		if strings.Contains(q, "tenant_modifier_groups") || strings.Contains(q, "tenant_product_modifier_groups") {
			t.Error("un producto sin extras no debe consultar grupos de modificadores")
		}
	}
}

// TestResolveRestaurantOrderItem_VariantsStillLoad: la optimización no debe cegar al motor.
// Un producto CON variantes sigue consultando sus presentaciones y validando.
func TestResolveRestaurantOrderItem_VariantsStillLoad(t *testing.T) {
	db, _ := setupComboOrderTestDB(t)
	f := seedComboFamiliar(t, db)

	// El agua pasa a tener presentaciones: ahora sí hay que cargarlas y exigir elección.
	db.Model(&f.Agua).Update("has_variants", true)
	if err := db.Create(&[]struct {
		ProductID uint
		Name      string
		SalePrice float64
		Active    bool
	}{}).Error; err != nil {
		_ = err // sin filas: el Create vacío no aplica
	}
	if err := db.Exec(
		"INSERT INTO tenant_product_presentations (product_id, name, sale_price, sort_order, active) VALUES (?, ?, ?, 0, 1)",
		f.Agua.ID, "Botella 1L", 4.0,
	).Error; err != nil {
		t.Fatal(err)
	}

	aguaID := f.Agua.ID
	item := &NewOrderItem{ProductID: &aguaID, Quantity: 1}

	_, sqls := countQueries(t, db, func(tx *gorm.DB) {
		_, err := resolveRestaurantOrderItem(tx, item)
		if err == nil {
			t.Fatal("con variantes y sin elegir, debe exigir la presentación")
		}
		if !strings.Contains(err.Error(), "presentación") {
			t.Fatalf("error inesperado: %v", err)
		}
	})

	found := false
	for _, q := range sqls {
		if strings.Contains(q, "tenant_product_presentations") {
			found = true
		}
	}
	if !found {
		t.Error("un producto con variantes SÍ debe consultar sus presentaciones")
	}
}

// TestResolveComboOrderItem_ReusesResolvedProduct: el combo no vuelve a leer la fila del
// producto que resolveRestaurantOrderItem ya trajo.
func TestResolveComboOrderItem_ReusesResolvedProduct(t *testing.T) {
	db, _ := setupComboOrderTestDB(t)
	f := seedComboFamiliar(t, db)

	comboID := f.Combo.ID
	item := &NewOrderItem{
		ProductID: &comboID, Quantity: 1,
		ComboJSON: comboSelectionJSON(t, f.BebidaG.ID, f.Agua.ID, 1),
	}
	product, err := resolveRestaurantOrderItem(db, item)
	if err != nil {
		t.Fatal(err)
	}

	_, sqls := countQueries(t, db, func(tx *gorm.DB) {
		drafts, err := resolveComboOrderItem(tx, item, product)
		if err != nil {
			t.Fatal(err)
		}
		if len(drafts) != 2 {
			t.Fatalf("esperaba 2 componentes, got %d", len(drafts))
		}
	})

	// Debe consultar grupos/items/componentes del combo, pero no `tenant_products WHERE id = combo`.
	for _, q := range sqls {
		if strings.Contains(q, "tenant_products") && strings.Contains(q, "LIMIT 1") {
			t.Errorf("el combo no debe releer el producto ya resuelto: %s", q)
		}
	}
}
