package service

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"tukifac/pkg/database"
	"tukifac/pkg/facturador"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestIsNoteSaleDocType(t *testing.T) {
	tests := []struct {
		docType string
		want    bool
	}{
		{"NOTA_CREDITO", true},
		{"NOTA_DEBITO", true},
		{"nota_debito", true},
		{"FACTURA", false},
		{"BOLETA", false},
	}
	for _, tc := range tests {
		if got := isNoteSaleDocType(tc.docType); got != tc.want {
			t.Errorf("isNoteSaleDocType(%q) = %v, want %v", tc.docType, got, tc.want)
		}
	}
}

func TestShouldRegenerateNotePayload(t *testing.T) {
	tests := []struct {
		name string
		inv  *database.TenantInvoice
		want bool
	}{
		{"nil", nil, true},
		{"empty payload", &database.TenantInvoice{}, true},
		{"error status", &database.TenantInvoice{NotePayloadJSON: `{"tipoDoc":"07"}`, SunatStatus: "error"}, true},
		{"pending cached", &database.TenantInvoice{NotePayloadJSON: `{"tipoDoc":"07"}`, SunatStatus: "pending"}, false},
		{"accepted cached", &database.TenantInvoice{NotePayloadJSON: `{"tipoDoc":"08"}`, SunatStatus: "accepted"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldRegenerateNotePayload(tc.inv); got != tc.want {
				t.Fatalf("shouldRegenerateNotePayload() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestParseNotePayloadJSON(t *testing.T) {
	raw := `{"tipoDoc":"08","serie":"FD01","correlativo":"1","mtoImpVenta":1180}`
	p, err := parseNotePayloadJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	if p.TipoDoc != "08" || p.Serie != "FD01" || p.MtoImpVenta != 1180 {
		t.Fatalf("unexpected payload: %+v", p)
	}
	if _, err := parseNotePayloadJSON(""); err == nil {
		t.Fatal("expected error for empty payload")
	}
}

func setupNoteDocumentTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	models := []interface{}{
		&database.TenantCompanyConfig{},
		&database.TenantContact{},
		&database.TenantDocumentSeries{},
		&database.TenantSale{},
		&database.TenantSaleItem{},
		&database.TenantInvoice{},
		&database.TenantSaleDetraccion{},
		&database.UbiRegion{},
		&database.UbiProvincia{},
		&database.UbiDistrito{},
	}
	for _, m := range models {
		if err := db.AutoMigrate(m); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Create(&database.UbiRegion{ID: "15", Nombre: "LIMA"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.UbiProvincia{ID: "1501", Nombre: "LIMA", RegionID: "15"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.UbiDistrito{ID: "150101", Nombre: "LIMA", ProvinciaID: "1501", RegionID: "15"}).Error; err != nil {
		t.Fatal(err)
	}
	return db
}

func seedNoteDocumentFixtures(t *testing.T, db *gorm.DB, noteDocType, sunatCode string) (origSaleID, noteSaleID uint) {
	t.Helper()
	now := time.Now()
	if err := db.Create(&database.TenantCompanyConfig{
		RUC: "20123456789", BusinessName: "EMPRESA SAC", TradeName: "EMPRESA",
		Address: "Av. Test 123", Ubigeo: "150101", SunatEnabled: true, TaxRate: 18,
	}).Error; err != nil {
		t.Fatal(err)
	}
	contactID := uint(1)
	if err := db.Create(&database.TenantContact{
		ID: contactID, DocType: "RUC", DocNumber: "20100070970", BusinessName: "CLIENTE SAC",
		Address: "Jr Cliente 1", Ubigeo: "150101",
	}).Error; err != nil {
		t.Fatal(err)
	}
	origSeriesID := uint(1)
	if err := db.Create(&database.TenantDocumentSeries{
		ID: origSeriesID, BranchID: 1, Category: "factura", SunatCode: "01", Series: "F001", Active: true,
	}).Error; err != nil {
		t.Fatal(err)
	}
	noteSeriesID := uint(2)
	category := "nota_credito"
	if sunatCode == "08" {
		category = "nota_debito"
	}
	if err := db.Create(&database.TenantDocumentSeries{
		ID: noteSeriesID, BranchID: 1, Category: category, SunatCode: sunatCode, Series: "FC01", Active: true,
	}).Error; err != nil {
		t.Fatal(err)
	}
	orig := database.TenantSale{
		BranchID: 1, ContactID: &contactID, SeriesID: origSeriesID, DocType: "FACTURA",
		Series: "F001", Correlative: 100, Number: "F001-00000100", IssueDate: now,
		Subtotal: 1000, TaxAmount: 180, Total: 1180, Currency: "PEN",
		OperationTypeCode: "1001", Status: "paid", BillingStatus: "accepted",
	}
	if err := db.Create(&orig).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.TenantSaleDetraccion{
		SaleID: orig.ID, GoodCode: "022", PaymentMethodCode: "001", BankAccount: "0000000000001",
		RatePercent: 12, BaseAmountPen: 1000, DetractionAmountPen: 120, InvoiceTotalPen: 1180, NetPayablePen: 1060,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.TenantSaleItem{
		SaleID: orig.ID, Code: "P001", Description: "Servicio", Unit: "NIU", Quantity: 1,
		UnitPrice: 1180, TaxRate: 18, IgvAffectationType: "10", Subtotal: 1000, TaxAmount: 180, Total: 1180,
	}).Error; err != nil {
		t.Fatal(err)
	}
	origID := orig.ID
	note := database.TenantSale{
		BranchID: 1, ContactID: &contactID, SeriesID: noteSeriesID, DocType: noteDocType,
		Series: "FC01", Correlative: 1, Number: "FC01-00000001", IssueDate: now,
		Subtotal: 1000, TaxAmount: 180, Total: 1180, Currency: "PEN",
		Status: "paid", BillingStatus: "pending", OriginalSaleID: &origID,
		Notes: "Anulación de la operación",
	}
	if err := db.Create(&note).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.TenantSaleItem{
		SaleID: note.ID, Code: "P001", Description: "Servicio", Unit: "NIU", Quantity: 1,
		UnitPrice: 1180, TaxRate: 18, IgvAffectationType: "10", Subtotal: 1000, TaxAmount: 180, Total: 1180,
	}).Error; err != nil {
		t.Fatal(err)
	}
	return orig.ID, note.ID
}

func mockFacturadorNoteServer(t *testing.T, pdfBody, xmlBody []byte) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/note/pdf"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(pdfBody)
		case strings.HasSuffix(r.URL.Path, "/note/xml"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(xmlBody)
		default:
			http.NotFound(w, r)
		}
	}))
}

func notePayloadFixture(tipoDoc string) string {
	p := facturador.NotePayload{
		TipoDoc: tipoDoc, Serie: "FC01", Correlativo: "1", MtoImpVenta: 1180,
		TipDocAfectado: "01", NumDocfectado: "F001-100",
	}
	b, _ := json.Marshal(p)
	return string(b)
}

func TestGetInvoicePDFContent_NotaCreditoAndDebito(t *testing.T) {
	pdfBytes := []byte("%PDF-note-test")
	server := mockFacturadorNoteServer(t, pdfBytes, []byte("<xml/>"))
	defer server.Close()

	tests := []struct {
		name    string
		docType string
		tipoDoc string
	}{
		{"NC PDF", "NOTA_CREDITO", "07"},
		{"ND PDF", "NOTA_DEBITO", "08"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db := setupNoteDocumentTestDB(t)
			_, noteID := seedNoteDocumentFixtures(t, db, tc.docType, tc.tipoDoc)
			if err := db.Create(&database.TenantInvoice{
				SaleID: noteID, NotePayloadJSON: notePayloadFixture(tc.tipoDoc), SunatStatus: "accepted",
			}).Error; err != nil {
				t.Fatal(err)
			}
			svc := &BillingService{db: db, baseURL: server.URL, token: "tok"}
			got, err := svc.GetInvoicePDFContent(noteID)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != string(pdfBytes) {
				t.Fatalf("PDF content mismatch: %q", got)
			}
		})
	}
}

func TestGetInvoiceXMLGeneratedContent_NotaCreditoAndDebito(t *testing.T) {
	xmlBytes := []byte(`<?xml version="1.0"?><Invoice/>`)
	server := mockFacturadorNoteServer(t, []byte("%PDF"), xmlBytes)
	defer server.Close()

	tests := []struct {
		name    string
		docType string
		tipoDoc string
	}{
		{"NC XML", "NOTA_CREDITO", "07"},
		{"ND XML", "NOTA_DEBITO", "08"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db := setupNoteDocumentTestDB(t)
			_, noteID := seedNoteDocumentFixtures(t, db, tc.docType, tc.tipoDoc)
			if err := db.Create(&database.TenantInvoice{
				SaleID: noteID, NotePayloadJSON: notePayloadFixture(tc.tipoDoc), SunatStatus: "accepted",
			}).Error; err != nil {
				t.Fatal(err)
			}
			svc := &BillingService{db: db, baseURL: server.URL, token: "tok"}
			got, err := svc.GetInvoiceXMLGeneratedContent(noteID)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != string(xmlBytes) {
				t.Fatalf("XML content mismatch: %q", got)
			}
		})
	}
}

