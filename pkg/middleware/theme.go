package middleware

import (
	"tukifac/pkg/database"
	"tukifac/pkg/utils"

	"github.com/gofiber/fiber/v3"
	"gorm.io/gorm"
)

// TenantTheme carga el tema de color del tenant y lo inyecta como local "Theme".
// Requiere que PassLocalsToViews esté activo en la configuración de Fiber.
func TenantTheme() fiber.Handler {
	return func(c fiber.Ctx) error {
		theme := utils.GetTheme("blue") // tema por defecto

		if tdb, ok := c.Locals("tenantDB").(*gorm.DB); ok && tdb != nil {
			var cfg database.TenantCompanyConfig
			if err := tdb.Select("color_theme").First(&cfg).Error; err == nil && cfg.ColorTheme != "" {
				theme = utils.GetTheme(cfg.ColorTheme)
			}
		}

		c.Locals("Theme", theme)
		return c.Next()
	}
}
