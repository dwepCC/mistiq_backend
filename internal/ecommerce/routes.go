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
	ordersDispatch := middleware.RequirePermission("ecommerce.orders_dispatch")
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
	api.Get("/ecommerce/orders/:id", mod, ordersView, h.GetOrderAPI)
	api.Get("/ecommerce/orders/:id/print-data", mod, ordersView, h.OrderPrintDataAPI)
	api.Put("/ecommerce/orders/:id/status", mod, ordersTransition, h.UpdateOrderStatusAPI)
	api.Post("/ecommerce/orders/:id/convert", mod, ordersConvert, h.ConvertOrderAPI)
	// Despacho operativo (Fase 8) — permiso fijo (a diferencia de status, esta acción no varía).
	api.Post("/ecommerce/orders/:id/dispatch", mod, ordersDispatch, h.CreateDispatchAPI)
	api.Patch("/ecommerce/dispatches/:id", mod, ordersDispatch, h.UpdateDispatchAPI)
	// Tracking/transición de despacho (Fase 9) — DESPACHADO->EN_TRANSITO->ENTREGADO, separado de
	// PATCH (metadata) a propósito.
	api.Put("/ecommerce/dispatches/:id/status", mod, ordersDispatch, h.UpdateDispatchStatusAPI)
}

// RegisterPublicRoutes rutas de la tienda pública (sin JWT de staff), resueltas por tenant vía
// TenantResolver (subdominio) + RequireEcommerceAvailable (módulo + ajustes + suscripción).
//
// Cuenta de cliente (Contrato ecommerce v2 §1.4, Fase 4): auth SEPARADA de TenantAuthAPI/staff —
// mismo grupo /public/ecommerce, pero /account/* exige EcommerceCustomerAuthRequired (token propio,
// secreto propio, jamás aceptado por rutas de TenantUser). /orders usa
// EcommerceCustomerAuthOptional: sirve invitado Y cliente logueado por el mismo endpoint —
// CreatePublicOrderAPI decide según si hay sesión de cliente en Locals.
func RegisterPublicRoutes(app fiber.Router) {
	h := handler.NewEcommerceHandler()
	g := app.Group("/public/ecommerce", middleware.RequireTenant(), middleware.RequireEcommerceAvailable())
	g.Get("/settings", h.PublicSettingsAPI)
	g.Get("/categories", h.PublicCategoriesAPI)
	g.Get("/price-bounds", h.PublicPriceBoundsAPI)
	g.Get("/products", h.PublicProductsAPI)
	g.Post("/orders", middleware.EcommerceCustomerAuthOptional(), h.CreatePublicOrderAPI)
	// Meta tags reales para crawlers (WhatsApp/Facebook/Twitter) — ver PublicPreviewAPI.
	// Nginx reenvía acá SOLO peticiones de bots detectados por User-Agent.
	g.Get("/preview", h.PublicPreviewAPI)

	g.Post("/auth/register", h.RegisterCustomerAPI)
	g.Post("/auth/login", h.LoginCustomerAPI)

	acc := g.Group("/account", middleware.EcommerceCustomerAuthRequired())
	acc.Get("/me", h.CustomerMeAPI)
	acc.Get("/addresses", h.ListCustomerAddressesAPI)
	acc.Post("/addresses", h.CreateCustomerAddressAPI)
	acc.Put("/addresses/:id", h.UpdateCustomerAddressAPI)
	acc.Delete("/addresses/:id", h.DeleteCustomerAddressAPI)
	acc.Get("/orders", h.ListCustomerOrdersAPI)
	acc.Get("/orders/:id", h.GetCustomerOrderAPI)
	acc.Post("/orders/link", h.LinkGuestOrderAPI)
}
