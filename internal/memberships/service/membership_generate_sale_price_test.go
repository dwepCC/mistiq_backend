package service

import (
	"fmt"
	"testing"
	"time"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func setupMembershipConvertDB(t *testing.T) *gorm.DB {
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
		&database.TenantInventoryOperationType{}, &database.TenantMembership{}, &database.TenantMembershipInvoice{},
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

// El monto de la membresía (cuota pactada) es, a propósito, un concepto distinto del SalePrice
// del producto vinculado (que puede no existir o representar otra cosa) — generar el cobro
// periódico no debe reevaluar m.Amount contra validateAuthorizedPrices (incidente 2026-09-17):
// sin PriceAuthorized=true en el ítem de GenerateSale esto se habría roto.
func TestGenerateSale_KeepsMembershipAmount_DifferentFromLinkedProductCatalogPrice(t *testing.T) {
	db := setupMembershipConvertDB(t)
	db.Create(&database.TenantBranch{ID: 1, Name: "Principal", IsMain: true, Active: true})

	contact := database.TenantContact{BusinessName: "Cliente Gym", Type: "customer", Active: true}
	if err := db.Create(&contact).Error; err != nil {
		t.Fatal(err)
	}
	// Producto vinculado con SalePrice de catálogo (100) distinto del monto real de la membresía (80).
	p := database.TenantProduct{
		Code: "MEMBRESIA-STD", Name: "Membresía Standard", Type: "service", Unit: "NIU", SalePrice: 100,
		IgvAffectationType: "10", PriceIncludesIgv: true, ManageStock: false, BranchID: 1, Active: true,
	}
	if err := db.Create(&p).Error; err != nil {
		t.Fatal(err)
	}
	series := database.TenantDocumentSeries{
		BranchID: 1, DocType: "Nota de Venta", SunatCode: "00", Category: "venta", Series: "NV01", Correlative: 1, Active: true,
	}
	if err := db.Create(&series).Error; err != nil {
		t.Fatal(err)
	}

	pid := p.ID
	m := database.TenantMembership{
		ContactID: contact.ID, ProductID: &pid, BranchID: 1, Title: "Plan mensual",
		BillingCycle: "monthly", Amount: 80, Currency: "PEN",
		StartDate: time.Now().AddDate(0, -1, 0), NextBillingDate: time.Now().AddDate(0, 0, -1),
		Status: "active", IgvAffectationType: "10", PriceIncludesIgv: true,
	}
	if err := db.Create(&m).Error; err != nil {
		t.Fatal(err)
	}

	svc := NewMembershipService(db)
	sale, inv, err := svc.GenerateSale(m.ID, 1, 0, GenerateSaleInput{
		SeriesID: series.ID, PaymentMethod: "cash",
	})
	if err != nil {
		t.Fatalf("generar el cobro con el monto pactado de la membresía (80, distinto del catálogo 100) debe funcionar (incidente 2026-09-17), got err: %v", err)
	}
	if sale == nil || inv == nil {
		t.Fatal("sale/inv nil sin error")
	}
}
