package agent

import "github.com/gofiber/fiber/v3"

// RegisterPublicRoutes monta el canal web público (sin auth, rate-limit
// propio) bajo /public/assistant/... — llamar con `app.Group("/api")` desde
// la sección pública de routes/routes.go (mismo patrón que
// restaurant.RegisterPublicRoutes).
func RegisterPublicRoutes(api fiber.Router) {
	g := api.Group("/public/assistant")
	g.Post("/chat", handlePublicChat)
	g.Get("/chat/messages", handlePublicChatMessages)
}

// RegisterRoutes monta los endpoints del panel (Fase 1: solo status +
// playground de pruebas; bandeja/config/knowledge/analytics llegan en la
// Fase 3). `saAPI` ya viene protegido con middleware.SuperAdminAuthAPI()
// desde internal/superadmin/routes.go — no se agrega permiso granular
// todavía (RequireSAPermission) porque el módulo no tiene UI real hasta la
// Fase 3; se agrega junto con esa UI.
func RegisterRoutes(saAPI fiber.Router) {
	saAPI.Get("/assistant/status", handleStatus)
	saAPI.Post("/assistant/test", handleTest)
}
