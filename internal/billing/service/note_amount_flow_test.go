package service

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"tukifac/pkg/database"
	"tukifac/pkg/facturador"
	"tukifac/pkg/logger"
	"tukifac/pkg/saas/docusage"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// Flujo REAL de emisión (CreateCreditNote), no importes armados a mano: crea la factura, emite la nota
// por monto y lee de vuelta lo que quedó guardado (venta NC + líneas + payload que se manda a SUNAT).
// Caso del cliente: venta de 5,521.22, restar S/ 90 → 5,431.22.

// setupAmountFlowCentral crea la BD central mínima (tenant + plan + suscripción + ciclo) para que la
// reserva de cupo de documentos (docusage) corra igual que en producción, y apunta svc a ese tenant.
func setupAmountFlowCentral(t *testing.T, svc *BillingService) {
	t.Helper()
	prevLog := logger.L
	logger.L = slog.New(slog.NewTextHandler(io.Discard, nil))
	t.Cleanup(func() { logger.L = prevLog })
	central, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s_central?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	prev := database.CentralDB
	database.CentralDB = central
	t.Cleanup(func() { database.CentralDB = prev })
	if err := central.AutoMigrate(
		&database.SaasPlan{}, &database.SaasSubscription{}, &database.SaasBillingCycle{},
		&database.SaasElectronicDocumentUsage{}, &database.SaasDocumentPackage{},
		&database.SaasTenantDocumentPackage{}, &database.SaasDocumentQuotaPeriod{}, &database.Tenant{},
	); err != nil {
		t.Fatal(err)
	}
	plan := database.SaasPlan{Name: "Test", Price: 10, BillingCycle: "monthly", Active: true, MonthlyDocumentsLimit: 100}
	if err := central.Create(&plan).Error; err != nil {
		t.Fatal(err)
	}
	tenant := database.Tenant{Slug: "nc-monto", DBName: "saas_tenant_nc_monto", Status: "active"}
	if err := central.Create(&tenant).Error; err != nil {
		t.Fatal(err)
	}
	sub := database.SaasSubscription{TenantID: tenant.ID, PlanID: plan.ID, BillingCycle: "monthly",
		StartDate: time.Now(), EndDate: time.Now().Add(30 * 24 * time.Hour), Status: database.SaasSubActive}
	if err := central.Create(&sub).Error; err != nil {
		t.Fatal(err)
	}
	if cycle, err := docusage.EnsureBillingCycleForSubscription(&sub); err != nil || cycle == nil {
		t.Fatalf("ciclo: %v", err)
	}
	svc.centralTenantID = tenant.ID
}

