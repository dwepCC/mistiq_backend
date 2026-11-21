package service

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"tukifac/pkg/database"
	"tukifac/pkg/saas"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

const quotaTenantID = 7

// setupQuotaDBs crea la BD central (plan + suscripción del tenant) y la BD del tenant con el
// dueño ya creado. maxUsers 0 = plan ilimitado.
func setupQuotaDBs(t *testing.T, maxUsers int) *gorm.DB {
	t.Helper()
	central, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := central.AutoMigrate(&database.SaasPlan{}, &database.SaasSubscription{}); err != nil {
		t.Fatal(err)
	}
	plan := database.SaasPlan{Name: "MICROEMPRESA", MaxUsers: maxUsers, Active: true}
	if err := central.Create(&plan).Error; err != nil {
		t.Fatal(err)
	}
	if err := central.Create(&database.SaasSubscription{TenantID: quotaTenantID, PlanID: plan.ID, Status: database.SaasSubActive}).Error; err != nil {
		t.Fatal(err)
	}
	prev := database.CentralDB
	database.CentralDB = central
	t.Cleanup(func() { database.CentralDB = prev })

	db := setupUserServiceTestDB(t)
	if err := db.Create(&database.TenantUser{Name: "Dueño", Email: "owner@t.com", RoleID: 1, Active: true}).Error; err != nil {
		t.Fatal(err)
	}
	return db
}

func quotaInput(n int) CreateUserInput {
	return CreateUserInput{Name: fmt.Sprintf("U%d", n), Email: fmt.Sprintf("u%d@t.com", n), Password: "secreto123", RoleID: 1, Active: true}
}

func asQuotaError(t *testing.T, err error) *saas.QuotaError {
	t.Helper()
	var q *saas.QuotaError
	if !errors.As(err, &q) {
		t.Fatalf("se esperaba *saas.QuotaError, llegó %v", err)
	}
	return q
}

func TestCreate_withinLimit_and_exactLimit_thenRejected(t *testing.T) {
	db := setupQuotaDBs(t, 3) // dueño + 2 = 3 → en el límite exacto
	svc := NewUserService(db).WithTenant(quotaTenantID)
	for i := 1; i <= 2; i++ {
		if _, err := svc.Create(quotaInput(i)); err != nil {
			t.Fatalf("alta %d dentro del límite debe pasar: %v", i, err)
		}
	}
	_, err := svc.Create(quotaInput(3)) // el 4.º
	q := asQuotaError(t, err)
	if q.Used != 3 || q.Max != 3 {
		t.Errorf("used/max = %d/%d, se esperaba 3/3", q.Used, q.Max)
	}
	want := "Tu plan MICROEMPRESA permite hasta 3 usuarios. Actualiza tu plan para agregar más."
	if q.Error() != want {
		t.Errorf("mensaje = %q", q.Error())
	}
	if n := saas.CountSeatUsers(db); n != 3 {
		t.Errorf("no debía crearse el usuario rechazado: activos = %d", n)
	}
}

func TestCreate_alreadyOverLimit_isRejectedAndExistingUntouched(t *testing.T) {
	db := setupQuotaDBs(t, 3)
	// Tenant heredado que ya excede (4 activos): se respetan, pero no se permite uno más.
	for i := 1; i <= 3; i++ {
		db.Create(&database.TenantUser{Name: fmt.Sprintf("X%d", i), Email: fmt.Sprintf("x%d@t.com", i), RoleID: 1, Active: true})
	}
	_, err := NewUserService(db).WithTenant(quotaTenantID).Create(quotaInput(1))
	asQuotaError(t, err)
	if n := saas.CountSeatUsers(db); n != 4 {
		t.Errorf("los existentes no se tocan: activos = %d, se esperaba 4", n)
	}
}

func TestCreate_unlimitedPlan_and_noTenantContext(t *testing.T) {
	db := setupQuotaDBs(t, 0)
	svc := NewUserService(db).WithTenant(quotaTenantID)
	for i := 1; i <= 10; i++ {
		if _, err := svc.Create(quotaInput(i)); err != nil {
			t.Fatalf("plan ilimitado no debe limitar: %v", err)
		}
	}
	// Sin tenant (uso interno) tampoco valida, aunque el plan tuviera tope.
	db2 := setupQuotaDBs(t, 1)
	if _, err := NewUserService(db2).Create(quotaInput(1)); err != nil {
		t.Fatalf("sin contexto de tenant no se valida: %v", err)
	}
}

