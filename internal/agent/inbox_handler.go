package agent

import (
	"context"
	"strconv"
	"time"

	agentpkg "tukifac/pkg/agent"
	"tukifac/pkg/database"
	"tukifac/pkg/logger"

	"github.com/gofiber/fiber/v3"
)

type conversationDTO struct {
	ID               uint      `json:"id"`
	Channel          string    `json:"channel"`
	ContactRef       string    `json:"contact_ref"`
	Status           string    `json:"status"`
	NeedsHumanReason string    `json:"needs_human_reason"`
	AssignedSAUserID uint      `json:"assigned_sa_user_id"`
	LastMsgAt        time.Time `json:"last_msg_at"`
	LeadName         string    `json:"lead_name"`
	LeadStatus       string    `json:"lead_status"`
	LastMessage      string    `json:"last_message"`
	LastMessageRole  string    `json:"last_message_role"`
}

// handleConversationsList: GET /assistant/conversations?status=
func handleConversationsList(c fiber.Ctx) error {
	if eng == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "asistente no disponible"})
	}
	status := c.Query("status")

	q := database.CentralDB.Where("assistant_id = ?", eng.platformAssistantID)
	if status != "" {
		q = q.Where("status = ?", status)
	}
	var convs []database.AssistantConversation
	if err := q.Order("last_msg_at DESC").Limit(200).Find(&convs).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "no se pudo listar conversaciones"})
	}

	out := make([]conversationDTO, 0, len(convs))
	for _, conv := range convs {
		dto := conversationDTO{
			ID: conv.ID, Channel: conv.Channel, ContactRef: conv.ContactRef, Status: conv.Status,
			NeedsHumanReason: conv.NeedsHumanReason, AssignedSAUserID: conv.AssignedSAUserID, LastMsgAt: conv.LastMsgAt,
		}
		var lead database.AssistantLead
		if err := database.CentralDB.Where("conversation_id = ?", conv.ID).Order("id DESC").First(&lead).Error; err == nil {
			dto.LeadName = lead.Name
			dto.LeadStatus = lead.Status
		}
		var lastMsg database.AssistantMessage
		if err := database.CentralDB.Where("conversation_id = ?", conv.ID).Order("id DESC").First(&lastMsg).Error; err == nil {
			dto.LastMessage = lastMsg.Content
			dto.LastMessageRole = lastMsg.Role
		}
		out = append(out, dto)
	}
	return c.JSON(fiber.Map{"data": out})
}