func TestBuildAndPersistNotePayload_regeneratesOnError_forCreditNoteOverDetractionInvoice(t *testing.T) {
	db := setupNoteDocumentTestDB(t)
	_, noteID := seedNoteDocumentFixtures(t, db, "NOTA_CREDITO", "07")
	stale := `{"tipoDoc":"07","serie":"FC01","correlativo":"1","mtoImpVenta":1,"tipDocAfectado":"01","numDocfectado":"F001-100"}`
	if err := db.Create(&database.TenantInvoice{
		SaleID: noteID, NotePayloadJSON: stale, SunatStatus: "error",
	}).Error; err != nil {
		t.Fatal(err)
	}
	svc := &BillingService{db: db}
	payload, err := svc.buildNotePayload(noteID)
	if err != nil {
		t.Fatal(err)
	}
	if payload.MtoImpVenta != 1180 {
		t.Fatalf("expected mtoImpVenta 1180 from sale items, got %v", payload.MtoImpVenta)
	}
	if payload.TipDocAfectado != "01" || payload.NumDocfectado == "" {
		t.Fatalf("expected reference to detraction invoice 01: %+v", payload)
	}
	if !shouldRegenerateNotePayload(&database.TenantInvoice{NotePayloadJSON: stale, SunatStatus: "error"}) {
		t.Fatal("expected regeneration flag for error status")
	}
}

