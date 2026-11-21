package service

import (
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"tukifac/pkg/database"
	"tukifac/pkg/logger"

	"gorm.io/gorm"
)

// Comprobantes (boleta/factura) que nacieron de una nota de venta: el comprobante no mueve caja ni
// stock, eso vive en la nota de venta. Al anularse el comprobante, la nota de venta se anula con
// toda su reversión.

// seedNVWithChild deja una nota de venta cobrada en efectivo (con su salida de kardex) y, emitido
// desde ella, un comprobante electrónico que NO movió caja ni stock (así se crea por conversión).
func seedNVWithChild(t *testing.T, db *gorm.DB, childBilling string) (nv, child *database.TenantSale) {
	t.Helper()
	if logger.L == nil {
		logger.L = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	nvSeries := database.TenantDocumentSeries{BranchID: 1, SunatCode: "00", Series: "NV01", DocType: "NOTA DE VENTA", Active: true, Correlative: 1}
	if err := db.Create(&nvSeries).Error; err != nil {
		t.Fatal(err)
	}
	session := database.TenantCashSession{BranchID: 1, UserID: 1, OpenedBy: 1, Status: "open", OpenedAt: time.Now()}
	if err := db.Create(&session).Error; err != nil {
		t.Fatal(err)
	}
	nv = &database.TenantSale{
		SeriesID: nvSeries.ID, Series: "NV01", Number: "NV01-00000001", DocType: "NOTA_VENTA",
		BranchID: 1, UserID: 1, Total: 100, Status: "paid", BillingStatus: "pending", CashSessionID: &session.ID,
	}
	if err := db.Create(nv).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.TenantCashMovement{
		CashSessionID: session.ID, Type: "income", Amount: 100, PaymentMethod: "cash",
		Category: "Venta", Reference: "VENTA/" + nv.Number, SaleID: &nv.ID, UserID: 1, CreatedAt: time.Now(),
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.TenantStockMovement{
		ProductID: 9, BranchID: 1, Type: "out", Quantity: 2, Reference: "VENTA/" + nv.Number, UserID: 1, CreatedAt: time.Now(),
	}).Error; err != nil {
		t.Fatal(err)
	}
	nvID := nv.ID
	child = &database.TenantSale{
		Series: "B002", Number: "B002-00000999", DocType: "BOLETA", BranchID: 1, UserID: 1, Total: 100,
		Status: "paid", BillingStatus: childBilling, SaleOrigin: "converted_from_nota", IssuedFromNotaSaleID: &nvID,
	}
	if err := db.Create(child).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.TenantInvoice{SaleID: child.ID, PipelineStatus: "SUNAT_REJECTED", SunatStatus: "rejected"}).Error; err != nil {
		t.Fatal(err)
	}
	return nv, child
}

func statusOf(db *gorm.DB, id uint) string {
	var s database.TenantSale
	db.First(&s, id)
	return s.Status
}

func cashExpensesOf(db *gorm.DB, saleID uint) int64 {
	var n int64
	db.Model(&database.TenantCashMovement{}).Where("sale_id = ? AND type = ?", saleID, "expense").Count(&n)
	return n
}

// Anular la boleta rechazada que nació de una nota de venta anula también la nota de venta, donde
// viven el pago y el stock, y revierte todo lo suyo.
func TestVoidRejectedSale_AnulaTambienLaNotaDeVentaDeOrigen(t *testing.T) {
	db := setupVoidRejectedDB(t)
	nv, child := seedNVWithChild(t, db, "rejected")
	s := &BillingService{db: db}

	if err := s.VoidRejectedSale(VoidRejectedInput{SaleID: child.ID, Reason: "numeración chocada", ActorID: 1}); err != nil {
		t.Fatalf("no debía fallar: %v", err)
	}
	if got := statusOf(db, child.ID); got != "cancelled" {
		t.Errorf("comprobante: status = %q, want cancelled", got)
	}
	if got := statusOf(db, nv.ID); got != "cancelled" {
		t.Errorf("nota de venta: status = %q, want cancelled", got)
	}
	if n := cashExpensesOf(db, nv.ID); n != 1 {
		t.Errorf("reversiones de caja de la nota de venta = %d, want 1", n)
	}
	if in := stockInFor(db, 9); in != 2 {
		t.Errorf("stock repuesto = %v, want 2", in)
	}
}

// Si la nota de venta respalda otro comprobante vigente, anular uno rechazado no la toca.
func TestVoidRejectedSale_NoAnulaLaNVSiRespaldaOtroComprobanteVigente(t *testing.T) {
	db := setupVoidRejectedDB(t)
	nv, child := seedNVWithChild(t, db, "rejected")
	nvID := nv.ID
	if err := db.Create(&database.TenantSale{
		Series: "B002", Number: "B002-00001000", DocType: "BOLETA", BranchID: 1, UserID: 1, Total: 100,
		Status: "paid", BillingStatus: "accepted", SaleOrigin: "converted_from_nota", IssuedFromNotaSaleID: &nvID,
	}).Error; err != nil {
		t.Fatal(err)
	}
	s := &BillingService{db: db}
	if err := s.VoidRejectedSale(VoidRejectedInput{SaleID: child.ID, Reason: "x", ActorID: 1}); err != nil {
		t.Fatalf("no debía fallar: %v", err)
	}
	if got := statusOf(db, nv.ID); got == "cancelled" {
		t.Error("la nota de venta respalda otro comprobante vigente y no debió anularse")
	}
	if n := cashExpensesOf(db, nv.ID); n != 0 {
		t.Errorf("no debió revertirse la caja de la nota de venta, got %d reversiones", n)
	}
}

