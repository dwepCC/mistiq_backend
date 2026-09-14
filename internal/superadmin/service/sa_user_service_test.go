package service

// Fase 5, etapa 3, Grupo 7, Paso E: tests de SAUserService — donde viven las invariantes
// (techo de delegación, cuenta protegida, último superadmin, transacciones). La superficie HTTP
// (permisos por ruta, mass-assignment, invalidación de JWT vía el middleware real) tiene su
// propia cobertura en internal/superadmin/handler/auth_sa_handler_grupo7_test.go.

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"tukifac/pkg/database"
	"tukifac/pkg/middleware"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func setupSAUserServiceTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&database.SuperAdminUser{}, &database.SARole{}, &database.SAPermission{}, &database.SARolePermission{},
	); err != nil {
		t.Fatal(err)
	}
	return db
}

// setupSAUserServiceConcurrencyDB usa el mismo DSN que el resto del proyecto para pruebas de
// transacciones concurrentes (WAL + busy_timeout generoso) — ver pkg/payments tests (Grupo 2).
func setupSAUserServiceConcurrencyDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared&_journal_mode=WAL&_busy_timeout=15000", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&database.SuperAdminUser{}); err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1) // fuerza que las dos goroutines compitan por la MISMA conexión física —
	// es justamente lo que hace observable la serialización de clause.Locking bajo SQLite (ver
	// comentario de TestSAUserService_LastSuperadmin_Concurrent*).
	t.Cleanup(func() { sqlDB.Close() })
	return db
}

func createSAUserFixture(t *testing.T, db *gorm.DB, email, role string, active bool) database.SuperAdminUser {
	t.Helper()
	u := database.SuperAdminUser{Name: "T", Email: email, Role: role, Active: true}
	if err := u.SetPassword("password123"); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&u).Error; err != nil {
		t.Fatal(err)
	}
	// Active tiene `gorm:"default:true"` — Create() OMITE el valor explícito false (es el
	// zero-value de bool), y la BD aplica su propio default (true) en su lugar. Se fuerza con un
	// UPDATE aparte, igual que el resto del proyecto (ver Fase 1 / auth_sa_handler_security_test.go).
	if !active {
		if err := db.Model(&u).UpdateColumn("active", false).Error; err != nil {
			t.Fatal(err)
		}
		u.Active = false
	}
	return u
}

func seedSAUserPermission(t *testing.T, db *gorm.DB, module, action string) database.SAPermission {
	t.Helper()
	p := database.SAPermission{Module: module, Action: action, Label: module + "." + action}
	if err := db.Create(&p).Error; err != nil {
		t.Fatal(err)
	}
	return p
}

func createSARoleWithPermissions(t *testing.T, db *gorm.DB, name string, permIDs ...uint) database.SARole {
	t.Helper()
	role := database.SARole{Name: name}
	if err := db.Create(&role).Error; err != nil {
		t.Fatal(err)
	}
	for _, pid := range permIDs {
		if err := db.Create(&database.SARolePermission{RoleID: role.ID, PermissionID: pid}).Error; err != nil {
			t.Fatal(err)
		}
	}
	return role
}

func nonSuperadminActor(userID uint, permissions ...string) *middleware.SuperAdminClaims {
	return &middleware.SuperAdminClaims{UserID: userID, Role: "admin", Permissions: permissions}
}

func superadminActor(userID uint) *middleware.SuperAdminClaims {
	return &middleware.SuperAdminClaims{UserID: userID, Role: "superadmin"}
}

// ==================== Create ====================

