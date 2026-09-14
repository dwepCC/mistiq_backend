package service

import (
	"fmt"
	"testing"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// Bug reportado: dos tenants (empresas) terminaron con el mismo RUC en producción — ni
// TenantService.Create ni la columna en BD validaban unicidad de RUC (solo el slug la
// tenía). Estos tests cubren el chequeo agregado en Create/Update; llegan a fallar ANTES
// de la parte pesada de Create (provisión real de BD del tenant), así que un sqlite en
// memoria con solo la tabla `tenants` migrada alcanza.
func setupTenantRucTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&database.Tenant{}); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestTenantServiceCreate_RejectsDuplicateRUC(t *testing.T) {
	db := setupTenantRucTestDB(t)
	existing := database.Tenant{Name: "Empresa Uno", Slug: "empresauno", DBName: "saas_tenant_empresauno", RUC: "10426620401"}
	if err := db.Create(&existing).Error; err != nil {
		t.Fatal(err)
	}

	svc := &TenantService{db: db}
	_, err := svc.Create(CreateTenantInput{
		Name: "Empresa Dos", Slug: "empresados", Address: "Av. Test 123", Ubigeo: "150101",
		AdminEmail: "admin@test.com", AdminPassword: "secret123", RUC: "10426620401",
	})
	if err == nil || err.Error() != "ya existe una empresa registrada con ese RUC" {
		t.Fatalf("expected duplicate-RUC error, got %v", err)
	}
}

// Espacios alrededor del RUC no deben permitir esquivar el chequeo.
func TestTenantServiceCreate_RejectsDuplicateRUC_TrimsWhitespace(t *testing.T) {
	db := setupTenantRucTestDB(t)
	existing := database.Tenant{Name: "Empresa Uno", Slug: "empresauno", DBName: "saas_tenant_empresauno", RUC: "10426620401"}
	if err := db.Create(&existing).Error; err != nil {
		t.Fatal(err)
	}

	svc := &TenantService{db: db}
	_, err := svc.Create(CreateTenantInput{
		Name: "Empresa Dos", Slug: "empresados", Address: "Av. Test 123", Ubigeo: "150101",
		AdminEmail: "admin@test.com", AdminPassword: "secret123", RUC: "  10426620401  ",
	})
	if err == nil || err.Error() != "ya existe una empresa registrada con ese RUC" {
		t.Fatalf("expected duplicate-RUC error (con espacios), got %v", err)
	}
}

func TestTenantServiceUpdate_RejectsDuplicateRUC(t *testing.T) {
	db := setupTenantRucTestDB(t)
	a := database.Tenant{Name: "Empresa Uno", Slug: "empresauno", DBName: "saas_tenant_empresauno", RUC: "10426620401"}
	b := database.Tenant{Name: "Empresa Dos", Slug: "empresados", DBName: "saas_tenant_empresados", RUC: "20111222333"}
	if err := db.Create(&a).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&b).Error; err != nil {
		t.Fatal(err)
	}

	svc := &TenantService{db: db}
	// Intentar editar B para que tenga el RUC de A.
	err := svc.Update(b.ID, database.Tenant{Name: b.Name, RUC: a.RUC, Status: "active"})
	if err == nil || err.Error() != "ya existe una empresa registrada con ese RUC" {
		t.Fatalf("expected duplicate-RUC error, got %v", err)
	}

	// Reafirmar el MISMO RUC de B (sin cambiarlo) no debe autobloquearse.
	if err := svc.Update(b.ID, database.Tenant{Name: b.Name, RUC: b.RUC, Status: "active"}); err != nil {
		t.Fatalf("actualizar sin cambiar el RUC propio no debería fallar: %v", err)
	}
}
