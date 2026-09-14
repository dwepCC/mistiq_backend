package superadmin

// Fase 5 (etapa 2) — prueba de wiring HTTP real: monta el RegisterRoutes de PRODUCCIÓN (el mismo
// que usa main.go, incluyendo todos los sub-RegisterRoutes de plans/subscriptions/saasadmin/
// saasdocuments/payments/ajustes) contra un fiber.App real, y golpea cada una de las 59 rutas que
// esta etapa protegió con RequireSAPermission. No reimplementa el árbol de rutas a mano — así
// cualquier desajuste entre este archivo y routes.go (ruta mal escrita, permiso equivocado,
// middleware faltante) lo detecta este test.
//
// Solo se prueba la capa de AUTORIZACIÓN: 401 sin token (SuperAdminAuthAPI presente) y 403 con
// token válido pero sin el permiso exacto (RequireSAPermission presente y con el permiso
// correcto) — ambos casos se rechazan ANTES de que el handler real se ejecute, así que no hace
// falta poblar ninguna tabla de negocio (tenants, planes, pagos, fiscal, etc.).
//
// Deliberadamente NO se prueba el camino "con el permiso correcto, la request pasa" ejecutando
// los handlers reales: fiber.App.Test() corre el handler en una goroutine interna que un panic
// ahí no se puede recuperar desde el test (se comprobó en la práctica — un handler que toca una
// tabla inexistente en el sqlite de fixture puede paniquear en vez de solo devolver un error, y
// eso tumba el binario de test completo, no un solo caso). Esa propiedad ("con el permiso
// correcto, RequireSAPermission llama Next()") ya está probada exhaustivamente a nivel de
// middleware puro en pkg/middleware/sa_permissions_test.go (18 tests) — es una propiedad de la
// función RequireSAPermission en sí, no depende de qué string de permiso se le pase, así que no
// hace falta repetirla por cada una de las 59 rutas contra fixtures reales.

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"tukifac/config"
	"tukifac/pkg/database"
	"tukifac/pkg/logger"
	"tukifac/pkg/middleware"

	"github.com/glebarez/sqlite"
	"github.com/gofiber/fiber/v3"
	"github.com/golang-jwt/jwt/v5"
	"gorm.io/gorm"
)

const routeWiringJWTSecret = "route-wiring-test-secret"

func init() {
	// Varios servicios de producción (p. ej. exchangerate.CacheService) usan el logger global sin
	// verificar que esté inicializado — en el binario real siempre lo está (main.go), pero un test
	// que solo importa el paquete nunca llama a logger.Init. Sin esto, cualquier ruta que llegue a
	// loguear (aunque sea un log informativo) paniquea con nil pointer DENTRO de la goroutine
	// interna de fiber.App.Test() — una goroutine que este test NO puede recuperar con recover(),
	// así que el panic tumba todo el binario de test en vez de fallar un solo caso. Inicializar el
	// logger aquí es la forma correcta de evitarlo (no es un problema del RBAC ni de este test).
	logger.Init(&config.Config{LogLevel: "error", AppEnv: "development"})
}

// setupRouteWiringApp monta el árbol de rutas REAL de producción (superadmin.RegisterRoutes,
// que a su vez registra plans/subscriptions/saasadmin/saasdocuments/payments/ajustes), con un
// usuario "admin" (no superadmin) — el caso general para probar que el permiso se exige.
func setupRouteWiringApp(t *testing.T) (*fiber.App, uint) {
	t.Helper()
	return setupRouteWiringAppWithRole(t, "admin")
}

// setupRouteWiringAppWithRole es la variante parametrizada por rol — usada para probar el bypass
// real de superadmin (RequireSuperAdminOnly, RequireSAPermission) contra el árbol de rutas real.
func setupRouteWiringAppWithRole(t *testing.T, role string) (*fiber.App, uint) {
	t.Helper()
	dsn := fmt.Sprintf("file:%s_%s?mode=memory&cache=shared", t.Name(), role)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	// AuditLog: no lo necesita ninguna ruta que se rechace en 401/403 (la mayoría de este
	// archivo), pero las nuevas pruebas de Grupo 6 sí dejan pasar la request hasta el handler
	// fiscal real (para probar bulk vs individual con :action dinámico) — sin esta tabla, el
	// INSERT de auditoría falla en cada corrida (tolerado, GORM solo lo loguea) y ensucia la
	// salida del test con un error que no es tal.
	// SARole/SAPermission/SARolePermission: igual que AuditLog, las pruebas de Grupo 7 dejan pasar
	// la request hasta SARoleHandler real (para confirmar que roles.manage de verdad desbloquea
	// create/update/delete) — sin estas tablas, GetByID/Create fallan con "no such table" (500)
	// en vez de la respuesta real (404 rol inexistente, o 201 creado).
	if err := db.AutoMigrate(
		&database.SuperAdminUser{}, &database.AuditLog{},
		&database.SARole{}, &database.SAPermission{}, &database.SARolePermission{},
	); err != nil {
		t.Fatal(err)
	}
	prevDB := database.CentralDB
	database.CentralDB = db
	t.Cleanup(func() { database.CentralDB = prevDB })

	prevCfg := config.AppConfig
	config.AppConfig = &config.Config{AppEnv: "development", SAJWTSecret: routeWiringJWTSecret}
	t.Cleanup(func() { config.AppConfig = prevCfg })

	user := database.SuperAdminUser{Name: "Wiring Test", Email: "wiring-" + role + "@example.com", Role: role, Active: true}
	if err := user.SetPassword("password123"); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}

	app := fiber.New()
	RegisterRoutes(app) // el mismo que usa main.go — sin reimplementar nada a mano
	return app, user.ID
}

func mintWiringToken(t *testing.T, userID uint, permissions []string) string {
	t.Helper()
	return mintWiringTokenWithRole(t, userID, "admin", permissions)
}

func mintWiringTokenWithRole(t *testing.T, userID uint, role string, permissions []string) string {
	t.Helper()
	claims := &middleware.SuperAdminClaims{
		UserID: userID, Email: "wiring@example.com", Role: role, Type: "superadmin",
		TokenVersion: 0, Permissions: permissions, SAJWTVersion: middleware.CurrentSuperAdminJWTVersion(),
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(8 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	s, err := tok.SignedString([]byte(routeWiringJWTSecret))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// safeTestRequest ejecuta la request y recupera de un panic del handler (posible cuando, con el
// permiso correcto, la ruta sigue de largo hasta lógica que necesita tablas/fixtures que este
// test no monta — no es lo que se está probando aquí, así que se tolera y se registra).
func safeTestRequest(t *testing.T, app *fiber.App, req *http.Request) (status int, panicked bool) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			panicked = true
			t.Logf("handler panic tolerado (fuera del alcance de este test de wiring): %v", r)
		}
	}()
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	return resp.StatusCode, false
}

type protectedRoute struct {
	method     string
	path       string
	permission string
}