func TestReactivate_viaToggleAndUpdate_respectLimit(t *testing.T) {
	db := setupQuotaDBs(t, 2) // dueño + 1
	svc := NewUserService(db).WithTenant(quotaTenantID)
	active, err := svc.Create(quotaInput(1))
	if err != nil {
		t.Fatal(err)
	}
	inactive := database.TenantUser{Name: "Baja", Email: "baja@t.com", RoleID: 1, Active: true}
	db.Create(&inactive)
	db.Model(&inactive).Update("active", false)

	asQuotaError(t, svc.ToggleActive(inactive.ID))
	upd := UpdateUserInput{Name: inactive.Name, Email: inactive.Email, RoleID: 1, Active: true}
	asQuotaError(t, svc.Update(inactive.ID, upd))

	// Desactivar libera el cupo y entonces sí se puede reactivar al otro.
	if err := svc.ToggleActive(active.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.ToggleActive(inactive.ID); err != nil {
		t.Fatalf("con cupo libre la reactivación debe pasar: %v", err)
	}
	// Editar un usuario ya activo (sin reactivar) nunca se bloquea, aunque el tenant esté al tope.
	again := UpdateUserInput{Name: "Renombrado", Email: inactive.Email, RoleID: 1, Active: true}
	if err := svc.Update(inactive.ID, again); err != nil {
		t.Fatalf("editar un activo no consume cupo: %v", err)
	}
}

func TestSoftDeletedUsers_doNotCount(t *testing.T) {
	db := setupQuotaDBs(t, 2)
	svc := NewUserService(db).WithTenant(quotaTenantID)
	u, err := svc.Create(quotaInput(1))
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Delete(u.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Create(quotaInput(2)); err != nil {
		t.Fatalf("un usuario eliminado libera su cupo: %v", err)
	}
}

// Altas simultáneas: sea cual sea el orden, nunca se supera el tope. En MySQL la serialización la
// da el bloqueo de fila de ReserveUserSeat; SQLite (tests) serializa por su bloqueo de escritura.
func TestCreate_concurrent_neverExceedsLimit(t *testing.T) {
	central, _ := gorm.Open(sqlite.Open("file:"+filepath.Join(t.TempDir(), "c.db")+"?_busy_timeout=15000"), &gorm.Config{})
	t.Cleanup(func() { sqlDB, _ := central.DB(); _ = sqlDB.Close() })
	_ = central.AutoMigrate(&database.SaasPlan{}, &database.SaasSubscription{})
	plan := database.SaasPlan{Name: "MICROEMPRESA", MaxUsers: 3, Active: true}
	central.Create(&plan)
	central.Create(&database.SaasSubscription{TenantID: quotaTenantID, PlanID: plan.ID, Status: database.SaasSubActive})
	prev := database.CentralDB
	database.CentralDB = central
	t.Cleanup(func() { database.CentralDB = prev })

	dsn := "file:" + filepath.Join(t.TempDir(), "q.db") + "?_busy_timeout=15000"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB, _ := db.DB(); _ = sqlDB.Close() })
	if err := db.AutoMigrate(&database.TenantUser{}, &database.TenantRole{}); err != nil {
		t.Fatal(err)
	}
	db.Create(&database.TenantUser{Name: "Dueño", Email: "owner@t.com", RoleID: 1, Active: true})

	var wg sync.WaitGroup
	for i := 1; i <= 8; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			_, _ = NewUserService(db).WithTenant(quotaTenantID).Create(quotaInput(n))
		}(i)
	}
	wg.Wait()
	if n := saas.CountSeatUsers(db); n > 3 {
		t.Fatalf("activos = %d, superó el tope 3", n)
	}
}

func TestQuotaError_otherResourcesKeepGenericMessage(t *testing.T) {
	e := &saas.QuotaError{Resource: saas.QuotaBranches, Used: 1, Max: 1}
	if !strings.Contains(e.Error(), "sucursales") {
		t.Errorf("mensaje inesperado: %s", e.Error())
	}
}
