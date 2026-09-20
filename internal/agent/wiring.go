// Package agent es la capa HTTP del módulo "Assistant IA": arma el motor
// (pkg/agent/*) con sus dependencias reales (BD central, Redis, proveedor
// LLM, WhatsApp) y expone los endpoints HTTP. Fases 1+4+5 (ver
// docs/CHATBOT-AGENT-ARCHITECTURE.md §6.3): motor + canal web síncrono +
// canal WhatsApp (cola Redis) + RAG + memoria + catálogo de acciones
// comerciales — falta el panel en mistiq_central (Fase 3).
package agent

import (
	"context"
	"fmt"
	"time"

	"tukifac/config"
	commercialactions "tukifac/internal/agent/actions"
	"tukifac/internal/agent/service"
	agentpkg "tukifac/pkg/agent"
	"tukifac/pkg/agent/actions"
	"tukifac/pkg/agent/bizctx"
	"tukifac/pkg/agent/channels/whatsapp"
	"tukifac/pkg/agent/knowledge"
	knowledgemysql "tukifac/pkg/agent/knowledge/mysql"
	"tukifac/pkg/agent/memory"
	"tukifac/pkg/agent/orchestrator"
	"tukifac/pkg/agent/providers"
	"tukifac/pkg/agent/providers/deepseek"
	"tukifac/pkg/agent/providers/openai"
	"tukifac/pkg/agent/queue"
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

	whatsapp *whatsapp.Client
	queue    *queue.Queue
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
		Tools:     commercialactions.Names(),
		MaxRounds: 4,
	}); err != nil {
		return fmt.Errorf("agent: registrar tipo de agente: %w", err)
	}

	memStore := memory.NewStore(database.CentralDB, rdb)
	convStore := memory.NewConversationStore(database.CentralDB)

	actionsRegistry := actions.NewRegistry()
	if err := commercialactions.RegisterAll(actionsRegistry, cfg, memStore); err != nil {
		return fmt.Errorf("agent: registrar catálogo de acciones: %w", err)
	}

	orch := orchestrator.New(orchestrator.Deps{
		Resolver:      resolver,
		Conversations: convStore,
		Memory:        memStore,
		Knowledge:     newKnowledgeRetriever(resolver),
		Actions:       actionsRegistry,
		Types:         types,
		LLMFor:        llmFor(resolver, cfg),
	})

	waClient, err := buildWhatsApp(context.Background(), assistantID, cfg)
	if err != nil {
		return fmt.Errorf("agent: construir cliente WhatsApp: %w", err)
	}

	eng = &engine{
		cfg:                 cfg,
		resolver:            resolver,
		orch:                orch,
		conversations:       convStore,
		mem:                 memStore,
		platformAssistantID: assistantID,
		chatLimiter:         newSessionRateLimiter(cfg.AssistantRateLimitPerMin, time.Minute), // mismo tope que WhatsApp
		whatsapp:            waClient,
	}

	eng.queue = queue.New(rdb, queue.Config{
		Workers:    cfg.AssistantQueueWorkers,
		RatePerMin: cfg.AssistantRateLimitPerMin,
	}, whatsAppProcessor(orch, waClient))
	eng.queue.Start()

	logger.L.Info("agent_module_initialized",
		"assistant_id", assistantID,
		"redis_enabled", rdb != nil,
		"whatsapp_enabled", waClient.AccessToken != "",
	)
	return nil
}

// Shutdown detiene los workers de la cola de WhatsApp con gracia.
func Shutdown() {
	if eng != nil && eng.queue != nil {
		eng.queue.Stop()
	}
}

// whatsAppProcessor arma el Processor cableado a la cola: corre el
// orquestador y, si la respuesta no es silenciosa (Silent — p. ej. un
// humano ya tiene la conversación), la envía de vuelta por WhatsApp.
func whatsAppProcessor(orch *orchestrator.Orchestrator, wa *whatsapp.Client) queue.Processor {
	return func(ctx context.Context, in agentpkg.Inbound) {
		out, err := orch.Handle(ctx, in)
		if err != nil {
			logger.L.Warn("assistant_whatsapp_process_failed", "error", err.Error())
			return
		}
		if out.Silent || out.Text == "" {
			return
		}
		if _, err := wa.Send(ctx, out); err != nil {
			logger.L.Warn("assistant_whatsapp_send_failed", "error", err.Error())
		}
	}
}

// buildWhatsApp construye el cliente con las credenciales de la fila
// `assistants` (por instancia) y respaldo de `.env` — mismo patrón que
// buildLLM. Si no hay AccessToken/PhoneNumberID configurados (ni por
// instancia ni por .env), el cliente igual se construye (para que
// handleWhatsAppVerify/Receive no sean nil) pero cualquier intento real de
// envío fallará limpio contra la API de Meta.
func buildWhatsApp(ctx context.Context, assistantID uint, appCfg *config.Config) (*whatsapp.Client, error) {
	var row database.Assistant
	if err := database.CentralDB.WithContext(ctx).First(&row, assistantID).Error; err != nil {
		return nil, fmt.Errorf("agent: leer credenciales WhatsApp: %w", err)
	}
	return whatsapp.New(
		firstNonEmpty(row.WhatsAppPhoneNumberID, appCfg.WhatsAppPhoneNumberID),
		firstNonEmpty(row.WhatsAppAccessToken, appCfg.WhatsAppAccessToken),
		firstNonEmpty(row.WhatsAppAppSecret, appCfg.WhatsAppAppSecret),
		firstNonEmpty(row.WhatsAppVerifyToken, appCfg.WhatsAppVerifyToken),
		appCfg.WhatsAppGraphURL,
		appCfg.WhatsAppAPIVersion,
	), nil
}

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
