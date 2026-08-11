package tenantmigrations

import (
	"fmt"
	"testing"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestV090InventorySeriesPerBranch_BackfillAllBranches(t *testing.T) {
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.ApplyBaselineSchema(db); err != nil {
		t.Fatal(err)
	}
	b1 := database.TenantBranch{Name: "Principal", Active: true, IsMain: true}
	b2 := database.TenantBranch{Name: "Sucursal 2", Active: true}
	if err := db.Create(&b1).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&b2).Error; err != nil {
		t.Fatal(err)
	}
	// Simula sucursal principal con series de almacén ya creadas (V083 con seed global).
	for _, item := range []struct {
		code    string
		docType string
	}{
		{"ING001", "INGRESO_INVENTARIO"},
		{"EGR001", "EGRESO_INVENTARIO"},
	} {
		if err := db.Create(&database.TenantDocumentSeries{
			BranchID: b1.ID, DocType: item.docType, SunatCode: "00", Category: "almacen",
			Series: item.code, Correlative: 1, Active: true,
		}).Error; err != nil {
			t.Fatal(err)
		}
	}

	if err := (V090InventorySeriesPerBranch{}).Up(db); err != nil {
		t.Fatal(err)
	}
	for _, branchID := range []uint{b1.ID, b2.ID} {
		for _, code := range []string{"ING001", "EGR001"} {
			var n int64
			db.Model(&database.TenantDocumentSeries{}).
				Where("branch_id = ? AND category = ? AND series = ?", branchID, "almacen", code).
				Count(&n)
			if n != 1 {
				t.Fatalf("branch %d series %s count=%d want 1", branchID, code, n)
			}
		}
	}
}
