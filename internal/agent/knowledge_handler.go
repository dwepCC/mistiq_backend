package agent

import (
	"context"
	"strconv"
	"strings"

	"tukifac/pkg/agent/knowledge"
	knowledgemysql "tukifac/pkg/agent/knowledge/mysql"
	"tukifac/pkg/database"

	"github.com/gofiber/fiber/v3"
)

type knowledgeItemDTO struct {
	ID         uint   `json:"id"`
	SourceType string `json:"source_type"`
	Title      string `json:"title"`
	Content    string `json:"content"`
}

// handleKnowledgeList: GET /assistant/knowledge
func handleKnowledgeList(c fiber.Ctx) error {
	if eng == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "asistente no disponible"})
	}
	var rows []database.AssistantKnowledgeChunk
	if err := database.CentralDB.
		Where("assistant_id = ? AND kb_id = ?", eng.platformAssistantID, eng.platformAssistantID).
		Order("title, id").Find(&rows).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "no se pudo listar el conocimiento"})
	}
	out := make([]knowledgeItemDTO, len(rows))
	for i, r := range rows {
		out[i] = knowledgeItemDTO{ID: r.ID, SourceType: r.SourceType, Title: r.Title, Content: r.Content}
	}
	return c.JSON(fiber.Map{"data": out})
}

type createKnowledgeRequest struct {
	SourceType string `json:"source_type"`
	Title      string `json:"title"`
	Content    string `json:"content"`
}

// handleKnowledgeCreate: POST /assistant/knowledge — trocea, embebe y
// guarda (reemplaza cualquier fragmento previo con el mismo título, ver
// knowledge.Indexer.Upsert). Requiere que el asistente tenga un proveedor
// de embeddings configurado (mismo o de respaldo).
func handleKnowledgeCreate(c fiber.Ctx) error {
	if eng == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "asistente no disponible"})
	}
	var body createKnowledgeRequest
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "JSON inválido"})
	}
	body.Title = strings.TrimSpace(body.Title)
	body.Content = strings.TrimSpace(body.Content)
	if body.Title == "" || body.Content == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "title y content son requeridos"})
	}
	if body.SourceType == "" {
		body.SourceType = "manual"
	}

	store, err := knowledgeStoreFor(c.Context(), eng.platformAssistantID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	scope := knowledgeScope()
	chunks, err := store.Upsert(c.Context(), scope, body.SourceType, body.Title, body.Content)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"chunks": chunks})
}

// handleKnowledgeDelete: DELETE /assistant/knowledge/:id — borra UN
// fragmento (el panel borra todos los de un "documento" llamando esto una
// vez por cada id del grupo, igual que el original).
func handleKnowledgeDelete(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 64)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "id inválido"})
	}
	if err := database.CentralDB.Delete(&database.AssistantKnowledgeChunk{}, uint(id)).Error; err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "no se pudo borrar"})
	}
	return c.SendStatus(fiber.StatusOK)
}

// defaultKnowledgeSeed: corpus mínimo real sobre Mistiq para que el
// asistente no arranque sin nada que citar. Deliberadamente corto — el
// contenido real de producto lo carga el equipo comercial desde el panel.
var defaultKnowledgeSeed = []struct{ Title, Content string }{
	{
		Title: "Qué es Mistiq",
		Content: "Mistiq es un sistema de gestión (ERP/POS) para negocios peruanos: punto de venta, " +
			"facturación electrónica SUNAT, inventario, cuentas por cobrar, y un módulo especializado " +
			"para restaurantes (comandas, mesas, delivery). Funciona en la nube, se usa desde el navegador " +
			"y también hay una app de punto de venta para Android/escritorio.",
	},
	{
		Title: "Cómo empezar",
		Content: "Para empezar a usar Mistiq no se necesita instalar nada: se crea la cuenta con el RUC " +
			"del negocio, se elige un plan, y en minutos queda lista para emitir boletas/facturas y " +
			"vender. El equipo comercial ayuda con la configuración inicial (productos, series de " +
			"comprobantes, usuarios) sin costo adicional durante el primer contacto.",
	},
	{
		Title: "Soporte",
		Content: "El soporte de Mistiq se da por WhatsApp y correo. Cualquier duda técnica o de " +
			"facturación electrónica que el asistente no pueda resolver se deriva a un asesor humano.",
	},
}

// handleKnowledgeSeedDefaults: POST /assistant/knowledge/seed-defaults —
// idempotente (Upsert reemplaza por título), pensado para el botón
// "Cargar contenido por defecto" del panel en una instalación nueva.
func handleKnowledgeSeedDefaults(c fiber.Ctx) error {
	if eng == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "asistente no disponible"})
	}
	store, err := knowledgeStoreFor(c.Context(), eng.platformAssistantID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	scope := knowledgeScope()
	totalChunks := 0
	for _, doc := range defaultKnowledgeSeed {
		n, err := store.Upsert(c.Context(), scope, "faq", doc.Title, doc.Content)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
		}
		totalChunks += n
	}
	return c.JSON(fiber.Map{"docs": len(defaultKnowledgeSeed), "chunks": totalChunks})
}

func knowledgeScope() knowledge.KBScope {
	return knowledge.ScopeFor(eng.platformAssistantID)
}

// knowledgeStoreFor resuelve el proveedor de embeddings vigente y arma un
// Store fresco — mismo criterio que newKnowledgeRetriever en wiring.go
// (barato, sin caché propia: el resolver ya cachea la Config).
func knowledgeStoreFor(ctx context.Context, assistantID uint) (*knowledgemysql.Store, error) {
	cfg, err := eng.resolver.Resolve(ctx, assistantID)
	if err != nil {
		return nil, err
	}
	return knowledgemysql.New(database.CentralDB, buildEmbedder(cfg)), nil
}
