package superadmin

// Fase 10 — QA integral del RBAC central: escenario end-to-end con 5 personas REALES (login real
// contra LoginAPI, JWT real con permisos resueltos exactamente como en producción vía
// saPermissionsForUser) contra el árbol de rutas REAL (RegisterRoutes) — nunca claims
// hand-crafted. El valor específico de este archivo frente a todo lo ya probado en Grupos 1-7 es
// cerrar el loop: la lógica de permisos ya está probada en unidad (pkg/middleware,
// internal/superadmin/service) — esto prueba que el SISTEMA DESPLEGADO decide exactamente igual,
// reutilizando las tablas protectedRoutes* ya construidas en cada grupo anterior (nunca
// reinventadas) y cruzándolas contra las 5 personas.
//
// NO se conecta a producción, NO se conecta al VPS, NO se ejecuta RunUserRoleMigration. Todo
// corre contra SQLite en memoria, igual que el resto de esta suite.

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"tukifac/config"
	"tukifac/internal/superadmin/service"
	"tukifac/pkg/database"
	"tukifac/pkg/middleware"

	"github.com/glebarez/sqlite"
	"github.com/gofiber/fiber/v3"
	"github.com/golang-jwt/jwt/v5"
	"gorm.io/gorm"
)

// parseFase10Token decodifica (CON verificación de firma, usando el secreto real que este mismo
// test configuró) el JWT recién emitido por el login real — para poder leer sus claims (permisos
// resueltos de verdad) sin necesitar ningún endpoint /me ni exportar nada nuevo del backend solo
// para pruebas.
func parseFase10Token(t *testing.T, token string) *middleware.SuperAdminClaims {
	t.Helper()
	claims := &middleware.SuperAdminClaims{}
	parsed, err := jwt.ParseWithClaims(token, claims, func(*jwt.Token) (interface{}, error) {
		return []byte(fase10JWTSecret), nil
	})
	if err != nil || !parsed.Valid {
		t.Fatalf("no se pudo parsear/verificar el JWT: %v", err)
	}
	return claims
}

const fase10JWTSecret = "fase10-qa-secret"

type qaPersona struct {
	name   string
	userID uint
	email  string
	token  string
	claims *middleware.SuperAdminClaims
}

