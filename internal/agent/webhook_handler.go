package agent

import (
	"tukifac/pkg/logger"

	"github.com/gofiber/fiber/v3"
)

// handleWhatsAppVerify: GET /webhooks/assistant/whatsapp — challenge de
// verificación de Meta al registrar el webhook (hub.mode/hub.verify_token/
// hub.challenge).
func handleWhatsAppVerify(c fiber.Ctx) error {
	if eng == nil || eng.whatsapp == nil {
		return c.SendStatus(fiber.StatusServiceUnavailable)
	}
	if c.Query("hub.mode") != "subscribe" {
		return c.SendStatus(fiber.StatusForbidden)
	}
	if c.Query("hub.verify_token") != eng.whatsapp.VerifyToken() {
		return c.SendStatus(fiber.StatusForbidden)
	}
	return c.SendString(c.Query("hub.challenge"))
}

// handleWhatsAppReceive: POST /webhooks/assistant/whatsapp — recepción de
// mensajes. Valida firma HMAC (condicional a que haya AppSecret
// configurado — ver whatsapp.Client.VerifySignature), parsea, y ENCOLA
// (responde 200 de inmediato, no espera a que se procese). Un 4xx haría
// que Meta reintente un payload que no va a mejorar, así que ante
// error de parseo/firma se responde igual con 200 y se loggea.
func handleWhatsAppReceive(c fiber.Ctx) error {
	if eng == nil || eng.whatsapp == nil {
		return c.SendStatus(fiber.StatusServiceUnavailable)
	}

	body := c.Body()

	if !eng.whatsapp.VerifySignature(body, c.Get("X-Hub-Signature-256")) {
		return c.SendStatus(fiber.StatusUnauthorized)
	}

	messages, err := eng.whatsapp.Parse(eng.platformAssistantID, body)
	if err != nil {
		logWebhookParseError(err)
		return c.SendStatus(fiber.StatusOK)
	}

	for _, in := range messages {
		if _, err := eng.queue.Enqueue(c.Context(), in); err != nil {
			logWebhookParseError(err)
		}
	}

	return c.SendStatus(fiber.StatusOK)
}

func logWebhookParseError(err error) {
	logger.L.Warn("assistant_whatsapp_webhook_error", "error", err.Error())
}