func TestSAUserService_Create_Basic(t *testing.T) {
	db := setupSAUserServiceTestDB(t)
	svc := NewSAUserService(db)

	user, err := svc.Create(superadminActor(1), "Nuevo", "nuevo@example.com", "password123", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if user.Role != "admin" {
		t.Fatalf("Role = %q, want admin (Create SIEMPRE crea admin)", user.Role)
	}
	if !user.CheckPassword("password123") {
		t.Fatal("la contraseña no quedó hasheada correctamente")
	}
}

// §4/decisión confirmada: Create() no tiene forma de producir Role="superadmin" — ni siquiera
// llamado por un actor que SÍ es superadmin real. La única vía es ChangeSystemRole.
func TestSAUserService_Create_NeverProducesSuperadmin_EvenWithSuperadminActor(t *testing.T) {
	db := setupSAUserServiceTestDB(t)
	svc := NewSAUserService(db)

	user, err := svc.Create(superadminActor(1), "Nuevo", "nuevo@example.com", "password123", nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if user.Role != "admin" {
		t.Fatalf("Role = %q — Create() NUNCA debe producir superadmin, sin importar quién lo llame", user.Role)
	}
}

func TestSAUserService_Create_ValidatesInput(t *testing.T) {
	db := setupSAUserServiceTestDB(t)
	svc := NewSAUserService(db)

	if _, err := svc.Create(superadminActor(1), "", "a@example.com", "password123", nil); !errors.Is(err, ErrSAUserNameRequired) {
		t.Errorf("nombre vacío: err = %v, want ErrSAUserNameRequired", err)
	}
	if _, err := svc.Create(superadminActor(1), "N", "", "password123", nil); !errors.Is(err, ErrSAUserEmailRequired) {
		t.Errorf("email vacío: err = %v, want ErrSAUserEmailRequired", err)
	}
	if _, err := svc.Create(superadminActor(1), "N", "a@example.com", "short", nil); !errors.Is(err, ErrSAUserPasswordTooShort) {
		t.Errorf("password corta: err = %v, want ErrSAUserPasswordTooShort", err)
	}
}

func TestSAUserService_Create_RejectsDuplicateEmail(t *testing.T) {
	db := setupSAUserServiceTestDB(t)
	svc := NewSAUserService(db)
	createSAUserFixture(t, db, "dup@example.com", "admin", true)

	if _, err := svc.Create(superadminActor(1), "N", "dup@example.com", "password123", nil); !errors.Is(err, ErrSAUserEmailTaken) {
		t.Fatalf("err = %v, want ErrSAUserEmailTaken", err)
	}
}

// §19: crear usuario + RoleID que el actor no puede delegar → rechazado. El usuario NO debe
// quedar creado (no hay estado parcial: "usuario creado pero sin el rol pedido" sería peor que
// rechazar todo).
func TestSAUserService_Create_RejectsRoleIDActorCannotDelegate(t *testing.T) {
	db := setupSAUserServiceTestDB(t)
	svc := NewSAUserService(db)
	resetPerm := seedSAUserPermission(t, db, "usuarios_central", "reset_password")
	role := createSARoleWithPermissions(t, db, "Con reset_password", resetPerm.ID)

	actor := nonSuperadminActor(1, "usuarios_central.create") // sin usuarios_central.reset_password
	_, err := svc.Create(actor, "N", "n@example.com", "password123", &role.ID)
	if !errors.Is(err, ErrSAUserCannotDelegate) {
		t.Fatalf("err = %v, want ErrSAUserCannotDelegate", err)
	}
	var count int64
	db.Model(&database.SuperAdminUser{}).Where("email = ?", "n@example.com").Count(&count)
	if count != 0 {
		t.Fatal("no debió crearse ningún usuario cuando el rol no es delegable")
	}
}

// §33: role_id de un rol que el actor SÍ puede delegar → permitido.
func TestSAUserService_Create_AllowsRoleIDActorCanDelegate(t *testing.T) {
	db := setupSAUserServiceTestDB(t)
	svc := NewSAUserService(db)
	viewPerm := seedSAUserPermission(t, db, "empresas", "view")
	role := createSARoleWithPermissions(t, db, "Soporte", viewPerm.ID)

	actor := nonSuperadminActor(1, "usuarios_central.create", "empresas.view")
	user, err := svc.Create(actor, "N", "n@example.com", "password123", &role.ID)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if user.RoleID == nil || *user.RoleID != role.ID {
		t.Fatalf("RoleID = %v, want %d", user.RoleID, role.ID)
	}
}

func TestSAUserService_Create_RejectsNonExistentRoleID(t *testing.T) {
	db := setupSAUserServiceTestDB(t)
	svc := NewSAUserService(db)
	fakeID := uint(99999)

	_, err := svc.Create(superadminActor(1), "N", "n@example.com", "password123", &fakeID)
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("err = %v, want gorm.ErrRecordNotFound", err)
	}
}

// ==================== UpdateBasicInfo ====================

func TestSAUserService_UpdateBasicInfo_NameAndEmail(t *testing.T) {
	db := setupSAUserServiceTestDB(t)
	svc := NewSAUserService(db)
	u := createSAUserFixture(t, db, "old@example.com", "admin", true)

	name := "Nuevo Nombre"
	email := "new@example.com"
	updated, err := svc.UpdateBasicInfo(u.ID, &name, &email)
	if err != nil {
		t.Fatalf("UpdateBasicInfo: %v", err)
	}
	if updated.Name != name || updated.Email != email {
		t.Fatalf("got name=%q email=%q", updated.Name, updated.Email)
	}
}

func TestSAUserService_UpdateBasicInfo_RejectsDuplicateEmail(t *testing.T) {
	db := setupSAUserServiceTestDB(t)
	svc := NewSAUserService(db)
	createSAUserFixture(t, db, "taken@example.com", "admin", true)
	u := createSAUserFixture(t, db, "me@example.com", "admin", true)

	email := "taken@example.com"
	if _, err := svc.UpdateBasicInfo(u.ID, nil, &email); !errors.Is(err, ErrSAUserEmailTaken) {
		t.Fatalf("err = %v, want ErrSAUserEmailTaken", err)
	}
}

// ==================== ChangeRole (RoleID granular) ====================

func TestSAUserService_ChangeRole_Success_IncrementsTokenVersion(t *testing.T) {
	db := setupSAUserServiceTestDB(t)
	svc := NewSAUserService(db)
	viewPerm := seedSAUserPermission(t, db, "empresas", "view")
	role := createSARoleWithPermissions(t, db, "Soporte", viewPerm.ID)
	target := createSAUserFixture(t, db, "t@example.com", "admin", true)
	if err := db.Model(&target).Update("token_version", uint(5)).Error; err != nil {
		t.Fatal(err)
	}

	actor := nonSuperadminActor(1, "usuarios_central.change_role", "empresas.view")
	updated, gotRole, err := svc.ChangeRole(actor, target.ID, role.ID)
	if err != nil {
		t.Fatalf("ChangeRole: %v", err)
	}
	if updated.RoleID == nil || *updated.RoleID != role.ID {
		t.Fatalf("RoleID = %v, want %d", updated.RoleID, role.ID)
	}
	if gotRole.Name != "Soporte" {
		t.Fatalf("role name = %q, want Soporte", gotRole.Name)
	}
	if updated.TokenVersion != 6 {
		t.Fatalf("TokenVersion = %d, want 6 (5+1)", updated.TokenVersion)
	}
}

// §18 — caso obligatorio del spec: actor con roles.manage + usuarios_central.change_role +
// usuarios_central.view, SIN usuarios_central.reset_password, no puede asignar un rol que
// contenga usuarios_central.reset_password.
func TestSAUserService_ChangeRole_RejectsRoleActorCannotDelegate(t *testing.T) {
	db := setupSAUserServiceTestDB(t)
	svc := NewSAUserService(db)
	resetPerm := seedSAUserPermission(t, db, "usuarios_central", "reset_password")
	role := createSARoleWithPermissions(t, db, "Con reset_password", resetPerm.ID)
	target := createSAUserFixture(t, db, "t@example.com", "admin", true)

	actor := nonSuperadminActor(1, "roles.manage", "usuarios_central.change_role", "usuarios_central.view")
	_, _, err := svc.ChangeRole(actor, target.ID, role.ID)
	if !errors.Is(err, ErrSAUserCannotDelegate) {
		t.Fatalf("err = %v, want ErrSAUserCannotDelegate", err)
	}
	var reloaded database.SuperAdminUser
	db.First(&reloaded, target.ID)
	if reloaded.RoleID != nil {
		t.Fatal("RoleID no debió cambiar")
	}
}

// §9/§23 — self role change: un actor no puede asignarse a SÍ MISMO un rol que exceda lo que
// puede delegar. Sin caso especial en el código: el mismo techo de delegación aplica.
func TestSAUserService_ChangeRole_SelfCannotEscalate(t *testing.T) {
	db := setupSAUserServiceTestDB(t)
	svc := NewSAUserService(db)
	resetPerm := seedSAUserPermission(t, db, "usuarios_central", "reset_password")
	role := createSARoleWithPermissions(t, db, "Con reset_password", resetPerm.ID)
	self := createSAUserFixture(t, db, "self@example.com", "admin", true)

	actor := nonSuperadminActor(self.ID, "usuarios_central.change_role")
	_, _, err := svc.ChangeRole(actor, self.ID, role.ID)
	if !errors.Is(err, ErrSAUserCannotDelegate) {
		t.Fatalf("err = %v, want ErrSAUserCannotDelegate (self-escalation)", err)
	}
}

func TestSAUserService_ChangeRole_TargetNotFound(t *testing.T) {
	db := setupSAUserServiceTestDB(t)
	svc := NewSAUserService(db)
	viewPerm := seedSAUserPermission(t, db, "empresas", "view")
	role := createSARoleWithPermissions(t, db, "Soporte", viewPerm.ID)

	_, _, err := svc.ChangeRole(superadminActor(1), 99999, role.ID)
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("err = %v, want gorm.ErrRecordNotFound", err)
	}
}

