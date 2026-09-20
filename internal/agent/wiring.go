// Package agent es la capa HTTP del módulo "Assistant IA": arma el motor
// (pkg/agent/*) con sus dependencias reales (BD central, Redis, proveedor
// LLM) y expone los endpoints HTTP. Fase 1 (ver
// docs/CHATBOT-AGENT-ARCHITECTURE.md §6.3): motor + canal web síncrono +
// RAG + memoria, SIN acciones de negocio ni panel todavía — el catálogo de
// acciones (pkg/agent/actions.Registry) arranca vacío a propósito.
package agent

import (
	"context"
	"fmt"
	"time"

	"tukifac/config"
	"tukifac/internal/agent/service"
	agentpkg "tukifac/pkg/agent"
	"tukifac/pkg/agent/actions"
	"tukifac/pkg/agent/bizctx"
	"tukifac/pkg/agent/knowledge"
	knowledgemysql "tukifac/pkg/agent/knowledge/mysql"
	"tukifac/pkg/agent/memory"
	"tukifac/pkg/agent/orchestrator"
	"tukifac/pkg/agent/providers"
	"tukifac/pkg/agent/providers/deepseek"
	"tukifac/pkg/agent/providers/openai"
	"tukifac/pkg/database"
	"tukifac/pkg/logger"

	"github.com/redis/go-redis/v9"
)

// engine agrupa todo lo que los handlers HTTP necesitan.
type engine struct {
	cfg      *config.Config
	resolver *service.Resolver
	orch     *orchestrator.Orchestrator

	conversations *memory.ConversationStore
	mem           memory.Memory

	// platformAssistantID: v1 solo hay un asistente de plataforma (ver
	// service.PlatformAssistantID) — todos los canales resuelven contra él.
	platformAssistantID uint

	chatLimiter *sessionRateLimiter
}

var eng *engine

// Init arranca el módulo: migra el esquema, siembra el asistente de
// plataforma si no existe, y arma el motor. Se invoca una vez al levantar
// el servidor (pkg/runtime/bootstrap.go), igual que el resto de módulos.
func Init(cfg *config.Config, rdb *redis.Client) error {
	if err := database.EnsureAssistantSchema(); err != nil {
		return fmt.Errorf("agent: migrar esquema: %w", err)
	}

	assistantID, err := service.PlatformAssistantID(context.Background(), database.CentralDB)
	if err != nil {
		return fmt.Errorf("agent: sembrar asistente de plataforma: %w", err)
	}

	resolver := service.NewResolver(database.CentralDB)

	types := agentpkg.NewTypeRegistry()
	if err := types.Register(agentpkg.AgentType{
		Key:       "commercial",
		Scope:     bizctx.ScopePlatform,
		Tools:     nil, // catálogo de acciones comerciales: Fase 5
		MaxRounds: 4,
	}); err != nil {
		return fmt.Errorf("agent: registrar tipo de agente: %w", err)
	}

	memStore := memory.NewStore(database.CentralDB, rdb)
	convStore := memory.NewConversationStore(database.CentralDB)
	actionsRegistry := actions.NewRegistry() // vacío en Fase 1, a propósito

	orch := orchestrator.New(orchestrator.Deps{
		Resolver:      resolver,
		Conversations: convStore,
		Memory:        memStore,
		Knowledge:     newKnowledgeRetriever(resolver),
		Actions:       actionsRegistry,
		Types:         types,
		LLMFor:        llmFor(resolver, cfg),
	})

	eng = &engine{
		cfg:                 cfg,
		resolver:            resolver,
		orch:                orch,
		conversations:       convStore,
		mem:                 memStore,
		platformAssistantID: assistantID,
		chatLimiter:         newSessionRateLimiter(15, time.Minute), // mismo tope que WhatsApp (ASSISTANT_RATE_LIMIT_PER_MIN default)
	}

	logger.L.Info("agent_module_initialized",
		"assistant_id", assistantID,
		"redis_enabled", rdb != nil,
	)
	return nil
}

// Shutdown no tiene nada que liberar en Fase 1 (sin cola/workers propios
// todavía — eso llega con el canal WhatsApp en Fase 4). Se deja el punto
// de extensión para no tener que tocar pkg/runtime/bootstrap.go otra vez.
func Shutdown() {}

// newKnowledgeRetriever construye el RAG con el embedder del proveedor
// ACTIVO resuelto en cada llamada (no cacheado aparte: resolver.Resolve ya
// tiene su propio TTL, y construir el cliente HTTP es barato, sin I/O).
func newKnowledgeRetriever(resolver *service.Resolver) knowledge.Retriever {
	return &lazyKnowledge{resolver: resolver}
}

type lazyKnowledge struct {
	resolver *service.Resolver
}

func (k *lazyKnowledge) Retrieve(ctx context.Context, scope knowledge.KBScope, query string, topK int) ([]knowledge.Chunk, error) {
	cfg, err := k.resolver.Resolve(ctx, scope.AssistantID)
	if err != nil {
		return nil, err
	}
	embedder := buildEmbedder(cfg)
	store := knowledgemysql.New(database.CentralDB, embedder)
	return store.Retrieve(ctx, scope, query, topK)
}

// llmFor resuelve el cliente LLM activo para un asistente, a partir de la
// config vigente (proveedor intercambiable, sin fallback automático entre
// proveedores — ver docs/CHATBOT-AGENT-ARCHITECTURE.md §2.6).
func llmFor(resolver *service.Resolver, cfg *config.Config) func(assistantID uint) (providers.LLM, error) {
	return func(assistantID uint) (providers.LLM, error) {
		c, err := resolver.Resolve(context.Background(), assistantID)
		if err != nil {
			return nil, err
		}
		return buildLLM(c, cfg), nil
	}
}

func buildLLM(c agentpkg.Config, appCfg *config.Config) providers.LLM {
	apiKey := firstNonEmpty(c.LLMAPIKey, appCfg.OpenAIAPIKey)
	baseURL := firstNonEmpty(c.LLMBaseURL, appCfg.OpenAIBaseURL)
	model := firstNonEmpty(c.LLMModel, appCfg.OpenAIChatModel)
	timeout := appCfg.OpenAITimeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	switch c.LLMProvider {
	case "deepseek":
		fallbackKey := appCfg.OpenAIAPIKey // clave de respaldo para embeddings (DeepSeek no las expone)
		return deepseek.New(apiKey, baseURL, model, fallbackKey, timeout)
	default:
		return openai.New(apiKey, baseURL, model, firstNonEmpty(c.EmbedModel, appCfg.OpenAIEmbedModel), timeout)
	}
}

func buildEmbedder(c agentpkg.Config) knowledge.Embedder {
	if eng == nil {
		return nil
	}
	return buildLLM(c, eng.cfg)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