// setupFase10QA construye el escenario completo: BD SQLite fresca, catálogo REAL sembrado
// (database.SASeedRolesAndPermissions — el mismo que corre en producción), un rol personalizado
// adicional para anti-escalamiento, los 5 usuarios pedidos, y los deja logueados de verdad
// (LoginAPI real) para que sus JWT tengan permisos resueltos genuinamente, no inventados.
func setupFase10QA(t *testing.T) (*fiber.App, *gorm.DB, map[string]*qaPersona) {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared&_journal_mode=WAL&_busy_timeout=15000", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&database.SuperAdminUser{}, &database.SARole{}, &database.SAPermission{}, &database.SARolePermission{},
		&database.AuditLog{}, &database.SAUserRoleMigrationBackup{}, &database.SAMigrationLock{},
	); err != nil {
		t.Fatal(err)
	}
	if err := database.SASeedRolesAndPermissions(db); err != nil {
		t.Fatal(err)
	}

	prevDB := database.CentralDB
	database.CentralDB = db
	t.Cleanup(func() { database.CentralDB = prevDB })
	prevCfg := config.AppConfig
	config.AppConfig = &config.Config{AppEnv: "development", SAJWTSecret: fase10JWTSecret}
	t.Cleanup(func() { config.AppConfig = prevCfg })

	roleID := func(name string) uint {
		var r database.SARole
		if err := db.Where("name = ?", name).First(&r).Error; err != nil {
			t.Fatalf("rol %q no encontrado en el seed real: %v", name, err)
		}
		return r.ID
	}
	permID := func(module, action string) uint {
		var p database.SAPermission
		if err := db.Where("module = ? AND action = ?", module, action).First(&p).Error; err != nil {
			t.Fatalf("permiso %s.%s no encontrado en el catálogo real: %v", module, action, err)
		}
		return p.ID
	}

	adminRoleID := roleID("Admin")
	soporteRoleID := roleID("Soporte")
	finanzasRoleID := roleID("Finanzas")

	// Rol personalizado para anti-escalamiento (§7): tiene roles.manage + usuarios_central.view +
	// usuarios_central.change_role (así SÍ alcanza la capa de ruta de PUT /users/:id/role y de
	// las rutas de roles), pero NUNCA usuarios_central.destroy/reset_password/change_status — el
	// techo de delegación (CanDelegateAll) debe seguir bloqueándolo con esos.
	customRole := database.SARole{Name: "QA Personalizado"}
	if err := db.Create(&customRole).Error; err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"roles.manage", "usuarios_central.view", "usuarios_central.change_role"} {
		mod, act, _ := splitDotFase10(key)
		if err := db.Create(&database.SARolePermission{RoleID: customRole.ID, PermissionID: permID(mod, act)}).Error; err != nil {
			t.Fatal(err)
		}
	}

	// "Rol B" del escenario de anti-escalamiento: deliberadamente EXCEDE lo que el rol
	// personalizado de arriba puede delegar (incluye destroy/reset_password).
	superiorRole := database.SARole{Name: "QA Superior"}
	if err := db.Create(&superiorRole).Error; err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"usuarios_central.change_role", "usuarios_central.destroy", "usuarios_central.reset_password"} {
		mod, act, _ := splitDotFase10(key)
		if err := db.Create(&database.SARolePermission{RoleID: superiorRole.ID, PermissionID: permID(mod, act)}).Error; err != nil {
			t.Fatal(err)
		}
	}

	create := func(email, role string, rid *uint) database.SuperAdminUser {
		u := database.SuperAdminUser{Name: email, Email: email, Role: role, RoleID: rid, Active: true}
		if err := u.SetPassword("password123"); err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&u).Error; err != nil {
			t.Fatal(err)
		}
		return u
	}

	superadminUser := create("superadmin@fase10.qa", "superadmin", nil)
	adminUser := create("admin@fase10.qa", "admin", &adminRoleID)
	soporteUser := create("soporte@fase10.qa", "admin", &soporteRoleID)
	finanzasUser := create("finanzas@fase10.qa", "admin", &finanzasRoleID)
	customUser := create("custom@fase10.qa", "admin", &customRole.ID)

	app := fiber.New()
	RegisterRoutes(app) // el mismo que main.go — sin reimplementar nada

	login := func(email string) *qaPersona {
		body, _ := json.Marshal(map[string]string{"email": email, "password": "password123"})
		req := httptest.NewRequest("POST", "/api/superadmin/login", strings.NewReader(string(body)))
		req.Header.Set("Content-Type", "application/json")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatalf("login %s: %v", email, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != fiber.StatusOK {
			t.Fatalf("login %s: status = %d", email, resp.StatusCode)
		}
		var out struct {
			Token string `json:"token"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			t.Fatal(err)
		}
		claims := parseFase10Token(t, out.Token)
		return &qaPersona{token: out.Token, claims: claims}
	}

	personas := map[string]*qaPersona{}
	for key, u := range map[string]database.SuperAdminUser{
		"superadmin": superadminUser, "admin": adminUser, "soporte": soporteUser,
		"finanzas": finanzasUser, "custom": customUser,
	} {
		p := login(u.Email)
		p.name, p.userID, p.email = key, u.ID, u.Email
		personas[key] = p
	}

	return app, db, personas
}

func splitDotFase10(key string) (module, action string, ok bool) {
	for i := 0; i < len(key); i++ {
		if key[i] == '.' {
			return key[:i], key[i+1:], true
		}
	}
	return "", "", false
}

func fase10Request(t *testing.T, app *fiber.App, method, path, token, body string) int {
	t.Helper()
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader(`{}`)
	} else {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, reader)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	status, _ := safeTestRequest(t, app, req)
	return status
}

// ==================== §1/§3-§6: matriz real persona × ruta ====================
//
// Reutiliza TODAS las tablas protectedRoutes* ya construidas en los Grupos 1-7 (nunca
// reinventadas) — para cada persona, calcula lo que middleware.HasSAPermission predice a partir
// de sus permisos REALES (resueltos por el login real) y confirma que el sistema desplegado
// decide exactamente lo mismo.
func TestFase10_PermissionMatrix_AllPersonasAgainstAllKnownRoutes(t *testing.T) {
	app, _, personas := setupFase10QA(t)

	allRoutes := map[string][]protectedRoute{
		"Etapa2":           protectedRoutesEtapa2,
		"Grupo1":           protectedRoutesGrupo1,
		"Grupo2":           protectedRoutesGrupo2,
		"Grupo3":           protectedRoutesGrupo3,
		"Grupo4":           protectedRoutesGrupo4,
		"Grupo5Planes":     protectedRoutesGrupo5Planes,
		"Grupo5Documentos": protectedRoutesGrupo5Documentos,
		"Grupo7Roles":      protectedRoutesGrupo7Roles,
		"Grupo7Usuarios":   protectedRoutesGrupo7Usuarios,
	}

	// Esta matriz SOLO prueba la dirección "no autorizado → 403" — la única que es 100% libre de
	// efectos secundarios (RequireSAPermission corta la cadena ANTES de tocar cualquier handler,
	// así que un 403 nunca puede haber mutado nada). La dirección "autorizado → pasa" para rutas
	// de ESCRITURA involucra ejecutar handlers reales contra las 5 personas COMPARTIENDO una sola
	// BD — se comprobó en la práctica que esto es peligroso: un superadmin autorizado ejecutando
	// de verdad, p. ej., PUT /roles/1/permissions con body {} vacío BORRA los permisos reales del
	// rol "Admin" y por lo tanto invalida (TokenVersion) al usuario admin de este mismo escenario
	// antes de que le toque su turno — un efecto cascada real, no un bug de RBAC. Esa dirección ya
	// está probada exhaustivamente y de forma segura en los wiring tests dedicados de cada Grupo
	// (fixtures desechables por grupo, ver protectedRoutesGrupoN_Wiring en route_wiring_test.go);
	// aquí se complementa con un spot-check de solo-lectura (ver
	// TestFase10_PermissionMatrix_ReadOnlyRoutesNotBlockedWhenAuthorized, inherentemente segura).
	for personaName, persona := range personas {
		for groupName, routes := range allRoutes {
			t.Run(personaName+"/"+groupName, func(t *testing.T) {
				for _, r := range routes {
					if middleware.HasSAPermission(persona.claims, r.permission) {
						continue
					}
					status := fase10Request(t, app, r.method, r.path, persona.token, "")
					if status != fiber.StatusForbidden {
						t.Errorf("%s %s: %s NO tiene %q, quería 403, status=%d", r.method, r.path, personaName, r.permission, status)
					}
				}
			})
		}
	}
}

// Complemento seguro de la matriz de arriba: SOLO rutas GET (nunca mutan nada) — confirma la
// dirección "autorizado → no bloqueado" contra las 5 personas reales, sin ningún riesgo de efecto
// cascada entre ellas.
func TestFase10_PermissionMatrix_ReadOnlyRoutesNotBlockedWhenAuthorized(t *testing.T) {
	app, _, personas := setupFase10QA(t)

	allRoutes := [][]protectedRoute{
		protectedRoutesEtapa2, protectedRoutesGrupo1, protectedRoutesGrupo2, protectedRoutesGrupo3,
		protectedRoutesGrupo4, protectedRoutesGrupo5Planes, protectedRoutesGrupo5Documentos,
		protectedRoutesGrupo7Roles, protectedRoutesGrupo7Usuarios,
	}

	for personaName, persona := range personas {
		t.Run(personaName, func(t *testing.T) {
			for _, group := range allRoutes {
				for _, r := range group {
					if r.method != "GET" || !middleware.HasSAPermission(persona.claims, r.permission) {
						continue
					}
					status := fase10Request(t, app, r.method, r.path, persona.token, "")
					if status == fiber.StatusUnauthorized || status == fiber.StatusForbidden {
						t.Errorf("%s %s: %s tiene %q pero status=%d", r.method, r.path, personaName, r.permission, status)
					}
				}
			}
		})
	}
}

// ==================== §3: superadmin — operaciones exclusivas ====================

func TestFase10_Superadmin_ExclusiveOperations_NotBlockedByAuthz(t *testing.T) {
	app, _, personas := setupFase10QA(t)
	sa := personas["superadmin"]

	// destroy-complete queda deliberadamente FUERA de esta lista: además de RequireSuperAdminOnly
	// exige una operations-key correcta por header (segunda capa, ajena al RBAC — ver Grupo 1),
	// así que un superadmin SIN esa key también recibe 403 ahí, correctamente. Esa combinación ya
	// tiene su propia cobertura dedicada en TestGrupo1_DestroyComplete_SuperadminNotBlockedByAuthz
	// (route_wiring_test.go), que sí configura la key — no se duplica aquí.
	exclusive := []struct{ method, path string }{
		{"PATCH", "/api/superadmin/tenants/1/master-access"},
		{"PUT", "/api/superadmin/saas-settings/operations-key"},
		{"PUT", "/api/superadmin/users/2/system-role"},
	}
	for _, r := range exclusive {
		t.Run(r.method+" "+r.path, func(t *testing.T) {
			status := fase10Request(t, app, r.method, r.path, sa.token, `{"role":"admin"}`)
			if status == fiber.StatusUnauthorized || status == fiber.StatusForbidden {
				t.Errorf("superadmin real no debería ser bloqueado: status=%d", status)
			}
		})
	}
}

func TestFase10_Admin_CannotReachSuperadminExclusiveOperations(t *testing.T) {
	app, _, personas := setupFase10QA(t)
	admin := personas["admin"]

	exclusive := []struct{ method, path string }{
		{"PUT", "/api/superadmin/saas-settings/operations-key"},
		{"POST", "/api/superadmin/tenants/1/destroy-complete"},
		{"PUT", "/api/superadmin/users/2/system-role"},
	}
	for _, r := range exclusive {
		t.Run(r.method+" "+r.path, func(t *testing.T) {
			status := fase10Request(t, app, r.method, r.path, admin.token, `{"role":"superadmin"}`)
			if status != fiber.StatusForbidden {
				t.Errorf("status = %d, want 403 (Admin nunca alcanza esto, ni con todos sus permisos granulares)", status)
			}
		})
	}
}

// ==================== §6: Finanzas — límites finos dentro de un mismo módulo ====================

func TestFase10_Finanzas_PagosBoundaries(t *testing.T) {
	_, _, personas := setupFase10QA(t)
	finanzas := personas["finanzas"]

	for _, key := range []string{"pagos.view", "pagos.approve", "pagos.reject", "pagos.refund",
		"suscripciones.view", "suscripciones.update", "planes.view", "documentos.view", "documentos.approve_purchase"} {
		if !middleware.HasSAPermission(finanzas.claims, key) {
			t.Errorf("Finanzas debería tener %q según SADefaultRoles", key)
		}
	}
	// pagos.view NO implica approve/reject/refund (no hay ningún ".manage" en el módulo pagos, ver
	// saManageImpliedActions) — y Finanzas tampoco tiene ningún módulo fuera de su lista.
	deniedButRelated := []string{"empresas.view", "fiscal.view", "migraciones.view", "roles.view", "usuarios_central.view"}
	for _, key := range deniedButRelated {
		if middleware.HasSAPermission(finanzas.claims, key) {
			t.Errorf("Finanzas NO debería tener %q", key)
		}
	}
}

// Límites finos EXACTOS pedidos en §6: cada uno de pagos.view/approve/reject/refund y
// documentos.view en aislamiento (nadie que solo tenga uno debe alcanzar los demás) — Finanzas
// tiene los 4 juntos, así que no basta para probar esta independencia; se usan tokens con
// exactamente un permiso a la vez (mismo mecanismo que route_wiring_test.go).
func TestFase10_PagosDocumentos_ExactPermissionBoundaries(t *testing.T) {
	app, userID := setupRouteWiringApp(t)

	cases := []struct {
		granted string
		method  string
		path    string
		denied  bool
	}{
		{"pagos.view", "PATCH", "/api/superadmin/payments/1/approve", true},
		{"pagos.view", "PATCH", "/api/superadmin/payments/1/reject", true},
		{"pagos.approve", "PATCH", "/api/superadmin/payments/1/reject", true},
		{"pagos.reject", "PATCH", "/api/superadmin/payments/1/approve", true},
		{"documentos.view", "PATCH", "/api/superadmin/document-packages/purchases/1/approve", true},
	}
	for _, c := range cases {
		t.Run(c.granted+" -> "+c.method+" "+c.path, func(t *testing.T) {
			token := mintWiringToken(t, userID, []string{c.granted})
			req := httptest.NewRequest(c.method, c.path, strings.NewReader(`{}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+token)
			status, _ := safeTestRequest(t, app, req)
			if c.denied && status != fiber.StatusForbidden {
				t.Errorf("status = %d, want 403 (%q no debe alcanzar %s %s)", status, c.granted, c.method, c.path)
			}
		})
	}
}