// Segunda barrera independiente (defensa en profundidad, §28): incluso si un rol YA contiene
// (por la razón que sea, seed directo aquí) un permiso que el actor no posee, ChangeRole lo
// bloquea igual — no depende de que SetRolePermissionsAPI haya sido la única puerta.
func TestSAUserService_ChangeRole_SecondBarrier_BlocksEvenIfRoleAlreadyExceedsCeiling(t *testing.T) {
	db := setupSAUserServiceTestDB(t)
	svc := NewSAUserService(db)
	changeRolePerm := seedSAUserPermission(t, db, "usuarios_central", "change_role")
	destroyPerm := seedSAUserPermission(t, db, "usuarios_central", "destroy")
	// Rol creado directamente en BD con MÁS permisos de los que el actor de abajo posee —
	// simula que, por cualquier vía, el rol ya excede el techo del actor.
	role := createSARoleWithPermissions(t, db, "Excedido", changeRolePerm.ID, destroyPerm.ID)
	target := createSAUserFixture(t, db, "t@example.com", "admin", true)

	actor := nonSuperadminActor(1, "usuarios_central.change_role") // NO tiene usuarios_central.destroy
	_, _, err := svc.ChangeRole(actor, target.ID, role.ID)
	if !errors.Is(err, ErrSAUserCannotDelegate) {
		t.Fatalf("err = %v, want ErrSAUserCannotDelegate — la segunda barrera debió bloquear igual", err)
	}
}

