package service

import (
	"fmt"
	"testing"
	"time"

	"tukifac/pkg/database"
	"tukifac/pkg/salecurrency"
	"tukifac/pkg/tax"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func setupIssueElectronicFromNotaTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	models := []interface{}{
		&database.TenantCompanyConfig{},
		&database.TenantDocumentSeries{},
		&database.TenantSale{},
		&database.TenantSaleItem{},
		&database.TenantSalePayment{},
	}
	for _, m := range models {
		if err := db.AutoMigrate(m); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Create(&database.TenantCompanyConfig{ID: 1, SunatEnabled: true, TaxRate: 18}).Error; err != nil {
		t.Fatal(err)
	}
	return db
}

func seedNotaVentaUSD(t *testing.T, db *gorm.DB, withTC bool) (notaID, boletaSeriesID uint) {
	t.Helper()
	nvSeries := database.TenantDocumentSeries{
		BranchID: 1, DocType: "NOTA DE VENTA", SunatCode: "00", Category: "venta",
		Series: "NV001", Correlative: 1, Active: true,
	}
	boletaSeries := database.TenantDocumentSeries{
		BranchID: 1, DocType: "BOLETA", SunatCode: "03", Category: "venta",
		Series: "B001", Correlative: 1, Active: true,
	}
	if err := db.Create(&nvSeries).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&boletaSeries).Error; err != nil {
		t.Fatal(err)
	}
	var rate *float64
	if withTC {
		r := 3.75
		rate = &r
	}
	nota := database.TenantSale{
		BranchID: 1, UserID: 1, SeriesID: nvSeries.ID, DocType: "NOTA DE VENTA",
		Series: "NV001", Correlative: 1, Number: "NV001-00000001",
		IssueDate: time.Now(), Subtotal: 100, TaxAmount: 18, Total: 118,
		Currency: salecurrency.CurrencyUSD, ExchangeRate: rate, Status: "paid",
	}
	if err := db.Create(&nota).Error; err != nil {
		t.Fatal(err)
	}
	item := database.TenantSaleItem{
		SaleID: nota.ID, Code: "P1", Description: "Producto", Unit: "NIU",
		Quantity: 1, UnitPrice: 100, Subtotal: 100, TaxAmount: 18, Total: 118,
		IgvAffectationType: "10",
	}
	if err := db.Create(&item).Error; err != nil {
		t.Fatal(err)
	}
	pay := database.TenantSalePayment{SaleID: nota.ID, Method: "cash", Amount: 118}
	if err := db.Create(&pay).Error; err != nil {
		t.Fatal(err)
	}
	return nota.ID, boletaSeries.ID
}

func TestIssueElectronicFromNota_copiesCurrencyAndExchangeRate(t *testing.T) {
	db := setupIssueElectronicFromNotaTestDB(t)
	notaID, boletaSeriesID := seedNotaVentaUSD(t, db, true)

	svc := NewSaleService(db)
	child, err := svc.IssueElectronicFromNota(notaID, boletaSeriesID, 1, "", 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if child.Currency != salecurrency.CurrencyUSD {
		t.Fatalf("currency: got %q", child.Currency)
	}
	if child.ExchangeRate == nil || *child.ExchangeRate != 3.75 {
		t.Fatalf("exchange_rate: %+v", child.ExchangeRate)
	}
	if child.IssuedFromNotaSaleID == nil || *child.IssuedFromNotaSaleID != notaID {
		t.Fatalf("issued_from_nota_sale_id: %+v", child.IssuedFromNotaSaleID)
	}
}

func TestIssueElectronicFromNota_rejectsUSDWithoutExchangeRate(t *testing.T) {
	db := setupIssueElectronicFromNotaTestDB(t)
	notaID, boletaSeriesID := seedNotaVentaUSD(t, db, false)

	svc := NewSaleService(db)
	_, err := svc.IssueElectronicFromNota(notaID, boletaSeriesID, 1, "", 0, nil)
	if err == nil {
		t.Fatal("expected error for USD NV without exchange rate")
	}
}

func TestIssueElectronicFromNota_usesDefaultOperationType(t *testing.T) {
	db := setupIssueElectronicFromNotaTestDB(t)
	notaID, boletaSeriesID := seedNotaVentaUSD(t, db, true)

	svc := NewSaleService(db)
	child, err := svc.IssueElectronicFromNota(notaID, boletaSeriesID, 1, "", 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if child.OperationTypeCode != salecurrency.OpVentaInterna {
		t.Fatalf("operation_type_code: got %q", child.OperationTypeCode)
	}
	_ = tax.DefaultConfig()
}
