package dashboard

import (
	"tukifac/internal/dashboard/handler"
	"tukifac/pkg/middleware"

	"github.com/gofiber/fiber/v3"
)

// RegisterRoutes registra las rutas del dashboard. El catálogo ya define dashboard.view (ver
// internal/users/service/role_service.go) pero ningún endpoint lo exigía. Se agrega
// RequirePermission; el filtrado de datos por usuario no-Administrador (dentro del handler) no
// cambia.
func RegisterRoutes(api fiber.Router) {
	h := handler.NewDashboardHandler()
	view := middleware.RequirePermission("dashboard.view")
	api.Get("/dashboard/stats", view, h.StatsAPI)
	api.Get("/dashboard/analytics", view, h.AnalyticsAPI)
}
