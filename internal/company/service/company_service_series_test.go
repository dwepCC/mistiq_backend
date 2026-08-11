package service

import (
	"fmt"
	"testing"
	"time"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func setupCompanySeriesTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&database.TenantDocumentSeries{}, &database.TenantSale{}); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestCompanyService_UpdateSeries_highCorrelativeWithoutDocsAllowed(t *testing.T) {
	db := setupCompanySeriesTestDB(t)
	svc := NewCompanyService(db)
	ser := database.TenantDocumentSeries{
		BranchID: 1, DocType: "NOTA DE VENTA", SunatCode: "00", Category: "venta",
		Series: "NV99", Correlative: 1, Active: true,
	}
	if err := db.Create(&ser).Error; err != nil {
		t.Fatal(err)
	}
	newCorr := uint(250)
	if err := svc.UpdateSeries(ser.ID, "NV99", true, "NOTA DE VENTA", &newCorr); err != nil {
		t.Fatalf("UpdateSeries: %v", err)
	}
	var loaded database.TenantDocumentSeries
	if err := db.First(&loaded, ser.ID).Error; err != nil {
		t.Fatal(err)
	}
	if loaded.Correlative != 250 {
		t.Fatalf("correlative=%d want 250", loaded.Correlative)
	}
}

func TestCompanyService_UpdateSeries_inUseOnlyActiveEditable(t *testing.T) {
	db := setupCompanySeriesTestDB(t)
	svc := NewCompanyService(db)
	ser := database.TenantDocumentSeries{
		BranchID: 1, DocType: "NOTA DE VENTA", SunatCode: "00", Category: "venta",
		Series: "NV01", Correlative: 2, Active: true,
	}
	if err := db.Create(&ser).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.TenantSale{
		BranchID: 1, UserID: 1, SeriesID: ser.ID, DocType: "NOTA DE VENTA",
		Series: ser.Series, Correlative: 1, Number: "NV01-1",
		IssueDate: time.Now(), Subtotal: 10, TaxAmount: 0, Total: 10,
	}).Error; err != nil {
		t.Fatal(err)
	}

	newCorr := uint(99)
	err := svc.UpdateSeries(ser.ID, "NV02", false, "NOTA DE VENTA", &newCorr)
	if err == nil {
		t.Fatal("debe rechazar cambio de serie/correlativo cuando hay documentos")
	}

	if err := svc.UpdateSeries(ser.ID, ser.Series, false, ser.DocType, nil); err != nil {
		t.Fatalf("debe permitir cambiar active: %v", err)
	}
	var loaded database.TenantDocumentSeries
	if err := db.First(&loaded, ser.ID).Error; err != nil {
		t.Fatal(err)
	}
	if loaded.Active {
		t.Fatal("active debe ser false")
	}
	if loaded.Series != "NV01" {
		t.Fatalf("serie no debe cambiar: %s", loaded.Series)
	}
}

func TestCompanyService_DeleteSeries_sameUsageLogic(t *testing.T) {
	db := setupCompanySeriesTestDB(t)
	svc := NewCompanyService(db)
	ser := database.TenantDocumentSeries{
		BranchID: 1, DocType: "NOTA DE VENTA", SunatCode: "00", Category: "venta",
		Series: "NVDEL", Correlative: 1, Active: true,
	}
	if err := db.Create(&ser).Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteSeries(ser.ID); err != nil {
		t.Fatalf("serie sin uso debe eliminarse: %v", err)
	}

	ser2 := database.TenantDocumentSeries{
		BranchID: 1, DocType: "NOTA DE VENTA", SunatCode: "00", Category: "venta",
		Series: "NVUSE", Correlative: 1, Active: true,
	}
	if err := db.Create(&ser2).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.TenantSale{
		BranchID: 1, UserID: 1, SeriesID: ser2.ID, DocType: "NOTA DE VENTA",
		Series: ser2.Series, Correlative: 1, Number: "NVUSE-1",
		IssueDate: time.Now(), Subtotal: 10, TaxAmount: 0, Total: 10,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteSeries(ser2.ID); err == nil {
		t.Fatal("no debe eliminar serie en uso")
	}
}

func TestCompanyService_CreateSeries_almacenDifferentCodesPerBranch(t *testing.T) {
	db := setupCompanySeriesTestDB(t)
	if err := db.AutoMigrate(&database.TenantBranch{}); err != nil {
		t.Fatal(err)
	}
	b1 := database.TenantBranch{Name: "Principal", Active: true, IsMain: true}
	b2 := database.TenantBranch{Name: "Sucursal 2", Active: true}
	if err := db.Create(&b1).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&b2).Error; err != nil {
		t.Fatal(err)
	}
	svc := NewCompanyService(db)
	if err := svc.CreateSeries(b1.ID, "INGRESO_INVENTARIO", "ING001", nil); err != nil {
		t.Fatalf("branch1 ING001: %v", err)
	}
	if err := svc.CreateSeries(b2.ID, "INGRESO_INVENTARIO", "ING002", nil); err != nil {
		t.Fatalf("branch2 ING002: %v", err)
	}
	if err := svc.CreateSeries(b1.ID, "INGRESO_INVENTARIO", "ING001", nil); err == nil {
		t.Fatal("debe rechazar ING001 duplicado en la misma sucursal")
	}
}

