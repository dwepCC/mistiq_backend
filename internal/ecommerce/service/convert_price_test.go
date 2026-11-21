package service

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"tukifac/pkg/database"
	"tukifac/pkg/tax"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func setupEcommerceConvertDB(t *testing.T) *gorm.DB {
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
		&database.TenantInventoryOperationType{}, &database.TenantEcommerceOrder{},
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

// El precio del pedido web es el que el cliente vio y pagó al hacer el pedido — puede diferir del
// catálogo vigente al momento de convertir (el pedido puede quedar pendiente un tiempo mientras el
// precio del producto cambia). Convertir no debe reevaluarlo contra validateAuthorizedPrices
// (incidente 2026-09-17): sin PriceAuthorized=true en ConvertToSale esto se habría roto.
func TestConvertToSale_KeepsOrderPrice_EvenIfCatalogChangedSince(t *testing.T) {
	db := setupEcommerceConvertDB(t)
	db.Create(&database.TenantBranch{ID: 1, Name: "Principal", IsMain: true, Active: true})

	p := database.TenantProduct{
		Code: "TAZA", Name: "Taza", Type: "product", Unit: "NIU", SalePrice: 12,
		IgvAffectationType: "10", PriceIncludesIgv: true, ManageStock: false, BranchID: 1, Active: true,
	}
	if err := db.Create(&p).Error; err != nil {
		t.Fatal(err)
	}
	nvSeries := database.TenantDocumentSeries{
		BranchID: 1, DocType: "Nota de Venta", SunatCode: "00", Series: "NV01", Correlative: 1, Active: true,
	}
	if err := db.Create(&nvSeries).Error; err != nil {
		t.Fatal(err)
	}

	itemsJSON, err := json.Marshal([]OrderItemInput{{ProductID: p.ID, Name: p.Name, Quantity: 1, UnitPrice: 12}})
	if err != nil {
		t.Fatal(err)
	}
	order := database.TenantEcommerceOrder{ItemsJSON: string(itemsJSON), Total: 12, Status: "nuevo"}
	if err := db.Create(&order).Error; err != nil {
		t.Fatal(err)
	}

	// El catálogo sube de precio DESPUÉS de que el cliente hizo el pedido.
	if err := db.Model(&p).Update("sale_price", 15).Error; err != nil {
		t.Fatal(err)
	}

	svc := &EcommerceService{db: db}
	sale, err := svc.ConvertToSale(order.ID, ConvertInput{
		Target: "nota_venta", SeriesID: nvSeries.ID, BranchID: 1, IssueDate: time.Now(), UserID: 1,
		TaxConfig: tax.DefaultConfig(),
	})
	if err != nil {
		t.Fatalf("convertir pedido web con precio distinto al catálogo actual debe funcionar (incidente 2026-09-17), got err: %v", err)
	}
	if sale == nil {
		t.Fatal("sale nil sin error")
	}
}
