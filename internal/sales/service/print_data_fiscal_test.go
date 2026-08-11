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