// protectedRoutesEtapa2 — las 59 rutas protegidas al cierre de la etapa 2 (7 de la etapa 1 + 52
// nuevas), con el permiso exacto que se registró en el *_routes.go correspondiente. Mantener en
// sync con esos archivos es justamente lo que este test verifica.
var protectedRoutesEtapa2 = []protectedRoute{
	// ajustes
	{"GET", "/api/superadmin/ajustes", "ajustes.view"},
	{"PUT", "/api/superadmin/ajustes", "ajustes.manage"},
	// saas-settings (saasadmin)
	{"GET", "/api/superadmin/saas-settings", "ajustes.view"},
	{"PUT", "/api/superadmin/saas-settings", "ajustes.manage"},
	{"POST", "/api/superadmin/saas-settings/upload-qr", "ajustes.manage"},
	{"POST", "/api/superadmin/saas-settings/upload-logo", "ajustes.manage"},
	// pagos
	{"GET", "/api/superadmin/payments", "pagos.view"},
	{"GET", "/api/superadmin/payments/alerts", "pagos.view"},
	{"GET", "/api/superadmin/payments/1", "pagos.view"},
	// planes
	{"GET", "/api/superadmin/saas-modules", "planes.view"},
	{"GET", "/api/superadmin/plans", "planes.view"},
	{"GET", "/api/superadmin/plans/1", "planes.view"},
	// documentos
	{"GET", "/api/superadmin/document-packages/", "documentos.view"},
	{"GET", "/api/superadmin/document-packages/purchases/pending", "documentos.view"},
	// suscripciones
	{"GET", "/api/superadmin/subscriptions", "suscripciones.view"},
	{"GET", "/api/superadmin/tenants/1/subscription", "suscripciones.view"},
	{"GET", "/api/superadmin/billing-cycles", "suscripciones.view"},
	{"GET", "/api/superadmin/billing-cycles/preview", "suscripciones.view"},
	{"GET", "/api/superadmin/tenants/1/billing-cycles", "suscripciones.view"},
	// dashboard / sistema
	{"GET", "/api/superadmin/platform-domains", "dashboard.view"},
	{"GET", "/api/superadmin/stats", "dashboard.view"},
	{"GET", "/api/superadmin/exchange-rates/today", "dashboard.view"},
	{"POST", "/api/superadmin/exchange-rates/refresh", "ajustes.manage"},
	// usuarios centrales
	{"GET", "/api/superadmin/users", "usuarios_central.view"},
	// roles/permisos
	{"GET", "/api/superadmin/roles", "roles.view"},
	{"GET", "/api/superadmin/roles/1", "roles.view"},
	{"GET", "/api/superadmin/roles/1/permissions", "roles.view"},
	{"GET", "/api/superadmin/permissions", "roles.view"},
	// empresas
	{"GET", "/api/superadmin/tenants", "empresas.view"},
	{"GET", "/api/superadmin/tenants/1", "empresas.view"},
	{"GET", "/api/superadmin/tenants/1/modules", "empresas.view"},
	// ubigeo + consulta (empresas.view — ver informe de la etapa)
	{"GET", "/api/superadmin/ubigeo/regiones", "empresas.view"},
	{"GET", "/api/superadmin/ubigeo/provincias", "empresas.view"},
	{"GET", "/api/superadmin/ubigeo/distritos", "empresas.view"},
	{"POST", "/api/superadmin/consulta/dni", "empresas.view"},
	{"POST", "/api/superadmin/consulta/ruc", "empresas.view"},
	// facturador
	{"GET", "/api/superadmin/tenants/conectados-sunat", "facturador.view"},
	{"GET", "/api/superadmin/tenants/conectados-facturador", "facturador.view"},
	{"GET", "/api/superadmin/pse/empresas", "facturador.view"},
	{"GET", "/api/superadmin/pse/empresas/1", "facturador.view"},
	{"GET", "/api/superadmin/tenants/1/sunat-config", "facturador.view"},
	{"POST", "/api/superadmin/tenants/1/fiscal-test-connection", "facturador.view"},
	// migraciones
	{"GET", "/api/superadmin/backfills", "migraciones.view"},
	{"GET", "/api/superadmin/migrations", "migraciones.view"},
	{"GET", "/api/superadmin/migrations/summary", "migraciones.view"},
	{"GET", "/api/superadmin/migrations/jobs", "migraciones.view"},
	{"GET", "/api/superadmin/migrations/jobs/1", "migraciones.view"},
	{"GET", "/api/superadmin/migrations/1/history", "migraciones.view"},
	{"GET", "/api/superadmin/migrations/1/drift", "migraciones.view"},
	// fiscal
	{"GET", "/api/superadmin/fiscal/stats", "fiscal.view"},
	{"GET", "/api/superadmin/fiscal/health", "fiscal.view"},
	{"GET", "/api/superadmin/fiscal/operations/summary", "fiscal.view"},
	{"GET", "/api/superadmin/fiscal/operations/tenants", "fiscal.view"},
	{"GET", "/api/superadmin/fiscal/operations/queue", "fiscal.view"},
	{"GET", "/api/superadmin/fiscal/alerts", "fiscal.view"},
	{"GET", "/api/superadmin/fiscal/documents", "fiscal.view"},
	{"GET", "/api/superadmin/fiscal/documents/abc/audit-timeline", "fiscal.view"},
	{"GET", "/api/superadmin/fiscal/documents/abc/download/pdf", "fiscal.view"},
	{"GET", "/api/superadmin/fiscal/documents/abc", "fiscal.view"},
}

func TestProtectedRoutesEtapa2_Count(t *testing.T) {
	// 7 de la etapa 1 + 52 de la etapa 2 = 59.
	if len(protectedRoutesEtapa2) != 59 {
		t.Fatalf("protectedRoutesEtapa2 tiene %d entradas, esperado 59 — actualiza este test junto con el informe si cambia intencionalmente", len(protectedRoutesEtapa2))
	}
}

func TestProtectedRoutesEtapa2_Wiring(t *testing.T) {
	app, userID := setupRouteWiringApp(t)
	noPermToken := mintWiringToken(t, userID, []string{})

	for _, r := range protectedRoutesEtapa2 {
		t.Run(r.method+" "+r.path, func(t *testing.T) {
			// Sin token → 401 (SuperAdminAuthAPI sigue siendo la capa exterior). Se rechaza antes
			// de llegar al handler real — no hay riesgo de tocar tablas de negocio.
			req := httptest.NewRequest(r.method, r.path, nil)
			status, _ := safeTestRequest(t, app, req)
			if status != fiber.StatusUnauthorized {
				t.Errorf("sin token: status = %d, want 401", status)
			}

			// Con token válido pero SIN el permiso → 403 (no 404 de ruta inexistente, no 200).
			// También se rechaza antes del handler real — RequireSAPermission corta la cadena.
			req2 := httptest.NewRequest(r.method, r.path, nil)
			req2.Header.Set("Authorization", "Bearer "+noPermToken)
			status2, _ := safeTestRequest(t, app, req2)
			if status2 != fiber.StatusForbidden {
				t.Errorf("sin permiso %q: status = %d, want 403", r.permission, status2)
			}
		})
	}
}

