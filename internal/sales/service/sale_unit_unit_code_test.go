package service

import (
	"testing"
	"time"

	"tukifac/pkg/database"
	"tukifac/pkg/tax"

	"gorm.io/gorm"
)

// Corrección "Unidad Comercial Fiscal" (posterior a Fase 7E): TenantSaleItem.Unit debe resolverse
// SIEMPRE server-side para líneas con producto de catálogo — nunca confiar en el `unit` que mande
// el cliente. Estos tests cubren los Casos 1/2/3/7 del encargo (los Casos 4/5, sobre el detalle
// fiscal/XML, se cubren en internal/billing/service/invoice_discounts_unit_code_test.go — mismo
// dato de entrada, TenantSaleItem.Unit, reutilizado tal cual por BuildInvoiceDetailsFromSaleItems).

// setupTeclado arma un producto "Teclado" (unidad base NIU, Price1 legacy S/4) con una SaleUnit
// "Caja" (factor 12, Price1 S/45, Unit="BX" — ya con su código SUNAT propio configurado) y todo lo
// necesario (stock, sucursal, caja, serie) para registrar ventas contra él.
func setupTeclado(t *testing.T) (db *gorm.DB, product database.TenantProduct, caja database.TenantProductSaleUnit, branchID uint, seriesID uint) {
	t.Helper()
	d := setupSaleUnitIntegrationDB(t)
	product = database.TenantProduct{
		Code: "TEC-1", Name: "Teclado", Type: "product", Unit: "NIU", SalePrice: 4,
		ManageStock: true, IgvAffectationType: "10", PriceIncludesIgv: true, Active: true,
	}
	if err := d.Create(&product).Error; err != nil {
		t.Fatal(err)
	}
	if err := d.Create(&database.TenantProductStock{ProductID: product.ID, BranchID: 1, Quantity: 100}).Error; err != nil {
		t.Fatal(err)
	}
	caja = database.TenantProductSaleUnit{
		ProductID: product.ID, Name: "Caja", Unit: "BX", ConversionFactor: 12, Price1: 45, Active: true,
	}
	if err := d.Create(&caja).Error; err != nil {
		t.Fatal(err)
	}
	branch := database.TenantBranch{Name: "Sucursal A", Active: true}
	if err := d.Create(&branch).Error; err != nil {
		t.Fatal(err)
	}
	if err := d.Create(&database.TenantCashSession{
		BranchID: branch.ID, UserID: 1, OpenedBy: 1, Status: "open", OpenedAt: time.Now(),
	}).Error; err != nil {
		t.Fatal(err)
	}
	series := database.TenantDocumentSeries{
		BranchID: branch.ID, DocType: "Nota de Venta", SunatCode: "00", Series: "NV01", Correlative: 1, Active: true,
	}
	if err := d.Create(&series).Error; err != nil {
		t.Fatal(err)
	}
	return d, product, caja, branch.ID, series.ID
}

