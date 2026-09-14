package modules

import (
	"tukifac/internal/modules/handler"
	"tukifac/pkg/middleware"

	"github.com/gofiber/fiber/v3"
)

// RegisterRoutes registra las rutas de activación de módulos del plan. Antes Toggle/Ping exigían
// el rol hardcodeado "Administrador" (string exacto) y ForwardAPI (proxy genérico hacia el
// microservicio externo del módulo, firmado con su propio API key) no exigía ningún rol — cualquier
// usuario autenticado del tenant podía invocarlo. El catálogo ya define modules.manage (ver
// internal/users/service/role_service.go); con el backfill de la migración v131, "Administrador"
// sigue teniendo acceso (tiene todos los permisos) y además queda delegable a otros roles.
func RegisterRoutes(api fiber.Router) {
	h := handler.NewModuleHandler()
	manage := middleware.RequirePermission("modules.manage")
	api.Post("/modules/:key/toggle", manage, h.ToggleAPI)
	api.Get("/modules/:key/ping", manage, h.PingAPI)
	api.All("/modules/:key/forward/*", manage, h.ForwardAPI)
}
