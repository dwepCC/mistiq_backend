package equipos

import (
	"tukifac/internal/equipos/handler"

	"github.com/gofiber/fiber/v3"
)

// RegisterPublicRoutes monta las entregas entrantes de terceros (sin sesión; se autentican por firma).
func RegisterPublicRoutes(app *fiber.App) {
	app.Post("/api/internal/webhooks/shalom", handler.ShalomWebhook)
}