// ==================== ChangeSystemRole (admin ↔ superadmin) ====================

func TestSAUserService_ChangeSystemRole_RejectsNonSuperadminActor(t *testing.T) {
	db := setupSAUserServiceTestDB(t)
	svc := NewSAUserService(db)
	target := createSAUserFixture(t, db, "t@example.com", "admin", true)

	// Actor con TODOS los permisos granulares imaginables, pero Role != "superadmin".
	actor := nonSuperadminActor(1, "*")
	_, err := svc.ChangeSystemRole(actor, target.ID, "superadmin")
	if !errors.Is(err, ErrSAUserNotSuperadmin) {
		t.Fatalf("err = %v, want ErrSAUserNotSuperadmin", err)
	}
	var reloaded database.SuperAdminUser
	db.First(&reloaded, target.ID)
	if reloaded.Role != "admin" {
		t.Fatal("Role no debió cambiar")
	}
}

func TestSAUserService_ChangeSystemRole_RejectsSelfPromotion(t *testing.T) {
	db := setupSAUserServiceTestDB(t)
	svc := NewSAUserService(db)
	self := createSAUserFixture(t, db, "self@example.com", "admin", true)

	actor := nonSuperadminActor(self.ID, "*")
	if _, err := svc.ChangeSystemRole(actor, self.ID, "superadmin"); !errors.Is(err, ErrSAUserNotSuperadmin) {
		t.Fatalf("err = %v, want ErrSAUserNotSuperadmin (self → system-role superadmin imposible para admin)", err)
	}
}

func TestSAUserService_ChangeSystemRole_PromoteAdminToSuperadmin(t *testing.T) {
	db := setupSAUserServiceTestDB(t)
	svc := NewSAUserService(db)
	createSAUserFixture(t, db, "root@example.com", "superadmin", true) // asegura que no es "el último" de forma trivial
	target := createSAUserFixture(t, db, "t@example.com", "admin", true)

	updated, err := svc.ChangeSystemRole(superadminActor(1), target.ID, "superadmin")
	if err != nil {
		t.Fatalf("ChangeSystemRole: %v", err)
	}
	if updated.Role != "superadmin" {
		t.Fatalf("Role = %q, want superadmin", updated.Role)
	}
}

