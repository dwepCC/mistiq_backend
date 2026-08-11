package service

import (
	"fmt"
	"testing"
	"time"

	invsvc "tukifac/internal/inventory/service"
	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func setupSeriesUsageTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&database.TenantDocumentSeries{},
		&database.TenantSale{},
		&database.TenantQuotation{},
		&database.TenantDespatch{},
		&database.TenantRetention{},
		&database.TenantPerception{},
		&database.TenantInventoryDocument{},
	); err != nil {
		t.Fatal(err)
	}
	return db
}

func seedSeries(t *testing.T, db *gorm.DB, category, seriesCode string, correlative uint) database.TenantDocumentSeries {
	t.Helper()
	row := database.TenantDocumentSeries{
		BranchID:    1,
		DocType:     "NOTA DE VENTA",
		SunatCode:   "00",
		Category:    category,
		Series:      seriesCode,
		Correlative: correlative,
		Active:      true,
	}
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	return row
}

func TestIsSeriesInUse_newSeriesNotLocked(t *testing.T) {
	db := setupSeriesUsageTestDB(t)
	svc := NewSeriesUsageService(db)
	ser := seedSeries(t, db, "venta", "NV01", 1)

	inUse, info, err := svc.IsSeriesInUse(ser.ID)
	if err != nil {
		t.Fatal(err)
	}
	if inUse {
		t.Fatal("serie nueva no debe estar en uso")
	}
	if info.Count != 0 || info.Reason != "" {
		t.Fatalf("info=%+v", info)
	}
}

func TestIsSeriesInUse_highCorrelativeWithoutDocumentsNotLocked(t *testing.T) {
	db := setupSeriesUsageTestDB(t)
	svc := NewSeriesUsageService(db)
	ser := seedSeries(t, db, "venta", "NV02", 500)

	inUse, _, err := svc.IsSeriesInUse(ser.ID)
	if err != nil {
		t.Fatal(err)
	}
	if inUse {
		t.Fatal("correlativo alto sin documentos no debe bloquear")
	}
}

