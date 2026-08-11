package service

import (
	"fmt"
	"testing"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func setupNoteRefDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []interface{}{
		&database.TenantSale{}, &database.TenantDocumentSeries{}, &database.TenantInvoice{},
	} {
		if err := db.AutoMigrate(m); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

// El listado mostraba «Venta #id»; SUNAT identifica el documento por tipo + serie-correlativo.
func TestEnrichNotesAffectedDoc_DesdeLaVentaOriginal(t *testing.T) {
	db := setupNoteRefDB(t)
	serie := database.TenantDocumentSeries{Series: "F001", SunatCode: "01"}
	if err := db.Create(&serie).Error; err != nil {
		t.Fatal(err)
	}
	orig := database.TenantSale{
		DocType: "FACTURA", Series: "F001", Number: "F001-00000015", Correlative: 15,
		SeriesID: serie.ID,
	}
	if err := db.Create(&orig).Error; err != nil {
		t.Fatal(err)
	}

	sales := []database.TenantSale{
		{ID: 500, DocType: "NOTA_CREDITO", Series: "FC01", Number: "FC01-1", OriginalSaleID: &orig.ID},
	}
	enrichNotesAffectedDoc(db, sales)

	got := sales[0]
	if got.AffectedDocSunatCode != "01" {
		t.Fatalf("affected_doc_sunat_code = %q, se esperaba 01", got.AffectedDocSunatCode)
	}
	if got.AffectedDocType != "FACTURA" {
		t.Fatalf("affected_doc_type = %q, se esperaba FACTURA", got.AffectedDocType)
	}
	if got.AffectedDocSeries != "F001" {
		t.Fatalf("affected_doc_series = %q, se esperaba F001", got.AffectedDocSeries)
	}
	if got.AffectedDocNumber != "F001-15" {
		t.Fatalf("affected_doc_number = %q, se esperaba F001-15", got.AffectedDocNumber)
	}
	// Sin codMotivo declarado, la descripción sale del catálogo 09.
	if got.NoteTypeReason != "" && got.NoteTypeCode == "" {
		t.Fatalf("sin código no debía inventarse motivo, quedó %q", got.NoteTypeReason)
	}
}

// Notas migradas o emitidas fuera del sistema: el dato vive en el payload del facturador.
func TestEnrichNotesAffectedDoc_DesdeElPayload(t *testing.T) {
	db := setupNoteRefDB(t)
	sales := []database.TenantSale{
		{ID: 600, DocType: "NOTA_CREDITO", Series: "BC01", Number: "BC01-3"},
	}
	if err := db.Create(&database.TenantInvoice{
		SaleID: 600,
		NotePayloadJSON: `{"tipDocAfectado":"03","numDocfectado":"B001-4",
			"codMotivo":"01","desMotivo":"Anulación de la operación"}`,
	}).Error; err != nil {
		t.Fatal(err)
	}

	enrichNotesAffectedDoc(db, sales)

	got := sales[0]
	if got.AffectedDocSunatCode != "03" {
		t.Fatalf("affected_doc_sunat_code = %q, se esperaba 03", got.AffectedDocSunatCode)
	}
	if got.AffectedDocType != "BOLETA DE VENTA" {
		t.Fatalf("affected_doc_type = %q", got.AffectedDocType)
	}
	if got.AffectedDocNumber != "B001-4" {
		t.Fatalf("affected_doc_number = %q, se esperaba B001-4", got.AffectedDocNumber)
	}
	if got.AffectedDocSeries != "B001" {
		t.Fatalf("affected_doc_series = %q, se esperaba B001", got.AffectedDocSeries)
	}
	if got.NoteTypeCode != "01" {
		t.Fatalf("note_type_code = %q, se esperaba 01", got.NoteTypeCode)
	}
	if got.NoteTypeReason != "Anulación de la operación" {
		t.Fatalf("note_type_reason = %q", got.NoteTypeReason)
	}
}

// Sin desMotivo declarado se usa la descripción del catálogo, que es la que SUNAT espera.
func TestEnrichNotesAffectedDoc_MotivoDelCatalogo(t *testing.T) {
	db := setupNoteRefDB(t)
	sales := []database.TenantSale{
		{ID: 700, DocType: "NOTA_CREDITO", Series: "BC01", Number: "BC01-9"},
	}
	if err := db.Create(&database.TenantInvoice{
		SaleID:          700,
		NotePayloadJSON: `{"tipDocAfectado":"03","numDocfectado":"B001-9","codMotivo":"06"}`,
	}).Error; err != nil {
		t.Fatal(err)
	}

	enrichNotesAffectedDoc(db, sales)

	if sales[0].NoteTypeReason != "Devolución total" {
		t.Fatalf("note_type_reason = %q, se esperaba «Devolución total»", sales[0].NoteTypeReason)
	}
}

// Las facturas y boletas del mismo listado no deben tocarse.
func TestEnrichNotesAffectedDoc_IgnoraComprobantesNormales(t *testing.T) {
	db := setupNoteRefDB(t)
	sales := []database.TenantSale{
		{ID: 800, DocType: "FACTURA", Series: "F001", Number: "F001-1"},
	}
	enrichNotesAffectedDoc(db, sales)

	if sales[0].AffectedDocNumber != "" || sales[0].NoteTypeCode != "" {
		t.Fatal("una factura no debía recibir datos de nota")
	}
}
