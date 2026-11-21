package tenantmigrations

import (
	"fmt"
	"testing"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestV180PurchaseOrders_SeedsOneSeriesPerActiveBranchAndIsIdempotent(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&database.TenantBranch{}, &database.TenantDocumentSeries{}); err != nil {
		t.Fatal(err)
	}
	db.Create(&database.TenantBranch{Name: "Principal", Active: true, IsMain: true})
	db.Create(&database.TenantBranch{Name: "Sucursal 2", Active: true})
	closed := database.TenantBranch{Name: "Cerrada", Active: true}
	db.Create(&closed)
	db.Model(&closed).Update("active", false) // el default:true de GORM ignora el false en Create
	// Un tenant que ya usa "OC01" para otra cosa: el sembrado no debe chocar con el índice único de serie.
	db.Create(&database.TenantDocumentSeries{BranchID: 1, DocType: "X", Category: "venta", Series: "OC01", Active: true})

	for i := 0; i < 2; i++ { // segunda pasada: idempotente
		if err := (V195PurchaseOrders{}).Up(db); err != nil {
			t.Fatalf("pasada %d: %v", i+1, err)
		}
	}
	var rows []database.TenantDocumentSeries
	db.Where("category = ?", "orden_compra").Order("branch_id").Find(&rows)
	if len(rows) != 2 {
		t.Fatalf("una serie por sucursal activa, got %d: %+v", len(rows), rows)
	}
	if rows[0].Series == rows[1].Series {
		t.Errorf("las series deben ser distintas: %s / %s", rows[0].Series, rows[1].Series)
	}
	for _, r := range rows {
		if r.Series == "OC01" {
			t.Errorf("OC01 ya estaba usada por otro documento")
		}
		if !r.Active || r.Correlative != 1 {
			t.Errorf("serie mal sembrada: %+v", r)
		}
	}
	if !db.Migrator().HasTable(&database.TenantPurchaseOrder{}) || !db.Migrator().HasTable(&database.TenantPurchaseOrderItem{}) {
		t.Error("faltan las tablas de órdenes de compra")
	}
	if !db.Migrator().HasColumn(&database.TenantPurchase{}, "purchase_order_id") {
		t.Error("falta tenant_purchases.purchase_order_id")
	}
}
