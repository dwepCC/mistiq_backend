package agent

import (
	"strconv"
	"time"

	"tukifac/pkg/database"

	"github.com/gofiber/fiber/v3"
)

type metricsDTO struct {
	TotalConversations int64 `json:"total_conversations"`
	NeedsHuman         int64 `json:"needs_human"`
	InProgress         int64 `json:"in_progress"`
	LeadsTotal         int64 `json:"leads_total"`
	Messages7d         int64 `json:"messages_7d"`
}

// handleMetrics: GET /assistant/metrics — chips rápidos de la bandeja.
func handleMetrics(c fiber.Ctx) error {
	if eng == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "asistente no disponible"})
	}
	id := eng.platformAssistantID
	var m metricsDTO
	database.CentralDB.Model(&database.AssistantConversation{}).Where("assistant_id = ?", id).Count(&m.TotalConversations)
	database.CentralDB.Model(&database.AssistantConversation{}).Where("assistant_id = ? AND status = ?", id, database.ConvStatusNeedsHuman).Count(&m.NeedsHuman)
	database.CentralDB.Model(&database.AssistantConversation{}).Where("assistant_id = ? AND status = ?", id, database.ConvStatusHuman).Count(&m.InProgress)
	database.CentralDB.Model(&database.AssistantLead{}).Where("assistant_id = ?", id).Count(&m.LeadsTotal)
	database.CentralDB.Model(&database.AssistantMessage{}).
		Joins("JOIN assistant_conversations ON assistant_conversations.id = assistant_messages.conversation_id").
		Where("assistant_conversations.assistant_id = ? AND assistant_messages.created_at >= ?", id, time.Now().AddDate(0, 0, -7)).
		Count(&m.Messages7d)
	return c.JSON(fiber.Map{"data": m})
}

type funnelDTO struct {
	Conversations int64 `json:"conversations"`
	Engaged       int64 `json:"engaged"`
	AskedPricing  int64 `json:"asked_pricing"`
	Leads         int64 `json:"leads"`
	Qualified     int64 `json:"qualified"`
	Customers     int64 `json:"customers"`
	Demos         int64 `json:"demos"`
	Handoffs      int64 `json:"handoffs"`
}

type dailyPointDTO struct {
	Date          string `json:"date"`
	Conversations int64  `json:"conversations"`
	Leads         int64  `json:"leads"`
}

type toolUsageDTO struct {
	Name          string `json:"name"`
	Executions    int64  `json:"executions"`
	Conversations int64  `json:"conversations"`
}

type tokensDTO struct {
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
}

type analyticsDTO struct {
	Funnel      funnelDTO       `json:"funnel"`
	Daily       []dailyPointDTO `json:"daily"`
	Tools       []toolUsageDTO  `json:"tools"`
	Tokens      tokensDTO       `json:"tokens"`
	MeasureNote string          `json:"measure_note"`
}