func TestFase10_Soporte_OnlyReadPermissions(t *testing.T) {
	_, _, personas := setupFase10QA(t)
	soporte := personas["soporte"]

	for _, key := range []string{"dashboard.view", "empresas.view", "fiscal.view", "migraciones.view"} {
		if !middleware.HasSAPermission(soporte.claims, key) {
			t.Errorf("Soporte debería tener %q", key)
		}
	}
	for _, key := range []string{
		"pagos.approve", "suscripciones.update", "planes.create", "roles.view",
		"usuarios_central.update", "migraciones.run", "empresas.master_access",
	} {
		if middleware.HasSAPermission(soporte.claims, key) {
			t.Errorf("Soporte NUNCA debería tener %q", key)
		}
	}
}

// ==================== §7: anti-escalamiento (OBLIGATORIO) ====================

func TestFase10_AntiEscalation_CustomActorCannotSelfAssignSuperiorRole(t *testing.T) {
	app, db, personas := setupFase10QA(t)
	custom := personas["custom"]
	var superior database.SARole
	db.Where("name = ?", "QA Superior").First(&superior)

	status := fase10Request(t, app, "PUT", fmt.Sprintf("/api/superadmin/users/%d/role", custom.userID), custom.token,
		fmt.Sprintf(`{"role_id":%d}`, superior.ID))
	if status != fiber.StatusForbidden {
		t.Fatalf("status = %d, want 403 (no puede auto-asignarse un rol que excede su techo)", status)
	}
	var reloaded database.SuperAdminUser
	db.First(&reloaded, custom.userID)
	if reloaded.RoleID == nil || *reloaded.RoleID == superior.ID {
		t.Fatal("el RoleID no debió cambiar a QA Superior")
	}
}