func seedAmountFlowSale(t *testing.T, svc *BillingService, total float64, items ...database.TenantSaleItem) uint {
	t.Helper()
	setupAmountFlowCentral(t, svc)
	db := svc.db
	if err := db.Create(&database.TenantCompanyConfig{
		RUC: "20123456789", BusinessName: "EMPRESA SAC", TradeName: "EMPRESA",
		Address: "Av. Test 123", Ubigeo: "150101", SunatEnabled: true, TaxRate: 18,
	}).Error; err != nil {
		t.Fatal(err)
	}
	contactID := uint(1)
	if err := db.Create(&database.TenantContact{ID: contactID, DocType: "RUC", DocNumber: "20100070970",
		BusinessName: "CLIENTE SAC", Address: "Jr Cliente 1", Ubigeo: "150101"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.TenantDocumentSeries{ID: 1, BranchID: 1, Category: "factura", SunatCode: "01", Series: "F001", Active: true}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.TenantDocumentSeries{ID: 2, BranchID: 1, Category: "nota_credito", SunatCode: "07", Series: "FC01", Active: true}).Error; err != nil {
		t.Fatal(err)
	}
	var sub, tax float64
	for _, it := range items {
		sub += it.Subtotal
		tax += it.TaxAmount
	}
	orig := database.TenantSale{
		BranchID: 1, ContactID: &contactID, SeriesID: 1, DocType: "FACTURA", Series: "F001", Correlative: 1,
		Number: "F001-00000001", IssueDate: time.Now(), Subtotal: round2(sub), TaxAmount: round2(tax), Total: total,
		Currency: "PEN", OperationTypeCode: "0101", Status: "paid", BillingStatus: "accepted",
	}
	if err := db.Create(&orig).Error; err != nil {
		t.Fatal(err)
	}
	for i := range items {
		items[i].SaleID = orig.ID
		if err := db.Create(&items[i]).Error; err != nil {
			t.Fatal(err)
		}
	}
	return orig.ID
}

func TestCreateCreditNote_PorMonto_FlujoCompleto(t *testing.T) {
	db := setupNoteDocumentTestDB(t)
	svc := &BillingService{db: db, baseURL: "http://127.0.0.1:1", token: "tok"}
	line := gravadaItem(0, "Servicios prestados", 1, 5521.22)
	line.ID = 0
	origID := seedAmountFlowSale(t, svc, 5521.22, line)

	nc, inv, err := svc.CreateCreditNote(origID, "Disminución del valor acordada", "09", nil, 90, "")
	// El envío real a SUNAT no existe en el test: lo que importa es lo que quedó guardado.
	if nc == nil {
		t.Fatalf("no se creó la nota: %v", err)
	}
	var saved database.TenantSale
	if e := db.First(&saved, nc.ID).Error; e != nil {
		t.Fatal(e)
	}
	if saved.DocType != "NOTA_CREDITO" || saved.NoteReasonCode != "09" {
		t.Fatalf("nota guardada: %s motivo %s", saved.DocType, saved.NoteReasonCode)
	}
	if !near(saved.Total, 90) || !near(saved.Subtotal, 76.27) || !near(saved.TaxAmount, 13.73) {
		t.Fatalf("totales guardados: sub=%.2f igv=%.2f total=%.2f", saved.Subtotal, saved.TaxAmount, saved.Total)
	}
	var lines []database.TenantSaleItem
	db.Where("sale_id = ?", nc.ID).Find(&lines)
	if len(lines) != 1 || lines[0].OriginalSaleItemID != nil {
		t.Fatalf("debía haber 1 línea sin vínculo a la línea original, got %d: %+v", len(lines), lines)
	}

	// Payload final: el que se guarda en la factura para enviar al facturador.
	var rec database.TenantInvoice
	if e := db.Where("sale_id = ?", nc.ID).First(&rec).Error; e != nil {
		t.Fatalf("no quedó registro fiscal: %v (inv=%v, err=%v)", e, inv, err)
	}
	var p facturador.NotePayload
	if e := json.Unmarshal([]byte(rec.NotePayloadJSON), &p); e != nil {
		t.Fatal(e)
	}
	if p.TipoDoc != "07" || p.CodMotivo != "09" || p.TipDocAfectado != "01" || p.NumDocfectado != "F001-1" {
		t.Fatalf("cabecera: tipo=%s motivo=%s afecta %s %s", p.TipoDoc, p.CodMotivo, p.TipDocAfectado, p.NumDocfectado)
	}
	if !near(p.MtoImpVenta, 90) || !near(p.MtoOperGravadas, 76.27) || !near(p.MtoIGV, 13.73) {
		t.Fatalf("importes: impVenta=%.2f gravadas=%.2f igv=%.2f", p.MtoImpVenta, p.MtoOperGravadas, p.MtoIGV)
	}
	if len(p.Details) != 1 || !near(p.Details[0].MtoValorVenta, 76.27) || !near(p.Details[0].Igv, 13.73) || !near(p.Details[0].MtoPrecioUnitario, 90) {
		t.Fatalf("detalle: %+v", p.Details)
	}

	// Segunda nota: ya hay 90 acreditados → quedan 5,431.22; pedir más de eso se rechaza.
	if _, _, e := svc.CreateCreditNote(origID, "", "09", nil, 5431.23, ""); e == nil {
		t.Fatal("acreditar más de lo vendido debía rechazarse")
	}
}

func TestCreateCreditNote_PorMonto_MotivoDeDevolucionNoAdmiteMonto(t *testing.T) {
	db := setupNoteDocumentTestDB(t)
	svc := &BillingService{db: db, baseURL: "http://127.0.0.1:1", token: "tok"}
	line := gravadaItem(0, "Servicio", 1, 118)
	line.ID = 0
	origID := seedAmountFlowSale(t, svc, 118, line)
	nc, _, err := svc.CreateCreditNote(origID, "", "07", nil, 10, "")
	if err == nil || nc != nil && nc.ID != 0 {
		t.Fatalf("devolución por ítem (07) con monto debía rechazarse antes de crear nada: nc=%v err=%v", nc, err)
	}
	var n int64
	db.Model(&database.TenantSale{}).Where("doc_type = ?", "NOTA_CREDITO").Count(&n)
	if n != 0 {
		t.Fatalf("no debía crearse ninguna nota, hay %d", n)
	}
}
