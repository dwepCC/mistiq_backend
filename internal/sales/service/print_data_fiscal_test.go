package service

import (
	"fmt"
	"testing"
	"time"

	"tukifac/internal/fiscal/salecontext"
	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func setupPrintDataFiscalTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	models := []interface{}{
		&database.TenantSaleFiscalProfile{},
		&database.TenantSaleFiscalReference{},
		&database.TenantSaleFiscalObligation{},
		&database.TenantCompanyConfig{},
		&database.TenantUser{},
		&database.TenantSaleDetraccion{},
	}
	for _, m := range models {
		if err := db.AutoMigrate(m); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func TestEnrichFiscalPrintData_retentionAndGuia(t *testing.T) {
	db := setupPrintDataFiscalTestDB(t)

	retention := true
	_, err := salecontext.NewService(db).Persist(salecontext.PersistInput{
		SaleID:       7,
		UserID:       1,
		SunatDocCode: "01",
		SaleTotal:    800,
		Currency:     "PEN",
		Contact: &salecontext.ContactSnapshot{
			DocType:             "6",
			EsAgenteDeRetencion: true,
		},
		FiscalContext: &salecontext.FiscalContextInput{
			HasIgvRetention:     &retention,
			PurchaseOrderNumber: "OC-55",
			References: []salecontext.FiscalReferenceInput{
				{ReferenceKind: salecontext.RefKindGuiaRemitente, ReferencedFullNumber: "T001-2"},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	pd := &PrintData{Total: 800, Currency: "PEN"}
	enrichFiscalPrintData(db, 7, 800, pd)
	if pd.Fiscal == nil {
		t.Fatal("expected fiscal block in print_data")
	}
	if !pd.Fiscal.RetentionApplied || pd.Fiscal.IgvRetentionAmount != 24 {
		t.Fatalf("retention: applied=%v amount=%v", pd.Fiscal.RetentionApplied, pd.Fiscal.IgvRetentionAmount)
	}
	if pd.Fiscal.NetCollectible != 776 {
		t.Fatalf("net: %v", pd.Fiscal.NetCollectible)
	}
	if pd.Fiscal.PurchaseOrderNumber != "OC-55" || len(pd.Fiscal.Guias) != 1 {
		t.Fatalf("fiscal meta: %+v", pd.Fiscal)
	}
}

// Bug reportado: el PDF local (Tukifac) no mostraba la misma "Información Adicional" que el
// PDF del facturador para una factura con detracción — le faltaba la leyenda SUNAT (catálogo
// 2006) y la descripción del medio de pago (el bien/servicio sí se resolvía, el medio de pago
// solo traía el código crudo "001" sin su etiqueta "Depósito en cuenta").
func TestEnrichFiscalPrintData_detraccionLegendAndPaymentMethodLabel(t *testing.T) {
	db := setupPrintDataFiscalTestDB(t)
	det := database.TenantSaleDetraccion{
		SaleID: 323, GoodCode: "022", PaymentMethodCode: "001", BankAccount: "00046080866",
		RatePercent: 12, BaseAmountPen: 2498.05, DetractionAmountPen: 353.72,
		InvoiceTotalPen: 2947.70, NetPayablePen: 2593.98, BnConfirmationStatus: "pending",
	}
	if err := db.Create(&det).Error; err != nil {
		t.Fatal(err)
	}

	pd := &PrintData{Total: 2947.70, Currency: "PEN"}
	enrichFiscalPrintData(db, 323, 2947.70, pd)
	if pd.Fiscal == nil || !pd.Fiscal.HasDetraccion {
		t.Fatalf("expected has_detraccion=true, got %+v", pd.Fiscal)
	}
	if pd.Fiscal.DetraccionGoodCode != "022" || pd.Fiscal.DetraccionGoodLabel == "" || pd.Fiscal.DetraccionGoodLabel == "022" {
		t.Fatalf("bien/servicio sin resolver: %+v", pd.Fiscal)
	}
	if pd.Fiscal.DetraccionPaymentMethodCode != "001" {
		t.Fatalf("payment method code: %q", pd.Fiscal.DetraccionPaymentMethodCode)
	}
	if pd.Fiscal.DetraccionPaymentMethodLabel != "Depósito en cuenta" {
		t.Fatalf("medio de pago sin resolver: %q", pd.Fiscal.DetraccionPaymentMethodLabel)
	}
	if pd.Fiscal.DetraccionLegendText != "Operación sujeta a detracción" {
		t.Fatalf("leyenda SUNAT 2006 ausente: %q", pd.Fiscal.DetraccionLegendText)
	}
}

func TestEnrichFiscalPrintData_noProfile(t *testing.T) {
	db := setupPrintDataFiscalTestDB(t)
	pd := &PrintData{Total: 100}
	enrichFiscalPrintData(db, 404, 100, pd)
	if pd.Fiscal != nil {
		t.Fatal("POS-like sale without fiscal profile should not set fiscal block")
	}
}

func TestEnrichFiscalPrintData_termsFromCompany(t *testing.T) {
	db := setupPrintDataFiscalTestDB(t)
	db.Create(&database.TenantCompanyConfig{
		ID:                 1,
		TermsAndConditions: "Plazo de pago 30 días",
	})

	_, err := salecontext.NewService(db).Persist(salecontext.PersistInput{
		SaleID:       8,
		UserID:       1,
		SunatDocCode: "01",
		SaleTotal:    100,
		Currency:     "PEN",
		FiscalContext: &salecontext.FiscalContextInput{
			ShowTermsConditions: true,
			PurchaseOrderNumber: "X",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	pd := &PrintData{Total: 100}
	enrichFiscalPrintData(db, 8, 100, pd)
	if pd.Fiscal == nil || !pd.Fiscal.ShowTermsConditions || pd.Fiscal.TermsText != "Plazo de pago 30 días" {
		t.Fatalf("terms: %+v", pd.Fiscal)
	}
}

func TestEnrichFiscalPrintData_sellerOverride(t *testing.T) {
	db := setupPrintDataFiscalTestDB(t)
	sellerID := uint(5)
	db.Create(&database.TenantUser{ID: 5, Name: "Vendedor Fiscal", Email: "v@test.com", CreatedAt: time.Now()})

	_, err := salecontext.NewService(db).Persist(salecontext.PersistInput{
		SaleID:       9,
		UserID:       1,
		SunatDocCode: "01",
		SaleTotal:    100,
		Currency:     "PEN",
		FiscalContext: &salecontext.FiscalContextInput{
			SellerUserID:        &sellerID,
			PurchaseOrderNumber: "OC-1",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	pd := &PrintData{Total: 100, SellerName: "Cajero POS"}
	enrichFiscalPrintData(db, 9, 100, pd)
	if pd.SellerName != "Vendedor Fiscal" {
		t.Fatalf("seller should come from fiscal profile, got %q", pd.SellerName)
	}
}
