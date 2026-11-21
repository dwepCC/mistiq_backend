package tenantmigrations

import (
	"fmt"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// Agrega solo la columna, deja las filas existentes sin etiqueta (documentos anteriores no se tocan) y es idempotente.
func TestV191SaleItemPackLabel_AddsColumnsWithoutTouchingRows(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`CREATE TABLE tenant_sale_items (id INTEGER PRIMARY KEY AUTOINCREMENT, description TEXT)`,
		`CREATE TABLE tenant_quotation_items (id INTEGER PRIMARY KEY AUTOINCREMENT, description TEXT)`,
		`INSERT INTO tenant_sale_items (description) VALUES ('Galletas')`,
	} {
		if err := db.Exec(q).Error; err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 2; i++ {
		if err := (V206SaleItemPackLabel{}).Up(db); err != nil {
			t.Fatalf("pasada %d: %v", i, err)
		}
	}
	for _, table := range []string{"tenant_sale_items", "tenant_quotation_items"} {
		if !db.Migrator().HasColumn(table, "pack_label") {
			t.Errorf("falta %s.pack_label", table)
		}
	}
	var label *string
	db.Raw("SELECT pack_label FROM tenant_sale_items WHERE id = 1").Row().Scan(&label)
	if label != nil && *label != "" {
		t.Errorf("la fila existente debe quedar sin etiqueta, got %q", *label)
	}
}
