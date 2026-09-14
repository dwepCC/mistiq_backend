package service

import (
	"fmt"
	"testing"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// Test directo de la regla de unicidad de sesión abierta (OpenSession). No existía ningún test
// propio para esto en el repo. No se cambia la regla — solo se deja evidencia de cuál es
// exactamente el alcance actual: único por (branch_id, user_id), NO global por usuario ni por
// sucursal.

func setupOpenSessionUniquenessTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&database.TenantCashSession{}); err != nil {
		t.Fatal(err)
	}
	return db
}

// 1. Mismo usuario + misma sucursal: la segunda apertura debe rechazarse mientras la primera
// siga abierta.
func TestOpenSession_mismoUsuarioMismaSucursal_rechazaSegundaApertura(t *testing.T) {
	db := setupOpenSessionUniquenessTestDB(t)
	svc := NewCashBankService(db)

	if _, err := svc.OpenSession(OpenSessionInput{BranchID: 1, UserID: 5, OpeningBalance: 100}); err != nil {
		t.Fatalf("primera apertura no debía fallar: %v", err)
	}
	if _, err := svc.OpenSession(OpenSessionInput{BranchID: 1, UserID: 5, OpeningBalance: 50}); err == nil {
		t.Fatal("se esperaba rechazo al abrir una segunda sesión con la primera aún abierta")
	}
}

// 2. Tras cerrar la primera, el mismo usuario/sucursal puede abrir una nueva sesión sin problema.
func TestOpenSession_mismoUsuarioMismaSucursal_permiteNuevaTrasCerrar(t *testing.T) {
	db := setupOpenSessionUniquenessTestDB(t)
	svc := NewCashBankService(db)

	first, err := svc.OpenSession(OpenSessionInput{BranchID: 1, UserID: 5, OpeningBalance: 100})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.CloseSession(first.ID, 5, 100, "", nil, false); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.OpenSession(OpenSessionInput{BranchID: 1, UserID: 5, OpeningBalance: 0}); err != nil {
		t.Fatalf("debía permitir una nueva sesión tras cerrar la anterior: %v", err)
	}
}

// 3. Comportamiento actual (sin cambiar arquitectura): usuarios DISTINTOS pueden tener sesión
// abierta simultánea en la MISMA sucursal — varios cajeros en un mismo punto de venta.
func TestOpenSession_usuariosDistintosMismaSucursal_ambasQuedanAbiertas(t *testing.T) {
	db := setupOpenSessionUniquenessTestDB(t)
	svc := NewCashBankService(db)

	if _, err := svc.OpenSession(OpenSessionInput{BranchID: 1, UserID: 5, OpeningBalance: 100}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.OpenSession(OpenSessionInput{BranchID: 1, UserID: 9, OpeningBalance: 100}); err != nil {
		t.Fatalf("un usuario distinto en la misma sucursal no debía ser rechazado: %v", err)
	}

	var count int64
	db.Model(&database.TenantCashSession{}).Where("branch_id = ? AND status = ?", 1, "open").Count(&count)
	if count != 2 {
		t.Errorf("sesiones abiertas en la sucursal = %d, want 2", count)
	}
}

// 4. Comportamiento actual (sin cambiar arquitectura): el MISMO usuario puede tener sesión
// abierta simultánea en sucursales DISTINTAS — la unicidad es por (branch_id, user_id), no
// global por usuario.
func TestOpenSession_mismoUsuarioSucursalesDistintas_ambasQuedanAbiertas(t *testing.T) {
	db := setupOpenSessionUniquenessTestDB(t)
	svc := NewCashBankService(db)

	if _, err := svc.OpenSession(OpenSessionInput{BranchID: 1, UserID: 5, OpeningBalance: 100}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.OpenSession(OpenSessionInput{BranchID: 2, UserID: 5, OpeningBalance: 100}); err != nil {
		t.Fatalf("el mismo usuario en otra sucursal no debía ser rechazado: %v", err)
	}

	var count int64
	db.Model(&database.TenantCashSession{}).Where("user_id = ? AND status = ?", 5, "open").Count(&count)
	if count != 2 {
		t.Errorf("sesiones abiertas del usuario = %d, want 2 (una por sucursal)", count)
	}
}
