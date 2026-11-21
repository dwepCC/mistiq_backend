package service

import (
	"math"
	"strings"
	"testing"
	"time"

	"tukifac/pkg/database"
	"tukifac/pkg/tax"

	"gorm.io/gorm"
)

// Venta a granel por gramo/mililitro: la unidad de venta "Gramo" (GRM, factor 0.001) cuesta una
// fracción de céntimo (S/ 0.0045 si el kilo vale S/ 4.50). Cubre el precio con más de 2 decimales,
// el descuento de stock en unidad base y la unidad SUNAT que se declara en la línea.

type bulkFixture struct {
	db       *gorm.DB
	product  database.TenantProduct
	gramo    database.TenantProductSaleUnit
	branchID uint
	seriesID uint
}

func setupArrozGranel(t *testing.T) bulkFixture {
	t.Helper()
	d := setupSaleUnitIntegrationDB(t)
	product := database.TenantProduct{
		Code: "ARR-GR", Name: "Arroz a granel", Type: "product", Unit: "KGM", SalePrice: 4.5,
		ManageStock: true, IgvAffectationType: "10", PriceIncludesIgv: true, Active: true,
	}
	if err := d.Create(&product).Error; err != nil {
		t.Fatal(err)
	}
	if err := d.Create(&database.TenantProductStock{ProductID: product.ID, BranchID: 1, Quantity: 100}).Error; err != nil {
		t.Fatal(err)
	}
	gramo := database.TenantProductSaleUnit{
		ProductID: product.ID, Name: "Gramo", Unit: "GRM", ConversionFactor: 0.001,
		AllowFraction: false, Price1: 0.0045, Active: true,
	}
	if err := d.Create(&gramo).Error; err != nil {
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
	return bulkFixture{d, product, gramo, branch.ID, series.ID}
}

func (f bulkFixture) sell(qty, unitPrice, pay float64) (*database.TenantSale, error) {
	pid, suid := f.product.ID, f.gramo.ID
	return NewSaleService(f.db).Create(CreateSaleInput{
		BranchID: f.branchID, UserID: 1, SeriesID: f.seriesID, DocType: "00",
		IssueDate: time.Now(), Currency: "PEN", TaxConfig: tax.DefaultConfig(),
		Payments: []PaymentInput{{Method: "cash", Amount: pay}},
		Items: []SaleItemInput{{
			ProductID: &pid, SaleUnitID: &suid, Quantity: qty, UnitPrice: unitPrice,
			Unit: "KGM", IgvAffectationType: "10", PriceIncludesIgv: true,
		}},
	})
}

// 250 g a S/ 0.0045: la línea se declara como 250 GRM (nunca 250 KGM), el precio unitario conserva
// sus decimales y el stock baja 0.25 kg.
func TestBulkSale_Grams_UnitPriceUnitCodeAndStock(t *testing.T) {
	f := setupArrozGranel(t)
	sale, err := f.sell(250, 0.0045, 1.13)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	var item database.TenantSaleItem
	if err := f.db.Where("sale_id = ?", sale.ID).First(&item).Error; err != nil {
		t.Fatal(err)
	}
	if item.Unit != "GRM" {
		t.Errorf("Unit = %q, want GRM", item.Unit)
	}
	if item.Quantity != 250 {
		t.Errorf("Quantity = %g, want 250", item.Quantity)
	}
	if math.Abs(item.UnitPrice-0.0045) > 1e-9 {
		t.Errorf("UnitPrice = %g, want 0.0045 (sin truncar a 2 decimales)", item.UnitPrice)
	}
	if math.Abs(sale.Total-1.125) > 0.005 {
		t.Errorf("Total = %g, want ≈1.125 (250 × 0.0045)", sale.Total)
	}
	var stock database.TenantProductStock
	if err := f.db.Where("product_id = ?", f.product.ID).First(&stock).Error; err != nil {
		t.Fatal(err)
	}
	if math.Abs(stock.Quantity-99.75) > 1e-9 {
		t.Errorf("stock = %g, want 99.75 (250 g = 0.25 kg)", stock.Quantity)
	}
	var mv database.TenantStockMovement
	if err := f.db.Where("product_id = ?", f.product.ID).Order("id DESC").First(&mv).Error; err != nil {
		t.Fatal(err)
	}
	if math.Abs(mv.Quantity-0.25) > 1e-9 {
		t.Errorf("Kardex.Quantity = %g, want 0.25 (unidad base)", mv.Quantity)
	}
}

// El precio autorizado se valida con la precisión completa de la unidad de venta: S/ 0.005 no es
// S/ 0.0045 aunque ambos redondeen igual a 2 decimales.
func TestBulkSale_Grams_PriceMismatchRejected(t *testing.T) {
	f := setupArrozGranel(t)
	_, err := f.sell(250, 0.005, 1.25)
	if err == nil || !strings.Contains(err.Error(), "precio autorizado") {
		t.Fatalf("esperaba rechazo por precio no autorizado, got %v", err)
	}
	if !strings.Contains(err.Error(), "0.0045") {
		t.Errorf("el mensaje debe mostrar el precio real 0.0045, got %q", err.Error())
	}
}

// Gramo no admite fracciones (AllowFraction=false): 0.5 g se rechaza.
func TestBulkSale_Grams_FractionRejected(t *testing.T) {
	f := setupArrozGranel(t)
	if _, err := f.sell(0.5, 0.0045, 0.01); err == nil {
		t.Fatal("0.5 g debería rechazarse cuando la unidad no admite fracciones")
	}
}

// Nota de venta → boleta con una línea de 250 g: la boleta copia unidad GRM, cantidad 250 y precio
// 0.0045 sin tocar el stock otra vez (la NV ya descontó 0.25 kg).
func TestBulkSale_Grams_NotaToBoletaKeepsUnitQuantityAndPrice(t *testing.T) {
	f := setupArrozGranel(t)
	nota, err := f.sell(250, 0.0045, 1.13)
	if err != nil {
		t.Fatalf("Create NV: %v", err)
	}
	if err := f.db.AutoMigrate(&database.TenantSaleCreditInstallment{}); err != nil {
		t.Fatal(err)
	}
	boleta := database.TenantDocumentSeries{
		BranchID: f.branchID, DocType: "BOLETA", SunatCode: "03", Category: "venta",
		Series: "B001", Correlative: 1, Active: true,
	}
	if err := f.db.Create(&boleta).Error; err != nil {
		t.Fatal(err)
	}
	child, err := NewSaleService(f.db).IssueElectronicFromNota(nota.ID, boleta.ID, 1, "", 0, nil)
	if err != nil {
		t.Fatalf("IssueElectronicFromNota: %v", err)
	}
	var item database.TenantSaleItem
	if err := f.db.Where("sale_id = ?", child.ID).First(&item).Error; err != nil {
		t.Fatal(err)
	}
	if item.Unit != "GRM" || item.Quantity != 250 || math.Abs(item.UnitPrice-0.0045) > 1e-9 {
		t.Errorf("línea de la boleta = %s %g × %g, want GRM 250 × 0.0045", item.Unit, item.Quantity, item.UnitPrice)
	}
	var stock database.TenantProductStock
	if err := f.db.Where("product_id = ?", f.product.ID).First(&stock).Error; err != nil {
		t.Fatal(err)
	}
	if math.Abs(stock.Quantity-99.75) > 1e-9 {
		t.Errorf("stock = %g, want 99.75 (la boleta no descuenta de nuevo)", stock.Quantity)
	}
}

// El costo snapshot de la línea va en la unidad VENDIDA: kilo a S/ 3 → gramo a S/ 0.003. Sin la
// conversión, la utilidad de 250 g restaba 250 × S/ 3.
func TestBulkSale_Grams_PurchasePriceSnapshotInSoldUnit(t *testing.T) {
	f := setupArrozGranel(t)
	if err := f.db.Model(&database.TenantProduct{}).Where("id = ?", f.product.ID).
		Update("purchase_price", 3.0).Error; err != nil {
		t.Fatal(err)
	}
	pid, suid := f.product.ID, f.gramo.ID
	got := resolveSaleItemPurchasePrice(f.db, SaleItemInput{ProductID: &pid, SaleUnitID: &suid})
	if got == nil || math.Abs(*got-0.003) > 1e-9 {
		t.Fatalf("costo en gramos = %v, want 0.003", got)
	}
	base := resolveSaleItemPurchasePrice(f.db, SaleItemInput{ProductID: &pid})
	if base == nil || math.Abs(*base-3.0) > 1e-9 {
		t.Fatalf("costo en unidad base = %v, want 3", base)
	}
}
