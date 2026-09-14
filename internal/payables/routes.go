package payables

import (
	"tukifac/internal/payables/handler"
	"tukifac/pkg/middleware"

	"github.com/gofiber/fiber/v3"
)

// RegisterRoutes CxP (cuentas por pagar) — ligado a compras (módulo purchases), mismo patrón que
// internal/purchases/routes.go (no usa LoadRestaurantPermissions: las compras no aplican a
// Tukichef).
//
// Antes reutilizaba purchases.view/purchases.create: un rol de Compras quedaba automáticamente
// habilitado para pagar cuentas por pagar (acción financiera), sin forma de separarlo. Catálogo
// propio: payables.{view,pay}.
func RegisterRoutes(api fiber.Router) {
	h := handler.NewPayableHandler()
	mod := middleware.RequireModule("purchases")

	api.Get("/payables",
		mod, middleware.RequirePermission("payables.view"), h.ListAPI)
	api.Get("/payables/summary",
		mod, middleware.RequirePermission("payables.view"), h.SummaryAPI)
	api.Post("/payables/:purchaseId/pay",
		mod, middleware.RequirePermission("payables.pay"), h.PayAPI)
}
