package agent

import (
	"tukifac/pkg/middleware"

	"github.com/gofiber/fiber/v3"
)

// RegisterPublicRoutes monta el canal web público (sin auth, rate-limit
// propio) bajo /public/assistant/... — llamar con `app.Group("/api")` desde
// la sección pública de routes/routes.go (mismo patrón que
// restaurant.RegisterPublicRoutes).
func RegisterPublicRoutes(api fiber.Router) {
	g := api.Group("/public/assistant")
	g.Post("/chat", handlePublicChat)
	g.Get("/chat/messages", handlePublicChatMessages)
}

// RegisterWebhookRoutes monta el webhook de WhatsApp en la RAÍZ (no bajo
// /api): Meta no manda JWT, la protección es la firma HMAC propia del
// canal. Llamar con el *fiber.App directo desde routes/routes.go.
func RegisterWebhookRoutes(app fiber.Router) {
	app.Get("/webhooks/assistant/whatsapp", handleWhatsAppVerify)
	app.Post("/webhooks/assistant/whatsapp", handleWhatsAppReceive)
}

// RegisterRoutes monta los endpoints del panel. `saAPI` ya viene protegido
// con middleware.SuperAdminAuthAPI() desde internal/superadmin/routes.go;
// acá se suma el permiso granular real del módulo ("assistant.view" para
// lectura, "assistant.manage" para escritura — sembrados en
// pkg/database/sa_rbac_seed.go). Fase 3: bandeja + configuración.
// Conocimiento/analítica quedan para la Fase 6.
func RegisterRoutes(saAPI fiber.Router) {
	view := middleware.RequireSAPermission("assistant.view")
	manage := middleware.RequireSAPermission("assistant.manage")

	saAPI.Get("/assistant/status", view, handleStatus)
	saAPI.Post("/assistant/test", view, handleTest)

	saAPI.Get("/assistant/config", view, handleConfigGet)
	saAPI.Put("/assistant/config", manage, handleConfigUpdate)

	saAPI.Get("/assistant/conversations", view, handleConversationsList)
	saAPI.Get("/assistant/conversations/:id/messages", view, handleConversationMessages)
	saAPI.Get("/assistant/conversations/:id/lead", view, handleConversationLead)
	saAPI.Post("/assistant/conversations/:id/take", manage, handleConversationTake)
	saAPI.Post("/assistant/conversations/:id/reply", manage, handleConversationReply)
	saAPI.Post("/assistant/conversations/:id/close", manage, handleConversationClose)
	saAPI.Post("/assistant/conversations/:id/reopen", manage, handleConversationReopen)
	saAPI.Post("/assistant/conversations/:id/validate-payment", manage, handleConversationValidatePayment)

	saAPI.Get("/assistant/knowledge", view, handleKnowledgeList)
	saAPI.Post("/assistant/knowledge", manage, handleKnowledgeCreate)
	saAPI.Post("/assistant/knowledge/seed-defaults", manage, handleKnowledgeSeedDefaults)
	saAPI.Delete("/assistant/knowledge/:id", manage, handleKnowledgeDelete)

	saAPI.Get("/assistant/metrics", view, handleMetrics)
	saAPI.Get("/assistant/analytics", view, handleAnalytics)
}
