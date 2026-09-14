package service

import (
	"testing"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func setupContactServiceDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := "file:" + t.Name() + "?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&database.TenantContact{}, &database.TenantContactPerson{}); err != nil {
		t.Fatal(err)
	}
	return db
}

// El bug real: un usuario pudo registrar el mismo cliente dos veces con datos idénticos, porque
// ContactService.Create no hacía ningún chequeo de duplicado antes de insertar.
func TestContactServiceCreate_rejectsSameDocSameRole(t *testing.T) {
	db := setupContactServiceDB(t)
	svc := NewContactService(db)

	if _, err := svc.Create(ContactInput{Type: "customer", DocType: "6", DocNumber: "20123456789", BusinessName: "Acme SAC"}); err != nil {
		t.Fatalf("primer alta no debería fallar: %v", err)
	}

	_, err := svc.Create(ContactInput{Type: "customer", DocType: "6", DocNumber: "20123456789", BusinessName: "Acme SAC"})
	if err == nil {
		t.Fatal("esperaba error al registrar un segundo cliente con el mismo documento, no falló")
	}

	var count int64
	db.Model(&database.TenantContact{}).Where("doc_number = ?", "20123456789").Count(&count)
	if count != 1 {
		t.Errorf("no debía duplicarse: hay %d filas con el mismo documento", count)
	}
}

// El mismo documento debe poder registrarse como cliente Y como proveedor (roles distintos,
// filas separadas) — no es un duplicado real.
func TestContactServiceCreate_allowsSameDocDifferentRole(t *testing.T) {
	db := setupContactServiceDB(t)
	svc := NewContactService(db)

	if _, err := svc.Create(ContactInput{Type: "customer", DocType: "6", DocNumber: "20123456789", BusinessName: "Acme SAC"}); err != nil {
		t.Fatalf("alta de cliente no debería fallar: %v", err)
	}
	if _, err := svc.Create(ContactInput{Type: "supplier", DocType: "6", DocNumber: "20123456789", BusinessName: "Acme SAC"}); err != nil {
		t.Fatalf("alta de proveedor con el mismo documento no debería fallar: %v", err)
	}

	var count int64
	db.Model(&database.TenantContact{}).Where("doc_number = ?", "20123456789").Count(&count)
	if count != 2 {
		t.Errorf("esperaba 2 filas (cliente + proveedor), got %d", count)
	}
}

// "both" ocupa los dos roles a la vez: no puede coexistir con un "customer" o "supplier" suelto
// del mismo documento, ni con otro "both".
func TestContactServiceCreate_bothCollidesWithEitherRole(t *testing.T) {
	db := setupContactServiceDB(t)
	svc := NewContactService(db)

	if _, err := svc.Create(ContactInput{Type: "customer", DocType: "6", DocNumber: "20123456789", BusinessName: "Acme SAC"}); err != nil {
		t.Fatalf("alta de cliente no debería fallar: %v", err)
	}
	if _, err := svc.Create(ContactInput{Type: "both", DocType: "6", DocNumber: "20123456789", BusinessName: "Acme SAC"}); err == nil {
		t.Fatal("«both» debería chocar contra un cliente ya existente con el mismo documento")
	}
}

// Documentos distintos (diferente doc_type o diferente número) nunca deberían chocar entre sí.
func TestContactServiceCreate_differentDocNeverCollides(t *testing.T) {
	db := setupContactServiceDB(t)
	svc := NewContactService(db)

	if _, err := svc.Create(ContactInput{Type: "customer", DocType: "1", DocNumber: "45678912", BusinessName: "Juan Pérez"}); err != nil {
		t.Fatalf("alta no debería fallar: %v", err)
	}
	if _, err := svc.Create(ContactInput{Type: "customer", DocType: "1", DocNumber: "45678913", BusinessName: "Juan Pérez"}); err != nil {
		t.Fatalf("mismo nombre con documento distinto no debería chocar: %v", err)
	}
}

// Update no debe chocar contra sí mismo al re-guardar los mismos datos.
func TestContactServiceUpdate_excludesSelfFromDuplicateCheck(t *testing.T) {
	db := setupContactServiceDB(t)
	svc := NewContactService(db)

	c, err := svc.Create(ContactInput{Type: "customer", DocType: "6", DocNumber: "20123456789", BusinessName: "Acme SAC"})
	if err != nil {
		t.Fatalf("alta no debería fallar: %v", err)
	}

	if err := svc.Update(c.ID, ContactInput{Type: "customer", DocType: "6", DocNumber: "20123456789", BusinessName: "Acme SAC actualizado"}); err != nil {
		t.Fatalf("actualizar el propio contacto con el mismo documento no debería chocar consigo mismo: %v", err)
	}
}

// Un duplicado desactivado (la forma de "retirarlo" sin borrar su historial, mientras no hay
// UNIQUE INDEX en BD) no debe bloquear la edición del contacto que quedó activo para ese
// documento — si no, apenas se desactiva un duplicado, el que sigue vigente queda imposible
// de editar.
func TestContactServiceUpdate_inactiveDuplicateDoesNotBlockEditingTheActiveOne(t *testing.T) {
	db := setupContactServiceDB(t)
	svc := NewContactService(db)

	dupA, err := svc.Create(ContactInput{Type: "customer", DocType: "6", DocNumber: "20123456789", BusinessName: "Acme SAC (duplicado viejo)"})
	if err != nil {
		t.Fatalf("alta no debería fallar: %v", err)
	}
	if err := db.Model(&database.TenantContact{}).Where("id = ?", dupA.ID).Update("active", false).Error; err != nil {
		t.Fatalf("no se pudo desactivar: %v", err)
	}

	dupB, err := svc.Create(ContactInput{Type: "customer", DocType: "6", DocNumber: "20123456789", BusinessName: "Acme SAC"})
	if err != nil {
		t.Fatalf("crear un contacto nuevo para un documento cuyo único titular está inactivo no debería chocar: %v", err)
	}

	if err := svc.Update(dupB.ID, ContactInput{Type: "customer", DocType: "6", DocNumber: "20123456789", BusinessName: "Acme SAC actualizado"}); err != nil {
		t.Fatalf("editar el contacto activo no debería chocar contra su gemelo desactivado: %v", err)
	}
}

// Update sí debe chocar si se intenta reasignar el documento de otro contacto ya existente
// con el mismo rol.
func TestContactServiceUpdate_rejectsDocAlreadyUsedByAnother(t *testing.T) {
	db := setupContactServiceDB(t)
	svc := NewContactService(db)

	if _, err := svc.Create(ContactInput{Type: "customer", DocType: "6", DocNumber: "20123456789", BusinessName: "Acme SAC"}); err != nil {
		t.Fatalf("alta no debería fallar: %v", err)
	}
	other, err := svc.Create(ContactInput{Type: "customer", DocType: "6", DocNumber: "20999999999", BusinessName: "Otro SAC"})
	if err != nil {
		t.Fatalf("alta no debería fallar: %v", err)
	}

	if err := svc.Update(other.ID, ContactInput{Type: "customer", DocType: "6", DocNumber: "20123456789", BusinessName: "Otro SAC"}); err == nil {
		t.Fatal("esperaba error al reasignar un documento que ya usa otro cliente")
	}
}
