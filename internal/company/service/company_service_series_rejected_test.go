package service

import (
	"testing"
	"time"

	"tukifac/pkg/database"
)

// Escenario real: una serie de boleta se creó con formato inválido ("005" en vez de "B002");
// SUNAT rechaza el ZIP antes de validar el documento, así que las 2 boletas emitidas bajo esa
// serie nunca llegaron a existir para SUNAT — la serie debe poder corregirse sin necesidad de
// crear una nueva, aunque ya tenga documentos asociados.
func TestCompanyService_UpdateSeries_rejectedOnlyUsageAllowsRename(t *testing.T) {
	db := setupCompanySeriesTestDB(t)
	svc := NewCompanyService(db)

	ser := database.TenantDocumentSeries{
		BranchID: 2, DocType: "BOLETA", SunatCode: "03", Category: "venta",
		Series: "005", Correlative: 3, Active: true,
	}
	if err := db.Create(&ser).Error; err != nil {
		t.Fatal(err)
	}
	for i, n := range []string{"1", "2"} {
		if err := db.Create(&database.TenantSale{
			BranchID: 2, UserID: 1, SeriesID: ser.ID, DocType: "BOLETA",
			Series: ser.Series, Correlative: uint(i + 1), Number: "005-" + n,
			BillingStatus: "rejected",
			IssueDate:     time.Now(), Subtotal: 10, TaxAmount: 0, Total: 10,
		}).Error; err != nil {
			t.Fatal(err)
		}
	}

	if err := svc.UpdateSeries(ser.ID, "B002", true, "BOLETA", nil, nil); err != nil {
		t.Fatalf("debe permitir renombrar una serie cuyo único uso está rechazado: %v", err)
	}
	var loaded database.TenantDocumentSeries
	if err := db.First(&loaded, ser.ID).Error; err != nil {
		t.Fatal(err)
	}
	if loaded.Series != "B002" {
		t.Fatalf("serie: got %q want B002", loaded.Series)
	}
	if loaded.Correlative != 3 {
		t.Fatalf("correlative no debe tocarse sin pedirlo: got %d want 3", loaded.Correlative)
	}
}

// Si al menos UNA venta con esa serie no está rechazada (pending/accepted/etc.), sí hay
// continuidad fiscal real que proteger — sigue bloqueado, igual que antes.
func TestCompanyService_UpdateSeries_mixedUsageStillBlocksRename(t *testing.T) {
	db := setupCompanySeriesTestDB(t)
	svc := NewCompanyService(db)

	ser := database.TenantDocumentSeries{
		BranchID: 2, DocType: "BOLETA", SunatCode: "03", Category: "venta",
		Series: "005", Correlative: 3, Active: true,
	}
	if err := db.Create(&ser).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.TenantSale{
		BranchID: 2, UserID: 1, SeriesID: ser.ID, DocType: "BOLETA",
		Series: ser.Series, Correlative: 1, Number: "005-1",
		BillingStatus: "rejected",
		IssueDate:     time.Now(), Subtotal: 10, TaxAmount: 0, Total: 10,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.TenantSale{
		BranchID: 2, UserID: 1, SeriesID: ser.ID, DocType: "BOLETA",
		Series: ser.Series, Correlative: 2, Number: "005-2",
		BillingStatus: "accepted",
		IssueDate:     time.Now(), Subtotal: 10, TaxAmount: 0, Total: 10,
	}).Error; err != nil {
		t.Fatal(err)
	}

	err := svc.UpdateSeries(ser.ID, "B002", true, "BOLETA", nil, nil)
	if err == nil {
		t.Fatal("debe seguir bloqueado: hay una venta no-rechazada con esta serie")
	}
}

// El preventivo: CreateSeries ahora rechaza formatos inválidos de factura/boleta al crear.
func TestCompanyService_CreateSeries_rejectsInvalidBoletaFormat(t *testing.T) {
	db := setupCompanySeriesTestDB(t)
	svc := NewCompanyService(db)

	if err := svc.CreateSeries(2, "BOLETA", "005", nil, false); err == nil {
		t.Fatal("debe rechazar serie de boleta con formato inválido")
	}
	if err := svc.CreateSeries(2, "BOLETA", "B002", nil, false); err != nil {
		t.Fatalf("debe aceptar formato válido: %v", err)
	}
}
