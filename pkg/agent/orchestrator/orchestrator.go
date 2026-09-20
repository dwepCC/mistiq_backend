// Package orchestrator es el único punto de entrada al motor: recibe un
// agent.Inbound, decide qué hacer (saludo desnudo / handoff humano / turno
// real con LLM), y produce un agent.Outbound.
package orchestrator

import (
	"context"
	"fmt"
	"strings"

	agentpkg "tukifac/pkg/agent"
	"tukifac/pkg/agent/actions"
	"tukifac/pkg/agent/bizctx"
	"tukifac/pkg/agent/executor"
	"tukifac/pkg/agent/knowledge"
	"tukifac/pkg/agent/memory"
	"tukifac/pkg/agent/policies"
	"tukifac/pkg/agent/prompt"
	"tukifac/pkg/agent/providers"
	"tukifac/pkg/database"
)

const (
	historyWindow = 20
	ragTopK       = 8
	maxToolRounds = 3 // default de fábrica del motor; el tipo de agente/instancia puede subirlo
)

// Deps son las dependencias inyectadas al orquestador.
type Deps struct {
	Resolver      agentpkg.Resolver
	Conversations *memory.ConversationStore
	Memory        memory.Memory
	Knowledge     knowledge.Retriever
	Actions       *actions.Registry
	Types         *agentpkg.TypeRegistry
	// LLMFor resuelve el cliente LLM activo para un asistente (permite
	// hot-swap del proveedor al cambiar config sin reiniciar el proceso —
	// ver capa de wiring en internal/agent).
	LLMFor func(assistantID uint) (providers.LLM, error)
}

type Orchestrator struct {
	deps Deps
}

func New(deps Deps) *Orchestrator {
	return &Orchestrator{deps: deps}
}

// Handle procesa un mensaje entrante y produce la respuesta. Orden real
// (verificado contra el original, ver docs/CHATBOT-AGENT-ARCHITECTURE.md
// §2.1 — el turno del usuario se persiste SIEMPRE, incluso si después la
// conversación resulta estar en manos de un humano):
//
//  1. Resolver Config del asistente.
//  2. GetOrCreate de la conversación.
//  3. Cortocircuito de saludo desnudo (conversación nueva + texto = solo
//     saludo): responder fijo, sin tocar LLM/RAG.
//  4. Persistir SIEMPRE el turno del usuario.
//  5. Si el estado no es "bot" (human/needs_human/closed): responder
//     Silent, sin generar nada más.
//  6. Cargar historial, RAG, armar prompt, correr ReAct, persistir la
//     respuesta con tokens reales.
func (o *Orchestrator) Handle(ctx context.Context, in agentpkg.Inbound) (agentpkg.Outbound, error) {
	cfg, err := o.deps.Resolver.Resolve(ctx, in.AssistantID)
	if err != nil {
		return agentpkg.Outbound{}, fmt.Errorf("orchestrator: resolver config: %w", err)
	}
	if !cfg.Active {
		return agentpkg.Outbound{Silent: true}, nil
	}

	conv, isNew, err := o.deps.Conversations.GetOrCreate(ctx, in.AssistantID, in.Channel, in.From, firstNonEmpty(in.Locale, cfg.Locale))
	if err != nil {
		return agentpkg.Outbound{}, fmt.Errorf("orchestrator: obtener conversación: %w", err)
	}

	text := strings.TrimSpace(in.Text)

	// Persistir SIEMPRE el turno del usuario, antes de mirar el estado o de
	// cortocircuitar por saludo desnudo — así un asesor humano que revise
	// la conversación después ve el hilo completo, aunque el saludo no
	// haya tocado el LLM.
	if _, err := o.deps.Memory.AppendTurn(ctx, memory.Turn{
		ConversationID: conv.ID,
		Role:           memory.RoleUser,
		Content:        text,
		ChannelMsgID:   in.ChannelMsgID,
	}); err != nil {
		return agentpkg.Outbound{}, fmt.Errorf("orchestrator: persistir turno de usuario: %w", err)
	}
	_ = o.deps.Conversations.TouchLastMsgAt(ctx, conv.ID)

	if isNew && isBareGreeting(text) {
		reply := bareGreetingReply(cfg)
		if _, err := o.deps.Memory.AppendTurn(ctx, memory.Turn{
			ConversationID: conv.ID,
			Role:           memory.RoleAssistant,
			Content:        reply,
		}); err != nil {
			return agentpkg.Outbound{}, fmt.Errorf("orchestrator: persistir saludo: %w", err)
		}
		return agentpkg.Outbound{To: in.From, Text: reply}, nil
	}
	isFirstTurn := isNew && text != ""

	if conv.Status != database.ConvStatusBot {
		return agentpkg.Outbound{Silent: true}, nil
	}

	history, err := o.deps.Memory.LoadContext(ctx, conv.ID, historyWindow)
	if err != nil {
		return agentpkg.Outbound{}, fmt.Errorf("orchestrator: cargar historial: %w", err)
	}

	snippets := o.retrieve(ctx, in.AssistantID, text)

	def := o.definitionFor(cfg)
	systemPrompt := prompt.NewComposer().BuildSystem(prompt.Input{
		Locale:              firstNonEmpty(cfg.Locale, "es"),
		SystemOverride:      cfg.SystemPromptOverride,
		PersonalityOverride: cfg.PersonalityOverride,
		IsFirstTurn:         isFirstTurn,
		KnowledgeSnippets:   snippets,
	})

	messages := buildMessages(systemPrompt, history)

	llm, err := o.deps.LLMFor(in.AssistantID)
	if err != nil {
		return agentpkg.Outbound{}, fmt.Errorf("orchestrator: resolver proveedor LLM: %w", err)
	}

	bc := bizctx.Context{
		Scope:          def.Scope(),
		AssistantID:    in.AssistantID,
		ConversationID: conv.ID,
		ContactRef:     in.From,
		Channel:        in.Channel,
		Locale:         cfg.Locale,
	}

	toolset := def.Toolset()
	specs := o.deps.Actions.SpecsFor(toolset)
	tools := toToolSpecs(specs)

	exec := executor.New(o.deps.Actions, defaultPolicies(o.deps.Memory, toolset), nil)
	react := executor.NewReAct(llm, exec)

	rounds := def.MaxRounds()
	if rounds <= 0 {
		rounds = maxToolRounds
	}

	reply, usage, err := react.Run(ctx, bc, def.Model(), messages, tools, rounds)
	if err != nil {
		return agentpkg.Outbound{}, fmt.Errorf("orchestrator: ejecutar ReAct: %w", err)
	}

	if _, err := o.deps.Memory.AppendTurn(ctx, memory.Turn{
		ConversationID:   conv.ID,
		Role:             memory.RoleAssistant,
		Content:          reply.Content,
		PromptTokens:     usage.PromptTokens,
		CompletionTokens: usage.CompletionTokens,
		TotalTokens:      usage.TotalTokens,
	}); err != nil {
		return agentpkg.Outbound{}, fmt.Errorf("orchestrator: persistir turno de respuesta: %w", err)
	}

	return agentpkg.Outbound{To: in.From, Text: reply.Content}, nil
}

