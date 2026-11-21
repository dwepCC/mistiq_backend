package service

import (
	"errors"
	"strings"
	"testing"

	"tukifac/pkg/database"

	"gorm.io/gorm"
)

func createContactForDelete(t *testing.T, db *gorm.DB, doc string) uint {
	t.Helper()
	c, err := NewContactService(db).Create(ContactInput{Type: "customer", DocType: "6", DocNumber: doc, BusinessName: "Cliente " + doc})
	if err != nil {
		t.Fatal(err)
	}
	return c.ID
}

// Un contacto con ventas (u otros documentos) no se elimina: el comprobante perdería a su cliente.
func TestContactServiceDelete_BlockedWhenItHasDocuments(t *testing.T) {
	db := setupContactServiceDB(t)
	if err := db.Exec("CREATE TABLE tenant_sales (id integer primary key, contact_id integer)").Error; err != nil {
		t.Fatal(err)
	}
	id := createContactForDelete(t, db, "20565959027")
	if err := db.Exec("INSERT INTO tenant_sales (id, contact_id) VALUES (10, ?), (11, ?)", id, id).Error; err != nil {
		t.Fatal(err)
	}

	err := NewContactService(db).Delete(id)
	var inUse *ContactInUseError
	if !errors.As(err, &inUse) || inUse.Documents != 2 {
		t.Fatalf("esperaba ContactInUseError con 2 documentos, got %v", err)
	}
	if !strings.Contains(err.Error(), "desactív") {
		t.Fatalf("el mensaje debe ofrecer desactivar: %v", err)
	}
	var n int64
	db.Model(&database.TenantContact{}).Where("id = ?", id).Count(&n)
	if n != 1 {
		t.Fatal("el contacto no debe eliminarse")
	}
}

func TestContactServiceDelete_AllowedWithoutDocuments(t *testing.T) {
	db := setupContactServiceDB(t)
	if err := db.Exec("CREATE TABLE tenant_sales (id integer primary key, contact_id integer)").Error; err != nil {
		t.Fatal(err)
	}
	id := createContactForDelete(t, db, "20111111111")
	other := createContactForDelete(t, db, "20222222222")
	if err := db.Exec("INSERT INTO tenant_sales (id, contact_id) VALUES (1, ?)", other).Error; err != nil {
		t.Fatal(err)
	}

	if err := NewContactService(db).Delete(id); err != nil {
		t.Fatalf("un contacto sin documentos sí se elimina: %v", err)
	}
	var n int64
	db.Model(&database.TenantContact{}).Where("id = ?", id).Count(&n)
	if n != 0 {
		t.Fatal("debió eliminarse")
	}
}

func TestContactServiceDelete_IgnoresMissingOptionalTables(t *testing.T) {
	db := setupContactServiceDB(t)
	id := createContactForDelete(t, db, "20333333333")
	if err := NewContactService(db).Delete(id); err != nil {
		t.Fatalf("sin tablas de documentos no debe fallar: %v", err)
	}
}
