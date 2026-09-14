package service

import (
	"os"
	"testing"
	"time"

	"tukifac/pkg/billingstate"
	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func setupPersistInvoiceDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := "file:persist_invoice_test.db?_journal_mode=WAL&_busy_timeout=15000"
	t.Cleanup(func() { os.Remove("persist_invoice_test.db") })
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&database.TenantSale{}, &database.TenantInvoice{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

// El bug real que dejaba al tenant viendo el 3251 viejo para siempre: reemitir un comprobante ya
// "rejected" (justo lo que hace una corrección de soporte) pasaba por persistInvoiceAfterEmit,
// que veía el estado previo como "final" y SOLO parcheaba metadata — nunca reseteaba
// sunat_status/payload_json al nuevo intento. waitForFacturadorOutcome leía ese mismo registro
// enseguida y devolvía el mensaje VIEJO sin haber consultado nada del reintento actual.
func TestPersistInvoiceAfterEmit_reissueResetsRejectedInvoice(t *testing.T) {
	db := setupPersistInvoiceDB(t)

	sale := database.TenantSale{
		BranchID: 1, UserID: 1, SeriesID: 1, DocType: "FACTURA", Series: "F001", Correlative: 3,
		Number: "F001-3", IssueDate: time.Now(), Total: 65, Currency: "PEN", BillingStatus: "rejected",
	}
	db.Create(&sale)

	sentAt := time.Now().Add(-time.Hour)
	oldPayload := `{"formaPago":{"tipo":"Credito","moneda":"PEN"}}` // el payload viejo, sin monto
	db.Create(&database.TenantInvoice{
		SaleID: sale.ID, SunatStatus: "rejected",
		SunatMessage:   "Si el tipo de transaccion es al Credito debe consignarse el Monto neto pendiente de pago",
		PayloadJSON:    oldPayload,
		PipelineStatus: billingstate.SUNAT_REJECTED,
		SentAt:         &sentAt,
	})

	svc := &BillingService{db: db, reissueMode: true}
	newPayload := `{"formaPago":{"tipo":"Credito","moneda":"PEN","monto":65}}` // el payload corregido
	inv, err := svc.persistInvoiceAfterEmit(sale.ID, "new-doc-uuid", newPayload)
	if err != nil {
		t.Fatalf("persistInvoiceAfterEmit: %v", err)
	}

	if inv.SunatStatus != "pending" {
		t.Errorf("sunat_status = %q, want pending (reseteado para el nuevo intento)", inv.SunatStatus)
	}
	if inv.PayloadJSON != newPayload {
		t.Errorf("payload_json quedó con el viejo (%q), want el nuevo (%q) — este es exactamente el bug", inv.PayloadJSON, newPayload)
	}
	if inv.ExternalID != "new-doc-uuid" {
		t.Errorf("external_id = %q, want new-doc-uuid", inv.ExternalID)
	}
}

// Fuera de una reemisión (reissueMode=false), el guard de "no pisar un resultado final" debe
// seguir protegiendo — comportamiento de siempre, no se toca.
func TestPersistInvoiceAfterEmit_nonReissueKeepsFinalStateProtection(t *testing.T) {
	db := setupPersistInvoiceDB(t)

	sale := database.TenantSale{
		BranchID: 1, UserID: 1, SeriesID: 1, DocType: "FACTURA", Series: "F001", Correlative: 5,
		Number: "F001-5", IssueDate: time.Now(), Total: 100, Currency: "PEN", BillingStatus: "accepted",
	}
	db.Create(&sale)

	sentAt := time.Now().Add(-time.Hour)
	db.Create(&database.TenantInvoice{
		SaleID: sale.ID, SunatStatus: "accepted", SunatMessage: "aceptado",
		PayloadJSON: "old", ExternalID: "already-set", PipelineStatus: billingstate.SUNAT_ACCEPTED, SentAt: &sentAt,
	})

	svc := &BillingService{db: db, reissueMode: false}
	inv, err := svc.persistInvoiceAfterEmit(sale.ID, "should-not-apply", "should-not-apply")
	if err != nil {
		t.Fatalf("persistInvoiceAfterEmit: %v", err)
	}
	if inv.SunatStatus != "accepted" || inv.PayloadJSON != "old" || inv.ExternalID != "already-set" {
		t.Errorf("un resultado final ya resuelto no debería pisarse fuera de una reemisión, obtuve %+v", inv)
	}
}
