package prepayment

import (
	"github.com/gofiber/fiber/v3"
)

// ConfigHandler expone configuración del módulo de anticipos.
type ConfigHandler struct{}

func NewConfigHandler() *ConfigHandler {
	return &ConfigHandler{}
}

// GetConfigAPI GET /prepayment/config
func (h *ConfigHandler) GetConfigAPI(c fiber.Ctx) error {
	return c.JSON(GetModuleConfig())
}
