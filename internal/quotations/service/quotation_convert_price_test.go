package service

import (
	"fmt"
	"testing"
	"time"

	"tukifac/pkg/database"
	"tukifac/pkg/tax"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// setupQuotationConvertDB monta lo mínimo para crear una cotización y convertirla a nota de
// venta: mismas tablas que exige SaleService.Create (ver sale_price_authorization_test.go en
// internal/sales/service) más TenantQuotation/TenantQuotationItem.
func setupQuotationConvertDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	models := []interface{}{
		&database.TenantCompanyConfig{}, &database.TenantDocumentSeries{}, &database.TenantContact{},
		&database.TenantSale{}, &database.TenantSaleItem{}, &database.TenantSalePayment{},
		&database.TenantCashSession{}, &database.TenantPaymentMethod{}, &database.TenantProduct{},
		&database.TenantBranch{}, &database.TenantProductStock{}, &database.TenantStockMovement{},
		&database.TenantInventoryOperationType{}, &database.TenantQuotation{}, &database.TenantQuotationItem{},
		&database.TenantProductBranchPrice{},
	}
	for _, m := range models {
		if err := db.AutoMigrate(m); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Create(&database.TenantCompanyConfig{ID: 1, SunatEnabled: true, TaxRate: 18}).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.SeedInventoryOperationTypes(db); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.TenantPaymentMethod{Code: "cash", Name: "Efectivo", IsSystem: true, Active: true}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.TenantCashSession{
		BranchID: 1, UserID: 1, OpenedBy: 1, Status: "open", OpenedAt: time.Now(),
	}).Error; err != nil {
		t.Fatal(err)
	}
	return db
}

// Reproduce el escenario real que sale_price_authorization.go rompería si esta conversión no
// marcara PriceAuthorized=true: una cotización pactó un precio (25) distinto al de catálogo
// vigente (30, cambiado después de cotizar) — convertirla a nota de venta debe seguir
// funcionando, sin reevaluar el precio contra el catálogo actual (incidente 2026-09-17).
func TestConvertToSale_KeepsNegotiatedPrice_EvenIfCatalogChangedSince(t *testing.T) {
	db := setupQuotationConvertDB(t)
	db.Create(&database.TenantBranch{ID: 1, Name: "Principal", IsMain: true, Active: true})

	p := database.TenantProduct{
		Code: "SILLA", Name: "Silla de oficina", Type: "product", Unit: "NIU", SalePrice: 25,
		IgvAffectationType: "10", PriceIncludesIgv: true, ManageStock: false, BranchID: 1, Active: true,
	}
	if err := db.Create(&p).Error; err != nil {
		t.Fatal(err)
	}

	quoteSeries := database.TenantDocumentSeries{
		BranchID: 1, DocType: "Cotización", Category: "cotizacion", Series: "COT1", Correlative: 1, Active: true,
	}
	if err := db.Create(&quoteSeries).Error; err != nil {
		t.Fatal(err)
	}
	nvSeries := database.TenantDocumentSeries{
		BranchID: 1, DocType: "Nota de Venta", SunatCode: "00", Series: "NV01", Correlative: 1, Active: true,
	}
	if err := db.Create(&nvSeries).Error; err != nil {
		t.Fatal(err)
	}

	pid := p.ID
	svc := NewQuotationService(db)
	q, err := svc.Create(CreateQuotationInput{
		BranchID: 1, UserID: 1, SeriesID: quoteSeries.ID, IssueDate: time.Now(), Currency: "PEN",
		Items: []QuotationItemInput{{
			ProductID: &pid, Quantity: 1, UnitPrice: 25, Unit: "NIU", IgvAffectationType: "10", PriceIncludesIgv: true,
		}},
		TaxConfig: tax.DefaultConfig(),
	})
	if err != nil {
		t.Fatalf("Create cotización: %v", err)
	}

	// El precio de catálogo cambia DESPUÉS de cotizar — la cotización sigue con el precio pactado.
	if err := db.Model(&p).Update("sale_price", 30).Error; err != nil {
		t.Fatal(err)
	}

	sale, err := svc.ConvertToSale(q.ID, ConvertInput{
		Target: "nota_venta", SeriesID: nvSeries.ID, IssueDate: time.Now(), UserID: 1,
		TaxConfig: tax.DefaultConfig(),
	})
	if err != nil {
		t.Fatalf("convertir cotización con precio pactado distinto al catálogo actual debe funcionar (incidente 2026-09-17), got err: %v", err)
	}
	if sale == nil {
		t.Fatal("sale nil sin error")
	}
}
