package superadmin

import (
	"tukifac/internal/ajustes"
	consultaHandler "tukifac/internal/consulta/handler"
	exchangeRateHandler "tukifac/internal/exchangerate/handler"
	"tukifac/internal/payments"
	"tukifac/internal/plans"
	"tukifac/internal/saasadmin"
	"tukifac/internal/saasdocuments"
	"tukifac/internal/subscriptions"
	"tukifac/internal/superadmin/handler"
	ubigeo "tukifac/internal/ubigeo"
	"tukifac/pkg/middleware"

	"github.com/gofiber/fiber/v3"
)

func RegisterRoutes(app *fiber.App) {
	authHandler := handler.NewAuthSAHandler()
	dashHandler := handler.NewDashboardHandler()
	tenantHandler := handler.NewTenantHandler()
	migrationHandler := handler.NewMigrationHandler()
	ubigeoCentral := ubigeo.NewCentralHandler()
	saRoleHandler := handler.NewSARoleHandler()

	// Login público
	app.Post("/api/superadmin/login", authHandler.LoginAPI)

	// API protegida
	saAPI := app.Group("/api/superadmin", middleware.SuperAdminAuthAPI())
	// Fase 5 (etapa 2 — más lecturas + escrituras de bajo riesgo verificadas): ver matriz completa
	// en el informe de la fase. El resto de rutas de este archivo sin RequireSAPermission son
	// operaciones críticas/masivas deliberadamente no tocadas todavía, o casos señalados aparte.
	saAPI.Get("/platform-domains", middleware.RequireSAPermission("dashboard.view"), handler.PlatformDomainsAPI)
	saAPI.Get("/stats", middleware.RequireSAPermission("dashboard.view"), dashHandler.StatsAPI)
	// Fase 5 (etapa 1 — solo lectura): RequireSAPermission aplicado a un grupo pequeño de rutas
	// GET representativo de varios módulos, para validar el middleware antes de extenderlo. El
	// resto de rutas de este archivo sigue protegido únicamente por SuperAdminAuthAPI() (login),
	// tal como en las fases anteriores — no se tocó nada más.
	// Fase 5, etapa 3, Grupo 7, Paso E: superficie de usuarios centrales separada en 7 endpoints
	// dedicados. UpdateUserAPI (name/email) NO lleva RequireSAPermission a nivel de ruta a
	// propósito: el auto-servicio (editar el propio nombre/email) nunca requirió permiso — el
	// gate para editar a OTRO usuario vive dentro del handler (usuarios_central.update). Ver
	// comentario en auth_sa_handler.go.
	saAPI.Get("/users", middleware.RequireSAPermission("usuarios_central.view"), authHandler.ListUsersAPI)
	saAPI.Post("/users", middleware.RequireSAPermission("usuarios_central.create"), authHandler.CreateUserAPI)
	saAPI.Put("/users/:id", authHandler.UpdateUserAPI)
	saAPI.Put("/users/:id/role", middleware.RequireSAPermission("usuarios_central.change_role"), authHandler.ChangeUserRoleAPI)
	saAPI.Put("/users/:id/system-role", middleware.RequireSuperAdminOnly(), authHandler.ChangeUserSystemRoleAPI)
	saAPI.Patch("/users/:id/status", middleware.RequireSAPermission("usuarios_central.change_status"), authHandler.ChangeUserStatusAPI)
	saAPI.Post("/users/:id/password", middleware.RequireSAPermission("usuarios_central.reset_password"), authHandler.ResetUserPasswordAPI)
	saAPI.Delete("/users/:id", middleware.RequireSAPermission("usuarios_central.destroy"), authHandler.DestroyUserAPI)
	saAPI.Post("/me/password", authHandler.ChangeMyPasswordAPI)

	// RBAC del panel central (Fase 5, etapa 3, Grupo 7): CRUD de roles + asignación de permisos ya
	// llevan RequireSAPermission. La segunda capa (techo de delegación, CanDelegateAll) vive DENTRO
	// de DeleteAPI/SetRolePermissionsAPI, porque depende del CONTENIDO del rol/del body, no solo
	// del path — ver comentarios en sa_role_handler.go.
	saAPI.Get("/roles", middleware.RequireSAPermission("roles.view"), saRoleHandler.ListAPI)
	saAPI.Get("/roles/:id", middleware.RequireSAPermission("roles.view"), saRoleHandler.GetAPI)
	saAPI.Post("/roles", middleware.RequireSAPermission("roles.create"), saRoleHandler.CreateAPI)
	saAPI.Put("/roles/:id", middleware.RequireSAPermission("roles.update"), saRoleHandler.UpdateAPI)
	saAPI.Delete("/roles/:id", middleware.RequireSAPermission("roles.delete"), saRoleHandler.DeleteAPI)
	saAPI.Get("/roles/:id/permissions", middleware.RequireSAPermission("roles.view"), saRoleHandler.RolePermissionsAPI)
	saAPI.Put("/roles/:id/permissions", middleware.RequireSAPermission("roles.manage"), saRoleHandler.SetRolePermissionsAPI)
	saAPI.Get("/permissions", middleware.RequireSAPermission("roles.view"), saRoleHandler.PermissionsCatalogAPI)

	saAPI.Get("/tenants", middleware.RequireSAPermission("empresas.view"), tenantHandler.ListAPI)
	saAPI.Get("/tenants/conectados-sunat", middleware.RequireSAPermission("facturador.view"), tenantHandler.ListConectadosSunatAPI)
	saAPI.Get("/tenants/conectados-facturador", middleware.RequireSAPermission("facturador.view"), tenantHandler.ListConectadosSunatAPI)
	// Fase 5 (etapa 3, Grupo 1 — empresas/tenants): facturador.manage cubre configuración de
	// facturación/PSE de un tenant (verificado que ninguno de estos handlers toca credenciales
	// SUNAT — eso sigue en sync-facturador, ver más abajo).
	saAPI.Patch("/tenants/facturador-enabled", middleware.RequireSAPermission("facturador.manage"), tenantHandler.SetFacturadorEnabledAPI)
	saAPI.Get("/pse/empresas", middleware.RequireSAPermission("facturador.view"), tenantHandler.ListPSEEmpresasAPI)
	saAPI.Get("/pse/empresas/:id", middleware.RequireSAPermission("facturador.view"), tenantHandler.GetPSEEmpresaAPI)
	saAPI.Post("/pse/empresas", middleware.RequireSAPermission("facturador.manage"), tenantHandler.CreatePSEEmpresaAPI)
	saAPI.Put("/pse/empresas/:id", middleware.RequireSAPermission("facturador.manage"), tenantHandler.UpdatePSEEmpresaAPI)
	saAPI.Patch("/pse/empresas/:id/toggle", middleware.RequireSAPermission("facturador.manage"), tenantHandler.TogglePSEEmpresaAPI)
	saAPI.Get("/tenants/:id", middleware.RequireSAPermission("empresas.view"), tenantHandler.GetAPI)
	// empresas.master_access: permiso real y otorgable (no bypass-only) — ver comentario en
	// MasterAccessAPI. empresas.update NUNCA implica esto (el módulo "empresas" no tiene ".manage").
	saAPI.Post("/tenants/:id/master-access", middleware.RequireSAPermission("empresas.master_access"), tenantHandler.MasterAccessAPI)
	saAPI.Post("/tenants", middleware.RequireSAPermission("empresas.create"), tenantHandler.CreateAPI)
	saAPI.Put("/tenants/:id", middleware.RequireSAPermission("empresas.update"), tenantHandler.UpdateAPI)
	// destroy-complete: bypass exclusivo de superadmin, NUNCA un permiso otorgable — ver
	// comentario en DestroyCompleteAPI y en middleware.RequireSuperAdminOnly.
	saAPI.Post("/tenants/:id/destroy-complete", middleware.RequireSuperAdminOnly(), tenantHandler.DestroyCompleteAPI)
	saAPI.Patch("/tenants/:id/status", middleware.RequireSAPermission("empresas.change_status"), tenantHandler.ToggleStatusAPI)
	saAPI.Get("/tenants/:id/modules", middleware.RequireSAPermission("empresas.view"), tenantHandler.GetModulesAPI)
	// SetModuleAPI cambia entitlements del ERP de un tenant — mismo permiso que editar el tenant.
	saAPI.Post("/tenants/:id/modules", middleware.RequireSAPermission("empresas.update"), tenantHandler.SetModuleAPI)
	// Fase 5 (etapa 3, Grupo 4): hallazgo — /tenants/:id/migrate es una RUTA DUPLICADA de
	// /migrations/:tenantId/migrate (mismo efecto real: migra un tenant, por un código distinto,
	// TenantService.RunMigrations en vez de MigrationFleetService.MigrateOne). Se cierra con el
	// mismo permiso que su gemela para no dejar un bypass abierto.
	saAPI.Post("/tenants/:id/migrate", middleware.RequireSAPermission("migraciones.run"), tenantHandler.MigrateAPI)
	// tenants/migrate-all: hallazgo — sin límite alguno (TODA la flota), es la ruta gemela de
	// /backfills/run-all. Confirmado con el usuario: queda pendiente, mismo criterio que las
	// operaciones de flota ya diferidas en grupos anteriores (RunJobsAPI, check-expirations).
	saAPI.Post("/tenants/migrate-all", tenantHandler.MigrateAllAPI)
	saAPI.Get("/backfills", middleware.RequireSAPermission("migraciones.view"), tenantHandler.ListBackfillsAPI)
	saAPI.Post("/tenants/:id/backfill", middleware.RequireSAPermission("migraciones.backfill"), tenantHandler.RunBackfillAPI)
	// backfills/run-all: confirmado sin límite (TODA la flota, engine.RunBackfillFleet sin cap) —
	// pendiente, mismo criterio que tenants/migrate-all arriba.
	saAPI.Post("/backfills/run-all", tenantHandler.RunBackfillAllAPI)
	saAPI.Post("/tenants/:id/cleanup-abandoned-orders", tenantHandler.CleanupAbandonedOrdersAPI)
	saAPI.Post("/maintenance/cleanup-abandoned-orders", tenantHandler.CleanupAbandonedOrdersAllAPI)

	saAPI.Get("/migrations", middleware.RequireSAPermission("migraciones.view"), migrationHandler.ListAPI)
	saAPI.Get("/migrations/summary", middleware.RequireSAPermission("migraciones.view"), migrationHandler.SummaryAPI)
	saAPI.Get("/migrations/jobs", middleware.RequireSAPermission("migraciones.view"), migrationHandler.ListJobsAPI)
	saAPI.Get("/migrations/jobs/:jobId", middleware.RequireSAPermission("migraciones.view"), migrationHandler.GetJobAPI)
	// drift-scan: verificado en el código — DryRun queda hardcodeado en true tanto en el camino
	// síncrono como en el job asíncrono (StartDriftScanJob → runDriftScanJob(..., true)). Nunca
	// repara ni migra nada, sin importar cuántos tenants escanee — es un reporte de diagnóstico,
	// confirmado con el usuario: migraciones.view (no migraciones.run).
	saAPI.Post("/migrations/drift-scan", middleware.RequireSAPermission("migraciones.view"), migrationHandler.DriftScanAPI)
	// bulk/repair, bulk/repair-drifted, bulk/retry-failed: acotadas (lista elegida por el caller,
	// o límite=50 por defecto) y reutilizan exactamente las mismas funciones que ya gatean
	// migraciones.repair/run individualmente — confirmado con el usuario: mismo permiso.
	saAPI.Post("/migrations/bulk/repair", middleware.RequireSAPermission("migraciones.repair"), migrationHandler.BulkRepairAPI)
	saAPI.Post("/migrations/bulk/repair-drifted", middleware.RequireSAPermission("migraciones.repair"), migrationHandler.BulkRepairDriftedAPI)
	saAPI.Post("/migrations/bulk/retry-failed", middleware.RequireSAPermission("migraciones.run"), migrationHandler.BulkRetryFailedAPI)
	// resume-fleet: no es por-tenant (reinicia el circuit breaker GLOBAL de migraciones) —
	// confirmado con el usuario: mismo permiso que resume individual.
	saAPI.Post("/migrations/resume-fleet", middleware.RequireSAPermission("migraciones.resume"), migrationHandler.ResumeFleetAPI)
	saAPI.Get("/migrations/:tenantId/history", middleware.RequireSAPermission("migraciones.view"), migrationHandler.HistoryAPI)
	saAPI.Get("/migrations/:tenantId/drift", middleware.RequireSAPermission("migraciones.view"), migrationHandler.DriftAPI)
	saAPI.Post("/migrations/:tenantId/repair", middleware.RequireSAPermission("migraciones.repair"), migrationHandler.RepairAPI)
	saAPI.Post("/migrations/:tenantId/retry", middleware.RequireSAPermission("migraciones.run"), migrationHandler.RetryAPI)
	saAPI.Post("/migrations/:tenantId/migrate", middleware.RequireSAPermission("migraciones.run"), migrationHandler.MigrateAPI)
	saAPI.Post("/migrations/:tenantId/pause", middleware.RequireSAPermission("migraciones.pause"), migrationHandler.PauseAPI)
	saAPI.Post("/migrations/:tenantId/resume", middleware.RequireSAPermission("migraciones.resume"), migrationHandler.ResumeAPI)
	saAPI.Get("/tenants/:id/sunat-config", middleware.RequireSAPermission("facturador.view"), tenantHandler.GetSunatConfigAPI)
	saAPI.Put("/tenants/:id/sunat-config", middleware.RequireSAPermission("facturador.manage"), tenantHandler.UpdateSunatConfigAPI)
	saAPI.Patch("/tenants/:id/sunat-env", middleware.RequireSAPermission("facturador.manage"), tenantHandler.PatchSunatEnvAPI)
	// TestFiscalConnectionAPI verificado: solo prueba conectividad (RUC + config del facturador),
	// no persiste ni modifica nada — igual que un GET en efecto real, aunque el método sea POST.
	saAPI.Post("/tenants/:id/fiscal-test-connection", middleware.RequireSAPermission("facturador.view"), tenantHandler.TestFiscalConnectionAPI)
	// SyncFacturadorAPI recibe certificado digital, llave privada, contraseña del certificado y
	// credenciales SOL de SUNAT en el body — protegido con facturador.manage igual que el resto
	// de configuración de facturación, pero deliberadamente SIN auditoría de payload (ni siquiera
	// nombres de campo): cualquier log de este endpoint debe limitarse a "ocurrió", nunca a "qué
	// contenía", para no crear un rastro de credenciales en AuditLog.
	saAPI.Post("/tenants/:id/sync-facturador", middleware.RequireSAPermission("facturador.manage"), tenantHandler.SyncFacturadorAPI)

	// Ubigeo Perú (para formularios de empresas). Dato de referencia público (departamentos/
	// provincias/distritos del Perú), sin relación intrínseca con ningún módulo de negocio — lo
	// asigno a "empresas.view" porque el propio código las describe como soporte de "formularios
	// de empresas" (ver comentario original). Marcado como decisión de criterio, no un mapeo
	// obvio del catálogo — ver informe de esta etapa.
	saAPI.Get("/ubigeo/regiones", middleware.RequireSAPermission("empresas.view"), ubigeoCentral.RegionesAPI)
	saAPI.Get("/ubigeo/provincias", middleware.RequireSAPermission("empresas.view"), ubigeoCentral.ProvinciasAPI)
	saAPI.Get("/ubigeo/distritos", middleware.RequireSAPermission("empresas.view"), ubigeoCentral.DistritosAPI)

	// Ajustes del sistema central (nombre, slogan, token_consulta, etc.)
	ajustes.RegisterRoutes(saAPI)

	// Consulta DNI/RUC (apiperu.dev) — lookup externo de solo lectura (no crea ni modifica nada
	// local), usado al completar el formulario de alta de una empresa. Mismo criterio que ubigeo:
	// asignado a "empresas.view" por ser una consulta, no una creación — ver informe de esta etapa.
	consultaH := consultaHandler.NewConsultaHandler()
	saAPI.Post("/consulta/dni", middleware.RequireSAPermission("empresas.view"), consultaH.ConsultaDNIAPI)
	saAPI.Post("/consulta/ruc", middleware.RequireSAPermission("empresas.view"), consultaH.ConsultaRUCAPI)

	exchangeRateH := exchangeRateHandler.NewExchangeRateHandler()
	saAPI.Get("/exchange-rates/today", middleware.RequireSAPermission("dashboard.view"), exchangeRateH.TodayAPI)
	// RefreshAPI verificado: fuerza recalcular/guardar el tipo de cambio del día desde una fuente
	// externa — actualiza un único valor de referencia, sin efecto destructivo ni irreversible.
	saAPI.Post("/exchange-rates/refresh", middleware.RequireSAPermission("ajustes.manage"), exchangeRateH.RefreshAPI)

	// Planes, módulos del catálogo, suscripciones y pagos
	plans.RegisterRoutes(saAPI)
	subscriptions.RegisterRoutes(saAPI)
	saasadmin.RegisterRoutes(saAPI)
	saasdocuments.RegisterRoutes(saAPI)
	payments.RegisterRoutes(saAPI)
	fiscalH := handler.NewFiscalHandler()
	saFiscal := saAPI.Group("/fiscal")
	saFiscal.Get("/stats", middleware.RequireSAPermission("fiscal.view"), fiscalH.StatsAPI)
	saFiscal.Get("/health", middleware.RequireSAPermission("fiscal.view"), fiscalH.HealthAPI)
	saFiscal.Get("/operations/summary", middleware.RequireSAPermission("fiscal.view"), fiscalH.OperationsSummaryAPI)
	saFiscal.Get("/operations/tenants", middleware.RequireSAPermission("fiscal.view"), fiscalH.OperationsTenantsAPI)
	saFiscal.Get("/operations/queue", middleware.RequireSAPermission("fiscal.view"), fiscalH.OperationsQueueAPI)
	saFiscal.Get("/alerts", middleware.RequireSAPermission("fiscal.view"), fiscalH.AlertsAPI)
	saFiscal.Get("/documents", middleware.RequireSAPermission("fiscal.view"), fiscalH.ListDocumentsAPI)
	saFiscal.Get("/documents/:uuid/audit-timeline", middleware.RequireSAPermission("fiscal.view"), fiscalH.AuditTimelineAPI)
	saFiscal.Get("/documents/:uuid/download/:type", middleware.RequireSAPermission("fiscal.view"), fiscalH.DownloadAPI)
	// documents/bulk/:action y documents/:uuid/:action multiplexan varias acciones de distinta
	// criticidad (send/retry/force/email/poll/cancel) bajo un mismo :action dinámico — un solo
	// RequireSAPermission de ruta no puede exigir fiscal.retry para unas y fiscal.cancel para
	// otras a la vez. No se puede resolver a nivel de ruta sin dejar un hueco (alguien con solo
	// fiscal.retry podría llamar action=cancel por la misma ruta). Quedan pendientes junto con las
	// críticas — la autorización por acción deberá vivir dentro del handler en la próxima etapa.
	saFiscal.Post("/documents/bulk/:action", fiscalH.BulkActionAPI)
	saFiscal.Get("/documents/:uuid", middleware.RequireSAPermission("fiscal.view"), fiscalH.DocumentDetailAPI)
	saFiscal.Post("/documents/:uuid/:action", fiscalH.DocumentActionAPI)
}