func TestBuildAndPersistNotePayload_regeneratesOnError_forDebitNoteOverDetractionInvoice(t *testing.T) {
	db := setupNoteDocumentTestDB(t)
	_, noteID := seedNoteDocumentFixtures(t, db, "NOTA_DEBITO", "08")
	stale := `{"tipoDoc":"08","serie":"FC01","correlativo":"1","mtoImpVenta":999}`
	if err := db.Create(&database.TenantInvoice{
		SaleID: noteID, NotePayloadJSON: stale, SunatStatus: "error",
	}).Error; err != nil {
		t.Fatal(err)
	}
	svc := &BillingService{db: db}
	payload, err := svc.buildNotePayload(noteID)
	if err != nil {
		t.Fatal(err)
	}
	if payload.TipoDoc != "08" {
		t.Fatalf("expected ND tipo 08, got %q", payload.TipoDoc)
	}
	if payload.MtoImpVenta != 1180 {
		t.Fatalf("expected mtoImpVenta 1180, got %v", payload.MtoImpVenta)
	}
}

func TestEmitNoteDocument_usesCachedWhenNotError(t *testing.T) {
	db := setupNoteDocumentTestDB(t)
	_, noteID := seedNoteDocumentFixtures(t, db, "NOTA_CREDITO", "07")
	cached := notePayloadFixture("07")
	if err := db.Create(&database.TenantInvoice{
		SaleID: noteID, NotePayloadJSON: cached, SunatStatus: "pending",
	}).Error; err != nil {
		t.Fatal(err)
	}
	inv := &database.TenantInvoice{NotePayloadJSON: cached, SunatStatus: "pending"}
	if shouldRegenerateNotePayload(inv) {
		t.Fatal("pending note should not regenerate")
	}
}
