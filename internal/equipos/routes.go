// Package equipos registra las rutas del módulo «Gestión de Equipos» del panel central.
package equipos

import (
	"tukifac/internal/equipos/handler"
	"tukifac/pkg/middleware"

	"github.com/gofiber/fiber/v3"
)

// RegisterRoutes monta /api/superadmin/equipos/*. saAPI ya viene con SuperAdminAuthAPI(); cada ruta exige su permiso.
func RegisterRoutes(saAPI fiber.Router) {
	h := handler.New()
	p := middleware.RequireSAPermission

	saAPI.Get("/equipos/products", p("equipos.view"), h.ListProducts)
	saAPI.Post("/equipos/products", p("equipos.catalog"), h.CreateProduct)
	saAPI.Put("/equipos/products/:id", p("equipos.catalog"), h.UpdateProduct)
	saAPI.Post("/equipos/products/import", p("equipos.catalog"), h.ImportCatalog)

	saAPI.Get("/equipos/combos", p("equipos.view"), h.ListCombos)
	saAPI.Post("/equipos/combos", p("equipos.catalog"), h.CreateCombo)
	saAPI.Put("/equipos/combos/:id", p("equipos.catalog"), h.UpdateCombo)

	saAPI.Get("/equipos/plans", p("equipos.view"), h.ListPlans)
	saAPI.Get("/equipos/carriers", p("equipos.view"), h.ListCarriers)
	saAPI.Post("/equipos/carriers", p("equipos.carriers"), h.CreateCarrier)
	saAPI.Put("/equipos/carriers/:id", p("equipos.carriers"), h.UpdateCarrier)
	saAPI.Get("/equipos/carriers/:id/agencies", p("equipos.view"), h.ListAgencies)
	saAPI.Post("/equipos/carriers/:id/agencies", p("equipos.carriers"), h.CreateAgency)
	saAPI.Post("/equipos/carriers/:id/agencies/import", p("equipos.carriers"), h.ImportAgencies)
	saAPI.Put("/equipos/agencies/:id", p("equipos.carriers"), h.UpdateAgency)

	// Integración con Shalom API Perú (la API key se guarda cifrada en la BD y nunca se devuelve).
	saAPI.Get("/equipos/provider/shalom", p("equipos.view"), h.GetProvider)
	saAPI.Put("/equipos/provider/shalom", p("equipos.carriers"), h.SaveProvider)
	saAPI.Post("/equipos/provider/shalom/sync-agencies", p("equipos.carriers"), h.SyncProviderAgencies)
	saAPI.Post("/equipos/provider/shalom/webhook", p("equipos.carriers"), h.SetupProviderWebhook)
	saAPI.Post("/equipos/provider/shalom/webhook/rotate", p("equipos.carriers"), h.RotateProviderWebhook)
	saAPI.Post("/equipos/provider/shalom/sync-shipments", p("equipos.shipments"), h.SyncProviderShipments)
	saAPI.Get("/equipos/provider/shalom/departments", p("equipos.view"), h.ProviderDepartments)
	saAPI.Get("/equipos/provider/shalom/departments/:dep/provinces", p("equipos.view"), h.ProviderProvinces)
	saAPI.Get("/equipos/provider/shalom/departments/:dep/provinces/:prov/districts", p("equipos.view"), h.ProviderDistricts)
	saAPI.Post("/equipos/orders/:id/provider/link", p("equipos.shipments"), h.LinkShipmentTracking)
	saAPI.Post("/equipos/orders/:id/provider/refresh", p("equipos.shipments"), h.RefreshShipmentTracking)
	saAPI.Post("/equipos/orders/:id/provider/tariff", p("equipos.shipments"), h.QuoteShipmentTariff)
	saAPI.Get("/equipos/orders/:id/provider/events", p("equipos.view"), h.ShipmentProviderEvents)
	saAPI.Get("/equipos/notifications", p("equipos.view"), h.ListNotifications)
	saAPI.Post("/equipos/notifications/read", p("equipos.view"), h.MarkNotificationsRead)

	saAPI.Get("/equipos/settings", p("equipos.view"), h.GetSettings)
	saAPI.Put("/equipos/settings", p("equipos.settings"), h.UpdateSettings)
	saAPI.Post("/equipos/settings/pin", p("equipos.settings"), h.SetSecurityPin)
	saAPI.Get("/equipos/lookup/:type", p("equipos.create"), h.Lookup)

	saAPI.Get("/equipos/stock", p("equipos.stock_view"), h.StockReport)
	saAPI.Get("/equipos/stock/:productId/movements", p("equipos.stock_view"), h.ProductMovements)
	saAPI.Post("/equipos/stock/movements", p("equipos.stock_adjust"), h.AddMovement)
	saAPI.Get("/equipos/stock/warehouses", p("equipos.stock_view"), h.StockByWarehouse)

	saAPI.Get("/equipos/warehouses", p("equipos.stock_view"), h.ListWarehouses)
	saAPI.Post("/equipos/warehouses", p("equipos.warehouses"), h.CreateWarehouse)
	saAPI.Put("/equipos/warehouses/:id", p("equipos.warehouses"), h.UpdateWarehouse)

	saAPI.Get("/equipos/requirements", p("equipos.stock_view"), h.ListRequirements)
	saAPI.Get("/equipos/requirements/:id", p("equipos.stock_view"), h.GetRequirement)
	saAPI.Post("/equipos/requirements", p("equipos.stock_transfer"), h.CreateRequirement)
	saAPI.Post("/equipos/requirements/:id/cancel", p("equipos.stock_transfer_cancel"), h.CancelRequirement)

	saAPI.Get("/equipos/transfers", p("equipos.stock_view"), h.ListTransfers)
	saAPI.Get("/equipos/transfers/:id", p("equipos.stock_view"), h.GetTransfer)
	saAPI.Post("/equipos/transfers", p("equipos.stock_transfer"), h.CreateTransfer)
	saAPI.Post("/equipos/transfers/:id/cancel", p("equipos.stock_transfer_cancel"), h.CancelTransfer)

	saAPI.Post("/equipos/import/preview", p("equipos.import"), h.ImportPreview)
	saAPI.Post("/equipos/import/commit", p("equipos.import"), h.ImportCommit)
	saAPI.Get("/equipos/import/batches", p("equipos.import"), h.ImportBatches)

	saAPI.Get("/equipos/customers", p("equipos.view"), h.ListCustomers)
	saAPI.Post("/equipos/customers", p("equipos.create"), h.CreateCustomer)
	saAPI.Put("/equipos/customers/:id", p("equipos.create"), h.UpdateCustomer)
	saAPI.Get("/equipos/customers/:id/account", p("equipos.payments_view"), h.CustomerAccount)
	saAPI.Get("/equipos/customers/:id/open-balances", p("equipos.payments_view"), h.OpenBalances)

	saAPI.Get("/equipos/orders", p("equipos.view"), h.ListOrders)
	saAPI.Get("/equipos/orders/users", p("equipos.view"), h.OrderUsers)
	saAPI.Get("/equipos/orders/assignable-users", middleware.RequireSuperAdminOnly(), h.AssignableUsers)
	saAPI.Patch("/equipos/orders/:id/creator", middleware.RequireSuperAdminOnly(), h.AssignOrderCreator)
	saAPI.Post("/equipos/orders", p("equipos.create"), h.CreateOrder)
	saAPI.Get("/equipos/orders/:id", p("equipos.view"), h.GetOrder)
	saAPI.Put("/equipos/orders/:id", p("equipos.update"), h.UpdateOrder)
	saAPI.Post("/equipos/orders/:id/confirm", p("equipos.create"), h.ConfirmOrder)
	saAPI.Post("/equipos/orders/:id/cancel", p("equipos.cancel"), h.CancelOrder)
	saAPI.Post("/equipos/orders/:id/validate", p("equipos.validate"), h.ValidateOrder)
	saAPI.Post("/equipos/orders/:id/observe", p("equipos.validate"), h.ObserveOrder)
	saAPI.Post("/equipos/orders/:id/no-payment", p("equipos.payments"), h.SetNoPayment)

	saAPI.Get("/equipos/shipments", p("equipos.view"), h.ListShipments)
	saAPI.Put("/equipos/orders/:id/shipment", p("equipos.shipments"), h.UpdateShipment)
	saAPI.Patch("/equipos/orders/:id/shipping-data", p("equipos.shipments"), h.UpdateShippingData)
	saAPI.Post("/equipos/orders/:id/dispatch", p("equipos.shipments"), handler.DispatchOrder)
	saAPI.Post("/equipos/orders/:id/arrived", p("equipos.shipments"), handler.ArrivedOrder)
	saAPI.Post("/equipos/orders/:id/picked-up", p("equipos.shipments"), handler.PickedUpOrder)
	saAPI.Post("/equipos/orders/:id/undo-pickup", p("equipos.shipments"), h.UndoPickupOrder)
	saAPI.Post("/equipos/orders/:id/label-printed", p("equipos.shipments"), h.LabelPrinted)

	saAPI.Get("/equipos/payments", p("equipos.payments_view"), h.ListPayments)
	saAPI.Get("/equipos/payments/:id", p("equipos.payments_view"), h.GetPayment)
	saAPI.Post("/equipos/payments", p("equipos.payments"), h.CreatePayment)
	saAPI.Post("/equipos/payments/:id/void", p("equipos.payments"), h.VoidPayment)
	saAPI.Post("/equipos/payments/:id/voucher", p("equipos.payments"), h.UploadPaymentVoucher)
	saAPI.Get("/equipos/payments/:id/voucher", p("equipos.payments_view"), h.GetPaymentVoucher)
	saAPI.Post("/equipos/payments/:id/allocate", p("equipos.payments"), h.AllocatePayment)

	saAPI.Get("/equipos/returns", p("equipos.view"), h.ListReturns)
	saAPI.Post("/equipos/returns", p("equipos.returns"), h.CreateReturn)
	saAPI.Put("/equipos/returns/:id", p("equipos.returns"), h.UpdateReturn)
	saAPI.Post("/equipos/returns/:id/reship", p("equipos.returns"), h.ReshipReturn)
	saAPI.Post("/equipos/returns/:id/cancel", p("equipos.returns_cancel"), h.CancelReturn)
	saAPI.Patch("/equipos/returns/:id/guide", p("equipos.returns"), h.SetReturnGuide)
	saAPI.Post("/equipos/returns/:id/provider/refresh", p("equipos.returns"), h.RefreshReturnTracking)
	saAPI.Post("/equipos/returns/:id/receive", p("equipos.returns_receive"), h.ReceiveReturn)

	saAPI.Get("/equipos/dashboard", p("equipos.view"), h.Dashboard)
	saAPI.Get("/equipos/alerts", p("equipos.view"), h.Alerts)

	saAPI.Get("/equipos/reports/summary", p("equipos.reports"), h.ReportSummary)
	saAPI.Get("/equipos/reports/sales", p("equipos.reports"), h.ReportSales)
	saAPI.Get("/equipos/reports/collections", p("equipos.reports"), h.ReportCollections)
	saAPI.Get("/equipos/reports/replenishment", p("equipos.reports"), h.ReportReplenishment)
	saAPI.Get("/equipos/reports/profit", p("equipos.reports"), h.ReportProfit)
	saAPI.Get("/equipos/periods", p("equipos.reports"), h.ClosedPeriods)
	saAPI.Post("/equipos/periods/:period/close", p("equipos.settings"), h.ClosePeriod)
	saAPI.Post("/equipos/periods/:period/reopen", p("equipos.settings"), h.ReopenPeriod)
}
