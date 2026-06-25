package restaurant

import (
	"tukifac/internal/restaurant/handler"
	"tukifac/pkg/middleware"
	"tukifac/pkg/restaurantperm"

	"github.com/gofiber/fiber/v3"
)

func RegisterRoutes(api fiber.Router) {
	h := handler.New()

	r := api.Group("/restaurant",
		middleware.RequireModule("restaurant"),
		middleware.LoadRestaurantPermissions(),
		middleware.RequireRestaurantStaff(),
	)

	manage := middleware.RequireRestaurantAdminOrTenantAdmin()

	r.Get("/session/permissions", h.SessionPermissions)
	r.Get("/staff", manage, h.ListStaff)
	r.Get("/staff/management", manage, h.ListStaffManagement)
	r.Post("/staff/users", manage, h.CreateStaffUser)
	r.Put("/users/:id/staff", manage, h.UpsertUserStaff)

	r.Get("/settings", middleware.RequireAnyRestaurantPerm(restaurantperm.OrdersCharge, restaurantperm.TablesOpen, restaurantperm.KitchenView, restaurantperm.SettingsManage), h.GetSettings)
	r.Put("/settings", manage, h.UpdateSettings)

	r.Get("/floors", middleware.RequireRestaurantPerm(restaurantperm.TablesView), h.ListFloors)
	r.Post("/floors", middleware.RequireRestaurantPerm(restaurantperm.ProductsManage), h.CreateFloor)
	r.Put("/floors/:id", middleware.RequireRestaurantPerm(restaurantperm.ProductsManage), h.UpdateFloor)
	r.Delete("/floors/:id", middleware.RequireRestaurantPerm(restaurantperm.ProductsManage), h.DeleteFloor)

	r.Get("/tables", middleware.RequireRestaurantPerm(restaurantperm.TablesView), h.ListTables)
	r.Post("/tables", middleware.RequireRestaurantPerm(restaurantperm.ProductsManage), h.CreateTable)
	r.Put("/tables/:id", middleware.RequireRestaurantPerm(restaurantperm.ProductsManage), h.UpdateTable)
	r.Delete("/tables/:id", middleware.RequireRestaurantPerm(restaurantperm.ProductsManage), h.DeleteTable)
	r.Get("/tables/:id/session", middleware.RequireRestaurantPerm(restaurantperm.TablesView), h.GetTableSession)

	r.Get("/orders", middleware.RequireAnyRestaurantPerm(restaurantperm.TablesView, restaurantperm.KitchenView, restaurantperm.POSUse), h.ListOpenOrders)
	r.Get("/delivery-companies", middleware.RequireRestaurantPerm(restaurantperm.DeliveryView), h.ListDeliveryCompanies)
	r.Post("/delivery-companies", middleware.RequireRestaurantPerm(restaurantperm.ProductsManage), h.CreateDeliveryCompany)
	r.Put("/delivery-companies/:id", middleware.RequireRestaurantPerm(restaurantperm.ProductsManage), h.UpdateDeliveryCompany)
	r.Delete("/delivery-companies/:id", middleware.RequireRestaurantPerm(restaurantperm.ProductsManage), h.DeleteDeliveryCompany)
	r.Get("/delivery-drivers", middleware.RequireRestaurantPerm(restaurantperm.DeliveryView), h.ListDeliveryDrivers)
	r.Post("/delivery-drivers", middleware.RequireRestaurantPerm(restaurantperm.ProductsManage), h.CreateDeliveryDriver)
	r.Put("/delivery-drivers/:id", middleware.RequireRestaurantPerm(restaurantperm.ProductsManage), h.UpdateDeliveryDriver)
	r.Delete("/delivery-drivers/:id", middleware.RequireRestaurantPerm(restaurantperm.ProductsManage), h.DeleteDeliveryDriver)

	r.Post("/sessions", middleware.RequireRestaurantPerm(restaurantperm.TablesOpen), h.OpenSession)
	r.Get("/sessions/:id", middleware.RequireRestaurantPerm(restaurantperm.TablesView), h.GetSession)
	r.Patch("/sessions/:id", middleware.RequireRestaurantPerm(restaurantperm.TablesView), h.UpdateSession)
	r.Put("/sessions/:id/order-status", middleware.RequireAnyRestaurantPerm(restaurantperm.TablesView, restaurantperm.KitchenView), h.UpdateOrderStatus)
	r.Get("/sessions/:id/precuenta", middleware.RequireRestaurantPerm(restaurantperm.TablesView), h.GetPrecuenta)
	r.Post("/sessions/:id/orders", middleware.RequireRestaurantPerm(restaurantperm.TablesOpen), h.AddOrder)
	r.Post("/table-orders/:id/printed", middleware.RequireRestaurantPerm(restaurantperm.TablesOpen), h.MarkTableOrderPrinted)
	r.Post("/sessions/:id/bill", middleware.RequireRestaurantPerm(restaurantperm.OrdersCharge), h.BillSession)
	r.Post("/sessions/:id/close", middleware.RequireRestaurantPerm(restaurantperm.OrdersCharge), h.CloseSession)
	r.Post("/sessions/:id/cancel", middleware.RequireAnyRestaurantPerm(restaurantperm.OrdersCharge, restaurantperm.OrdersCancel, restaurantperm.SettingsManage), h.CancelSession)
	r.Post("/sessions/:id/cancel-comandas", middleware.RequireAnyRestaurantPerm(restaurantperm.SettingsManage, restaurantperm.OrdersCancel), h.CancelAllComandas)

	r.Patch("/comandas/:id/notes", middleware.RequireAnyRestaurantPerm(restaurantperm.TablesOpen, restaurantperm.OrdersCreate, restaurantperm.POSUse), h.UpdateComandaNotes)
	r.Put("/comandas/:id/status", middleware.RequireRestaurantPerm(restaurantperm.KitchenUpdate), h.UpdateComandaStatus)
	r.Post("/comandas/:id/print", middleware.RequireRestaurantPerm(restaurantperm.KitchenView), h.PrintComanda)
	r.Delete("/comandas/:id", middleware.RequireAnyRestaurantPerm(restaurantperm.SettingsManage, restaurantperm.OrdersCancel), h.CancelComanda)

	r.Get("/kitchen", middleware.RequireRestaurantPerm(restaurantperm.KitchenView), h.KitchenView)

	r.Get("/dashboard",
		middleware.RequireAnyRestaurantPerm(
			restaurantperm.OrdersCharge,
			restaurantperm.CashView,
			restaurantperm.SettingsManage,
			restaurantperm.TablesView,
		),
		h.Dashboard,
	)
}

// RegisterSalePaymentRoutes registra los endpoints de pagos bajo /api/sales
func RegisterSalePaymentRoutes(api fiber.Router) {
	h := handler.New()
	mod := middleware.RequireModule("sales")
	loadRest := middleware.LoadRestaurantPermissions()
	api.Post("/sales/:id/payments", mod, loadRest, middleware.RequireSalesAccess("create"), h.RegisterPayments)
	api.Get("/sales/:id/payments", mod, loadRest, middleware.RequireSalesAccess("view"), h.GetSalePayments)
}
