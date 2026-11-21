// Package accounting registra las rutas del módulo Contabilidad (módulo «accounting»).
package accounting

import (
	"tukifac/internal/accounting/handler"
	"tukifac/pkg/middleware"

	"github.com/gofiber/fiber/v3"
)

// RegisterRoutes monta /api/accounting/*. TODAS las rutas exigen el módulo «accounting» (el tenant
// sin el módulo en su plan recibe 403) y un permiso propio por pantalla/acción.
func RegisterRoutes(api fiber.Router) {
	h := handler.New()
	RegisterRoutesWith(api, h)
}

// RegisterRoutesWith permite inyectar el handler (pruebas).
func RegisterRoutesWith(api fiber.Router, h *handler.Handler) {
	mod := middleware.RequireModule("accounting")
	perm := middleware.RequirePermission

	api.Get("/accounting/registers/sales", mod, perm("accounting.sales_register"), h.SalesRegister)
	api.Get("/accounting/registers/purchases", mod, perm("accounting.purchases_register"), h.PurchasesRegister)
	api.Get("/accounting/summary", mod, perm("accounting.summary_report"), h.Summary)
	api.Get("/accounting/formats", mod, perm("accounting.export_formats"), h.Formats)
	api.Get("/accounting/formats/export", mod, perm("accounting.export_formats"), h.ExportFormat)
	api.Get("/accounting/ledger", mod, perm("accounting.ledger"), h.Ledger)

	api.Get("/accounting/settings", mod, perm("accounting.settings"), h.GetSettings)
	api.Put("/accounting/settings", mod, perm("accounting.settings"), h.SaveSettings)

	api.Get("/accounting/sire/settings", mod, perm("accounting.settings"), h.GetSireSettings)
	api.Put("/accounting/sire/settings", mod, perm("accounting.settings"), h.SaveSireSettings)
	api.Get("/accounting/sire/:book/periods", mod, perm("accounting.sire_view"), h.SirePeriods)
	api.Get("/accounting/sire/:book/tickets", mod, perm("accounting.sire_view"), h.SireTickets)
	api.Post("/accounting/sire/:book/query", mod, perm("accounting.sire_view"), h.SireQuery)
	api.Post("/accounting/sire/:book/tickets", mod, perm("accounting.sire_manage"), h.SireRequestTicket)
	api.Post("/accounting/sire/sale/accept", mod, perm("accounting.sire_manage"), h.SireAccept)
}