func TestFase10_AntiEscalation_CustomActorCannotAssignSuperiorRoleToOther(t *testing.T) {
	app, db, personas := setupFase10QA(t)
	custom, finanzas := personas["custom"], personas["finanzas"]
	var superior database.SARole
	db.Where("name = ?", "QA Superior").First(&superior)

	status := fase10Request(t, app, "PUT", fmt.Sprintf("/api/superadmin/users/%d/role", finanzas.userID), custom.token,
		fmt.Sprintf(`{"role_id":%d}`, superior.ID))
	if status != fiber.StatusForbidden {
		t.Fatalf("status = %d, want 403", status)
	}
	var reloaded database.SuperAdminUser
	db.First(&reloaded, finanzas.userID)
	if reloaded.RoleID != nil && *reloaded.RoleID == superior.ID {
		t.Fatal("finanzas no debió terminar con el rol superior")
	}
}

func TestFase10_AntiEscalation_CustomActorCannotAddSuperiorPermissionToAnyRole(t *testing.T) {
	app, db, personas := setupFase10QA(t)
	custom := personas["custom"]
	var destroyPerm database.SAPermission
	db.Where("module = ? AND action = ?", "usuarios_central", "destroy").First(&destroyPerm)

	targetRole := database.SARole{Name: "QA Objetivo Vacio"}
	db.Create(&targetRole)

	status := fase10Request(t, app, "PUT", fmt.Sprintf("/api/superadmin/roles/%d/permissions", targetRole.ID), custom.token,
		fmt.Sprintf(`{"permission_ids":[%d]}`, destroyPerm.ID))
	if status != fiber.StatusForbidden {
		t.Fatalf("status = %d, want 403 (no puede delegar usuarios_central.destroy)", status)
	}
	var count int64
	db.Model(&database.SARolePermission{}).Where("role_id = ?", targetRole.ID).Count(&count)
	if count != 0 {
		t.Fatal("el rol objetivo no debió recibir ningún permiso")
	}
}