func TestCompanyService_CreateSeries_cotizacionPerBranch(t *testing.T) {
	db := setupCompanySeriesTestDB(t)
	svc := NewCompanyService(db)
	if err := svc.CreateSeries(1, "Cotización", "COT001", nil); err != nil {
		t.Fatalf("CreateSeries: %v", err)
	}
	var row database.TenantDocumentSeries
	if err := db.Where("series = ?", "COT001").First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.SunatCode != "QT" || row.Category != "cotizacion" || row.DocType != "Cotización" {
		t.Fatalf("row=%+v", row)
	}
}

func TestCompanyService_CreateSeries_derivesDocumentCodeFromType(t *testing.T) {
	db := setupCompanySeriesTestDB(t)
	svc := NewCompanyService(db)
	if err := svc.CreateSeries(1, "BOLETA", "B001", nil); err != nil {
		t.Fatalf("CreateSeries: %v", err)
	}
	var row database.TenantDocumentSeries
	if err := db.Where("series = ?", "B001").First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.SunatCode != "03" || row.Category != "venta" || row.DocType != "BOLETA" {
		t.Fatalf("row=%+v", row)
	}
	if row.Correlative != 1 {
		t.Fatalf("correlative=%d want 1", row.Correlative)
	}
}

func TestCompanyService_CreateSeries_customCorrelative(t *testing.T) {
	db := setupCompanySeriesTestDB(t)
	svc := NewCompanyService(db)
	start := uint(150)
	if err := svc.CreateSeries(1, "NOTA DE VENTA", "NV150", &start); err != nil {
		t.Fatalf("CreateSeries: %v", err)
	}
	var row database.TenantDocumentSeries
	if err := db.Where("series = ?", "NV150").First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.Correlative != 150 {
		t.Fatalf("correlative=%d want 150", row.Correlative)
	}
}

func TestCompanyService_ListSeriesEnriched_returnsUsageMetadata(t *testing.T) {
	db := setupCompanySeriesTestDB(t)
	svc := NewCompanyService(db)
	ser := database.TenantDocumentSeries{
		BranchID: 1, DocType: "NOTA DE VENTA", SunatCode: "00", Category: "venta",
		Series: "NVLST", Correlative: 1, Active: true,
	}
	if err := db.Create(&ser).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.TenantSale{
		BranchID: 1, UserID: 1, SeriesID: ser.ID, DocType: "NOTA DE VENTA",
		Series: ser.Series, Correlative: 1, Number: "NVLST-1",
		IssueDate: time.Now(), Subtotal: 10, TaxAmount: 0, Total: 10,
	}).Error; err != nil {
		t.Fatal(err)
	}

	items, err := svc.ListSeriesEnriched(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("len=%d", len(items))
	}
	item := items[0]
	if !item.Locked || item.CanDelete || item.UsageCount != 1 {
		t.Fatalf("item=%+v", item)
	}
	if item.UsageTable != "tenant_sales" || item.UsageReason == "" {
		t.Fatalf("usage metadata incompleta: %+v", item)
	}
}
