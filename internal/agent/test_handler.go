package agent

import (
	"time"

	agentpkg "tukifac/pkg/agent"
	"tukifac/pkg/logger"

	"github.com/gofiber/fiber/v3"
)

// testChannel: canal de pruebas, separado de "webchat"/"whatsapp" a
// propósito — así el playground nunca aparece mezclado con conversaciones
// reales de clientes ni afecta sus métricas (Fase 3, cuando exista el
// panel con Analítica).
const testChannel = "test"

type testChatRequest struct {
	Text string `json:"text"`
	From string `json:"from"`
}

type testChatResponse struct {
	Reply  string `json:"reply"`
	Silent bool   `json:"silent"`
}

// handleTest: POST /api/superadmin/assistant/test — playground para un
// asesor: corre el pipeline real (RAG + LLM), en una sesión sintética
// identificada por `from` (que el propio frontend genera y reusa mientras
// dure la prueba).
func handleTest(c fiber.Ctx) error {
	if eng == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "asistente no disponible"})
	}
	var body testChatRequest
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "JSON inválido"})
	}
	if body.Text == "" || body.From == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "text y from son requeridos"})
	}

	in := agentpkg.Inbound{
		AssistantID: eng.platformAssistantID,
		Channel:     testChannel,
		From:        body.From,
		Text:        truncateRunes(body.Text, maxPublicChatChars),
		ReceivedAt:  time.Now(),
	}

	out, err := eng.orch.Handle(c.Context(), in)
	if err != nil {
		logger.L.Warn("assistant_test_process_failed", "error", err.Error())
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "no se pudo procesar el mensaje: " + err.Error()})
	}
	return c.JSON(testChatResponse{Reply: out.Text, Silent: out.Silent})
}

// handleStatus: GET /api/superadmin/assistant/status — salud básica del
// módulo para el panel (Fase 3).
func handleStatus(c fiber.Ctx) error {
	if eng == nil {
		return c.JSON(fiber.Map{"ready": false})
	}
	return c.JSON(fiber.Map{
		"ready":        true,
		"assistant_id": eng.platformAssistantID,
	})
}