// Spot-check: rutas críticas que siguen SIN proteger tras el Grupo 1 + Grupo 2 (pendientes para
// los próximos grupos: migraciones, suscripciones, planes/documentos, fiscal, roles/usuarios)
// deben seguir accesibles solo con autenticación — confirma que el estado "pendiente" documentado
// es real, no un olvido en sentido contrario tampoco. destroy-complete/operations-key/
// master-access (Grupo 1) y todo /payments/* de escritura (Grupo 2) YA NO están en esta lista.
func TestUnprotectedCriticalRoutes_StillOnlyRequireAuth(t *testing.T) {
	app, userID := setupRouteWiringApp(t)
	// Usuario autenticado sin ningún permiso — si estas rutas ya tuvieran RequireSAPermission
	// aplicado, esto daría 403. No debe pasar todavía.
	token := mintWiringToken(t, userID, []string{})

	criticalRoutes := []struct{ method, path string }{
		// plans/1 (DELETE) y document-packages purchases/approve: el Grupo 5 (Fase 5 etapa 3) las
		// protegió (planes.destroy / documentos.approve_purchase) — ver
		// TestProtectedRoutesGrupo5Planes_Wiring y TestProtectedRoutesGrupo5Documentos_Wiring.
		// fiscal/documents/:uuid/:action (incluyendo cancel): el Grupo 6 (Fase 5 etapa 3) la
		// protegió — la comprobación vive DENTRO del handler (requiredPermissionForFiscalAction),
		// no a nivel de ruta, porque :action es dinámico. Ver TestFiscalGrupo6_RealRouteWiring_*
		// más abajo, que ejercita exactamente eso contra el árbol de rutas real.
		// POST /roles y POST /users: el Grupo 7 (Fase 5 etapa 3, Pasos C y E) las protegió con
		// roles.create / usuarios_central.create — ver TestProtectedRoutesGrupo7Roles_Wiring y
		// TestProtectedRoutesGrupo7Usuarios_Wiring más abajo.
		// check-expirations: analizado en el Grupo 3 (Fase 5 etapa 3) — es una operación de
		// alcance de flota (recorre TODAS las suscripciones), tratada como las demás operaciones
		// masivas ya diferidas — deliberadamente sin permiso asignado todavía.
		{"POST", "/api/superadmin/cron/check-expirations"},
		// tenants/migrate-all y backfills/run-all: analizados en el Grupo 4 — sin ningún límite
		// (TODA la flota), confirmado con el usuario: quedan pendientes, mismo criterio que
		// check-expirations/RunJobsAPI.
		{"POST", "/api/superadmin/tenants/migrate-all"},
		{"POST", "/api/superadmin/backfills/run-all"},
	}
	for _, r := range criticalRoutes {
		t.Run(r.method+" "+r.path, func(t *testing.T) {
			req := httptest.NewRequest(r.method, r.path, nil)
			req.Header.Set("Authorization", "Bearer "+token)
			status, _ := safeTestRequest(t, app, req)
			if status == fiber.StatusForbidden {
				t.Errorf("status = 403 — esta ruta ya tiene RequireSAPermission aplicado, actualizar el informe de la fase")
			}
		})
	}
}

// ==================== Grupo 1 — empresas/tenants (Fase 5, etapa 3) ====================

var protectedRoutesGrupo1 = []protectedRoute{
	{"PATCH", "/api/superadmin/tenants/facturador-enabled", "facturador.manage"},
	{"POST", "/api/superadmin/pse/empresas", "facturador.manage"},
	{"PUT", "/api/superadmin/pse/empresas/1", "facturador.manage"},
	{"PATCH", "/api/superadmin/pse/empresas/1/toggle", "facturador.manage"},
	{"POST", "/api/superadmin/tenants", "empresas.create"},
	{"PUT", "/api/superadmin/tenants/1", "empresas.update"},
	{"PATCH", "/api/superadmin/tenants/1/status", "empresas.change_status"},
	{"POST", "/api/superadmin/tenants/1/modules", "empresas.update"},
	{"PUT", "/api/superadmin/tenants/1/sunat-config", "facturador.manage"},
	{"PATCH", "/api/superadmin/tenants/1/sunat-env", "facturador.manage"},
	{"POST", "/api/superadmin/tenants/1/sync-facturador", "facturador.manage"},
	{"POST", "/api/superadmin/tenants/1/unblock", "empresas.change_status"},
	{"POST", "/api/superadmin/tenants/1/master-access", "empresas.master_access"},
}

func TestProtectedRoutesGrupo1_Count(t *testing.T) {
	if len(protectedRoutesGrupo1) != 13 {
		t.Fatalf("protectedRoutesGrupo1 tiene %d entradas, esperado 13", len(protectedRoutesGrupo1))
	}
}

// Mismo patrón que TestProtectedRoutesEtapa2_Wiring: 401 sin token, 403 sin el permiso exacto,
// contra el árbol de rutas real.
func TestProtectedRoutesGrupo1_Wiring(t *testing.T) {
	app, userID := setupRouteWiringApp(t)
	noPermToken := mintWiringToken(t, userID, []string{})

	for _, r := range protectedRoutesGrupo1 {
		t.Run(r.method+" "+r.path, func(t *testing.T) {
			req := httptest.NewRequest(r.method, r.path, nil)
			status, _ := safeTestRequest(t, app, req)
			if status != fiber.StatusUnauthorized {
				t.Errorf("sin token: status = %d, want 401", status)
			}

			req2 := httptest.NewRequest(r.method, r.path, nil)
			req2.Header.Set("Authorization", "Bearer "+noPermToken)
			status2, _ := safeTestRequest(t, app, req2)
			if status2 != fiber.StatusForbidden {
				t.Errorf("sin permiso %q: status = %d, want 403", r.permission, status2)
			}
		})
	}
}

