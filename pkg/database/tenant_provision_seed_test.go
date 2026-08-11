package database

import (
	"fmt"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func newSeriesSeedTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&TenantDocumentSeries{}); err != nil {
		t.Fatal(err)
	}
	return db
}

// Un tenant nuevo debe quedar listo para cotizar: la migración que siembra la serie de
// cotización corre antes de que exista la sucursal, así que el seed es el único responsable.
func TestSeedDocumentSeries_includesQuotationSeries(t *testing.T) {
	db := newSeriesSeedTestDB(t)
	if err := seedDocumentSeries(db, 1); err != nil {
		t.Fatalf("seedDocumentSeries: %v", err)
	}

	var row TenantDocumentSeries
	if err := db.Where("category = ?", "cotizacion").First(&row).Error; err != nil {
		t.Fatalf("falta la serie de cotización: %v", err)
	}
	if row.Series != "COT01" {
		t.Fatalf("series = %q want COT01", row.Series)
	}
	if row.SunatCode != "QT" {
		t.Fatalf("sunat_code = %q want QT", row.SunatCode)
	}
	if row.Correlative != 1 {
		t.Fatalf("correlative = %d want 1", row.Correlative)
	}
	if !row.Active {
		t.Fatal("la serie de cotización debe quedar activa")
	}
}

// Cada categoría emitible debe quedar cubierta al provisionar el tenant.
func TestSeedDocumentSeries_coversEveryCategory(t *testing.T) {
	db := newSeriesSeedTestDB(t)
	if err := seedDocumentSeries(db, 1); err != nil {
		t.Fatalf("seedDocumentSeries: %v", err)
	}

	for _, category := range []string{
		"venta", "cotizacion", "nota_credito", "nota_debito",
		"guia_remision", "guia_transportista", "retencion", "percepcion",
	} {
		var count int64
		db.Model(&TenantDocumentSeries{}).Where("category = ?", category).Count(&count)
		if count == 0 {
			t.Errorf("categoría %q sin serie sembrada", category)
		}
	}
}

// El seed no debe duplicar series si el tenant ya las tiene.
func TestSeedDocumentSeries_skipsWhenSeriesExist(t *testing.T) {
	db := newSeriesSeedTestDB(t)
	if err := db.Create(&TenantDocumentSeries{
		BranchID: 1, DocType: "BOLETA", SunatCode: "03", Category: "venta",
		Series: "B001", Correlative: 5, Active: true,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := seedDocumentSeries(db, 1); err != nil {
		t.Fatalf("seedDocumentSeries: %v", err)
	}

	var total int64
	db.Model(&TenantDocumentSeries{}).Count(&total)
	if total != 1 {
		t.Fatalf("total series = %d want 1 (no debe re-sembrar)", total)
	}
}