func TestFase10_AntiEscalation_CustomActorCannotDeleteRoleOutsideCeiling(t *testing.T) {
	app, db, personas := setupFase10QA(t)
	custom := personas["custom"]
	var superior database.SARole
	db.Where("name = ?", "QA Superior").First(&superior)

	status := fase10Request(t, app, "DELETE", fmt.Sprintf("/api/superadmin/roles/%d", superior.ID), custom.token, "")
	if status != fiber.StatusForbidden {
		t.Fatalf("status = %d, want 403", status)
	}
	var count int64
	db.Model(&database.SARole{}).Where("id = ?", superior.ID).Count(&count)
	if count != 1 {
		t.Fatal("QA Superior no debió eliminarse")
	}
}

// Escenario textual exacto del spec (§7): el actor NO tiene usuarios_central.change_role en
// absoluto (a diferencia de "custom", que sí lo tiene pero le falta destroy/reset_password) — la
// barrera que actúa aquí es la de RUTA (RequireSAPermission), ni siquiera llega al techo de
// delegación. Soporte es el ejemplo real: no tiene change_role.
func TestFase10_AntiEscalation_ActorWithoutChangeRolePermission_RouteLevelBlocksBeforeCeiling(t *testing.T) {
	app, db, personas := setupFase10QA(t)
	soporte := personas["soporte"]
	if middleware.HasSAPermission(soporte.claims, "usuarios_central.change_role") {
		t.Fatal("precondición rota: Soporte no debería tener usuarios_central.change_role")
	}
	var superior database.SARole
	db.Where("name = ?", "QA Superior").First(&superior) // el rol destino SÍ tiene change_role

	status := fase10Request(t, app, "PUT", fmt.Sprintf("/api/superadmin/users/%d/role", soporte.userID), soporte.token,
		fmt.Sprintf(`{"role_id":%d}`, superior.ID))
	if status != fiber.StatusForbidden {
		t.Fatalf("status = %d, want 403 (bloqueado a nivel de ruta, sin permiso change_role)", status)
	}
}

