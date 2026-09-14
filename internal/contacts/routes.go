package contacts

import (
	"tukifac/internal/contacts/handler"
	"tukifac/pkg/middleware"

	"github.com/gofiber/fiber/v3"
)

// RegisterRoutes registra las rutas de contactos.
//
// Antes solo exigían el módulo del plan ("contacts"), nunca el rol del usuario — el catálogo ya
// define contacts.{view,create,edit,delete} y el frontend ya deja marcarlos en Roles, pero no
// tenían ningún efecto aquí. Un primer fix agregó RequirePermission a secas — mismo permiso para
// todos, sin importar de dónde venga la sesión. Eso rompió Tukichef: un staff logueado por PIN
// (frontend_restaurant_tenant) nunca tiene claims.Permissions poblado (usa pkg/restaurantperm, no
// TenantPermission), así que quedaba SIEMPRE afuera de buscar/crear un cliente para su venta —
// regresión real detectada en producción. RequireContactsAccess (pkg/middleware/contacts_access.go)
// agrega el mismo puente que ya usan sales/cashbank: permiso tenant O staff de restaurante con
// o.c (crear pedido). "delete" queda sin puente (no es una operación de POS).
func RegisterRoutes(api fiber.Router) {
	h := handler.NewContactHandler()
	mod := middleware.RequireModule("contacts")
	loadRest := middleware.LoadRestaurantPermissions()
	view := middleware.RequireContactsAccess("view")
	create := middleware.RequireContactsAccess("create")
	edit := middleware.RequireContactsAccess("edit")
	del := middleware.RequirePermission("contacts.delete")

	api.Get("/contacts/default", mod, loadRest, view, h.DefaultClientAPI)
	api.Get("/contacts", mod, loadRest, view, h.SearchAPI)
	api.Get("/contacts/:id", mod, loadRest, view, h.GetAPI)
	api.Post("/contacts", mod, loadRest, create, h.CreateAPI)
	// Import masivo antes de /contacts/:id para que «bulk-import» no se lea como un id.
	api.Post("/contacts/bulk-import", mod, loadRest, create, h.BulkImportAPI)
	api.Put("/contacts/:id", mod, loadRest, edit, h.UpdateAPI)
	api.Post("/contacts/:id/photo", mod, loadRest, edit, h.UploadPhotoAPI)
	api.Delete("/contacts/:id", mod, del, h.DeleteAPI)
	api.Patch("/contacts/:id/toggle", mod, loadRest, edit, h.ToggleAPI)
}