func TestIsSeriesInUse_firstSaleLocks(t *testing.T) {
	db := setupSeriesUsageTestDB(t)
	svc := NewSeriesUsageService(db)
	ser := seedSeries(t, db, "venta", "NV03", 1)
	if err := db.Create(&database.TenantSale{
		BranchID: 1, UserID: 1, SeriesID: ser.ID, DocType: "NOTA DE VENTA",
		Series: ser.Series, Correlative: 1, Number: "NV03-1",
		IssueDate: time.Now(), Subtotal: 10, TaxAmount: 0, Total: 10,
	}).Error; err != nil {
		t.Fatal(err)
	}

	inUse, info, err := svc.IsSeriesInUse(ser.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !inUse || info.Count != 1 || info.Table != "tenant_sales" {
		t.Fatalf("inUse=%v info=%+v", inUse, info)
	}
	if info.Reason != "Esta serie ya fue utilizada por documentos de venta." {
		t.Fatalf("reason=%q", info.Reason)
	}
}

func TestIsSeriesInUse_firstQuotationLocks(t *testing.T) {
	db := setupSeriesUsageTestDB(t)
	svc := NewSeriesUsageService(db)
	ser := seedSeries(t, db, "cotizacion", "C001", 1)
	if err := db.Create(&database.TenantQuotation{
		BranchID: 1, UserID: 1, SeriesID: ser.ID, Series: ser.Series,
		Correlative: 1, Number: "C001-1", IssueDate: time.Now(),
		Subtotal: 10, TaxAmount: 0, Total: 10,
	}).Error; err != nil {
		t.Fatal(err)
	}

	inUse, info, err := svc.IsSeriesInUse(ser.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !inUse || info.Table != "tenant_quotations" {
		t.Fatalf("inUse=%v info=%+v", inUse, info)
	}
}

func TestIsSeriesInUse_confirmedInventoryLocksDraftDoesNot(t *testing.T) {
	db := setupSeriesUsageTestDB(t)
	svc := NewSeriesUsageService(db)
	ser := seedSeries(t, db, "almacen", "AI01", 1)

	draft := database.TenantInventoryDocument{
		Number: "AI01-DRAFT", SeriesID: ser.ID, Correlative: 1, Direction: "IN",
		OperationTypeID: 1, BranchID: 1, DocumentDate: time.Now(), Status: "draft", CreatedBy: 1,
	}
	if err := db.Create(&draft).Error; err != nil {
		t.Fatal(err)
	}
	inUse, _, err := svc.IsSeriesInUse(ser.ID)
	if err != nil || inUse {
		t.Fatalf("borrador no debe bloquear: inUse=%v err=%v", inUse, err)
	}

	confirmed := database.TenantInventoryDocument{
		Number: "AI01-1", SeriesID: ser.ID, Correlative: 1, Direction: "IN",
		OperationTypeID: 1, BranchID: 1, DocumentDate: time.Now(),
		Status: invsvc.DocumentStatusConfirmed, CreatedBy: 1,
	}
	if err := db.Create(&confirmed).Error; err != nil {
		t.Fatal(err)
	}
	inUse, info, err := svc.IsSeriesInUse(ser.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !inUse || info.Table != "tenant_inventory_documents" {
		t.Fatalf("confirmado debe bloquear: inUse=%v info=%+v", inUse, info)
	}
}

func TestIsSeriesInUse_firstDespatchLocks(t *testing.T) {
	db := setupSeriesUsageTestDB(t)
	svc := NewSeriesUsageService(db)
	ser := seedSeries(t, db, "guia_remision", "T001", 1)
	if err := db.Create(&database.TenantDespatch{
		BranchID: 1, SeriesID: ser.ID, Series: ser.Series, Correlative: 1, IssueDate: time.Now(),
	}).Error; err != nil {
		t.Fatal(err)
	}

	inUse, info, err := svc.IsSeriesInUse(ser.ID)
	if err != nil || !inUse || info.Table != "tenant_despatches" {
		t.Fatalf("inUse=%v info=%+v err=%v", inUse, info, err)
	}
}

func TestIsSeriesInUse_retentionAndPerceptionBySeriesCode(t *testing.T) {
	db := setupSeriesUsageTestDB(t)
	svc := NewSeriesUsageService(db)

	retSer := seedSeries(t, db, "retencion", "R001", 1)
	if err := db.Create(&database.TenantRetention{
		Series: retSer.Series, Correlative: "1", FechaEmision: time.Now(),
	}).Error; err != nil {
		t.Fatal(err)
	}
	inUse, info, err := svc.IsSeriesInUse(retSer.ID)
	if err != nil || !inUse || info.Table != "tenant_retentions" {
		t.Fatalf("retención: inUse=%v info=%+v err=%v", inUse, info, err)
	}

	perSer := seedSeries(t, db, "percepcion", "P001", 1)
	if err := db.Create(&database.TenantPerception{
		Series: perSer.Series, Correlative: "1", FechaEmision: time.Now(),
	}).Error; err != nil {
		t.Fatal(err)
	}
	inUse, info, err = svc.IsSeriesInUse(perSer.ID)
	if err != nil || !inUse || info.Table != "tenant_perceptions" {
		t.Fatalf("percepción: inUse=%v info=%+v err=%v", inUse, info, err)
	}
}

func TestIsSeriesInUse_compraNeverLocked(t *testing.T) {
	db := setupSeriesUsageTestDB(t)
	svc := NewSeriesUsageService(db)
	ser := seedSeries(t, db, "compra", "C001", 99)

	inUse, _, err := svc.IsSeriesInUse(ser.ID)
	if err != nil || inUse {
		t.Fatalf("compra no usa series documentales: inUse=%v err=%v", inUse, err)
	}
}

func TestIsSeriesInUse_notaCreditoOnlyCountsCreditNotes(t *testing.T) {
	db := setupSeriesUsageTestDB(t)
	svc := NewSeriesUsageService(db)
	ser := seedSeries(t, db, "nota_credito", "FC01", 1)

	// Venta normal con mismo series_id no debe bloquear categoría nota_credito
	if err := db.Create(&database.TenantSale{
		BranchID: 1, UserID: 1, SeriesID: ser.ID, DocType: "FACTURA",
		Series: ser.Series, Correlative: 1, Number: "FC01-1",
		IssueDate: time.Now(), Subtotal: 10, TaxAmount: 0, Total: 10,
	}).Error; err != nil {
		t.Fatal(err)
	}
	inUse, _, err := svc.IsSeriesInUse(ser.ID)
	if err != nil || inUse {
		t.Fatal("venta con doc_type distinto no debe bloquear serie NC")
	}

	if err := db.Create(&database.TenantSale{
		BranchID: 1, UserID: 1, SeriesID: ser.ID, DocType: "NOTA_CREDITO",
		Series: ser.Series, Correlative: 1, Number: "FC01-1",
		IssueDate: time.Now(), Subtotal: 10, TaxAmount: 0, Total: 10,
	}).Error; err != nil {
		t.Fatal(err)
	}
	inUse, info, err := svc.IsSeriesInUse(ser.ID)
	if err != nil || !inUse {
		t.Fatalf("NC debe bloquear: inUse=%v info=%+v", inUse, info)
	}
}
