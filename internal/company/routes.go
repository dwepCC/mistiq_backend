package company

import (
	"tukifac/internal/company/handler"
	"tukifac/pkg/middleware"

	"github.com/gofiber/fiber/v3"
)

// RegisterRoutes registra las rutas de configuración de empresa.
//
// Antes no tenían NINGÚN chequeo de rol (ni siquiera de módulo) — cualquier usuario autenticado
// del tenant podía editar credenciales SUNAT, sucursales y series. Un primer intento gateó TODOS
// los GET con company.view — pero GetConfigAPI/GetSunatAPI/GetInvoicingAPI/ListBranchesAPI/
// ListSeriesAPI no exponen ningún secreto (las credenciales SOL/certificado nunca se leen de
// vuelta al cliente, ver internal/company/service/company_service.go) y son un prerrequisito para
// CUALQUIER venta/compra/documento — el propio código de ListSeriesAPI ya lo dice: "una serie
// desactivada no debe poder elegirse al emitir una venta [...] o cualquier otro documento". Exigir
// company.view ahí rompía Nueva venta/Nota de venta/POS para cualquier rol sin ese permiso (que no
// tiene nada que ver con vender, es para la pantalla de Ajustes → Empresa). Quedan abiertas a
// cualquier usuario autenticado del tenant, igual que products/contacts/branches ya lo están para
// lectura; company.edit sigue exigido en todas las mutaciones.
func RegisterRoutes(api fiber.Router) {
	h := handler.NewCompanyHandler()
	edit := middleware.RequirePermission("company.edit")

	api.Get("/company/config", h.GetConfigAPI)
	api.Put("/company/config", edit, h.UpdateConfigAPI)
	api.Put("/company/receipt-wallet", edit, h.UpdateReceiptWalletAPI)
	api.Post("/company/receipt-wallet/qr", edit, h.UploadReceiptWalletQRAPI)
	api.Post("/company/logo", edit, h.UploadCompanyLogoAPI)
	api.Delete("/company/logo", edit, h.DeleteCompanyLogoAPI)
	api.Get("/company/sunat", h.GetSunatAPI)
	api.Get("/company/invoicing", h.GetInvoicingAPI)
	api.Put("/company/sunat", edit, h.UpdateSunatAPI)
	// POST /company/sync-facturador eliminado: la sincronización con Lycet se hace solo desde el panel central.
	api.Get("/company/branches", h.ListBranchesAPI)
	api.Post("/company/branches", edit, h.CreateBranchAPI)
	api.Put("/company/branches/:id", edit, h.UpdateBranchAPI)
	api.Delete("/company/branches/:id", edit, h.DeleteBranchAPI)
	api.Get("/company/series/document-types", h.ListSeriesDocumentTypesAPI)
	api.Get("/company/series", h.ListSeriesAPI)
	api.Post("/company/series", edit, h.CreateSeriesAPI)
	api.Put("/company/series/:id", edit, h.UpdateSeriesAPI)
	api.Put("/company/series/:id/default", edit, h.SetDefaultSeriesAPI)
	api.Delete("/company/series/:id", edit, h.DeleteSeriesAPI)
}