// Si la nota de venta es a crédito y tiene cobros posteriores, se bloquea ANTES de anular nada.
func TestVoidRejectedSale_BloqueaPorCobrosDeLaNotaDeVentaDeOrigen(t *testing.T) {
	db := setupVoidRejectedDB(t)
	nv, child := seedNVWithChild(t, db, "rejected")
	db.Model(&database.TenantSale{}).Where("id = ?", nv.ID).
		Updates(map[string]interface{}{"status": "credit", "payment_condition_code": "credit"})
	if err := db.Create(&database.TenantSalePayment{SaleID: nv.ID, Method: "cash", Amount: 40, CreatedAt: time.Now().Add(48 * time.Hour)}).Error; err != nil {
		t.Fatal(err)
	}
	s := &BillingService{db: db}
	err := s.VoidRejectedSale(VoidRejectedInput{SaleID: child.ID, Reason: "x", ActorID: 1})
	if err == nil || !strings.Contains(err.Error(), "cobros registrados") {
		t.Fatalf("debe bloquear por los cobros de la nota de venta, got %v", err)
	}
	if got := statusOf(db, child.ID); got == "cancelled" {
		t.Error("el comprobante no debió anularse: la operación debía bloquearse antes de tocar nada")
	}
}

func seedAcceptedCreditNote(t *testing.T, db *gorm.DB, boleta *database.TenantSale, reasonCode, number string) *database.TenantSale {
	t.Helper()
	origID := boleta.ID
	nc := database.TenantSale{
		Series: "BC02", Number: number, DocType: "NOTA_CREDITO", NoteReasonCode: reasonCode,
		BranchID: 1, UserID: 1, Total: 100, Status: "paid", BillingStatus: "accepted", OriginalSaleID: &origID,
	}
	if err := db.Create(&nc).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.TenantInvoice{
		SaleID: nc.ID, PipelineStatus: "SUNAT_ACCEPTED", SunatStatus: "accepted", SunatCDRCode: "0",
		XMLURL: "xml", SunatHash: "hash", CDRURL: "cdr",
	}).Error; err != nil {
		t.Fatal(err)
	}
	return &nc
}

// Nota de crédito de anulación total (motivo 01) aceptada sobre una boleta emitida desde una nota
// de venta: se anula la boleta y también la nota de venta, con su reversión.
func TestPostFiscalAccept_NotaDeCredito01AnulaTambienLaNotaDeVenta(t *testing.T) {
	db := setupVoidRejectedDB(t)
	nv, boleta := seedNVWithChild(t, db, "accepted")
	nc := seedAcceptedCreditNote(t, db, boleta, "01", "BC02-00000001")

	s := &BillingService{db: db}
	s.PostFiscalAcceptSideEffects(nc.ID, "SUNAT_ACCEPTED")

	if got := statusOf(db, boleta.ID); got != "cancelled" {
		t.Errorf("boleta: status = %q, want cancelled", got)
	}
	if got := statusOf(db, nv.ID); got != "cancelled" {
		t.Errorf("nota de venta: status = %q, want cancelled", got)
	}
	if n := cashExpensesOf(db, nv.ID); n != 1 {
		t.Errorf("reversiones de caja de la nota de venta = %d, want 1", n)
	}
	if in := stockInFor(db, 9); in != 2 {
		t.Errorf("stock repuesto = %v, want 2", in)
	}

	// Reaceptar (webhook repetido o sincronización) no debe revertir dos veces.
	s.PostFiscalAcceptSideEffects(nc.ID, "SUNAT_ACCEPTED")
	if n := cashExpensesOf(db, nv.ID); n != 1 {
		t.Errorf("tras reprocesar: reversiones de caja = %d, want 1 (idempotente)", n)
	}
	if in := stockInFor(db, 9); in != 2 {
		t.Errorf("tras reprocesar: stock repuesto = %v, want 2", in)
	}
}

// Una nota de crédito con otro motivo (corrección, no anulación) no anula la boleta ni su nota.
func TestPostFiscalAccept_NotaDeCreditoDeCorreccionNoTocaLaNotaDeVenta(t *testing.T) {
	db := setupVoidRejectedDB(t)
	nv, boleta := seedNVWithChild(t, db, "accepted")
	nc := seedAcceptedCreditNote(t, db, boleta, "03", "BC02-00000002")

	(&BillingService{db: db}).PostFiscalAcceptSideEffects(nc.ID, "SUNAT_ACCEPTED")

	if statusOf(db, boleta.ID) == "cancelled" || statusOf(db, nv.ID) == "cancelled" {
		t.Error("una NC de corrección (motivo 03) no debe anular ni la boleta ni la nota de venta")
	}
}
