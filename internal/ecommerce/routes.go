package ecommerce

import (
	"tukifac/internal/ecommerce/handler"
	"tukifac/pkg/middleware"

	"github.com/gofiber/fiber/v3"
)

// RegisterRoutes rutas de administración (autenticadas, dentro de Tukifac). Cada una exige el
// módulo "ecommerce" habilitado en el plan del tenant — mismo mecanismo que usa "billing".
//
// ecommerce.{view,manage} gatea la CONFIGURACIÓN de la tienda (ajustes, logo, banners).
//
// Pedidos web: RBAC granular por responsabilidad (Contrato ecommerce v2 §7,
// docs/ECOMMERCE-EVOLUTION-CONTRACT.md) — reemplaza el permiso único "ecommerce.orders"
// (deprecado, se mantiene solo por compatibilidad, ver v138_ecommerce_orders_rbac_v2.go), que no
// permitía distinguir "solo preparar" (Almacenero) de "puede despachar/convertir/devolver"
// (Vendedor/Supervisor). GET/print-data exigen orders_view; convertir exige orders_convert; la
// transición de estado (PUT status) exige AL MENOS uno de los permisos de gestión de pedidos como
// filtro de entrada, y el handler valida ADEMÁS el permiso específico de la transición pedida
// (ver UpdateOrderStatusAPI) porque un mismo endpoint sirve confirmar/preparar/despachar/devolver.
func RegisterRoutes(api fiber.Router) {
	h := handler.NewEcommerceHandler()
	mod := middleware.RequireModule("ecommerce")
	view := middleware.RequirePermission("ecommerce.view")
	manage := middleware.RequirePermission("ecommerce.manage")
	ordersView := middleware.RequirePermission("ecommerce.orders_view")
	ordersConvert := middleware.RequirePermission("ecommerce.orders_convert")
	ordersTransition := middleware.RequireAnyPermission(
		"ecommerce.orders_manage", "ecommerce.orders_prepare",
		"ecommerce.orders_dispatch", "ecommerce.orders_return",
	)

	api.Get("/ecommerce/settings", mod, view, h.GetSettingsAPI)
	api.Put("/ecommerce/settings", mod, manage, h.UpdateSettingsAPI)
	api.Post("/ecommerce/settings/logo", mod, manage, h.UploadLogoAPI)
	api.Post("/ecommerce/settings/background", mod, manage, h.UploadBackgroundAPI)

	api.Get("/ecommerce/sliders", mod, view, h.ListSlidersAPI)
	api.Post("/ecommerce/sliders", mod, manage, h.CreateSliderAPI)
	api.Put("/ecommerce/sliders/:id", mod, manage, h.UpdateSliderAPI)
	api.Delete("/ecommerce/sliders/:id", mod, manage, h.DeleteSliderAPI)
	api.Post("/ecommerce/sliders/reorder", mod, manage, h.ReorderSlidersAPI)

	api.Get("/ecommerce/orders", mod, ordersView, h.ListOrdersAPI)
	api.Get("/ecommerce/orders/:id/print-data", mod, ordersView, h.OrderPrintDataAPI)
	api.Put("/ecommerce/orders/:id/status", mod, ordersTransition, h.UpdateOrderStatusAPI)
	api.Post("/ecommerce/orders/:id/convert", mod, ordersConvert, h.ConvertOrderAPI)
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
	// Meta tags reales para crawlers (WhatsApp/Facebook/Twitter) — ver PublicPreviewAPI.
	// Nginx reenvía acá SOLO peticiones de bots detectados por User-Agent.
	g.Get("/preview", h.PublicPreviewAPI)
}