// ==================== §8: cuentas superadmin protegidas ====================

func TestFase10_ProtectedSuperadminAccount_RejectedForNonSuperadminActors(t *testing.T) {
	app, _, personas := setupFase10QA(t)
	sa := personas["superadmin"]

	cases := []struct {
		personaKey string
		method     string
		pathFmt    string
		body       string
	}{
		{"admin", "POST", "/api/superadmin/users/%d/password", `{"new_password":"newpassword123"}`},
		{"admin", "PATCH", "/api/superadmin/users/%d/status", `{"active":false}`},
		{"admin", "DELETE", "/api/superadmin/users/%d", ""},
		{"finanzas", "PATCH", "/api/superadmin/users/%d/status", `{"active":false}`},
	}
	for _, c := range cases {
		actor := personas[c.personaKey]
		t.Run(c.personaKey+" "+c.method, func(t *testing.T) {
			status := fase10Request(t, app, c.method, fmt.Sprintf(c.pathFmt, sa.userID), actor.token, c.body)
			if status != fiber.StatusForbidden {
				t.Errorf("status = %d, want 403 (cuenta superadmin protegida)", status)
			}
		})
	}
}

func TestFase10_Superadmin_CanManageAnotherSuperadmin(t *testing.T) {
	app, db, personas := setupFase10QA(t)
	sa := personas["superadmin"]

	other := database.SuperAdminUser{Name: "otro-sa", Email: "otro-sa@fase10.qa", Role: "superadmin", Active: true}
	other.SetPassword("password123")
	db.Create(&other)

	status := fase10Request(t, app, "PATCH", fmt.Sprintf("/api/superadmin/users/%d/status", other.ID), sa.token, `{"active":false}`)
	if status == fiber.StatusUnauthorized || status == fiber.StatusForbidden {
		t.Fatalf("un superadmin real SÍ debe poder administrar a otro superadmin: status=%d", status)
	}
}

// ==================== §9: último superadmin + concurrencia (vía HTTP real) ====================

func TestFase10_LastSuperadmin_HTTPLevel(t *testing.T) {
	app, db, personas := setupFase10QA(t)
	sa1 := personas["superadmin"]
	sa2 := database.SuperAdminUser{Name: "sa2", Email: "sa2@fase10.qa", Role: "superadmin", Active: true}
	sa2.SetPassword("password123")
	db.Create(&sa2)

	// Con 2 activos: desactivar uno → permitido.
	status := fase10Request(t, app, "PATCH", fmt.Sprintf("/api/superadmin/users/%d/status", sa2.ID), sa1.token, `{"active":false}`)
	if status == fiber.StatusUnauthorized || status == fiber.StatusForbidden {
		t.Fatalf("desactivar un superadmin cuando queda otro activo debería permitirse: status=%d", status)
	}

	// Ahora solo sa1 queda activo — desactivarlo, demoverlo o eliminarlo debe rechazarse.
	statusDeactivate := fase10Request(t, app, "PATCH", fmt.Sprintf("/api/superadmin/users/%d/status", sa1.userID), sa1.token, `{"active":false}`)
	if statusDeactivate != fiber.StatusConflict {
		t.Errorf("desactivar al último superadmin: status = %d, want 409", statusDeactivate)
	}
	statusDemote := fase10Request(t, app, "PUT", fmt.Sprintf("/api/superadmin/users/%d/system-role", sa1.userID), sa1.token, `{"role":"admin"}`)
	if statusDemote != fiber.StatusConflict {
		t.Errorf("demover al último superadmin: status = %d, want 409", statusDemote)
	}
	statusDelete := fase10Request(t, app, "DELETE", fmt.Sprintf("/api/superadmin/users/%d", sa1.userID), sa1.token, "")
	if statusDelete != fiber.StatusConflict {
		t.Errorf("eliminar al último superadmin: status = %d, want 409", statusDelete)
	}
}