// Además: empresas.update NO debe otorgar master-access (permisos completamente separados, tal
// como exigió la Fase 5 etapa 3) — se prueba a nivel de middleware puro en
// pkg/middleware/sa_permissions_grupo1_test.go; aquí se confirma contra la ruta real.
func TestMasterAccessRoute_UpdatePermissionAloneIsNotEnough(t *testing.T) {
	app, userID := setupRouteWiringApp(t)
	token := mintWiringToken(t, userID, []string{"empresas.update", "empresas.view", "empresas.create", "empresas.change_status"})

	req := httptest.NewRequest("POST", "/api/superadmin/tenants/1/master-access", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	status, _ := safeTestRequest(t, app, req)
	if status != fiber.StatusForbidden {
		t.Errorf("status = %d, want 403 (empresas.update no debe otorgar master-access)", status)
	}
}

// ==================== Tests específicos de seguridad — destroy-complete / operations-key ====================
//
// Ambas rutas usan middleware.RequireSuperAdminOnly() — un admin normal, incluso con
// Permissions=["*"] (simulando un bug que le diera todos los permisos granulares), debe seguir
// rechazado: este middleware no consulta Permissions en absoluto, solo Role exacto.

func TestGrupo1_OperationsKey_NormalAdminRejected(t *testing.T) {
	app, userID := setupRouteWiringApp(t)
	token := mintWiringToken(t, userID, []string{"*", "ajustes.manage"})

	req := httptest.NewRequest("PUT", "/api/superadmin/saas-settings/operations-key", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	status, _ := safeTestRequest(t, app, req)
	if status != fiber.StatusForbidden {
		t.Errorf("status = %d, want 403 (operations-key es exclusivo de superadmin real)", status)
	}
}

func TestGrupo1_OperationsKey_SuperadminNotBlockedByAuthz(t *testing.T) {
	app, userID := setupRouteWiringAppWithRole(t, "superadmin")
	token := mintWiringTokenWithRole(t, userID, "superadmin", nil)

	req := httptest.NewRequest("PUT", "/api/superadmin/saas-settings/operations-key", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	status, _ := safeTestRequest(t, app, req)
	if status == fiber.StatusUnauthorized || status == fiber.StatusForbidden {
		t.Errorf("status = %d, superadmin no debería quedar bloqueado por autenticación/autorización", status)
	}
}

func TestGrupo1_DestroyComplete_NormalAdminRejected(t *testing.T) {
	app, userID := setupRouteWiringApp(t)
	token := mintWiringToken(t, userID, []string{"*", "empresas.destroy"}) // empresas.destroy ni existe en el catálogo

	req := httptest.NewRequest("POST", "/api/superadmin/tenants/1/destroy-complete", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	status, _ := safeTestRequest(t, app, req)
	if status != fiber.StatusForbidden {
		t.Errorf("status = %d, want 403 (destroy-complete es exclusivo de superadmin real)", status)
	}
}

func TestGrupo1_DestroyComplete_SuperadminNotBlockedByAuthz(t *testing.T) {
	app, userID := setupRouteWiringAppWithRole(t, "superadmin")
	token := mintWiringTokenWithRole(t, userID, "superadmin", nil)

	req := httptest.NewRequest("POST", "/api/superadmin/tenants/1/destroy-complete", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	status, _ := safeTestRequest(t, app, req)
	if status == fiber.StatusUnauthorized || status == fiber.StatusForbidden {
		t.Errorf("status = %d, superadmin no debería quedar bloqueado por autenticación/autorización (la operations_key inválida/tenant inexistente sí puede rechazarlo después, con otro código)", status)
	}
}

// ==================== Grupo 2 — pagos (Fase 5, etapa 3) ====================

var protectedRoutesGrupo2 = []protectedRoute{
	// CreateAPI aplica la aprobación en el mismo paso (ver comentario en payments/routes.go) —
	// mismo permiso que approve, confirmado con el usuario antes de asignarlo.
	{"POST", "/api/superadmin/payments", "pagos.approve"},
	{"PATCH", "/api/superadmin/payments/1/approve", "pagos.approve"},
	{"PATCH", "/api/superadmin/payments/1/reject", "pagos.reject"},
	{"PATCH", "/api/superadmin/payments/1/revert", "pagos.refund"},
	{"POST", "/api/superadmin/payments/1/fiscal-document", "pagos.approve"},
}

func TestProtectedRoutesGrupo2_Count(t *testing.T) {
	if len(protectedRoutesGrupo2) != 5 {
		t.Fatalf("protectedRoutesGrupo2 tiene %d entradas, esperado 5", len(protectedRoutesGrupo2))
	}
}

// Mismo patrón de wiring que los grupos anteriores: 401 sin token, 403 sin el permiso exacto,
// contra el árbol de rutas real — así se detecta si, por ejemplo, /approve terminara enganchado
// a pagos.view por error en vez de pagos.approve.
func TestProtectedRoutesGrupo2_Wiring(t *testing.T) {
	app, userID := setupRouteWiringApp(t)
	noPermToken := mintWiringToken(t, userID, []string{})

	for _, r := range protectedRoutesGrupo2 {
		t.Run(r.method+" "+r.path, func(t *testing.T) {
			req := httptest.NewRequest(r.method, r.path, nil)
			status, _ := safeTestRequest(t, app, req)
			if status != fiber.StatusUnauthorized {
				t.Errorf("sin token: status = %d, want 401", status)
			}

			req2 := httptest.NewRequest(r.method, r.path, nil)
			req2.Header.Set("Authorization", "Bearer "+noPermToken)
			status2, _ := safeTestRequest(t, app, req2)
			if status2 != fiber.StatusForbidden {
				t.Errorf("sin permiso %q: status = %d, want 403", r.permission, status2)
			}
		})
	}
}

// Cruce explícito contra las rutas reales: cada permiso de pagos solo abre SU propia ruta, nunca
// la de otra acción — reproduce exactamente el escenario que preocupaba ("endpoint approve →
// middleware pagos.view" en vez de "endpoint approve → pagos.approve").
func TestProtectedRoutesGrupo2_PermissionsDoNotCrossRoutes(t *testing.T) {
	app, userID := setupRouteWiringApp(t)

	cases := []struct {
		grantedPermission string
		method, path      string
	}{
		{"pagos.view", "PATCH", "/api/superadmin/payments/1/approve"},
		{"pagos.view", "PATCH", "/api/superadmin/payments/1/reject"},
		{"pagos.view", "PATCH", "/api/superadmin/payments/1/revert"},
		{"pagos.approve", "PATCH", "/api/superadmin/payments/1/reject"},
		{"pagos.approve", "PATCH", "/api/superadmin/payments/1/revert"},
		{"pagos.reject", "PATCH", "/api/superadmin/payments/1/revert"},
		{"pagos.reject", "PATCH", "/api/superadmin/payments/1/approve"},
		{"pagos.refund", "PATCH", "/api/superadmin/payments/1/approve"},
		{"pagos.refund", "PATCH", "/api/superadmin/payments/1/reject"},
	}
	for _, tc := range cases {
		t.Run(tc.grantedPermission+" -> "+tc.method+" "+tc.path, func(t *testing.T) {
			token := mintWiringToken(t, userID, []string{tc.grantedPermission})
			req := httptest.NewRequest(tc.method, tc.path, nil)
			req.Header.Set("Authorization", "Bearer "+token)
			status, _ := safeTestRequest(t, app, req)
			if status != fiber.StatusForbidden {
				t.Errorf("status = %d, want 403 (%q no debe abrir %s %s)", status, tc.grantedPermission, tc.method, tc.path)
			}
		})
	}
}

// ==================== Grupo 3 — suscripciones (Fase 5, etapa 3) ====================

var protectedRoutesGrupo3 = []protectedRoute{
	{"POST", "/api/superadmin/subscriptions", "suscripciones.create"},
	{"PATCH", "/api/superadmin/subscriptions/1/suspend", "suscripciones.change_status"},
	{"PATCH", "/api/superadmin/subscriptions/1/reactivate", "suscripciones.change_status"},
	{"PATCH", "/api/superadmin/subscriptions/1/cancel", "suscripciones.change_status"},
	{"PATCH", "/api/superadmin/subscriptions/1/adjust-validity", "suscripciones.update"},
	{"POST", "/api/superadmin/billing-cycles", "suscripciones.create"},
	{"PATCH", "/api/superadmin/billing-cycles/1/cancel", "suscripciones.change_status"},
}

func TestProtectedRoutesGrupo3_Count(t *testing.T) {
	if len(protectedRoutesGrupo3) != 7 {
		t.Fatalf("protectedRoutesGrupo3 tiene %d entradas, esperado 7", len(protectedRoutesGrupo3))
	}
}

func TestProtectedRoutesGrupo3_Wiring(t *testing.T) {
	app, userID := setupRouteWiringApp(t)
	noPermToken := mintWiringToken(t, userID, []string{})

	for _, r := range protectedRoutesGrupo3 {
		t.Run(r.method+" "+r.path, func(t *testing.T) {
			req := httptest.NewRequest(r.method, r.path, nil)
			status, _ := safeTestRequest(t, app, req)
			if status != fiber.StatusUnauthorized {
				t.Errorf("sin token: status = %d, want 401", status)
			}

			req2 := httptest.NewRequest(r.method, r.path, nil)
			req2.Header.Set("Authorization", "Bearer "+noPermToken)
			status2, _ := safeTestRequest(t, app, req2)
			if status2 != fiber.StatusForbidden {
				t.Errorf("sin permiso %q: status = %d, want 403", r.permission, status2)
			}
		})
	}
}

// Cruce explícito contra las rutas reales: confirma que adjust-validity → suscripciones.update y
// suspend/reactivate/cancel → suscripciones.change_status NO quedaron conectados accidentalmente
// a suscripciones.view (el riesgo concreto que preocupaba en esta etapa).
func TestProtectedRoutesGrupo3_ViewDoesNotOpenWriteRoutes(t *testing.T) {
	app, userID := setupRouteWiringApp(t)
	token := mintWiringToken(t, userID, []string{"suscripciones.view"})

	writeRoutes := []struct{ method, path string }{
		{"POST", "/api/superadmin/subscriptions"},
		{"PATCH", "/api/superadmin/subscriptions/1/suspend"},
		{"PATCH", "/api/superadmin/subscriptions/1/reactivate"},
		{"PATCH", "/api/superadmin/subscriptions/1/cancel"},
		{"PATCH", "/api/superadmin/subscriptions/1/adjust-validity"},
		{"POST", "/api/superadmin/billing-cycles"},
		{"PATCH", "/api/superadmin/billing-cycles/1/cancel"},
	}
	for _, r := range writeRoutes {
		t.Run(r.method+" "+r.path, func(t *testing.T) {
			req := httptest.NewRequest(r.method, r.path, nil)
			req.Header.Set("Authorization", "Bearer "+token)
			status, _ := safeTestRequest(t, app, req)
			if status != fiber.StatusForbidden {
				t.Errorf("status = %d, want 403 (suscripciones.view no debe abrir %s %s)", status, r.method, r.path)
			}
		})
	}
}

// ==================== Grupo 4 — migraciones (Fase 5, etapa 3) — ALTO RIESGO ====================

var protectedRoutesGrupo4 = []protectedRoute{
	// Individuales
	{"POST", "/api/superadmin/tenants/1/migrate", "migraciones.run"}, // ruta duplicada de /migrations/:tenantId/migrate, ver hallazgo en routes.go
	{"POST", "/api/superadmin/tenants/1/backfill", "migraciones.backfill"},
	{"POST", "/api/superadmin/migrations/1/repair", "migraciones.repair"},
	{"POST", "/api/superadmin/migrations/1/retry", "migraciones.run"},
	{"POST", "/api/superadmin/migrations/1/migrate", "migraciones.run"},
	{"POST", "/api/superadmin/migrations/1/pause", "migraciones.pause"},
	{"POST", "/api/superadmin/migrations/1/resume", "migraciones.resume"},
	// Masivas (decisión confirmada con el usuario — ver informe del Grupo 4)
	{"POST", "/api/superadmin/migrations/drift-scan", "migraciones.view"},
	{"POST", "/api/superadmin/migrations/bulk/repair", "migraciones.repair"},
	{"POST", "/api/superadmin/migrations/bulk/repair-drifted", "migraciones.repair"},
	{"POST", "/api/superadmin/migrations/bulk/retry-failed", "migraciones.run"},
	{"POST", "/api/superadmin/migrations/resume-fleet", "migraciones.resume"},
}

func TestProtectedRoutesGrupo4_Count(t *testing.T) {
	if len(protectedRoutesGrupo4) != 12 {
		t.Fatalf("protectedRoutesGrupo4 tiene %d entradas, esperado 12", len(protectedRoutesGrupo4))
	}
}

func TestProtectedRoutesGrupo4_Wiring(t *testing.T) {
	app, userID := setupRouteWiringApp(t)
	noPermToken := mintWiringToken(t, userID, []string{})

	for _, r := range protectedRoutesGrupo4 {
		t.Run(r.method+" "+r.path, func(t *testing.T) {
			req := httptest.NewRequest(r.method, r.path, nil)
			status, _ := safeTestRequest(t, app, req)
			if status != fiber.StatusUnauthorized {
				t.Errorf("sin token: status = %d, want 401", status)
			}

			req2 := httptest.NewRequest(r.method, r.path, nil)
			req2.Header.Set("Authorization", "Bearer "+noPermToken)
			status2, _ := safeTestRequest(t, app, req2)
			if status2 != fiber.StatusForbidden {
				t.Errorf("sin permiso %q: status = %d, want 403", r.permission, status2)
			}
		})
	}
}

// Cruce explícito contra las rutas reales: migraciones.view NO debe abrir ninguna de las rutas de
// escritura de este grupo (individuales ni masivas) — el riesgo concreto que preocupaba.
func TestProtectedRoutesGrupo4_ViewDoesNotOpenWriteRoutes(t *testing.T) {
	app, userID := setupRouteWiringApp(t)
	token := mintWiringToken(t, userID, []string{"migraciones.view"})

	for _, r := range protectedRoutesGrupo4 {
		if r.permission == "migraciones.view" {
			continue // drift-scan: view sí lo abre, es su permiso correcto
		}
		t.Run(r.method+" "+r.path, func(t *testing.T) {
			req := httptest.NewRequest(r.method, r.path, nil)
			req.Header.Set("Authorization", "Bearer "+token)
			status, _ := safeTestRequest(t, app, req)
			if status != fiber.StatusForbidden {
				t.Errorf("status = %d, want 403 (migraciones.view no debe abrir %s %s)", status, r.method, r.path)
			}
		})
	}
}

// ==================== Grupo 5, Parte A — planes (Fase 5, etapa 3) ====================

var protectedRoutesGrupo5Planes = []protectedRoute{
	{"POST", "/api/superadmin/saas-modules", "planes.create"},
	{"PUT", "/api/superadmin/saas-modules/1", "planes.update"},
	{"PATCH", "/api/superadmin/saas-modules/1/toggle", "planes.change_status"},
	{"DELETE", "/api/superadmin/saas-modules/1", "planes.destroy"},
	{"POST", "/api/superadmin/plans", "planes.create"},
	{"PUT", "/api/superadmin/plans/1", "planes.update"},
	{"PATCH", "/api/superadmin/plans/1/toggle", "planes.change_status"},
	{"DELETE", "/api/superadmin/plans/1", "planes.destroy"},
}

func TestProtectedRoutesGrupo5Planes_Count(t *testing.T) {
	if len(protectedRoutesGrupo5Planes) != 8 {
		t.Fatalf("protectedRoutesGrupo5Planes tiene %d entradas, esperado 8", len(protectedRoutesGrupo5Planes))
	}
}

func TestProtectedRoutesGrupo5Planes_Wiring(t *testing.T) {
	app, userID := setupRouteWiringApp(t)
	noPermToken := mintWiringToken(t, userID, []string{})

	for _, r := range protectedRoutesGrupo5Planes {
		t.Run(r.method+" "+r.path, func(t *testing.T) {
			req := httptest.NewRequest(r.method, r.path, nil)
			status, _ := safeTestRequest(t, app, req)
			if status != fiber.StatusUnauthorized {
				t.Errorf("sin token: status = %d, want 401", status)
			}

			req2 := httptest.NewRequest(r.method, r.path, nil)
			req2.Header.Set("Authorization", "Bearer "+noPermToken)
			status2, _ := safeTestRequest(t, app, req2)
			if status2 != fiber.StatusForbidden {
				t.Errorf("sin permiso %q: status = %d, want 403", r.permission, status2)
			}
		})
	}
}

// Cruce explícito contra las rutas reales: confirma que destroy → planes.destroy NO quedó
// conectado accidentalmente a planes.update (ni a ningún otro permiso de planes) — el escenario
// concreto que preocupaba en esta etapa.
func TestProtectedRoutesGrupo5Planes_DestroyRequiresExactPermission(t *testing.T) {
	app, userID := setupRouteWiringApp(t)

	destroyRoutes := []struct{ method, path string }{
		{"DELETE", "/api/superadmin/saas-modules/1"},
		{"DELETE", "/api/superadmin/plans/1"},
	}
	otherPerms := []string{"planes.view", "planes.create", "planes.update", "planes.change_status"}
	for _, r := range destroyRoutes {
		for _, perm := range otherPerms {
			t.Run(r.method+" "+r.path+" con "+perm, func(t *testing.T) {
				token := mintWiringToken(t, userID, []string{perm})
				req := httptest.NewRequest(r.method, r.path, nil)
				req.Header.Set("Authorization", "Bearer "+token)
				status, _ := safeTestRequest(t, app, req)
				if status != fiber.StatusForbidden {
					t.Errorf("status = %d, want 403 (%q no debe abrir %s %s)", status, perm, r.method, r.path)
				}
			})
		}
	}
}

// ==================== Grupo 5, Parte B — documentos (Fase 5, etapa 3) ====================

var protectedRoutesGrupo5Documentos = []protectedRoute{
	{"POST", "/api/superadmin/document-packages/", "documentos.manage"},
	{"PUT", "/api/superadmin/document-packages/1", "documentos.manage"},
	{"DELETE", "/api/superadmin/document-packages/1", "documentos.manage"},
	{"PATCH", "/api/superadmin/document-packages/purchases/1/approve", "documentos.approve_purchase"},
	{"PATCH", "/api/superadmin/document-packages/purchases/1/reject", "documentos.approve_purchase"},
}

func TestProtectedRoutesGrupo5Documentos_Count(t *testing.T) {
	if len(protectedRoutesGrupo5Documentos) != 5 {
		t.Fatalf("protectedRoutesGrupo5Documentos tiene %d entradas, esperado 5", len(protectedRoutesGrupo5Documentos))
	}
}

func TestProtectedRoutesGrupo5Documentos_Wiring(t *testing.T) {
	app, userID := setupRouteWiringApp(t)
	noPermToken := mintWiringToken(t, userID, []string{})

	for _, r := range protectedRoutesGrupo5Documentos {
		t.Run(r.method+" "+r.path, func(t *testing.T) {
			req := httptest.NewRequest(r.method, r.path, nil)
			status, _ := safeTestRequest(t, app, req)
			if status != fiber.StatusUnauthorized {
				t.Errorf("sin token: status = %d, want 401", status)
			}

			req2 := httptest.NewRequest(r.method, r.path, nil)
			req2.Header.Set("Authorization", "Bearer "+noPermToken)
			status2, _ := safeTestRequest(t, app, req2)
			if status2 != fiber.StatusForbidden {
				t.Errorf("sin permiso %q: status = %d, want 403", r.permission, status2)
			}
		})
	}
}

// Cruce explícito contra las rutas reales: documentos.manage NO debe abrir approve/reject de
// compras — el escenario concreto ("documentos.approve_purchase → documentos.manage") que
// preocupaba en esta etapa.
func TestProtectedRoutesGrupo5Documentos_ManageDoesNotOpenApprovePurchase(t *testing.T) {
	app, userID := setupRouteWiringApp(t)
	token := mintWiringToken(t, userID, []string{"documentos.manage"})

	approveRoutes := []struct{ method, path string }{
		{"PATCH", "/api/superadmin/document-packages/purchases/1/approve"},
		{"PATCH", "/api/superadmin/document-packages/purchases/1/reject"},
	}
	for _, r := range approveRoutes {
		t.Run(r.method+" "+r.path, func(t *testing.T) {
			req := httptest.NewRequest(r.method, r.path, nil)
			req.Header.Set("Authorization", "Bearer "+token)
			status, _ := safeTestRequest(t, app, req)
			if status != fiber.StatusForbidden {
				t.Errorf("status = %d, want 403 (documentos.manage no debe abrir %s %s)", status, r.method, r.path)
			}
		})
	}
}

// ==================== Grupo 6 — fiscal (Fase 5, etapa 3) ====================
//
// fiscal/documents/:uuid/:action y fiscal/documents/bulk/:action tienen :action dinámico, así
// que no encajan en el patrón protectedRoute{method,path,permission} usado en los grupos
// anteriores (un mismo path admite varias acciones, cada una con su propio permiso — o ninguno,
// para "force"). La comprobación vive DENTRO del handler (requiredPermissionForFiscalAction,
// ver internal/superadmin/handler/fiscal_handler.go) precisamente porque RequireSAPermission a
// nivel de ruta no puede depender del valor de :action.
//
// Estos tests montan el árbol de rutas REAL (RegisterRoutes, igual que el resto de este
// archivo) — a diferencia de fiscal_handler_grupo6_test.go, que monta el handler por separado.
// Ejercitan exactamente el caso que introdujo un bug de test (no de producción) durante el
// desarrollo de este grupo: "/documents/bulk/:action" está registrado ANTES que
// "/documents/:uuid/:action" en routes.go para que Fiber no confunda "bulk" con un :uuid — este
// test confirma esa propiedad contra el árbol de producción, no contra una reconstrucción local.

func TestFiscalGrupo6_RealRouteWiring_DocumentActionRequiresAuthThenPermission(t *testing.T) {
	app, userID := setupRouteWiringApp(t)

	cases := []struct {
		action     string
		permission string
	}{
		{"retry", "fiscal.retry"},
		{"cancel", "fiscal.cancel"},
	}
	for _, tc := range cases {
		t.Run(tc.action, func(t *testing.T) {
			path := "/api/superadmin/fiscal/documents/abc-123/" + tc.action

			// Sin token → 401.
			req := httptest.NewRequest("POST", path, strings.NewReader(`{}`))
			status, _ := safeTestRequest(t, app, req)
			if status != fiber.StatusUnauthorized {
				t.Errorf("sin token: status = %d, want 401", status)
			}

			// Con token pero sin el permiso exacto → 403, resuelto dentro del handler.
			noPermToken := mintWiringToken(t, userID, []string{"fiscal.view"})
			req2 := httptest.NewRequest("POST", path, strings.NewReader(`{}`))
			req2.Header.Set("Content-Type", "application/json")
			req2.Header.Set("Authorization", "Bearer "+noPermToken)
			status2, _ := safeTestRequest(t, app, req2)
			if status2 != fiber.StatusForbidden {
				t.Errorf("sin permiso %q: status = %d, want 403", tc.permission, status2)
			}

			// Con el permiso exacto → nunca 401/403 (en este entorno da 503: fiscaladmin no está
			// configurado, lo cual prueba que la autorización SÍ dejó pasar la request).
			token := mintWiringToken(t, userID, []string{tc.permission})
			req3 := httptest.NewRequest("POST", path, strings.NewReader(`{}`))
			req3.Header.Set("Content-Type", "application/json")
			req3.Header.Set("Authorization", "Bearer "+token)
			status3, _ := safeTestRequest(t, app, req3)
			if status3 == fiber.StatusUnauthorized || status3 == fiber.StatusForbidden {
				t.Errorf("con permiso %q: status = %d, no debería ser 401/403", tc.permission, status3)
			}
		})
	}
}

// Guarda de regresión específica del bug de orden de registro encontrado durante este grupo:
// contra el árbol de rutas REAL, fiscal.bulk debe habilitar bulk/retry y fiscal.retry (permiso
// individual) NO debe alcanzar — si "documents/:uuid/:action" alguna vez se registrara antes que
// "documents/bulk/:action" en routes.go, este test lo detectaría (fiscal.bulk pasaría a dar 403 y
// fiscal.retry dejaría de darlo).
func TestFiscalGrupo6_RealRouteWiring_BulkActionRoutesToBulkHandlerNotDocumentAction(t *testing.T) {
	app, userID := setupRouteWiringApp(t)
	const path = "/api/superadmin/fiscal/documents/bulk/retry"
	body := `{"document_uuids":["abc-123"]}`

	t.Run("sin_token", func(t *testing.T) {
		req := httptest.NewRequest("POST", path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		status, _ := safeTestRequest(t, app, req)
		if status != fiber.StatusUnauthorized {
			t.Errorf("status = %d, want 401", status)
		}
	})

	t.Run("fiscal_bulk_no_bloqueado", func(t *testing.T) {
		token := mintWiringToken(t, userID, []string{"fiscal.bulk"})
		req := httptest.NewRequest("POST", path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		status, _ := safeTestRequest(t, app, req)
		if status == fiber.StatusUnauthorized || status == fiber.StatusForbidden {
			t.Errorf("status = %d, no debería ser 401/403 (fiscal.bulk cubre retry masivo)", status)
		}
	})

	t.Run("fiscal_retry_individual_insuficiente", func(t *testing.T) {
		token := mintWiringToken(t, userID, []string{"fiscal.retry"})
		req := httptest.NewRequest("POST", path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		status, _ := safeTestRequest(t, app, req)
		if status != fiber.StatusForbidden {
			t.Errorf("status = %d, want 403 (fiscal.retry individual no debe conceder bulk)", status)
		}
	})
}

func TestFiscalGrupo6_RealRouteWiring_UnknownActionRejected(t *testing.T) {
	app, userID := setupRouteWiringApp(t)
	token := mintWiringToken(t, userID, []string{"fiscal.retry", "fiscal.cancel", "fiscal.bulk"})

	paths := []string{
		"/api/superadmin/fiscal/documents/abc-123/whatever",
		"/api/superadmin/fiscal/documents/bulk/whatever",
	}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest("POST", path, strings.NewReader(`{}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+token)
			status, _ := safeTestRequest(t, app, req)
			if status != fiber.StatusBadRequest {
				t.Errorf("status = %d, want 400 (acción desconocida)", status)
			}
		})
	}
}

// ==================== Grupo 7 — roles (Fase 5, etapa 3, Paso C) ====================
//
// Techo de delegación aparte: estos tests solo prueban la capa de AUTORIZACIÓN de ruta (401/403
// por permiso faltante), igual que el resto de este archivo. El techo de delegación
// (middleware.CanDelegateAll dentro de DeleteAPI/SetRolePermissionsAPI) tiene su propia cobertura
// dedicada en internal/superadmin/handler/sa_role_handler_grupo7_test.go, porque depende del
// CONTENIDO del rol/del body — no encaja en el patrón protectedRoute{method,path,permission}.

var protectedRoutesGrupo7Roles = []protectedRoute{
	{"POST", "/api/superadmin/roles", "roles.create"},
	{"PUT", "/api/superadmin/roles/1", "roles.update"},
	{"DELETE", "/api/superadmin/roles/1", "roles.delete"},
	{"PUT", "/api/superadmin/roles/1/permissions", "roles.manage"},
}

func TestProtectedRoutesGrupo7Roles_Count(t *testing.T) {
	if len(protectedRoutesGrupo7Roles) != 4 {
		t.Fatalf("protectedRoutesGrupo7Roles tiene %d entradas, esperado 4", len(protectedRoutesGrupo7Roles))
	}
}

func TestProtectedRoutesGrupo7Roles_Wiring(t *testing.T) {
	app, userID := setupRouteWiringApp(t)
	noPermToken := mintWiringToken(t, userID, []string{})

	for _, r := range protectedRoutesGrupo7Roles {
		t.Run(r.method+" "+r.path, func(t *testing.T) {
			req := httptest.NewRequest(r.method, r.path, strings.NewReader(`{}`))
			req.Header.Set("Content-Type", "application/json")
			status, _ := safeTestRequest(t, app, req)
			if status != fiber.StatusUnauthorized {
				t.Errorf("sin token: status = %d, want 401", status)
			}

			req2 := httptest.NewRequest(r.method, r.path, strings.NewReader(`{}`))
			req2.Header.Set("Content-Type", "application/json")
			req2.Header.Set("Authorization", "Bearer "+noPermToken)
			status2, _ := safeTestRequest(t, app, req2)
			if status2 != fiber.StatusForbidden {
				t.Errorf("sin permiso %q: status = %d, want 403", r.permission, status2)
			}
		})
	}
}

// roles.view (el único permiso que el rol "Admin" tiene sobre este módulo por defecto, ver
// sa_rbac_seed.go) NO debe abrir ninguna de las 4 rutas de escritura — es exactamente la barrera
// que separa "consultar roles" de "administrar roles".
func TestProtectedRoutesGrupo7Roles_ViewDoesNotOpenWriteRoutes(t *testing.T) {
	app, userID := setupRouteWiringApp(t)
	token := mintWiringToken(t, userID, []string{"roles.view"})

	for _, r := range protectedRoutesGrupo7Roles {
		t.Run(r.method+" "+r.path, func(t *testing.T) {
			req := httptest.NewRequest(r.method, r.path, strings.NewReader(`{}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+token)
			status, _ := safeTestRequest(t, app, req)
			if status != fiber.StatusForbidden {
				t.Errorf("status = %d, want 403 (roles.view no debe permitir %s %s)", status, r.method, r.path)
			}
		})
	}
}

// roles.manage SÍ debe abrir create/update/delete (expansión ya establecida en
// saManageImpliedActions, Fase 4) — spot-check contra el árbol de rutas real, no solo la unidad
// de middleware.
func TestProtectedRoutesGrupo7Roles_ManageImpliesCreateUpdateDelete(t *testing.T) {
	app, userID := setupRouteWiringApp(t)
	token := mintWiringToken(t, userID, []string{"roles.manage"})

	implied := []struct{ method, path string }{
		{"POST", "/api/superadmin/roles"},
		{"PUT", "/api/superadmin/roles/1"},
		{"DELETE", "/api/superadmin/roles/1"},
	}
	for _, r := range implied {
		t.Run(r.method+" "+r.path, func(t *testing.T) {
			req := httptest.NewRequest(r.method, r.path, strings.NewReader(`{}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+token)
			status, _ := safeTestRequest(t, app, req)
			if status == fiber.StatusUnauthorized || status == fiber.StatusForbidden {
				t.Errorf("status = %d, no debería ser 401/403 (roles.manage implica %s %s)", status, r.method, r.path)
			}
		})
	}
}

// ==================== Grupo 7 — usuarios centrales (Fase 5, etapa 3, Paso E) ====================
//
// PUT /users/:id (UpdateUserAPI) y PUT /users/:id/system-role (ChangeUserSystemRoleAPI) NO
// encajan en el patrón protectedRoute{method,path,permission}: el primero permite autoservicio
// sin ningún permiso (el gate solo aplica al editar a OTRO usuario); el segundo usa
// RequireSuperAdminOnly(), no un permiso otorgable. Ambos tienen su propia sección de tests más
// abajo, contra el árbol de rutas real.

var protectedRoutesGrupo7Usuarios = []protectedRoute{
	{"POST", "/api/superadmin/users", "usuarios_central.create"},
	{"PUT", "/api/superadmin/users/1/role", "usuarios_central.change_role"},
	{"PATCH", "/api/superadmin/users/1/status", "usuarios_central.change_status"},
	{"POST", "/api/superadmin/users/1/password", "usuarios_central.reset_password"},
	{"DELETE", "/api/superadmin/users/1", "usuarios_central.destroy"},
}

func TestProtectedRoutesGrupo7Usuarios_Count(t *testing.T) {
	if len(protectedRoutesGrupo7Usuarios) != 5 {
		t.Fatalf("protectedRoutesGrupo7Usuarios tiene %d entradas, esperado 5", len(protectedRoutesGrupo7Usuarios))
	}
}

func TestProtectedRoutesGrupo7Usuarios_Wiring(t *testing.T) {
	app, userID := setupRouteWiringApp(t)
	noPermToken := mintWiringToken(t, userID, []string{})

	for _, r := range protectedRoutesGrupo7Usuarios {
		t.Run(r.method+" "+r.path, func(t *testing.T) {
			req := httptest.NewRequest(r.method, r.path, strings.NewReader(`{}`))
			req.Header.Set("Content-Type", "application/json")
			status, _ := safeTestRequest(t, app, req)
			if status != fiber.StatusUnauthorized {
				t.Errorf("sin token: status = %d, want 401", status)
			}

			req2 := httptest.NewRequest(r.method, r.path, strings.NewReader(`{}`))
			req2.Header.Set("Content-Type", "application/json")
			req2.Header.Set("Authorization", "Bearer "+noPermToken)
			status2, _ := safeTestRequest(t, app, req2)
			if status2 != fiber.StatusForbidden {
				t.Errorf("sin permiso %q: status = %d, want 403", r.permission, status2)
			}
		})
	}
}

func TestProtectedRoutesGrupo7Usuarios_ViewDoesNotOpenWriteRoutes(t *testing.T) {
	app, userID := setupRouteWiringApp(t)
	token := mintWiringToken(t, userID, []string{"usuarios_central.view"})

	for _, r := range protectedRoutesGrupo7Usuarios {
		t.Run(r.method+" "+r.path, func(t *testing.T) {
			req := httptest.NewRequest(r.method, r.path, strings.NewReader(`{}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+token)
			status, _ := safeTestRequest(t, app, req)
			if status != fiber.StatusForbidden {
				t.Errorf("status = %d, want 403 (usuarios_central.view no debe permitir %s %s)", status, r.method, r.path)
			}
		})
	}
}

// §30: cada permiso de usuarios_central abre EXCLUSIVAMENTE su propia ruta — ninguno debe
// funcionar como puerta trasera de los demás. crossPermissionMatrix mapea permiso concedido →
// rutas que NO debe abrir (todas las de protectedRoutesGrupo7Usuarios salvo la propia).
func TestProtectedRoutesGrupo7Usuarios_PermissionsDoNotCrossRoutes(t *testing.T) {
	app, userID := setupRouteWiringApp(t)

	for _, granted := range protectedRoutesGrupo7Usuarios {
		token := mintWiringToken(t, userID, []string{granted.permission})
		for _, other := range protectedRoutesGrupo7Usuarios {
			if other.permission == granted.permission {
				continue // esta sí debe abrir, ya cubierto en TestProtectedRoutesGrupo7Usuarios_Wiring
			}
			t.Run(granted.permission+" -> "+other.method+" "+other.path, func(t *testing.T) {
				req := httptest.NewRequest(other.method, other.path, strings.NewReader(`{}`))
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Authorization", "Bearer "+token)
				status, _ := safeTestRequest(t, app, req)
				if status != fiber.StatusForbidden {
					t.Errorf("status = %d, want 403 (%q no debe abrir %s %s)", status, granted.permission, other.method, other.path)
				}
			})
		}
	}
}

// ==================== PUT /users/:id — autoservicio vs. usuarios_central.update ====================

func TestUpdateUserRoute_SelfBypassesPermission_RealRouteTree(t *testing.T) {
	app, userID := setupRouteWiringApp(t)
	token := mintWiringToken(t, userID, []string{}) // sin ningún permiso

	req := httptest.NewRequest("PUT", fmt.Sprintf("/api/superadmin/users/%d", userID), strings.NewReader(`{"name":"Yo"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	status, _ := safeTestRequest(t, app, req)
	if status != fiber.StatusOK {
		t.Fatalf("status = %d, want 200 (autoservicio de nombre propio, sin permiso)", status)
	}
}

func TestUpdateUserRoute_OtherUserRequiresPermission_RealRouteTree(t *testing.T) {
	app, userID := setupRouteWiringApp(t)
	other := database.SuperAdminUser{Name: "Otro", Email: "otro-wiring@example.com", Role: "admin", Active: true}
	if err := other.SetPassword("password123"); err != nil {
		t.Fatal(err)
	}
	if err := database.CentralDB.Create(&other).Error; err != nil {
		t.Fatal(err)
	}

	noPermToken := mintWiringToken(t, userID, []string{})
	req := httptest.NewRequest("PUT", fmt.Sprintf("/api/superadmin/users/%d", other.ID), strings.NewReader(`{"name":"Cambiado"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+noPermToken)
	status, _ := safeTestRequest(t, app, req)
	if status != fiber.StatusForbidden {
		t.Fatalf("sin permiso: status = %d, want 403 (editar a otro usuario)", status)
	}

	withPermToken := mintWiringToken(t, userID, []string{"usuarios_central.update"})
	req2 := httptest.NewRequest("PUT", fmt.Sprintf("/api/superadmin/users/%d", other.ID), strings.NewReader(`{"name":"Cambiado"}`))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("Authorization", "Bearer "+withPermToken)
	status2, _ := safeTestRequest(t, app, req2)
	if status2 != fiber.StatusOK {
		t.Fatalf("con usuarios_central.update: status = %d, want 200", status2)
	}
}

// ==================== PUT /users/:id/system-role — RequireSuperAdminOnly() ====================

func TestSystemRoleRoute_AdminRejected_RealRouteTree(t *testing.T) {
	app, userID := setupRouteWiringAppWithRole(t, "admin")
	// "*" no debe alcanzar nada aquí: RequireSuperAdminOnly() exige Role=="superadmin" exacto,
	// nunca un permiso granular, ni siquiera el comodín.
	token := mintWiringTokenWithRole(t, userID, "admin", []string{"*"})

	req := httptest.NewRequest("PUT", fmt.Sprintf("/api/superadmin/users/%d/system-role", userID),
		strings.NewReader(`{"role":"superadmin"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	status, _ := safeTestRequest(t, app, req)
	if status != fiber.StatusForbidden {
		t.Fatalf("status = %d, want 403 (un admin nunca alcanza system-role)", status)
	}
}

func TestSystemRoleRoute_SuperadminNotBlockedByAuthz_RealRouteTree(t *testing.T) {
	app, userID := setupRouteWiringAppWithRole(t, "superadmin")
	token := mintWiringTokenWithRole(t, userID, "superadmin", nil)

	other := database.SuperAdminUser{Name: "Otro", Email: "otro-system-role@example.com", Role: "admin", Active: true}
	if err := other.SetPassword("password123"); err != nil {
		t.Fatal(err)
	}
	if err := database.CentralDB.Create(&other).Error; err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("PUT", fmt.Sprintf("/api/superadmin/users/%d/system-role", other.ID),
		strings.NewReader(`{"role":"superadmin"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	status, _ := safeTestRequest(t, app, req)
	if status == fiber.StatusUnauthorized || status == fiber.StatusForbidden {
		t.Fatalf("status = %d, no debería ser 401/403 (superadmin real sí alcanza system-role)", status)
	}
}
