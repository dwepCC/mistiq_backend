package service

import (
	"fmt"
	"strings"
	"testing"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func setupContactImportDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&database.TenantContact{}); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestBulkImportContacts_CreaYNormaliza(t *testing.T) {
	db := setupContactImportDB(t)
	res, err := NewContactService(db).BulkImportContacts([]BulkImportContactItem{
		{RowNumber: 2, DocType: "RUC", DocNumber: "20123456789", BusinessName: "Distribuidora SAC", Type: "proveedor"},
		{RowNumber: 3, DocType: "DNI", DocNumber: "45678912", BusinessName: "Juan Pérez"},
		{RowNumber: 4, DocType: "", DocNumber: "20987654321", BusinessName: "Sin tipo (RUC por defecto)"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Created != 3 || len(res.Failed) != 0 {
		t.Fatalf("esperaba 3 creados sin fallos, got %d creados y %+v", res.Created, res.Failed)
	}

	// Variable nueva por consulta: si se reutiliza, GORM arrastra el ID ya cargado como
	// condición y la siguiente búsqueda no encuentra nada.
	var proveedor database.TenantContact
	db.Where("doc_number = ?", "20123456789").First(&proveedor)
	if proveedor.Type != "supplier" {
		t.Errorf("«proveedor» debe normalizarse a supplier, got %q", proveedor.Type)
	}
	if proveedor.DocType != "6" {
		t.Errorf("«RUC» debe normalizarse a 6, got %q", proveedor.DocType)
	}

	var sinTipo database.TenantContact
	db.Where("doc_number = ?", "20987654321").First(&sinTipo)
	if sinTipo.DocType != "6" || sinTipo.Type != "customer" {
		t.Errorf("sin tipo debe quedar RUC + cliente, got doc=%q type=%q", sinTipo.DocType, sinTipo.Type)
	}
}

// TestBulkImportContacts_ActualizaSiElDocumentoYaExiste: reimportar un padrón corregido no
// debe duplicar clientes, porque eso rompería el histórico de ventas.
func TestBulkImportContacts_ActualizaSiElDocumentoYaExiste(t *testing.T) {
	db := setupContactImportDB(t)
	svc := NewContactService(db)

	if _, err := svc.BulkImportContacts([]BulkImportContactItem{
		{RowNumber: 2, DocType: "RUC", DocNumber: "20123456789", BusinessName: "Nombre viejo"},
	}); err != nil {
		t.Fatal(err)
	}
	res, err := svc.BulkImportContacts([]BulkImportContactItem{
		{RowNumber: 2, DocType: "RUC", DocNumber: "20123456789", BusinessName: "Nombre corregido", Phone: "999888777"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Updated != 1 || res.Created != 0 {
		t.Fatalf("esperaba 1 actualizado y 0 creados, got %d/%d", res.Updated, res.Created)
	}

	var count int64
	db.Model(&database.TenantContact{}).Where("doc_number = ?", "20123456789").Count(&count)
	if count != 1 {
		t.Errorf("no debe duplicar: hay %d filas con el mismo documento", count)
	}
	var c database.TenantContact
	db.Where("doc_number = ?", "20123456789").First(&c)
	if c.BusinessName != "Nombre corregido" || c.Phone != "999888777" {
		t.Errorf("no se actualizaron los datos: %+v", c)
	}
}

// TestBulkImportContacts_NoTocaElClientePorDefecto: el «Clientes Varios» que crea el alta
// del tenant no debe poder modificarse desde una importación.
func TestBulkImportContacts_NoTocaElClientePorDefecto(t *testing.T) {
	db := setupContactImportDB(t)
	if err := db.Create(&database.TenantContact{
		Type: "customer", DocType: "0", DocNumber: "99999999",
		BusinessName: "Clientes Varios", IsDefaultWalkIn: true, Active: true,
	}).Error; err != nil {
		t.Fatal(err)
	}

	res, err := NewContactService(db).BulkImportContacts([]BulkImportContactItem{
		{RowNumber: 2, DocType: "0", DocNumber: "99999999", BusinessName: "Intento de sobrescribir"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Updated != 0 || len(res.Failed) != 1 {
		t.Fatalf("esperaba que se rechazara, got %d actualizados y %+v", res.Updated, res.Failed)
	}
	var c database.TenantContact
	db.Where("doc_number = ?", "99999999").First(&c)
	if c.BusinessName != "Clientes Varios" {
		t.Errorf("el cliente por defecto fue modificado: %q", c.BusinessName)
	}
}

func TestBulkImportContacts_Validaciones(t *testing.T) {
	db := setupContactImportDB(t)
	res, err := NewContactService(db).BulkImportContacts([]BulkImportContactItem{
		{RowNumber: 2, DocType: "RUC", DocNumber: "20123456789", BusinessName: ""},
		{RowNumber: 3, DocType: "RUC", DocNumber: "123", BusinessName: "RUC corto"},
		{RowNumber: 4, DocType: "DNI", DocNumber: "123", BusinessName: "DNI corto"},
		{RowNumber: 5, DocType: "INVENTADO", DocNumber: "20123456789", BusinessName: "Tipo doc inválido"},
		{RowNumber: 6, DocType: "RUC", DocNumber: "", BusinessName: "Sin documento"},
		{RowNumber: 7, DocType: "RUC", DocNumber: "20111111111", BusinessName: "Tipo inválido", Type: "socio"},
		{RowNumber: 8, DocType: "RUC", DocNumber: "20222222222", BusinessName: "Ubigeo corto", Ubigeo: "15"},
		// Repetido dentro del mismo archivo.
		{RowNumber: 9, DocType: "RUC", DocNumber: "20333333333", BusinessName: "Primero"},
		{RowNumber: 10, DocType: "RUC", DocNumber: "20333333333", BusinessName: "Duplicado"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Created != 1 {
		t.Errorf("solo la fila 9 debe crearse, got %d", res.Created)
	}
	if len(res.Failed) != 8 {
		t.Fatalf("esperaba 8 filas rechazadas, got %d: %+v", len(res.Failed), res.Failed)
	}
	esperado := map[int]string{
		2: "requerido", 3: "11 dígitos", 4: "8 dígitos", 5: "tipo_documento inválido",
		6: "requerido", 7: "tipo inválido", 8: "ubigeo", 10: "repetido",
	}
	for _, f := range res.Failed {
		want, ok := esperado[f.Row]
		if !ok {
			t.Errorf("fila %d no debía fallar: %s", f.Row, f.Error)
			continue
		}
		if !strings.Contains(strings.ToLower(f.Error), strings.ToLower(want)) {
			t.Errorf("fila %d: error %q, esperaba que contuviera %q", f.Row, f.Error, want)
		}
	}
}

func TestBulkImportContacts_RechazaArchivoVacioODemasiadoGrande(t *testing.T) {
	svc := NewContactService(setupContactImportDB(t))
	if _, err := svc.BulkImportContacts(nil); err == nil {
		t.Error("un archivo vacío debe rechazarse")
	}
	grande := make([]BulkImportContactItem, BulkImportMaxItems+1)
	if _, err := svc.BulkImportContacts(grande); err == nil {
		t.Error("por encima del tope debe rechazarse")
	}
}