func TestFase10_LastSuperadmin_ConcurrentHTTPRequests_NeverReachesZero(t *testing.T) {
	app, db, personas := setupFase10QA(t)
	sa1 := personas["superadmin"]
	sa2 := database.SuperAdminUser{Name: "sa2", Email: "sa2-conc@fase10.qa", Role: "superadmin", Active: true}
	sa2.SetPassword("password123")
	db.Create(&sa2)

	sqlDB, _ := db.DB()
	sqlDB.SetMaxOpenConns(1)

	statuses := make([]int, 2)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		statuses[0] = fase10Request(t, app, "PATCH", fmt.Sprintf("/api/superadmin/users/%d/status", sa1.userID), sa1.token, `{"active":false}`)
	}()
	go func() {
		defer wg.Done()
		statuses[1] = fase10Request(t, app, "PATCH", fmt.Sprintf("/api/superadmin/users/%d/status", sa2.ID), sa1.token, `{"active":false}`)
	}()
	wg.Wait()

	successCount := 0
	for _, s := range statuses {
		if s == fiber.StatusOK {
			successCount++
		}
	}
	if successCount != 1 {
		t.Fatalf("exactamente una debería tener éxito, statuses=%v", statuses)
	}
	var activeCount int64
	db.Model(&database.SuperAdminUser{}).Where("role = ? AND active = ?", "superadmin", true).Count(&activeCount)
	if activeCount != 1 {
		t.Fatalf("superadmins activos tras la concurrencia = %d, want 1 (nunca 0)", activeCount)
	}
}

// ==================== §10: TokenVersion end-to-end (JWT viejo rechazado tras cada operación) ====================

func TestFase10_TokenVersion_RoleChangeInvalidatesOldJWT(t *testing.T) {
	app, db, personas := setupFase10QA(t)
	sa, custom := personas["superadmin"], personas["custom"]
	oldToken := custom.token

	if s := fase10Request(t, app, "GET", "/api/superadmin/users", oldToken, ""); s != fiber.StatusForbidden && s == fiber.StatusUnauthorized {
		t.Fatalf("token viejo debería seguir sirviendo ANTES del cambio: status=%d", s)
	}

	var soporteRole database.SARole
	db.Where("name = ?", "Soporte").First(&soporteRole)
	status := fase10Request(t, app, "PUT", fmt.Sprintf("/api/superadmin/users/%d/role", custom.userID), sa.token,
		fmt.Sprintf(`{"role_id":%d}`, soporteRole.ID))
	if status != fiber.StatusOK {
		t.Fatalf("cambio de rol por superadmin: status = %d, want 200", status)
	}

	if s := fase10Request(t, app, "GET", "/api/superadmin/dashboard", oldToken, ""); s != fiber.StatusUnauthorized {
		t.Errorf("el JWT viejo de custom debería quedar inválido tras el cambio de rol: status=%d, want 401", s)
	}
}

func TestFase10_TokenVersion_SystemRoleChangeInvalidatesOldJWT(t *testing.T) {
	app, _, personas := setupFase10QA(t)
	sa, admin := personas["superadmin"], personas["admin"]
	oldToken := admin.token

	status := fase10Request(t, app, "PUT", fmt.Sprintf("/api/superadmin/users/%d/system-role", admin.userID), sa.token, `{"role":"superadmin"}`)
	if status != fiber.StatusOK {
		t.Fatalf("promover a superadmin: status = %d, want 200", status)
	}
	if s := fase10Request(t, app, "GET", "/api/superadmin/dashboard", oldToken, ""); s != fiber.StatusUnauthorized {
		t.Errorf("el JWT viejo debería quedar inválido tras el cambio de system-role: status=%d, want 401", s)
	}
}

func TestFase10_TokenVersion_DeactivateInvalidatesOldJWT(t *testing.T) {
	app, _, personas := setupFase10QA(t)
	sa, admin := personas["superadmin"], personas["admin"]
	oldToken := admin.token

	status := fase10Request(t, app, "PATCH", fmt.Sprintf("/api/superadmin/users/%d/status", admin.userID), sa.token, `{"active":false}`)
	if status != fiber.StatusOK {
		t.Fatalf("desactivar: status = %d, want 200", status)
	}
	if s := fase10Request(t, app, "GET", "/api/superadmin/dashboard", oldToken, ""); s != fiber.StatusUnauthorized {
		t.Errorf("status=%d, want 401 (usuario desactivado)", s)
	}
}

func TestFase10_TokenVersion_SoftDeletedUserCannotUseOldJWT(t *testing.T) {
	app, _, personas := setupFase10QA(t)
	sa, admin := personas["superadmin"], personas["admin"]
	oldToken := admin.token

	status := fase10Request(t, app, "DELETE", fmt.Sprintf("/api/superadmin/users/%d", admin.userID), sa.token, "")
	if status != fiber.StatusOK {
		t.Fatalf("eliminar: status = %d, want 200", status)
	}
	if s := fase10Request(t, app, "GET", "/api/superadmin/dashboard", oldToken, ""); s != fiber.StatusUnauthorized {
		t.Errorf("status=%d, want 401 (usuario eliminado)", s)
	}
}