func TestSAUserService_ChangeSystemRole_DemoteWithAnotherSuperadminRemaining(t *testing.T) {
	db := setupSAUserServiceTestDB(t)
	svc := NewSAUserService(db)
	other := createSAUserFixture(t, db, "other@example.com", "superadmin", true)
	target := createSAUserFixture(t, db, "t@example.com", "superadmin", true)

	updated, err := svc.ChangeSystemRole(superadminActor(other.ID), target.ID, "admin")
	if err != nil {
		t.Fatalf("ChangeSystemRole: %v", err)
	}
	if updated.Role != "admin" {
		t.Fatalf("Role = %q, want admin", updated.Role)
	}
}

// §11/§17 — no degradar al último superadmin activo.
func TestSAUserService_ChangeSystemRole_RejectsDemotingLastSuperadmin(t *testing.T) {
	db := setupSAUserServiceTestDB(t)
	svc := NewSAUserService(db)
	last := createSAUserFixture(t, db, "last@example.com", "superadmin", true)

	_, err := svc.ChangeSystemRole(superadminActor(last.ID), last.ID, "admin")
	if !errors.Is(err, ErrSAUserLastSuperadmin) {
		t.Fatalf("err = %v, want ErrSAUserLastSuperadmin", err)
	}
	var reloaded database.SuperAdminUser
	db.First(&reloaded, last.ID)
	if reloaded.Role != "superadmin" {
		t.Fatal("Role no debió cambiar")
	}
}

// Un superadmin ya inactivo (Active=false) no cuenta como "operativo" — degradarlo no reduce el
// conteo de superadmins ACTIVOS, así que no debería bloquearse por esta regla (aunque en la
// práctica un superadmin inactivo ya no puede autenticarse de todas formas).
func TestSAUserService_ChangeSystemRole_InactiveSuperadminDoesNotCountTowardLastCheck(t *testing.T) {
	db := setupSAUserServiceTestDB(t)
	svc := NewSAUserService(db)
	active := createSAUserFixture(t, db, "active@example.com", "superadmin", true)
	createSAUserFixture(t, db, "inactive@example.com", "superadmin", false)

	if _, err := svc.ChangeSystemRole(superadminActor(active.ID), active.ID, "admin"); !errors.Is(err, ErrSAUserLastSuperadmin) {
		t.Fatalf("err = %v, want ErrSAUserLastSuperadmin (el inactivo no cuenta como operativo)", err)
	}
}

func TestSAUserService_ChangeSystemRole_InvalidRoleValue(t *testing.T) {
	db := setupSAUserServiceTestDB(t)
	svc := NewSAUserService(db)
	target := createSAUserFixture(t, db, "t@example.com", "admin", true)

	if _, err := svc.ChangeSystemRole(superadminActor(1), target.ID, "root"); !errors.Is(err, ErrSAUserInvalidSystemRole) {
		t.Fatalf("err = %v, want ErrSAUserInvalidSystemRole", err)
	}
}

func TestSAUserService_ChangeSystemRole_NoOpDoesNotIncrementTokenVersion(t *testing.T) {
	db := setupSAUserServiceTestDB(t)
	svc := NewSAUserService(db)
	target := createSAUserFixture(t, db, "t@example.com", "admin", true)
	if err := db.Model(&target).Update("token_version", uint(9)).Error; err != nil {
		t.Fatal(err)
	}

	updated, err := svc.ChangeSystemRole(superadminActor(1), target.ID, "admin") // ya es admin
	if err != nil {
		t.Fatalf("ChangeSystemRole: %v", err)
	}
	if updated.TokenVersion != 9 {
		t.Fatalf("TokenVersion = %d, want 9 (sin cambios, no-op)", updated.TokenVersion)
	}
}

// ==================== ChangeStatus (Active) — cuenta protegida ====================

func TestSAUserService_ChangeStatus_ProtectedAccount_NonSuperadminActorCannotDeactivateSuperadmin(t *testing.T) {
	db := setupSAUserServiceTestDB(t)
	svc := NewSAUserService(db)
	createSAUserFixture(t, db, "other@example.com", "superadmin", true)
	target := createSAUserFixture(t, db, "sa@example.com", "superadmin", true)

	actor := nonSuperadminActor(1, "usuarios_central.change_status")
	_, err := svc.ChangeStatus(actor, target.ID, false)
	if !errors.Is(err, ErrSAUserProtectedAccount) {
		t.Fatalf("err = %v, want ErrSAUserProtectedAccount", err)
	}
	var reloaded database.SuperAdminUser
	db.First(&reloaded, target.ID)
	if !reloaded.Active {
		t.Fatal("Active no debió cambiar")
	}
	if reloaded.TokenVersion != 0 {
		t.Fatal("TokenVersion no debió incrementarse en una operación rechazada")
	}
}