type messageDTO struct {
	ID           uint      `json:"id"`
	Role         string    `json:"role"`
	Content      string    `json:"content"`
	ToolName     string    `json:"tool_name,omitempty"`
	ChannelMsgID string    `json:"channel_msg_id,omitempty"`
	ReplyToID    uint      `json:"reply_to_id,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
}

// handleConversationMessages: GET /assistant/conversations/:id/messages?after=
func handleConversationMessages(c fiber.Ctx) error {
	if eng == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "asistente no disponible"})
	}
	id, err := strconv.ParseUint(c.Params("id"), 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "id inválido"})
	}

	q := database.CentralDB.Where("conversation_id = ?", uint(id))
	if after := c.Query("after"); after != "" {
		q = q.Where("id > ?", after)
	}
	var rows []database.AssistantMessage
	if err := q.Order("id ASC").Limit(500).Find(&rows).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "no se pudieron cargar los mensajes"})
	}

	out := make([]messageDTO, 0, len(rows))
	for _, r := range rows {
		if r.Role == database.MsgRoleTool {
			continue // auditoría interna, no se muestra en la bandeja
		}
		out = append(out, messageDTO{
			ID: r.ID, Role: r.Role, Content: r.Content, ToolName: r.ToolName,
			ChannelMsgID: r.ChannelMsgID, ReplyToID: r.ReplyToID, CreatedAt: r.CreatedAt,
		})
	}
	return c.JSON(fiber.Map{"data": out})
}

// handleConversationTake: POST /assistant/conversations/:id/take — un
// asesor humano toma el control; el motor deja de responder (ver
// orchestrator: status != "bot" => Silent).
func handleConversationTake(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "id inválido"})
	}
	saUserID := currentSAUserID(c)
	if err := database.CentralDB.Model(&database.AssistantConversation{}).
		Where("id = ?", uint(id)).
		Updates(map[string]any{"status": database.ConvStatusHuman, "assigned_sa_user_id": saUserID}).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "no se pudo tomar la conversación"})
	}
	return c.SendStatus(fiber.StatusOK)
}

// handleConversationClose: POST /assistant/conversations/:id/close
func handleConversationClose(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "id inválido"})
	}
	if err := database.CentralDB.Model(&database.AssistantConversation{}).
		Where("id = ?", uint(id)).Update("status", database.ConvStatusClosed).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "no se pudo cerrar la conversación"})
	}
	return c.SendStatus(fiber.StatusOK)
}

// handleConversationReopen: POST /assistant/conversations/:id/reopen —
// vuelve a "bot", libera al asesor asignado (mismo criterio que
// close/reopen del original: reabrir = el bot retoma).
func handleConversationReopen(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "id inválido"})
	}
	if err := database.CentralDB.Model(&database.AssistantConversation{}).
		Where("id = ?", uint(id)).
		Updates(map[string]any{"status": database.ConvStatusBot, "needs_human_reason": "", "assigned_sa_user_id": 0}).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "no se pudo reabrir la conversación"})
	}
	return c.SendStatus(fiber.StatusOK)
}

type replyRequest struct {
	Text      string `json:"text"`
	ReplyToID uint   `json:"reply_to_id"`
}

// handleConversationReply: POST /assistant/conversations/:id/reply —
// respuesta de un asesor humano; se persiste como role="agent" y se
// entrega por el canal real del contacto (WhatsApp o webchat). Solo
// funciona si la conversación está en "human" (el asesor la tomó) — mismo
// candado que el compositor del panel debe respetar del lado UI.
func handleConversationReply(c fiber.Ctx) error {
	if eng == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "asistente no disponible"})
	}
	id, err := strconv.ParseUint(c.Params("id"), 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "id inválido"})
	}
	var body replyRequest
	if err := c.Bind().JSON(&body); err != nil || body.Text == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "text es requerido"})
	}

	var conv database.AssistantConversation
	if err := database.CentralDB.First(&conv, uint(id)).Error; err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "conversación no encontrada"})
	}
	if conv.Status != database.ConvStatusHuman {
		return c.Status(fiber.StatusConflict).JSON(fiber.Map{"error": "la conversación no está en manos de un asesor"})
	}

	row := database.AssistantMessage{
		ConversationID: conv.ID, Role: database.MsgRoleAgent, Content: body.Text, ReplyToID: body.ReplyToID,
	}
	if err := database.CentralDB.Create(&row).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "no se pudo guardar la respuesta"})
	}
	database.CentralDB.Model(&database.AssistantConversation{}).Where("id = ?", conv.ID).Update("last_msg_at", time.Now())

	go sendAgentReply(conv, body.Text)

	return c.SendStatus(fiber.StatusOK)
}

type leadDTO struct {
	ID            uint       `json:"id"`
	Name          string     `json:"name"`
	Phone         string     `json:"phone"`
	Email         string     `json:"email"`
	Interest      string     `json:"interest"`
	Qualification string     `json:"qualification"`
	Kind          string     `json:"kind"`
	Status        string     `json:"status"`
	TenantID      *uint      `json:"tenant_id,omitempty"`
	ScheduledAt   *time.Time `json:"scheduled_at,omitempty"`
	Notes         string     `json:"notes"`
}

// handleConversationLead: GET /assistant/conversations/:id/lead
func handleConversationLead(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "id inválido"})
	}
	var lead database.AssistantLead
	if err := database.CentralDB.Where("conversation_id = ?", uint(id)).Order("id DESC").First(&lead).Error; err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "sin datos de lead para esta conversación"})
	}
	return c.JSON(fiber.Map{"data": leadDTO{
		ID: lead.ID, Name: lead.Name, Phone: lead.Phone, Email: lead.Email, Interest: lead.Interest,
		Qualification: lead.Qualification, Kind: lead.Kind, Status: lead.Status, TenantID: lead.TenantID,
		ScheduledAt: lead.ScheduledAt, Notes: lead.Notes,
	}})
}

// handleConversationValidatePayment: POST /assistant/conversations/:id/validate-payment
// — SOLO un humano puede llamar esto (nunca una Action del agente). Exige
// que el lead esté en payment_reported; registra quién y cuándo.
func handleConversationValidatePayment(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "id inválido"})
	}
	var lead database.AssistantLead
	if err := database.CentralDB.Where("conversation_id = ?", uint(id)).Order("id DESC").First(&lead).Error; err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "sin lead para esta conversación"})
	}
	if lead.Status != "payment_reported" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "el lead no tiene un pago reportado pendiente"})
	}
	saUserID := currentSAUserID(c)
	now := time.Now()
	if err := database.CentralDB.Model(&database.AssistantLead{}).Where("id = ?", lead.ID).
		Updates(map[string]any{
			"status": "payment_validated", "payment_validated_at": now, "payment_validated_by_sa_user_id": saUserID,
		}).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "no se pudo validar el pago"})
	}
	return c.SendStatus(fiber.StatusOK)
}

// sendAgentReply entrega la respuesta de un asesor humano por el canal
// real del contacto. WhatsApp: mismo cliente que usa el motor. Chat web:
// TODO — no hay push en vivo hacia el visitante todavía (la Fase 3 no
// incluyó el hub SSE/WS del canal público); el mensaje queda persistido y
// el visitante lo ve en su próximo poll a GET /chat/messages.
func sendAgentReply(conv database.AssistantConversation, text string) {
	if conv.Channel != "whatsapp" || eng == nil || eng.whatsapp == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if _, err := eng.whatsapp.Send(ctx, agentpkg.Outbound{To: conv.ContactRef, Text: text}); err != nil {
		logger.L.Warn("assistant_agent_reply_send_failed", "error", err.Error())
	}
}

// currentSAUserID lee el id del usuario superadmin autenticado — mismo
// local que deja middleware.SuperAdminAuthAPI() y que usa el resto del
// panel (ver internal/superadmin/handler/*.go). 0 si no está disponible
// (queda como "sin asignar" en vez de fallar la operación).
func currentSAUserID(c fiber.Ctx) uint {
	v, _ := c.Locals("sa_user_id").(uint)
	return v
}
