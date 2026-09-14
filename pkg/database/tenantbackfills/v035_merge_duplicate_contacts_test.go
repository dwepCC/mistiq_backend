package tenantbackfills

import (
	"fmt"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// Modelos mínimos para el test (evita depender del paquete database completo, mismo patrón que
// v034_product_codes_test.go).
type tcontact struct {
	ID              uint `gorm:"primaryKey"`
	Type            string
	DocType         string
	DocNumber       string
	Active          bool
	IsDefaultWalkin bool `gorm:"column:is_default_walkin"`
	DeletedAt       *string
}

func (tcontact) TableName() string { return "tenant_contacts" }

type tsaleLink struct {
	ID        uint `gorm:"primaryKey"`
	ContactID *uint
}

func (tsaleLink) TableName() string { return "tenant_sales" }

type tcontactPerson struct {
	ID        uint `gorm:"primaryKey"`
	ContactID uint
}

func (tcontactPerson) TableName() string { return "tenant_contact_persons" }

func setupMergeContactsDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&tcontact{}, &tsaleLink{}, &tcontactPerson{}); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestV035_MergesDuplicateAndReassignsReferences(t *testing.T) {
	db := setupMergeContactsDB(t)
	db.Create(&tcontact{ID: 1, Type: "customer", DocType: "RUC", DocNumber: "20123456789", Active: true})
	db.Create(&tcontact{ID: 2, Type: "customer", DocType: "RUC", DocNumber: "20123456789", Active: false})

	cid2 := uint(2)
	db.Create(&tsaleLink{ID: 100, ContactID: &cid2})
	db.Create(&tcontactPerson{ID: 200, ContactID: 2})

	if err := (V035MergeDuplicateContacts{}).Run(db); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var sale tsaleLink
	db.First(&sale, 100)
	if sale.ContactID == nil || *sale.ContactID != 1 {
		t.Fatalf("tenant_sales.contact_id: got %v want 1", sale.ContactID)
	}
	var cp tcontactPerson
	db.First(&cp, 200)
	if cp.ContactID != 1 {
		t.Fatalf("tenant_contact_persons.contact_id: got %d want 1", cp.ContactID)
	}

	var active, dup tcontact
	db.First(&active, 1)
	db.First(&dup, 2)
	if active.DeletedAt != nil {
		t.Fatal("el contacto activo no debía tocarse")
	}
	if dup.DeletedAt == nil {
		t.Fatal("el duplicado debía quedar borrado (soft delete) tras fusionarse")
	}
}

// Un grupo con 2 activos no tiene un destino inequívoco al cual fusionar — se salta a propósito.
func TestV035_SkipsGroupWithMultipleActives(t *testing.T) {
	db := setupMergeContactsDB(t)
	db.Create(&tcontact{ID: 1, Type: "customer", DocType: "RUC", DocNumber: "20999999999", Active: true})
	db.Create(&tcontact{ID: 2, Type: "customer", DocType: "RUC", DocNumber: "20999999999", Active: false})
	db.Create(&tcontact{ID: 3, Type: "customer", DocType: "RUC", DocNumber: "20999999999", Active: true})

	if err := (V035MergeDuplicateContacts{}).Run(db); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var dup tcontact
	db.First(&dup, 2)
	if dup.DeletedAt != nil {
		t.Fatal("no debía fusionarse: el grupo tiene 2 activos, sin destino inequívoco")
	}
}

// Un grupo sin ningún activo tampoco tiene destino al cual fusionar.
func TestV035_SkipsGroupWithNoActive(t *testing.T) {
	db := setupMergeContactsDB(t)
	db.Create(&tcontact{ID: 1, Type: "customer", DocType: "RUC", DocNumber: "20111111111", Active: false})
	db.Create(&tcontact{ID: 2, Type: "customer", DocType: "RUC", DocNumber: "20111111111", Active: false})

	if err := (V035MergeDuplicateContacts{}).Run(db); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var c1, c2 tcontact
	db.First(&c1, 1)
	db.First(&c2, 2)
	if c1.DeletedAt != nil || c2.DeletedAt != nil {
		t.Fatal("ninguno debía tocarse: el grupo no tiene un activo al cual fusionar")
	}
}

// El walk-in por defecto nunca se fusiona ni se borra, aunque por algún motivo esté inactivo.
func TestV035_NeverTouchesDefaultWalkin(t *testing.T) {
	db := setupMergeContactsDB(t)
	db.Create(&tcontact{ID: 1, Type: "customer", DocType: "0", DocNumber: "99999999", Active: true})
	db.Create(&tcontact{ID: 2, Type: "customer", DocType: "0", DocNumber: "99999999", Active: false, IsDefaultWalkin: true})

	if err := (V035MergeDuplicateContacts{}).Run(db); err != nil {
		t.Fatalf("Run: %v", err)
	}

	var walkin tcontact
	db.First(&walkin, 2)
	if walkin.DeletedAt != nil {
		t.Fatal("el walk-in por defecto nunca debe eliminarse")
	}
}

// El cron y el panel pueden correrlo de nuevo: la segunda pasada no debe encontrar nada que hacer.
func TestV035_Idempotente(t *testing.T) {
	db := setupMergeContactsDB(t)
	db.Create(&tcontact{ID: 1, Type: "customer", DocType: "RUC", DocNumber: "20123456789", Active: true})
	db.Create(&tcontact{ID: 2, Type: "customer", DocType: "RUC", DocNumber: "20123456789", Active: false})
	cid2 := uint(2)
	db.Create(&tsaleLink{ID: 100, ContactID: &cid2})

	if err := (V035MergeDuplicateContacts{}).Run(db); err != nil {
		t.Fatalf("primera pasada: %v", err)
	}
	if err := (V035MergeDuplicateContacts{}).Run(db); err != nil {
		t.Fatalf("segunda pasada: %v", err)
	}

	var sale tsaleLink
	db.First(&sale, 100)
	if sale.ContactID == nil || *sale.ContactID != 1 {
		t.Fatalf("tenant_sales.contact_id tras dos pasadas: got %v want 1", sale.ContactID)
	}
}