// Extensión deliberada del diseño (aprobada): tampoco puede REACTIVAR una cuenta superadmin.
func TestSAUserService_ChangeStatus_ProtectedAccount_NonSuperadminActorCannotReactivateSuperadmin(t *testing.T) {
	db := setupSAUserServiceTestDB(t)
	svc := NewSAUserService(db)
	target := createSAUserFixture(t, db, "sa@example.com", "superadmin", false)

	actor := nonSuperadminActor(1, "usuarios_central.change_status")
	if _, err := svc.ChangeStatus(actor, target.ID, true); !errors.Is(err, ErrSAUserProtectedAccount) {
		t.Fatalf("err = %v, want ErrSAUserProtectedAccount", err)
	}
}

func TestSAUserService_ChangeStatus_SuperadminActorCanDeactivateSuperadmin(t *testing.T) {
	db := setupSAUserServiceTestDB(t)
	svc := NewSAUserService(db)
	other := createSAUserFixture(t, db, "other@example.com", "superadmin", true)
	target := createSAUserFixture(t, db, "sa@example.com", "superadmin", true)

	updated, err := svc.ChangeStatus(superadminActor(other.ID), target.ID, false)
	if err != nil {
		t.Fatalf("ChangeStatus: %v", err)
	}
	if updated.Active {
		t.Fatal("Active debió quedar en false")
	}
	if updated.TokenVersion != 1 {
		t.Fatalf("TokenVersion = %d, want 1", updated.TokenVersion)
	}
}

// §11/§17 — no desactivar al último superadmin activo, ni siquiera hecho por otro superadmin.
func TestSAUserService_ChangeStatus_RejectsDeactivatingLastSuperadmin(t *testing.T) {
	db := setupSAUserServiceTestDB(t)
	svc := NewSAUserService(db)
	last := createSAUserFixture(t, db, "last@example.com", "superadmin", true)

	_, err := svc.ChangeStatus(superadminActor(last.ID), last.ID, false)
	if !errors.Is(err, ErrSAUserLastSuperadmin) {
		t.Fatalf("err = %v, want ErrSAUserLastSuperadmin", err)
	}
}

func TestSAUserService_ChangeStatus_NonSuperadminTargetNoProtection(t *testing.T) {
	db := setupSAUserServiceTestDB(t)
	svc := NewSAUserService(db)
	target := createSAUserFixture(t, db, "admin@example.com", "admin", true)

	actor := nonSuperadminActor(1, "usuarios_central.change_status")
	updated, err := svc.ChangeStatus(actor, target.ID, false)
	if err != nil {
		t.Fatalf("ChangeStatus: %v", err)
	}
	if updated.Active {
		t.Fatal("Active debió quedar en false")
	}
}

// ==================== ResetPassword — cuenta protegida ====================

func TestSAUserService_ResetPassword_ProtectedAccount(t *testing.T) {
	db := setupSAUserServiceTestDB(t)
	svc := NewSAUserService(db)
	target := createSAUserFixture(t, db, "sa@example.com", "superadmin", true)
	originalHash := target.Password

	actor := nonSuperadminActor(1, "usuarios_central.reset_password")
	_, err := svc.ResetPassword(actor, target.ID, "newpassword123")
	if !errors.Is(err, ErrSAUserProtectedAccount) {
		t.Fatalf("err = %v, want ErrSAUserProtectedAccount", err)
	}
	var reloaded database.SuperAdminUser
	db.First(&reloaded, target.ID)
	if reloaded.Password != originalHash {
		t.Fatal("la contraseña no debió cambiar")
	}
	if reloaded.TokenVersion != 0 {
		t.Fatal("TokenVersion no debió incrementarse")
	}
}

func TestSAUserService_ResetPassword_SuperadminActorCanResetSuperadminPassword(t *testing.T) {
	db := setupSAUserServiceTestDB(t)
	svc := NewSAUserService(db)
	other := createSAUserFixture(t, db, "other@example.com", "superadmin", true)
	target := createSAUserFixture(t, db, "sa@example.com", "superadmin", true)

	updated, err := svc.ResetPassword(superadminActor(other.ID), target.ID, "newpassword123")
	if err != nil {
		t.Fatalf("ResetPassword: %v", err)
	}
	if !updated.CheckPassword("newpassword123") {
		t.Fatal("la contraseña nueva no quedó aplicada")
	}
}

