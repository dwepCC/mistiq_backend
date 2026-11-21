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
		mod, loadRest, middleware.RequireCashbankAccess("arqueo"), h.SaveArqueoAPI)
	api.Get("/cashbank/sessions/:id/balance",
		mod, loadRest, middleware.RequireCashbankAccess("view"), h.GetSessionBalanceAPI)
	api.Get("/cashbank/sessions/:id/movements",
		mod, loadRest, middleware.RequireCashbankAccess("view"), h.GetMovementsAPI)
	api.Post("/cashbank/sessions/:id/movements",
		mod, loadRest, middleware.RequireCashbankAccess("movements"), h.AddMovementAPI)
	api.Post("/cashbank/movements/:id/reverse",
		mod, loadRest, middleware.RequireCashbankAccess("movements"), h.ReverseMovementAPI)
	api.Get("/cashbank/sessions/:id/report", mod, loadRest, middleware.RequireCashbankAccess("view"), h.GetSessionReportAPI)
	api.Get("/cashbank/sessions/:id/report/products", mod, loadRest, middleware.RequireCashbankAccess("view"), h.GetSessionProductsReportAPI)
	api.Get("/cashbank/sessions/:id/report/documents", mod, loadRest, middleware.RequireCashbankAccess("view"), h.GetSessionDocumentsReportAPI)
	api.Get("/cashbank/reports/movements", mod, loadRest, middleware.RequireCashbankAccess("view"), h.ListMovementsReportAPI)
	api.Get("/cashbank/payment-methods", mod, loadRest, middleware.RequireCashbankAccess("view"), h.ListPaymentMethodsAPI)
	api.Get("/cashbank/payment-methods/:id", mod, loadRest, middleware.RequireCashbankAccess("payment_methods_manage"), h.GetPaymentMethodAPI)
	api.Post("/cashbank/payment-methods", mod, loadRest, middleware.RequireCashbankAccess("payment_methods_manage"), h.CreatePaymentMethodAPI)
	api.Put("/cashbank/payment-methods/:id", mod, loadRest, middleware.RequireCashbankAccess("payment_methods_manage"), h.UpdatePaymentMethodAPI)
	api.Delete("/cashbank/payment-methods/:id", mod, loadRest, middleware.RequireCashbankAccess("payment_methods_manage"), h.DeletePaymentMethodAPI)
	api.Get("/cashbank/bank-accounts", mod, loadRest, middleware.RequireCashbankAccess("view"), h.ListBankAccountsAPI)
	api.Post("/cashbank/transfers", mod, loadRest, middleware.RequireCashbankAccess("bank_manage"), h.CreateTransferAPI)
	api.Get("/cashbank/transfers", mod, loadRest, middleware.RequireCashbankAccess("bank_view"), h.ListTransfersAPI)
	api.Post("/cashbank/transfers/:id/void", mod, loadRest, middleware.RequireCashbankAccess("bank_manage"), h.VoidTransferAPI)
	api.Get("/cashbank/banks", mod, loadRest, middleware.RequireCashbankAccess("view"), h.ListBanksAPI)
	api.Post("/cashbank/banks", mod, loadRest, middleware.RequireCashbankAccess("bank_manage"), h.CreateBankAPI)
	api.Put("/cashbank/banks/:id", mod, loadRest, middleware.RequireCashbankAccess("bank_manage"), h.UpdateBankAPI)
	api.Get("/cashbank/bank-accounts/reconcile", mod, loadRest, middleware.RequireCashbankAccess("bank_manage"), h.ReconcileBankAccountsAPI)
	api.Get("/cashbank/bank-accounts/:id", mod, loadRest, middleware.RequireCashbankAccess("view"), h.GetBankAccountAPI)
	api.Post("/cashbank/bank-accounts", mod, loadRest, middleware.RequireCashbankAccess("bank_manage"), h.CreateBankAccountAPI)
	api.Put("/cashbank/bank-accounts/:id", mod, loadRest, middleware.RequireCashbankAccess("bank_manage"), h.UpdateBankAccountAPI)
	api.Get("/cashbank/bank-accounts/:id/movements", mod, loadRest, middleware.RequireCashbankAccess("bank_view"), h.GetBankMovementsAPI)
	api.Post("/cashbank/bank-accounts/:id/movements", mod, loadRest, middleware.RequireCashbankAccess("bank_manage"), h.AddBankMovementAPI)
}
