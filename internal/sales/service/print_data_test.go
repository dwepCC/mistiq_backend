package service

import (
	"fmt"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"tukifac/pkg/database"
)

func setupPrintDataTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	return db
}

// Bug reportado (tenant RUC 20548414424, F001-86): en el PDF de una factura a crédito, la
// cabecera mostraba "Fecha Vencimiento: 2026-08-31" (igual a la fecha de emisión) mientras la
// tabla de CUOTAS, más abajo, mostraba la fecha real de la cuota (2026-09-20) — misma venta,
// dos fechas distintas. Causa: PrintData.ValidUntil nunca se asignaba en BuildPrintData (solo
// lo llena quotations/service/print_data.go para cotizaciones); el frontend
// (receiptPdfA4.ts resolveDueDate) cae a issue_date cuando valid_until viene vacío.
func TestBuildPrintData_validUntilFromDueDate(t *testing.T) {
	db := setupPrintDataTestDB(t)
	due := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	sale := &database.TenantSale{
		ID:                   339,
		DocType:              "01",
		Series:               "F001",
		Number:               "F001-00000086",
		IssueDate:            time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC),
		DueDate:              &due,
		Currency:             "PEN",
		Total:                5331.24,
		PaymentConditionCode: "credit",
		Status:               "credit",
	}

	pd, err := BuildPrintData(db, sale, nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if pd.ValidUntil != "20/09/2026" {
		t.Fatalf("valid_until: got %q, want %q (misma fecha que la cuota, no la de emisión)", pd.ValidUntil, "20/09/2026")
	}
	if pd.ValidUntil == pd.IssueDate {
		t.Fatalf("valid_until no debe coincidir con issue_date en una venta a crédito: %q", pd.ValidUntil)
	}
}

// Venta al contado (o sin fecha de vencimiento): sale.DueDate es nil, valid_until debe quedar
// vacío — el frontend cae a issue_date, que es el comportamiento correcto en ese caso.
func TestBuildPrintData_validUntilEmptyWhenNoDueDate(t *testing.T) {
	db := setupPrintDataTestDB(t)
	sale := &database.TenantSale{
		ID:        1,
		DocType:   "03",
		Series:    "B001",
		Number:    "B001-1",
		IssueDate: time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC),
		Currency:  "PEN",
		Total:     100,
	}

	pd, err := BuildPrintData(db, sale, nil, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if pd.ValidUntil != "" {
		t.Fatalf("valid_until debería quedar vacío sin due_date, got %q", pd.ValidUntil)
	}
}
