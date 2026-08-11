package cashbank

import (
	"tukifac/internal/cashbank/handler"
	"tukifac/pkg/middleware"

	"github.com/gofiber/fiber/v3"
)

func RegisterRoutes(api fiber.Router) {
	h := handler.NewCashBankHandler()
	mod := middleware.RequireModule("cashbank")
	loadRest := middleware.LoadRestaurantPermissions()

	api.Get("/cashbank/sessions",
		mod, loadRest, middleware.RequireCashbankAccess("view"), h.ListSessionsAPI)
	api.Get("/cashbank/sessions/open/list",
		mod, loadRest, middleware.RequireCashbankAccess("view"), h.ListOpenSessionsInBranchAPI)
	api.Get("/cashbank/sessions/open",
		mod, loadRest, middleware.RequireCashbankAccess("view"), h.GetOpenSessionAPI)
	api.Get("/cashbank/sessions/:id",
		mod, loadRest, middleware.RequireCashbankAccess("view"), h.GetSessionAPI)
	api.Post("/cashbank/sessions",
		mod, loadRest, middleware.RequireCashbankAccess("open"), h.OpenSessionAPI)
	api.Post("/cashbank/sessions/:id/close",
		mod, loadRest, middleware.RequireCashbankAccess("close"), h.CloseSessionAPI)
	api.Patch("/cashbank/sessions/:id/opening-balance",
		mod, loadRest, middleware.RequireCashbankAccess("adjust_opening"), h.UpdateOpeningBalanceAPI)
	api.Delete("/cashbank/sessions/:id",
		mod, loadRest, middleware.RequireCashbankAccess("delete_session"), h.DeleteSessionAPI)
	api.Post("/cashbank/sessions/:id/arqueo",
		mod, loadRest, middleware.RequireCashbankAccess("view"), h.SaveArqueoAPI)
	api.Get("/cashbank/sessions/:id/movements",
		mod, loadRest, middleware.RequireCashbankAccess("view"), h.GetMovementsAPI)
	api.Post("/cashbank/sessions/:id/movements",
		mod, loadRest, middleware.RequireCashbankAccess("movements"), h.AddMovementAPI)
	api.Get("/cashbank/sessions/:id/report", mod, loadRest, middleware.RequireCashbankAccess("view"), h.GetSessionReportAPI)
	api.Get("/cashbank/sessions/:id/report/products", mod, loadRest, middleware.RequireCashbankAccess("view"), h.GetSessionProductsReportAPI)
	api.Get("/cashbank/reports/movements", mod, loadRest, middleware.RequireCashbankAccess("view"), h.ListMovementsReportAPI)
	api.Get("/cashbank/payment-methods", mod, loadRest, middleware.RequireCashbankAccess("view"), h.ListPaymentMethodsAPI)
	api.Get("/cashbank/payment-methods/:id", mod, loadRest, middleware.RequireCashbankAccess("manage"), h.GetPaymentMethodAPI)
	api.Post("/cashbank/payment-methods", mod, loadRest, middleware.RequireCashbankAccess("manage"), h.CreatePaymentMethodAPI)
	api.Put("/cashbank/payment-methods/:id", mod, loadRest, middleware.RequireCashbankAccess("manage"), h.UpdatePaymentMethodAPI)
	api.Delete("/cashbank/payment-methods/:id", mod, loadRest, middleware.RequireCashbankAccess("manage"), h.DeletePaymentMethodAPI)
	api.Get("/cashbank/bank-accounts", mod, loadRest, middleware.RequireCashbankAccess("view"), h.ListBankAccountsAPI)
	api.Get("/cashbank/bank-accounts/:id", mod, loadRest, middleware.RequireCashbankAccess("view"), h.GetBankAccountAPI)
	api.Post("/cashbank/bank-accounts", mod, loadRest, middleware.RequireCashbankAccess("manage"), h.CreateBankAccountAPI)
	api.Put("/cashbank/bank-accounts/:id", mod, loadRest, middleware.RequireCashbankAccess("manage"), h.UpdateBankAccountAPI)
	api.Get("/cashbank/bank-accounts/:id/movements", mod, loadRest, middleware.RequireCashbankAccess("manage"), h.GetBankMovementsAPI)
	api.Post("/cashbank/bank-accounts/:id/movements", mod, loadRest, middleware.RequireCashbankAccess("manage"), h.AddBankMovementAPI)
}