func (o *Orchestrator) retrieve(ctx context.Context, assistantID uint, query string) []string {
	if o.deps.Knowledge == nil || strings.TrimSpace(query) == "" {
		return nil
	}
	chunks, err := o.deps.Knowledge.Retrieve(ctx, knowledge.ScopeFor(assistantID), query, ragTopK)
	if err != nil {
		return nil
	}
	out := make([]string, len(chunks))
	for i, c := range chunks {
		out[i] = c.Content
	}
	return out
}

func (o *Orchestrator) definitionFor(cfg agentpkg.Config) agentpkg.Definition {
	agentType, ok := o.deps.Types.Get(cfg.AgentType)
	if !ok {
		agentType = agentpkg.AgentType{Key: cfg.AgentType, Scope: bizctx.ScopePlatform}
	}
	return agentpkg.Definition{Type: agentType, Instance: cfg}
}

func defaultPolicies(mem memory.Memory, toolset []string) []policies.Policy {
	return []policies.Policy{
		policies.ScopeIntegrity{},
		policies.NewToolset(toolset),
		policies.NewConfirmation(mem),
		policies.NewRateLimit(0, 0),
	}
}

// buildMessages arma la secuencia final para el LLM: system + historial.
// Omite turnos `tool` (auditoría interna, no se reenvían sueltos porque sin
// su `assistant`-`tool_call` padre rompería el formato del proveedor) y
// mapea `agent` (respuesta humana) a `assistant` para que el modelo
// entienda el contexto de un handoff previo.
func buildMessages(systemPrompt string, history []memory.Turn) []providers.Message {
	messages := make([]providers.Message, 0, len(history)+1)
	messages = append(messages, providers.Message{Role: providers.RoleSystem, Content: systemPrompt})
	for _, t := range history {
		switch t.Role {
		case memory.RoleTool:
			continue
		case memory.RoleAgent:
			messages = append(messages, providers.Message{Role: providers.RoleAssistant, Content: t.Content})
		default:
			messages = append(messages, providers.Message{Role: providers.Role(t.Role), Content: t.Content})
		}
	}
	return messages
}

func toToolSpecs(list []actions.Action) []providers.ToolSpec {
	if len(list) == 0 {
		return nil
	}
	out := make([]providers.ToolSpec, len(list))
	for i, a := range list {
		out[i] = providers.ToolSpec{Name: a.Name(), Description: a.Description(), Parameters: a.Schema()}
	}
	return out
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

var bareGreetings = map[string]bool{
	"hola": true, "hola!": true, "buenas": true, "buenos dias": true, "buenos días": true,
	"buenas tardes": true, "buenas noches": true, "hey": true, "hi": true, "hello": true,
	"que tal": true, "qué tal": true,
}

func isBareGreeting(text string) bool {
	norm := strings.ToLower(strings.TrimSpace(text))
	norm = strings.Trim(norm, "¡!¿?. ")
	if norm == "" {
		return false
	}
	if len(strings.Fields(norm)) > 3 {
		return false
	}
	return bareGreetings[norm]
}

func bareGreetingReply(cfg agentpkg.Config) string {
	name := firstNonEmpty(cfg.Name, "Mistiq")
	return fmt.Sprintf("¡Hola! Soy el asesor comercial de %s 👋 ¿En qué te puedo ayudar?", name)
}
