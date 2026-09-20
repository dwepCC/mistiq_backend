package agent

import (
	"time"

	agentpkg "tukifac/pkg/agent"
	"tukifac/pkg/agent/memory"
	"tukifac/pkg/logger"

	"github.com/gofiber/fiber/v3"
)

// maxPublicChatChars: límite de entrada del canal web, DISTINTO del de
// WhatsApp (4000, en la cola de Fase 4) — cada canal trunca en silencio a
// su propio tope. Ver docs/CHATBOT-AGENT-ARCHITECTURE.md §2.9.
const maxPublicChatChars = 2000

const webChannel = "webchat"

func webContactRef(sessionID string) string { return "web:" + sessionID }

type publicChatRequest struct {
	SessionID string `json:"session_id"`
	Text      string `json:"text"`
}

type publicChatResponse struct {
	Reply  string `json:"reply"`
	Silent bool   `json:"silent"`
}

// handlePublicChat: POST /api/public/assistant/chat — endpoint del
// visitante anónimo (sin login). 100% SÍNCRONO, sin pasar por ninguna cola
// (a diferencia de WhatsApp) — dos envíos casi simultáneos de la misma
// sesión pueden correr el orquestador en paralelo sin coordinación; el
// único freno es el rate-limit por sesión de abajo.
func handlePublicChat(c fiber.Ctx) error {
	if eng == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "asistente no disponible"})
	}

	var body publicChatRequest
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "JSON inválido"})
	}
	if body.SessionID == "" || body.Text == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "session_id y text son requeridos"})
	}
	text := truncateRunes(body.Text, maxPublicChatChars)

	if !eng.chatLimiter.Allow(body.SessionID) {
		return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{"error": "demasiados mensajes, espera un momento"})
	}

	in := agentpkg.Inbound{
		AssistantID: eng.platformAssistantID,
		Channel:     webChannel,
		From:        webContactRef(body.SessionID),
		Text:        text,
		ReceivedAt:  time.Now(),
	}

	out, err := eng.orch.Handle(c.Context(), in)
	if err != nil {
		logger.L.Warn("assistant_public_chat_process_failed", "error", err.Error())
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "no se pudo procesar el mensaje"})
	}

	return c.JSON(publicChatResponse{Reply: out.Text, Silent: out.Silent})
}

type publicChatMessage struct {
	ID      uint   `json:"id"`
	Role    string `json:"role"`
	Content string `json:"content"`
}

// handlePublicChatMessages: GET /api/public/assistant/chat/messages?session_id=
// rehidrata el hilo (respaldo one-shot si el visitante recarga la página).
// Omite turnos `tool` (auditoría interna, no se expone al visitante).
func handlePublicChatMessages(c fiber.Ctx) error {
	if eng == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "asistente no disponible"})
	}
	sessionID := c.Query("session_id")
	if sessionID == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "session_id es requerido"})
	}

	conv, found, err := eng.conversations.Find(c.Context(), eng.platformAssistantID, webChannel, webContactRef(sessionID))
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "error interno"})
	}
	if !found {
		return c.JSON(fiber.Map{"data": []publicChatMessage{}})
	}

	turns, err := eng.mem.LoadContext(c.Context(), conv.ID, 50)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "error interno"})
	}

	out := make([]publicChatMessage, 0, len(turns))
	for _, t := range turns {
		if t.Role == memory.RoleTool {
			continue
		}
		out = append(out, publicChatMessage{ID: t.ID, Role: string(t.Role), Content: t.Content})
	}
	return c.JSON(fiber.Map{"data": out})
}

func truncateRunes(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max])
}