// Caso 1: producto legacy (sin SaleUnit en la línea) → Unit = unidad base del producto ("NIU"),
// exactamente como antes de esta corrección. No debe romperse nada existente.
func TestSaleItemUnit_Legacy_NoSaleUnit(t *testing.T) {
	db, product, _, branchID, seriesID := setupTeclado(t)
	pid := product.ID

	sale, err := NewSaleService(db).Create(CreateSaleInput{
		BranchID: branchID, UserID: 1, SeriesID: seriesID, DocType: "00",
		IssueDate: time.Now(), Currency: "PEN", TaxConfig: tax.DefaultConfig(),
		Payments: []PaymentInput{{Method: "cash", Amount: 4}},
		Items: []SaleItemInput{{
			ProductID: &pid, Quantity: 1, UnitPrice: 4,
			Unit: "NIU", IgvAffectationType: "10", PriceIncludesIgv: true,
		}},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	var item database.TenantSaleItem
	if err := db.Where("sale_id = ?", sale.ID).First(&item).Error; err != nil {
		t.Fatal(err)
	}
	if item.Unit != "NIU" {
		t.Errorf("Unit = %q, want NIU", item.Unit)
	}
	if item.SaleUnitID != nil {
		t.Errorf("SaleUnitID = %v, want nil (línea legacy)", item.SaleUnitID)
	}
}

// Caso 2: venta de 1 Caja (SaleUnit con Unit="BX" ya configurado) → TenantSaleItem.Unit = "BX",
// SaleUnitID = el de la Caja. quantity/unit_price siguen siendo comerciales (1, S/45) — nunca se
// multiplica por el factor (12).
func TestSaleItemUnit_WithSaleUnit_UsesSaleUnitCode(t *testing.T) {
	db, product, caja, branchID, seriesID := setupTeclado(t)
	pid, suid := product.ID, caja.ID

	sale, err := NewSaleService(db).Create(CreateSaleInput{
		BranchID: branchID, UserID: 1, SeriesID: seriesID, DocType: "00",
		IssueDate: time.Now(), Currency: "PEN", TaxConfig: tax.DefaultConfig(),
		Payments: []PaymentInput{{Method: "cash", Amount: 45}},
		Items: []SaleItemInput{{
			ProductID: &pid, SaleUnitID: &suid, Quantity: 1, UnitPrice: 45,
			Unit: "BX", IgvAffectationType: "10", PriceIncludesIgv: true,
		}},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	var item database.TenantSaleItem
	if err := db.Where("sale_id = ?", sale.ID).First(&item).Error; err != nil {
		t.Fatal(err)
	}
	if item.Unit != "BX" {
		t.Errorf("Unit = %q, want BX (código propio de la SaleUnit Caja)", item.Unit)
	}
	if item.SaleUnitID == nil || *item.SaleUnitID != suid {
		t.Errorf("SaleUnitID = %v, want %d", item.SaleUnitID, suid)
	}
	if item.Quantity != 1 {
		t.Errorf("Quantity = %g, want 1 (comercial, nunca 12)", item.Quantity)
	}
	if item.UnitPrice != 45 {
		t.Errorf("UnitPrice = %g, want 45 (nunca 45×12)", item.UnitPrice)
	}
}

// Caso 3: el cliente envía sale_unit_id=Caja pero unit="NIU" (deliberadamente incorrecto/desviado
// del real) — el backend debe IGNORAR ese string y persistir "BX" de todas formas. Este es
// exactamente el ataque que la corrección busca cerrar: "sale_unit_id = Caja, unit = NIU" nunca
// debe terminar persistiendo NIU.
func TestSaleItemUnit_ClientMismatchedUnit_IsIgnored(t *testing.T) {
	db, product, caja, branchID, seriesID := setupTeclado(t)
	pid, suid := product.ID, caja.ID

	sale, err := NewSaleService(db).Create(CreateSaleInput{
		BranchID: branchID, UserID: 1, SeriesID: seriesID, DocType: "00",
		IssueDate: time.Now(), Currency: "PEN", TaxConfig: tax.DefaultConfig(),
		Payments: []PaymentInput{{Method: "cash", Amount: 45}},
		Items: []SaleItemInput{{
			ProductID: &pid, SaleUnitID: &suid, Quantity: 1, UnitPrice: 45,
			// Intento deliberado de desviar el código de unidad: el cliente manda "NIU" en vez
			// de "BX" para la línea que sí usa la SaleUnit Caja.
			Unit: "NIU", IgvAffectationType: "10", PriceIncludesIgv: true,
		}},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	var item database.TenantSaleItem
	if err := db.Where("sale_id = ?", sale.ID).First(&item).Error; err != nil {
		t.Fatal(err)
	}
	if item.Unit != "BX" {
		t.Errorf("Unit = %q, want BX — el backend debe ignorar el 'NIU' enviado por el cliente y resolverlo server-side", item.Unit)
	}
}

// Caso 7: una venta con dos líneas del mismo producto — una por Unidad (legacy) y otra por Caja
// (SaleUnit) — deben coexistir correctamente, cada una con su propio Unit/SaleUnitID/Quantity, sin
// mezclarse ni pisarse entre sí.
func TestSaleItemUnit_UnitAndSaleUnit_CoexistInSameSale(t *testing.T) {
	db, product, caja, branchID, seriesID := setupTeclado(t)
	pid, suid := product.ID, caja.ID

	sale, err := NewSaleService(db).Create(CreateSaleInput{
		BranchID: branchID, UserID: 1, SeriesID: seriesID, DocType: "00",
		IssueDate: time.Now(), Currency: "PEN", TaxConfig: tax.DefaultConfig(),
		Payments: []PaymentInput{{Method: "cash", Amount: 49}},
		Items: []SaleItemInput{
			{
				ProductID: &pid, Quantity: 1, UnitPrice: 4,
				Unit: "NIU", IgvAffectationType: "10", PriceIncludesIgv: true,
			},
			{
				ProductID: &pid, SaleUnitID: &suid, Quantity: 1, UnitPrice: 45,
				Unit: "BX", IgvAffectationType: "10", PriceIncludesIgv: true,
			},
		},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	var items []database.TenantSaleItem
	if err := db.Where("sale_id = ?", sale.ID).Order("id ASC").Find(&items).Error; err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("esperaba 2 líneas, got %d", len(items))
	}
	unidad, cajaItem := items[0], items[1]
	if unidad.Unit != "NIU" || unidad.SaleUnitID != nil || unidad.UnitPrice != 4 {
		t.Errorf("línea Unidad inesperada: %+v", unidad)
	}
	if cajaItem.Unit != "BX" || cajaItem.SaleUnitID == nil || *cajaItem.SaleUnitID != suid || cajaItem.UnitPrice != 45 {
		t.Errorf("línea Caja inesperada: %+v", cajaItem)
	}
}
