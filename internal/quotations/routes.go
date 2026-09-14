package quotations

import (
	"tukifac/internal/quotations/handler"
	"tukifac/pkg/middleware"

	"github.com/gofiber/fiber/v3"
)

// RegisterRoutes registra las rutas de cotizaciones. Antes reutilizaba sales.view/sales.create —
// un rol con permiso de Ventas podía crear/editar/eliminar/convertir cotizaciones aunque el
// tenant quisiera separar ambas cosas, y viceversa (dar solo Cotizaciones sin dar Ventas era
// imposible). Catálogo propio: quotations.{view,create,edit,delete,convert}.
func RegisterRoutes(api fiber.Router) {
	h := handler.NewQuotationHandler()
	mod := middleware.RequireModule("sales")
	view := middleware.RequirePermission("quotations.view")
	create := middleware.RequirePermission("quotations.create")
	edit := middleware.RequirePermission("quotations.edit")
	del := middleware.RequirePermission("quotations.delete")
	convert := middleware.RequirePermission("quotations.convert")

	api.Get("/quotations", mod, view, h.ListAPI)
	api.Get("/quotations/:id", mod, view, h.GetAPI)
	api.Post("/quotations", mod, create, h.CreateAPI)
	api.Patch("/quotations/:id", mod, edit, h.UpdateAPI)
	api.Delete("/quotations/:id", mod, del, h.DeleteAPI)
	api.Post("/quotations/:id/convert", mod, convert, h.ConvertAPI)
	api.Post("/quotations/:id/email-receipt", mod, view, h.EmailReceiptAPI)
}
