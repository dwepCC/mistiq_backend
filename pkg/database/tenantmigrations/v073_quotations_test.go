package tenantmigrations

import (
	"fmt"
	"testing"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func v073TestDB(t *testing.T) *gorm.DB {
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

// TestSeedQuotationSeries_MultiBranchGetsDistinctSeries: regresión del bug real encontrado en
// producción (industriassanma) — el chequeo de disponibilidad original filtraba por
// "branch_id = ? AND series = ?", así que 2 sucursales sin serie de cotización a la vez creían
// tener 'COT' libre cada una y la segunda violaba el índice único tenant-wide, abortando el
// resto del bucle.
func TestSeedQuotationSeries_MultiBranchGetsDistinctSeries(t *testing.T) {
	db := v073TestDB(t)
	if err := db.Create(&database.TenantBranch{Name: "Sucursal 1", Active: true}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.TenantBranch{Name: "Sucursal 2", Active: true}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.TenantBranch{Name: "Sucursal 3", Active: true}).Error; err != nil {
		t.Fatal(err)
	}

	if err := seedQuotationSeries(db); err != nil {
		t.Fatalf("seedQuotationSeries con 3 sucursales sin serie: %v", err)
	}

	var series []database.TenantDocumentSeries
	if err := db.Where("category = ?", "cotizacion").Order("branch_id ASC").Find(&series).Error; err != nil {
		t.Fatal(err)
	}
	if len(series) != 3 {
		t.Fatalf("esperaba 3 series creadas (una por sucursal, incluyendo la 3ra que antes se "+
			"quedaba sin nada porque la migración abortaba en la 2da), got %d", len(series))
	}
	seen := map[string]bool{}
	for _, s := range series {
		if seen[s.Series] {
			t.Fatalf("serie %q duplicada entre sucursales — violaría el índice único tenant-wide "+
				"en un DB real", s.Series)
		}
		seen[s.Series] = true
	}
	if series[0].Series != "COT" || series[1].Series != "COT1" || series[2].Series != "COT2" {
		t.Fatalf("esperaba COT/COT1/COT2, got %s/%s/%s", series[0].Series, series[1].Series, series[2].Series)
	}
}

func TestSeedQuotationSeries_Idempotent(t *testing.T) {
	db := v073TestDB(t)
	if err := db.Create(&database.TenantBranch{Name: "Sucursal 1", Active: true}).Error; err != nil {
		t.Fatal(err)
	}

	if err := seedQuotationSeries(db); err != nil {
		t.Fatalf("first: %v", err)
	}
	if err := seedQuotationSeries(db); err != nil {
		t.Fatalf("second (idempotent): %v", err)
	}

	var count int64
	db.Model(&database.TenantDocumentSeries{}).Where("category = ?", "cotizacion").Count(&count)
	if count != 1 {
		t.Fatalf("correrlo 2 veces no debe duplicar la serie ya creada, got count=%d", count)
	}
}
