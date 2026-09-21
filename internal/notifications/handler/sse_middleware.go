package handler

import (
	"strings"

	"github.com/gofiber/fiber/v3"
)

// SSEAccessTokenMiddleware permite EventSource autenticado vía ?access_token= (EventSource no
// puede mandar headers custom). Mismo contenido que
// internal/billing/handler/sse_middleware.go — duplicado a propósito en vez de importar el paquete
// billing (dominio no relacionado), siguiendo el mismo criterio de pequeñas duplicaciones puntuales
// entre módulos ya usado en este repo (ver internal/ecommerce/handler/ecommerce_scope.go vs
// internal/cashbank/handler/cashbank_scope.go).
func SSEAccessTokenMiddleware(c fiber.Ctx) error {
	if strings.TrimSpace(c.Get("Authorization")) == "" {
		if token := strings.TrimSpace(c.Query("access_token")); token != "" {
			c.Request().Header.Set("Authorization", "Bearer "+token)
		}
	}
	return c.Next()
}
