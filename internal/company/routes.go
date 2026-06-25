package company

import (
	"tukifac/internal/company/handler"

	"github.com/gofiber/fiber/v3"
)

func RegisterRoutes(api fiber.Router) {
	h := handler.NewCompanyHandler()
	api.Get("/company/config", h.GetConfigAPI)
	api.Put("/company/config", h.UpdateConfigAPI)
	api.Put("/company/receipt-wallet", h.UpdateReceiptWalletAPI)
	api.Post("/company/receipt-wallet/qr", h.UploadReceiptWalletQRAPI)
	api.Get("/company/sunat", h.GetSunatAPI)
	api.Get("/company/invoicing", h.GetInvoicingAPI)
	api.Put("/company/sunat", h.UpdateSunatAPI)
	// POST /company/sync-facturador eliminado: la sincronización con Lycet se hace solo desde el panel central.
	api.Get("/company/branches", h.ListBranchesAPI)
	api.Post("/company/branches", h.CreateBranchAPI)
	api.Put("/company/branches/:id", h.UpdateBranchAPI)
	api.Delete("/company/branches/:id", h.DeleteBranchAPI)
	api.Get("/company/series", h.ListSeriesAPI)
	api.Post("/company/series", h.CreateSeriesAPI)
	api.Put("/company/series/:id", h.UpdateSeriesAPI)
	api.Delete("/company/series/:id", h.DeleteSeriesAPI)
}