// Un cambio que NO modifica permisos (editar nombre/email) no debe invalidar la sesión.
func TestFase10_TokenVersion_BasicInfoUpdateDoesNotInvalidateJWT(t *testing.T) {
	app, _, personas := setupFase10QA(t)
	admin := personas["admin"]

	status := fase10Request(t, app, "PUT", fmt.Sprintf("/api/superadmin/users/%d", admin.userID), admin.token, `{"name":"Nuevo Nombre"}`)
	if status != fiber.StatusOK {
		t.Fatalf("autoservicio de nombre: status = %d, want 200", status)
	}
	if s := fase10Request(t, app, "GET", "/api/superadmin/dashboard", admin.token, ""); s == fiber.StatusUnauthorized {
		t.Error("el mismo token debería seguir sirviendo — cambiar el nombre no es un evento de seguridad")
	}
}

// ==================== §13: cambio de permisos de un rol invalida a TODOS sus usuarios ====================

func TestFase10_RolePermissionsChange_InvalidatesAllUsersOfThatRole(t *testing.T) {
	app, db, personas := setupFase10QA(t)
	sa, soporte := personas["superadmin"], personas["soporte"]
	oldToken := soporte.token
	roleSvc := service.NewSARoleService(db)

	var soporteRole database.SARole
	db.Where("name = ?", "Soporte").First(&soporteRole)
	ids, err := roleSvc.RolePermissions(soporteRole.ID)
	if err != nil {
		t.Fatal(err)
	}
	var viewPerm database.SAPermission
	db.Where("module = ? AND action = ?", "empresas", "view").First(&viewPerm)
	newIDs := append(append([]uint{}, ids...), viewPerm.ID) // no-op real (ya lo tiene), pero dispara el mismo camino de escritura

	status := fase10Request(t, app, "PUT", fmt.Sprintf("/api/superadmin/roles/%d/permissions", soporteRole.ID), sa.token,
		fmt.Sprintf(`{"permission_ids":%v}`, toJSONIntArray(newIDs)))
	if status != fiber.StatusOK {
		t.Fatalf("cambiar permisos del rol: status = %d, want 200", status)
	}
	if s := fase10Request(t, app, "GET", "/api/superadmin/dashboard", oldToken, ""); s != fiber.StatusUnauthorized {
		t.Errorf("el JWT viejo de Soporte debería quedar inválido tras el cambio de permisos del rol: status=%d, want 401", s)
	}
}

func toJSONIntArray(ids []uint) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = fmt.Sprint(id)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

// ==================== §15: eliminación de rol — las 3 reglas ====================

func TestFase10_DeleteRole_WithAssignedUsers_Fails(t *testing.T) {
	app, db, personas := setupFase10QA(t)
	sa := personas["superadmin"]
	var soporteRole database.SARole
	db.Where("name = ?", "Soporte").First(&soporteRole) // tiene a "soporte" asignado

	status := fase10Request(t, app, "DELETE", fmt.Sprintf("/api/superadmin/roles/%d", soporteRole.ID), sa.token, "")
	if status != fiber.StatusConflict {
		t.Errorf("status = %d, want 409 (rol con usuarios asignados)", status)
	}
}

func TestFase10_DeleteRole_SystemRole_Fails(t *testing.T) {
	app, db, personas := setupFase10QA(t)
	sa := personas["superadmin"]
	var finanzasRole database.SARole
	db.Where("name = ?", "Finanzas").First(&finanzasRole)
	// Se libera de usuarios asignados para aislar específicamente la regla "es de sistema".
	db.Model(&database.SuperAdminUser{}).Where("role_id = ?", finanzasRole.ID).Update("role_id", nil)

	status := fase10Request(t, app, "DELETE", fmt.Sprintf("/api/superadmin/roles/%d", finanzasRole.ID), sa.token, "")
	if status != fiber.StatusConflict {
		t.Errorf("status = %d, want 409 (rol de sistema)", status)
	}
}

func TestFase10_DeleteRole_CustomRoleWithoutUsers_Succeeds(t *testing.T) {
	app, db, personas := setupFase10QA(t)
	sa := personas["superadmin"]
	orphanRole := database.SARole{Name: "QA Huerfano Sin Usuarios"}
	db.Create(&orphanRole)

	status := fase10Request(t, app, "DELETE", fmt.Sprintf("/api/superadmin/roles/%d", orphanRole.ID), sa.token, "")
	if status != fiber.StatusOK {
		t.Errorf("status = %d, want 200 (rol personalizado sin usuarios, superadmin puede delegar cualquier cosa)", status)
	}
}