func TestSAUserService_ResetPassword_ValidatesLength(t *testing.T) {
	db := setupSAUserServiceTestDB(t)
	svc := NewSAUserService(db)
	target := createSAUserFixture(t, db, "t@example.com", "admin", true)

	actor := nonSuperadminActor(1, "usuarios_central.reset_password")
	if _, err := svc.ResetPassword(actor, target.ID, "short"); !errors.Is(err, ErrSAUserPasswordTooShort) {
		t.Fatalf("err = %v, want ErrSAUserPasswordTooShort", err)
	}
}

// ==================== Destroy — cuenta protegida + último superadmin ====================

func TestSAUserService_Destroy_ProtectedAccount(t *testing.T) {
	db := setupSAUserServiceTestDB(t)
	svc := NewSAUserService(db)
	target := createSAUserFixture(t, db, "sa@example.com", "superadmin", true)

	actor := nonSuperadminActor(1, "usuarios_central.destroy")
	_, err := svc.Destroy(actor, target.ID)
	if !errors.Is(err, ErrSAUserProtectedAccount) {
		t.Fatalf("err = %v, want ErrSAUserProtectedAccount", err)
	}
	var count int64
	db.Model(&database.SuperAdminUser{}).Where("id = ?", target.ID).Count(&count)
	if count != 1 {
		t.Fatal("el usuario no debió eliminarse")
	}
}

func TestSAUserService_Destroy_SuperadminActorCanDeleteSuperadmin(t *testing.T) {
	db := setupSAUserServiceTestDB(t)
	svc := NewSAUserService(db)
	other := createSAUserFixture(t, db, "other@example.com", "superadmin", true)
	target := createSAUserFixture(t, db, "sa@example.com", "superadmin", true)

	if _, err := svc.Destroy(superadminActor(other.ID), target.ID); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	if _, err := svc.GetByID(target.ID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("GetByID tras destroy = %v, want gorm.ErrRecordNotFound (soft-delete)", err)
	}
	// Soft-delete: la fila sigue existiendo físicamente (Unscoped).
	var count int64
	db.Unscoped().Model(&database.SuperAdminUser{}).Where("id = ?", target.ID).Count(&count)
	if count != 1 {
		t.Fatal("Destroy debe ser soft-delete: la fila debería seguir existiendo con Unscoped()")
	}
}

// §11/§17 — no eliminar al último superadmin activo.
func TestSAUserService_Destroy_RejectsDeletingLastSuperadmin(t *testing.T) {
	db := setupSAUserServiceTestDB(t)
	svc := NewSAUserService(db)
	last := createSAUserFixture(t, db, "last@example.com", "superadmin", true)

	_, err := svc.Destroy(superadminActor(last.ID), last.ID)
	if !errors.Is(err, ErrSAUserLastSuperadmin) {
		t.Fatalf("err = %v, want ErrSAUserLastSuperadmin", err)
	}
	var count int64
	db.Model(&database.SuperAdminUser{}).Where("id = ?", last.ID).Count(&count)
	if count != 1 {
		t.Fatal("no debió eliminarse")
	}
}

func TestSAUserService_Destroy_NonSuperadminTargetNoProtection(t *testing.T) {
	db := setupSAUserServiceTestDB(t)
	svc := NewSAUserService(db)
	target := createSAUserFixture(t, db, "admin@example.com", "admin", true)

	actor := nonSuperadminActor(1, "usuarios_central.destroy")
	if _, err := svc.Destroy(actor, target.ID); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
}

