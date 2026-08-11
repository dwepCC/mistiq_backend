package ecommerce

import (
	"tukifac/internal/ecommerce/handler"
	"tukifac/pkg/middleware"

	"github.com/gofiber/fiber/v3"
)

// RegisterRoutes rutas de administración (autenticadas, dentro de Tukifac). Cada una exige el
// módulo "ecommerce" habilitado en el plan del tenant — mismo mecanismo que usa "billing".
func RegisterRoutes(api fiber.Router) {
	h := handler.NewEcommerceHandler()
	mod := middleware.RequireModule("ecommerce")

	api.Get("/ecommerce/settings", mod, h.GetSettingsAPI)
	api.Put("/ecommerce/settings", mod, h.UpdateSettingsAPI)
	api.Post("/ecommerce/settings/logo", mod, h.UploadLogoAPI)
	api.Post("/ecommerce/settings/background", mod, h.UploadBackgroundAPI)

	api.Get("/ecommerce/sliders", mod, h.ListSlidersAPI)
	api.Post("/ecommerce/sliders", mod, h.CreateSliderAPI)
	api.Put("/ecommerce/sliders/:id", mod, h.UpdateSliderAPI)
	api.Delete("/ecommerce/sliders/:id", mod, h.DeleteSliderAPI)
	api.Post("/ecommerce/sliders/reorder", mod, h.ReorderSlidersAPI)

	api.Get("/ecommerce/orders", mod, h.ListOrdersAPI)
	api.Get("/ecommerce/orders/:id/print-data", mod, h.OrderPrintDataAPI)
	api.Put("/ecommerce/orders/:id/status", mod, h.UpdateOrderStatusAPI)
	api.Post("/ecommerce/orders/:id/convert", mod, h.ConvertOrderAPI)
}

// RegisterPublicRoutes rutas de la tienda pública (sin JWT), resueltas por tenant vía
// TenantResolver (subdominio) + RequireEcommerceAvailable (módulo + ajustes + suscripción).
func RegisterPublicRoutes(app fiber.Router) {
	h := handler.NewEcommerceHandler()
	g := app.Group("/public/ecommerce", middleware.RequireTenant(), middleware.RequireEcommerceAvailable())
	g.Get("/settings", h.PublicSettingsAPI)
	g.Get("/categories", h.PublicCategoriesAPI)
	g.Get("/price-bounds", h.PublicPriceBoundsAPI)
	g.Get("/products", h.PublicProductsAPI)
	g.Post("/orders", h.CreatePublicOrderAPI)
}
