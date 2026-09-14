package tenantmigrations

import (
	"fmt"
	"testing"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// El backfill debe marcar is_default en la Nota de Venta activa de CADA sucursal (la que ya se
// usaba como default de facto por el frontend), y dejar sin default a la sucursal que no tenga
// ninguna serie '00' activa.
func TestV114DocumentSeriesIsDefault_BackfillNotaVentaPerBranch(t *testing.T) {
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&database.TenantDocumentSeries{}); err != nil {
		t.Fatal(err)
	}

	// Sucursal 1: Nota de Venta activa + Boleta (no debe tocarse).
	nv1 := database.TenantDocumentSeries{
		BranchID: 1, DocType: "NOTA DE VENTA", SunatCode: "00", Category: "venta",
		Series: "NV01", Correlative: 1, Active: true,
	}
	if err := db.Create(&nv1).Error; err != nil {
		t.Fatal(err)
	}
	boleta1 := database.TenantDocumentSeries{
		BranchID: 1, DocType: "BOLETA", SunatCode: "03", Category: "venta",
		Series: "B001", Correlative: 1, Active: true,
	}
	if err := db.Create(&boleta1).Error; err != nil {
		t.Fatal(err)
	}

	// Sucursal 2: Nota de Venta DESACTIVADA (no debe marcarse) + otra activa.
	nv2Inactive := database.TenantDocumentSeries{
		BranchID: 2, DocType: "NOTA DE VENTA", SunatCode: "00", Category: "venta",
		Series: "NV02", Correlative: 1, Active: true,
	}
	if err := db.Create(&nv2Inactive).Error; err != nil {
		t.Fatal(err)
	}
	// bool zero-value + `gorm:"default:true"`: un Create con Active:false lo ignora y persiste
	// true (patrón conocido en este repo) — desactivar aparte, como hace UpdateSeries en runtime.
	if err := db.Model(&nv2Inactive).Update("active", false).Error; err != nil {
		t.Fatal(err)
	}

	// Sucursal 3: sin ninguna serie '00' — queda sin default tras el backfill.
	factura3 := database.TenantDocumentSeries{
		BranchID: 3, DocType: "FACTURA", SunatCode: "01", Category: "venta",
		Series: "F001", Correlative: 1, Active: true,
	}
	if err := db.Create(&factura3).Error; err != nil {
		t.Fatal(err)
	}

	if err := (V114DocumentSeriesIsDefault{}).Up(db); err != nil {
		t.Fatalf("Up: %v", err)
	}

	var reloadedNV1, reloadedBoleta1, reloadedNV2, reloadedFactura3 database.TenantDocumentSeries
	if err := db.First(&reloadedNV1, nv1.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.First(&reloadedBoleta1, boleta1.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.First(&reloadedNV2, nv2Inactive.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.First(&reloadedFactura3, factura3.ID).Error; err != nil {
		t.Fatal(err)
	}

	if !reloadedNV1.IsDefault {
		t.Fatal("sucursal 1: la nota de venta activa debía quedar marcada is_default")
	}
	if reloadedBoleta1.IsDefault {
		t.Fatal("sucursal 1: la boleta no debía tocarse")
	}
	if reloadedNV2.IsDefault {
		t.Fatal("sucursal 2: la nota de venta inactiva no debía marcarse is_default")
	}
	if reloadedFactura3.IsDefault {
		t.Fatal("sucursal 3: sin serie '00', no debe quedar ninguna marcada is_default")
	}
}