// ==================== Último superadmin bajo concurrencia (§17, §28) ====================
//
// Documentación honesta (§28): estos tests corren contra glebarez/sqlite (:memory:, WAL,
// _busy_timeout=15000, pool limitado a 1 conexión física — ver setupSAUserServiceConcurrencyDB).
// SQLite NO tiene locking por fila como MySQL/InnoDB — clause.Locking{Strength:"UPDATE"} se
// traduce a "SELECT ... FOR UPDATE", que el dialector sqlite de GORM simplemente omite de la
// consulta (no es un error, tampoco un lock real por fila). Lo que SÍ serializa correctamente
// estas dos transacciones en el test es: (a) el pool limitado a 1 conexión física obliga a que
// una transacción espere su turno para siquiera empezar a ejecutar SQL mientras la otra tiene la
// conexión, y (b) WAL + busy_timeout hacen que esa espera sea un bloqueo ordenado, no un error
// inmediato "database is locked". El resultado observable (que es lo que estos tests verifican)
// es el mismo que produciría un lock real: la segunda transacción en efectivamente ejecutar SIEMPRE
// ve el estado ya committeado por la primera. En PRODUCCIÓN (MySQL/InnoDB, ver
// pkg/saas/payments.go para el mismo idiom ya en uso) clause.Locking{Strength:"UPDATE"} SÍ genera
// un lock de fila real (SELECT ... FOR UPDATE real) — el mecanismo que realmente sostiene la
// invariante ahí es ese, no el límite de pool que este test usa para poder observarlo con SQLite.

func TestSAUserService_LastSuperadmin_ConcurrentDeactivate_NeverReachesZero(t *testing.T) {
	db := setupSAUserServiceConcurrencyDB(t)
	svc := NewSAUserService(db)
	sa1 := createSAUserFixture(t, db, "sa1@example.com", "superadmin", true)
	sa2 := createSAUserFixture(t, db, "sa2@example.com", "superadmin", true)
	actor := superadminActor(sa1.ID)

	errs := make([]error, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _, errs[0] = svc.ChangeStatus(actor, sa1.ID, false) }()
	go func() { defer wg.Done(); _, errs[1] = svc.ChangeStatus(actor, sa2.ID, false) }()
	wg.Wait()

	assertExactlyOneSucceededAndOneActiveSuperadminRemains(t, db, errs, ErrSAUserLastSuperadmin)
}

func TestSAUserService_LastSuperadmin_ConcurrentDemote_NeverReachesZero(t *testing.T) {
	db := setupSAUserServiceConcurrencyDB(t)
	svc := NewSAUserService(db)
	sa1 := createSAUserFixture(t, db, "sa1@example.com", "superadmin", true)
	sa2 := createSAUserFixture(t, db, "sa2@example.com", "superadmin", true)
	actor := superadminActor(sa1.ID)

	errs := make([]error, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _, errs[0] = svc.ChangeSystemRole(actor, sa1.ID, "admin") }()
	go func() { defer wg.Done(); _, errs[1] = svc.ChangeSystemRole(actor, sa2.ID, "admin") }()
	wg.Wait()

	assertExactlyOneSucceededAndOneActiveSuperadminRemains(t, db, errs, ErrSAUserLastSuperadmin)
}

func TestSAUserService_LastSuperadmin_ConcurrentDestroy_NeverReachesZero(t *testing.T) {
	db := setupSAUserServiceConcurrencyDB(t)
	svc := NewSAUserService(db)
	sa1 := createSAUserFixture(t, db, "sa1@example.com", "superadmin", true)
	sa2 := createSAUserFixture(t, db, "sa2@example.com", "superadmin", true)
	actor := superadminActor(sa1.ID)

	errs := make([]error, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _, errs[0] = svc.Destroy(actor, sa1.ID) }()
	go func() { defer wg.Done(); _, errs[1] = svc.Destroy(actor, sa2.ID) }()
	wg.Wait()

	assertExactlyOneSucceededAndOneActiveSuperadminRemains(t, db, errs, ErrSAUserLastSuperadmin)
}

func assertExactlyOneSucceededAndOneActiveSuperadminRemains(t *testing.T, db *gorm.DB, errs []error, wantErr error) {
	t.Helper()
	successes, rejections := 0, 0
	for _, e := range errs {
		switch {
		case e == nil:
			successes++
		case errors.Is(e, wantErr):
			rejections++
		default:
			t.Fatalf("error inesperado: %v", e)
		}
	}
	if successes != 1 || rejections != 1 {
		t.Fatalf("successes=%d rejections=%d, want 1 y 1 — la invariante permitió 0 o 2 operaciones", successes, rejections)
	}
	var activeCount int64
	db.Model(&database.SuperAdminUser{}).
		Where("role = ? AND active = ? AND deleted_at IS NULL", "superadmin", true).
		Count(&activeCount)
	if activeCount != 1 {
		t.Fatalf("quedaron %d superadmins activos, want exactamente 1 (nunca 0)", activeCount)
	}
}
