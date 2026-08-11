package auth

import (
	"tukifac/internal/auth/handler"

	"github.com/gofiber/fiber/v3"
)

func RegisterRoutes(app *fiber.App) {
	h := handler.NewAuthHandler()
	// Solo API — el frontend React maneja la UI de login
	app.Post("/api/login", h.LoginAPI)
	// Renovación de sesión: pública (el access token puede estar expirado); valida el refresh token.
	app.Post("/api/session/refresh", h.RefreshAPI)
}
