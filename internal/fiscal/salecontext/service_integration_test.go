package salecontext

import (
	"fmt"
	"strings"
	"testing"

	"tukifac/pkg/database"
	"tukifac/pkg/facturador"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func setupSaleContextTestDB(t *testing.T) *gorm.DB {
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
	}
	for _, m := range models {
		if err := db.AutoMigrate(m); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func TestIsEmptyFiscalInput(t *testing.T) {
	falseVal := false
	trueVal := true
	if !IsEmptyFiscalInput(nil) {
		t.Fatal("nil should be empty")
	}
	if !IsEmptyFiscalInput(&FiscalContextInput{HasIgvRetention: &falseVal}) {
		t.Fatal("all defaults with explicit false retention should be empty")
	}
	if IsEmptyFiscalInput(&FiscalContextInput{HasIgvRetention: &trueVal}) {
		t.Fatal("retention on should not be empty")
	}
	if IsEmptyFiscalInput(&FiscalContextInput{HasIgvRetention: nil}) {
		t.Fatal("nil retention allows auto-suggest; not empty")
	}
	if IsEmptyFiscalInput(&FiscalContextInput{
		HasIgvRetention: &falseVal,
		PurchaseOrderNumber: "OC-1",
	}) {
		t.Fatal("purchase order should not be empty")
	}
}

func TestPersistLoadRoundtrip_withRetention(t *testing.T) {
	db := setupSaleContextTestDB(t)
	svc := NewService(db)

	retention := true
	out, err := svc.Persist(PersistInput{
		SaleID:       42,
		UserID:       1,
		SunatDocCode: "01",
		SaleTotal:    1000,
		Currency:     "PEN",
		Contact: &ContactSnapshot{
			DocType:             "6",
			DocNumber:           "20100070970",
			EsAgenteDeRetencion: true,
		},
		FiscalContext: &FiscalContextInput{
			HasIgvRetention:     &retention,
			PurchaseOrderNumber: "OC-100",
			FiscalObservations:  "Entrega programada",
			References: []FiscalReferenceInput{
				{ReferenceKind: RefKindGuiaRemitente, ReferencedFullNumber: "T001-00000001"},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out == nil || !out.Summary.RetentionApplied || out.Summary.RetentionAmount != 30 {
		t.Fatalf("unexpected persist output: %+v", out)
	}

	loaded, err := svc.Load(42, 1000)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Profile.PurchaseOrderNumber != "OC-100" {
		t.Fatalf("profile: %+v", loaded.Profile)
	}
	if len(loaded.References) != 1 || loaded.References[0].ReferencedFullNumber != "T001-00000001" {
		t.Fatalf("refs: %+v", loaded.References)
	}
	if !loaded.Summary.RetentionApplied || loaded.Summary.NetCollectible != 970 {
		t.Fatalf("summary: %+v", loaded.Summary)
	}

	enrich := EnrichmentFromOutput(loaded)
	payload := &facturador.InvoicePayload{TipoOperacion: "0101", TipoMoneda: "PEN"}
	ApplyToInvoicePayload(payload, enrich)
	if payload.Compra != "OC-100" {
		t.Fatalf("lycet map compra=%q", payload.Compra)
	}
	if !strings.Contains(payload.Observacion, "Entrega programada") || !strings.Contains(payload.Observacion, "Retención IGV (3%)") {
		t.Fatalf("lycet map obs=%q", payload.Observacion)
	}
	if len(payload.Guias) != 1 || payload.Guias[0].NroDoc != "T001-00000001" {
		t.Fatalf("guias: %+v", payload.Guias)
	}
}

func TestPersist_skipsEmptyInput(t *testing.T) {
	db := setupSaleContextTestDB(t)
	svc := NewService(db)
	falseVal := false
	out, err := svc.Persist(PersistInput{
		SaleID:       99,
		UserID:       1,
		SunatDocCode: "03",
		SaleTotal:    50,
		Currency:     "PEN",
		FiscalContext: &FiscalContextInput{
			HasIgvRetention: &falseVal,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out != nil {
		t.Fatal("empty fiscal input should not persist")
	}
	var count int64
	db.Model(&database.TenantSaleFiscalProfile{}).Where("sale_id = ?", 99).Count(&count)
	if count != 0 {
		t.Fatalf("expected no profile row, got %d", count)
	}
}
