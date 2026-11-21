package service

import (
	"testing"
	"time"

	"tukifac/pkg/database"
)

func TestClassifySessionDocKind(t *testing.T) {
	cases := []struct{ code, docType, want string }{
		{"01", "x", SessionDocKindFactura},
		{"03", "x", SessionDocKindBoleta},
		{"00", "Boleta", SessionDocKindNotaVenta},
		{"", "Factura Electrónica", SessionDocKindFactura},
		{"", "boleta", SessionDocKindBoleta},
		{"", "nota_venta", SessionDocKindNotaVenta},
	}
	for _, c := range cases {
		if got := classifySessionDocKind(c.code, c.docType); got != c.want {
			t.Errorf("classify(%q,%q)=%q want %q", c.code, c.docType, got, c.want)
		}
	}
}

func TestSummarizeSessionDocuments_separaAnuladosYSubtotales(t *testing.T) {
	rep := summarizeSessionDocuments([]SessionDocumentRow{
		{Kind: SessionDocKindBoleta, Total: 20},
		{Kind: SessionDocKindBoleta, Total: 30},
		{Kind: SessionDocKindFactura, Total: 118},
		{Kind: SessionDocKindBoleta, Total: 50, Cancelled: true},
		{Kind: SessionDocKindNotaVenta, Total: 10},
	})
	if rep.ActiveCount != 4 || rep.ActiveTotal != 178 {
		t.Errorf("vigentes = %d/%v, want 4/178", rep.ActiveCount, rep.ActiveTotal)
	}
	if rep.CancelledCount != 1 || rep.CancelledTotal != 50 {
		t.Errorf("anulados = %d/%v, want 1/50", rep.CancelledCount, rep.CancelledTotal)
	}
	if len(rep.ByKind) != 3 || rep.ByKind[0].Kind != SessionDocKindNotaVenta || rep.ByKind[1].Kind != SessionDocKindBoleta {
		t.Fatalf("orden por tipo inesperado: %+v", rep.ByKind)
	}
	if b := rep.ByKind[1]; b.Count != 2 || b.Total != 50 || b.CancelledCount != 1 || b.CancelledTotal != 50 {
		t.Errorf("boletas = %+v", b)
	}
	if empty := summarizeSessionDocuments(nil); empty.Documents == nil || empty.ByKind == nil {
		t.Error("las listas vacías deben ser [] y no null")
	}
}

func TestCancelReasonFromSaleNotes(t *testing.T) {
	if got := cancelReasonFromSaleNotes("Entregar | ANULADA: error de precio"); got != "error de precio" {
		t.Errorf("got %q", got)
	}
	if got := cancelReasonFromSaleNotes("sin motivo"); got != "" {
		t.Errorf("got %q", got)
	}
}

// El detalle incluye comprobantes vigentes y anulados de LA sesión (no de otra), con método(s) de pago.
func TestGetSessionDocumentsReport_vigentesAnuladosYMetodos(t *testing.T) {
	db := setupCashReportPhysicalBalanceTestDB(t)
	svc := NewCashBankService(db)
	seedReportPaymentMethods(t, db)
	if err := db.AutoMigrate(&database.TenantContact{}); err != nil {
		t.Fatal(err)
	}

	s1 := &database.TenantCashSession{BranchID: 1, UserID: 1, OpenedBy: 1, Status: "open", OpenedAt: time.Now().Add(-time.Hour)}
	s2 := &database.TenantCashSession{BranchID: 1, UserID: 1, OpenedBy: 1, Status: "open", OpenedAt: time.Now().Add(-time.Hour)}
	if err := db.Create(s1).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(s2).Error; err != nil {
		t.Fatal(err)
	}
	createReportSale(t, db, svc, 1, s1.ID, map[string]float64{"cash": 20})
	createReportSale(t, db, svc, 2, s1.ID, map[string]float64{"yape": 30})
	createReportSale(t, db, svc, 3, s1.ID, map[string]float64{"cash": 10, "yape": 15})
	createReportSale(t, db, svc, 4, s1.ID, map[string]float64{"cash": 50}) // se anula
	createReportSale(t, db, svc, 5, s2.ID, map[string]float64{"cash": 99}) // otra sesión
	if err := db.Model(&database.TenantSale{}).Where("id = ?", 4).
		Updates(map[string]interface{}{"status": "cancelled", "notes": "ANULADA: cliente se arrepintió"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&database.TenantSale{}).Where("id = ?", 2).Update("doc_type", "factura").Error; err != nil {
		t.Fatal(err)
	}

	rep, err := svc.GetSessionDocumentsReport(s1.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Documents) != 4 {
		t.Fatalf("documentos = %d, want 4 (sin la otra sesión): %+v", len(rep.Documents), rep.Documents)
	}
	if rep.ActiveCount != 3 || rep.ActiveTotal != 75 {
		t.Errorf("vigentes = %d/%v, want 3/75", rep.ActiveCount, rep.ActiveTotal)
	}
	if rep.CancelledCount != 1 || rep.CancelledTotal != 50 {
		t.Errorf("anulados = %d/%v, want 1/50", rep.CancelledCount, rep.CancelledTotal)
	}
	byID := map[uint]SessionDocumentRow{}
	for _, d := range rep.Documents {
		byID[d.SaleID] = d
	}
	if d := byID[2]; d.Kind != SessionDocKindFactura || d.PaymentMethod != "yape" {
		t.Errorf("venta 2 = %+v", d)
	}
	if d := byID[3]; d.PaymentMethod != "mixto" || len(d.Payments) != 2 {
		t.Errorf("venta 3 (mixta) = %+v", d)
	}
	if d := byID[4]; !d.Cancelled || d.CancelReason != "cliente se arrepintió" {
		t.Errorf("venta 4 (anulada) = %+v", d)
	}
}