// handleAnalytics: GET /assistant/analytics?days=14
//
// Heurísticas explícitas (no hay tracking de eventos dedicado, se infieren
// de lo que ya se persiste — ver measure_note que viaja al frontend):
//   - "engaged" = conversación con 2+ turnos de usuario (más que el mensaje
//     inicial: hubo ida y vuelta real, no solo "hola").
//   - "asked_pricing" = algún mensaje de usuario contiene una palabra
//     asociada a precio/plan — aproximado, no NLP real.
//   - "customers" = leads con tenant_id asignado (se creó una cuenta real).
//   - "handoffs" = conversaciones que en algún momento quedaron con
//     needs_human_reason no vacío o pasaron a "human"/"needs_human".
func handleAnalytics(c fiber.Ctx) error {
	if eng == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "asistente no disponible"})
	}
	id := eng.platformAssistantID
	days, err := strconv.Atoi(c.Query("days"))
	if err != nil || days <= 0 || days > 90 {
		days = 14
	}
	since := time.Now().AddDate(0, 0, -days)

	var funnel funnelDTO
	database.CentralDB.Model(&database.AssistantConversation{}).
		Where("assistant_id = ? AND created_at >= ?", id, since).Count(&funnel.Conversations)

	database.CentralDB.Raw(`
		SELECT COUNT(*) FROM assistant_conversations c
		WHERE c.assistant_id = ? AND c.created_at >= ?
		AND (SELECT COUNT(*) FROM assistant_messages m WHERE m.conversation_id = c.id AND m.role = 'user') >= 2
	`, id, since).Scan(&funnel.Engaged)

	database.CentralDB.Raw(`
		SELECT COUNT(DISTINCT c.id) FROM assistant_conversations c
		JOIN assistant_messages m ON m.conversation_id = c.id
		WHERE c.assistant_id = ? AND c.created_at >= ? AND m.role = 'user'
		AND (m.content LIKE '%precio%' OR m.content LIKE '%costo%' OR m.content LIKE '%cuesta%' OR m.content LIKE '%plan%' OR m.content LIKE '%cuánto%' OR m.content LIKE '%cuanto%')
	`, id, since).Scan(&funnel.AskedPricing)

	database.CentralDB.Model(&database.AssistantLead{}).Where("assistant_id = ? AND created_at >= ?", id, since).Count(&funnel.Leads)
	database.CentralDB.Model(&database.AssistantLead{}).
		Where("assistant_id = ? AND created_at >= ? AND qualification IN ('hot','warm')", id, since).Count(&funnel.Qualified)
	database.CentralDB.Model(&database.AssistantLead{}).
		Where("assistant_id = ? AND created_at >= ? AND tenant_id IS NOT NULL", id, since).Count(&funnel.Customers)
	database.CentralDB.Model(&database.AssistantLead{}).
		Where("assistant_id = ? AND created_at >= ? AND kind = 'demo'", id, since).Count(&funnel.Demos)
	database.CentralDB.Model(&database.AssistantConversation{}).
		Where("assistant_id = ? AND created_at >= ? AND (status IN (?, ?) OR needs_human_reason <> '')",
			id, since, database.ConvStatusNeedsHuman, database.ConvStatusHuman).Count(&funnel.Handoffs)

	// Slices inicializados vacíos (no nil): un nil slice serializa a JSON
	// como `null`, y el frontend siempre espera un array (llama .length /
	// .map sobre el campo sin chequear null antes) — un `null` acá
	// rompería la página con un TypeError, no un simple "sin datos".
	dailyRows := []dailyPointDTO{}
	database.CentralDB.Raw(`
		SELECT DATE(created_at) AS date, COUNT(*) AS conversations, 0 AS leads
		FROM assistant_conversations WHERE assistant_id = ? AND created_at >= ?
		GROUP BY DATE(created_at) ORDER BY date
	`, id, since).Scan(&dailyRows)
	leadDaily := []dailyPointDTO{}
	database.CentralDB.Raw(`
		SELECT DATE(created_at) AS date, 0 AS conversations, COUNT(*) AS leads
		FROM assistant_leads WHERE assistant_id = ? AND created_at >= ?
		GROUP BY DATE(created_at) ORDER BY date
	`, id, since).Scan(&leadDaily)
	dailyRows = mergeDailyLeads(dailyRows, leadDaily)

	tools := []toolUsageDTO{}
	database.CentralDB.Raw(`
		SELECT m.tool_name AS name, COUNT(*) AS executions, COUNT(DISTINCT m.conversation_id) AS conversations
		FROM assistant_messages m
		JOIN assistant_conversations c ON c.id = m.conversation_id
		WHERE c.assistant_id = ? AND m.role = 'tool' AND m.created_at >= ? AND m.tool_name <> ''
		GROUP BY m.tool_name ORDER BY executions DESC
	`, id, since).Scan(&tools)

	var tokens tokensDTO
	database.CentralDB.Raw(`
		SELECT COALESCE(SUM(m.prompt_tokens),0) AS prompt_tokens, COALESCE(SUM(m.completion_tokens),0) AS completion_tokens
		FROM assistant_messages m
		JOIN assistant_conversations c ON c.id = m.conversation_id
		WHERE c.assistant_id = ? AND m.created_at >= ? AND m.role = 'assistant'
	`, id, since).Scan(&tokens)

	return c.JSON(fiber.Map{"data": analyticsDTO{
		Funnel: funnel, Daily: dailyRows, Tools: tools, Tokens: tokens,
		MeasureNote: "\"Preguntaron por precios\" y \"conversaron de verdad\" son aproximaciones (palabras clave / cantidad de turnos), no medición exacta de intención.",
	}})
}

func mergeDailyLeads(convDaily, leadDaily []dailyPointDTO) []dailyPointDTO {
	byDate := make(map[string]*dailyPointDTO, len(convDaily))
	order := make([]string, 0, len(convDaily))
	for i := range convDaily {
		byDate[convDaily[i].Date] = &convDaily[i]
		order = append(order, convDaily[i].Date)
	}
	for _, l := range leadDaily {
		if existing, ok := byDate[l.Date]; ok {
			existing.Leads = l.Leads
		} else {
			cp := l
			byDate[l.Date] = &cp
			order = append(order, l.Date)
		}
	}
	out := make([]dailyPointDTO, 0, len(byDate))
	seen := make(map[string]bool, len(byDate))
	for _, d := range order {
		if seen[d] {
			continue
		}
		seen[d] = true
		out = append(out, *byDate[d])
	}
	return out
}
