package tenantmigrations

import (
	"fmt"
	"testing"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func v062TestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&database.TenantBranch{}, &database.TenantDocumentSeries{}); err != nil {
		t.Fatal(err)
	}
	// Simula el índice único de V052SeriesGlobalUnique (serie única por TENANT, no por sucursal).
	if err := db.Exec(`CREATE UNIQUE INDEX uk_tenant_document_series_series ON tenant_document_series(series)`).Error; err != nil {
		t.Fatal(err)
	}
	return db
}

// TestV062_MultiBranchGetsDistinctSeries: regresión del bug real encontrado en producción
// (doriconta) — el INSERT...SELECT original asignaba el literal 'BC01' a TODAS las sucursales
// que lo necesitaran en una sola pasada, violando el índice único tenant-wide en cuanto había
// 2+ sucursales sin serie BC a la vez.
func TestV062_MultiBranchGetsDistinctSeries(t *testing.T) {
	db := v062TestDB(t)
	if err := db.Create(&database.TenantBranch{Name: "Sucursal 1", Active: true}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.TenantBranch{Name: "Sucursal 2", Active: true}).Error; err != nil {
		t.Fatal(err)
	}

	if err := (V062CreditNoteBCSeries{}).Up(db); err != nil {
		t.Fatalf("Up con 2 sucursales sin serie BC: %v", err)
	}

	var series []database.TenantDocumentSeries
	if err := db.Where("category = ?", "nota_credito").Order("branch_id ASC").Find(&series).Error; err != nil {
		t.Fatal(err)
	}
	if len(series) != 2 {
		t.Fatalf("esperaba 2 series creadas (una por sucursal), got %d", len(series))
	}
	if series[0].Series == series[1].Series {
		t.Fatalf("las 2 sucursales terminaron con la MISMA serie %q — violaría el índice único "+
			"tenant-wide en un DB real", series[0].Series)
	}
	if series[0].Series != "BC01" || series[1].Series != "BC02" {
		t.Fatalf("esperaba BC01/BC02, got %s/%s", series[0].Series, series[1].Series)
	}
}

func TestV062_Idempotent(t *testing.T) {
	db := v062TestDB(t)
	if err := db.Create(&database.TenantBranch{Name: "Sucursal 1", Active: true}).Error; err != nil {
		t.Fatal(err)
	}

	mig := V062CreditNoteBCSeries{}
	if err := mig.Up(db); err != nil {
		t.Fatalf("first Up: %v", err)
	}
	if err := mig.Up(db); err != nil {
		t.Fatalf("second Up (idempotent): %v", err)
	}

	var count int64
	db.Model(&database.TenantDocumentSeries{}).Where("category = ?", "nota_credito").Count(&count)
	if count != 1 {
		t.Fatalf("correrlo 2 veces no debe duplicar la serie ya creada, got count=%d", count)
	}
}

func TestV062_SkipsBranchThatAlreadyHasBCSeries(t *testing.T) {
	db := v062TestDB(t)
	b := database.TenantBranch{Name: "Sucursal existente", Active: true}
	if err := db.Create(&b).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.TenantDocumentSeries{
		BranchID: b.ID, DocType: "NOTA_CREDITO", SunatCode: "07", Category: "nota_credito",
		Series: "BC05", Correlative: 1, Active: true,
	}).Error; err != nil {
		t.Fatal(err)
	}

	if err := (V062CreditNoteBCSeries{}).Up(db); err != nil {
		t.Fatalf("Up: %v", err)
	}

	var count int64
	db.Model(&database.TenantDocumentSeries{}).Where("branch_id = ? AND category = ?", b.ID, "nota_credito").Count(&count)
	if count != 1 {
		t.Fatalf("sucursal que ya tenía serie BC no debe recibir una segunda, got count=%d", count)
	}
}
