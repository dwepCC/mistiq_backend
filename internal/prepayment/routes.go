package prepayment

import (
	"tukifac/pkg/middleware"

	"github.com/gofiber/fiber/v3"
)

// RegisterRoutes registra rutas del módulo de anticipos.
func RegisterRoutes(api fiber.Router) {
	cfg := NewConfigHandler()
	vouchers := NewVoucherHandler()
	mod := middleware.RequireModule("sales")
	api.Get("/prepayment/config", mod, cfg.GetConfigAPI)
	api.Get("/prepayment/vouchers", mod, vouchers.ListOpenVouchersAPI)
}
