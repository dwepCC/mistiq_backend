package service

import (
	"fmt"
	"testing"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func setupDespatchListDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []interface{}{
		&database.TenantDespatch{}, &database.TenantDocumentSeries{}, &database.TenantSale{},
	} {
		if err := db.AutoMigrate(m); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

// El listado debe distinguir 09 de 31 leyendo la serie de la propia guía, no el tipo de
// comprobante de la venta de origen (una factura/boleta nunca dice "GUIA" ni "TRANSPORT" en su
// doc_type, así que el criterio anterior nunca acertaba).
func TestListDespatches_ResuelveTipoDeGuiaDesdeSuPropiaSerie(t *testing.T) {
	db := setupDespatchListDB(t)
	// &BillingService{db: db} en vez de NewBillingService: este último toca config.AppConfig,
	// que no está inicializado fuera del arranque real del proceso.
	svc := &BillingService{db: db}

	serieRemitente := database.TenantDocumentSeries{Series: "T001", SunatCode: "09"}
	serieTransportista := database.TenantDocumentSeries{Series: "V001", SunatCode: "31"}
	db.Create(&serieRemitente)
	db.Create(&serieTransportista)

	venta := database.TenantSale{BranchID: 1, UserID: 1, DocType: "FACTURA", Number: "F001-1"}
	db.Create(&venta)

	db.Create(&database.TenantDespatch{
		SeriesID: serieRemitente.ID, Series: "T001", Correlative: 1, BranchID: 1, SaleID: &venta.ID,
	})
	db.Create(&database.TenantDespatch{
		SeriesID: serieTransportista.ID, Series: "V001", Correlative: 1, BranchID: 1,
	})

	list, err := svc.ListDespatches()
	if err != nil {
		t.Fatalf("ListDespatches: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("se esperaban 2 guías, hay %d", len(list))
	}
	byCode := map[string]DespatchListItem{}
	for _, it := range list {
		byCode[it.Series] = it
	}
	if byCode["T001"].GuiaSunatCode != "09" {
		t.Fatalf("T001: guia_sunat_code = %q, se esperaba 09", byCode["T001"].GuiaSunatCode)
	}
	if byCode["V001"].GuiaSunatCode != "31" {
		t.Fatalf("V001: guia_sunat_code = %q, se esperaba 31", byCode["V001"].GuiaSunatCode)
	}
	// La venta de origen es FACTURA: nunca debía derivarse el tipo de guía de ahí.
	if byCode["T001"].DocType != "FACTURA" {
		t.Fatalf("doc_type de la venta de origen no se preservó: %q", byCode["T001"].DocType)
	}
}

// GetDespatchListItem es lo que usa el endpoint de refresco de estado: debe devolver el mismo
// guia_sunat_code que el listado, o la guía desaparece de su vista dedicada al refrescar.
func TestGetDespatchListItem_MismoTipoQueElListado(t *testing.T) {
	db := setupDespatchListDB(t)
	// &BillingService{db: db} en vez de NewBillingService: este último toca config.AppConfig,
	// que no está inicializado fuera del arranque real del proceso.
	svc := &BillingService{db: db}

	serie := database.TenantDocumentSeries{Series: "V001", SunatCode: "31"}
	db.Create(&serie)
	d := database.TenantDespatch{SeriesID: serie.ID, Series: "V001", Correlative: 1, BranchID: 1}
	db.Create(&d)

	item, err := svc.GetDespatchListItem(d)
	if err != nil {
		t.Fatalf("GetDespatchListItem: %v", err)
	}
	if item.GuiaSunatCode != "31" {
		t.Fatalf("guia_sunat_code = %q, se esperaba 31", item.GuiaSunatCode)
	}
}
