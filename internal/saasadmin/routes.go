package saasadmin

import (
	"tukifac/internal/saasadmin/handler"
	"tukifac/pkg/middleware"

	"github.com/gofiber/fiber/v3"
)

func RegisterRoutes(saAPI fiber.Router) {
	h := handler.NewSettingsHandler()
	saAPI.Get("/saas-settings", middleware.RequireSAPermission("ajustes.view"), h.GetAPI)
	// PutAPI verificado (saas.PlatformSettings): recordatorios, métodos de pago, cuentas
	// bancarias, soporte — NO incluye la operations-key (eso vive en un endpoint aparte, ver
	// abajo, y sigue reservado exclusivamente al bypass de superadmin, fuera de este sistema).
	saAPI.Put("/saas-settings", middleware.RequireSAPermission("ajustes.manage"), h.PutAPI)
	// Reservado EXCLUSIVAMENTE al bypass de superadmin (Fase 0/1, ratificado en Fase 5 etapa 3) —
	// nunca RequireSAPermission, ni siquiera ajustes.manage.
	saAPI.Put("/saas-settings/operations-key", middleware.RequireSuperAdminOnly(), h.SetOperationsKeyAPI)
	// UploadQR/UploadLogo verificados: solo suben una imagen y actualizan su URL en la config —
	// sin efecto financiero ni destructivo.
	saAPI.Post("/saas-settings/upload-qr", middleware.RequireSAPermission("ajustes.manage"), h.UploadQR)
	saAPI.Post("/saas-settings/upload-logo", middleware.RequireSAPermission("ajustes.manage"), h.UploadLogo)
	// RunJobsAPI corre los jobs de recordatorios/suspensión/vencimientos sobre TODA la flota —
	// mismo perfil que una operación masiva. Pendiente (fuera del Grupo 1).
	saAPI.Post("/cron/saas-jobs", h.RunJobsAPI)
	// UnblockTenantAPI restaura acceso a un tenant bloqueado — mismo permiso que un cambio de
	// estado de empresa (Grupo 1).
	saAPI.Post("/tenants/:id/unblock", middleware.RequireSAPermission("empresas.change_status"), h.UnblockTenantAPI)
}
