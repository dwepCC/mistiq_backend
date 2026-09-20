// Package service contiene la capa de servicio del módulo agente: resuelve
// la Config vigente de un asistente desde BD (con caché), y expone la
// operación de alta del único asistente de plataforma (v1, ver
// docs/CHATBOT-AGENT-ARCHITECTURE.md §5).
package service

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	agentpkg "tukifac/pkg/agent"
	"tukifac/pkg/database"

	"gorm.io/gorm"
)

const resolverCacheTTL = 5 * time.Minute

type cacheEntry struct {
	cfg       agentpkg.Config
	expiresAt time.Time
}

// Resolver implementa agent.Resolver con caché en memoria de 5 minutos —
// se invalida explícitamente al guardar cambios desde el panel (Fase 3);
// un cambio hecho por otra vía (script, migración manual) puede tardar
// hasta ese TTL en reflejarse, igual que el original.
type Resolver struct {
	db *gorm.DB

	mu    sync.RWMutex
	cache map[uint]cacheEntry
}

var _ agentpkg.Resolver = (*Resolver)(nil)

func NewResolver(db *gorm.DB) *Resolver {
	return &Resolver{db: db, cache: make(map[uint]cacheEntry)}
}

func (r *Resolver) Resolve(ctx context.Context, assistantID uint) (agentpkg.Config, error) {
	r.mu.RLock()
	entry, ok := r.cache[assistantID]
	r.mu.RUnlock()
	if ok && time.Now().Before(entry.expiresAt) {
		return entry.cfg, nil
	}

	var row database.Assistant
	if err := r.db.WithContext(ctx).First(&row, assistantID).Error; err != nil {
		return agentpkg.Config{}, fmt.Errorf("service: resolver asistente %d: %w", assistantID, err)
	}
	cfg := toConfig(row)

	r.mu.Lock()
	r.cache[assistantID] = cacheEntry{cfg: cfg, expiresAt: time.Now().Add(resolverCacheTTL)}
	r.mu.Unlock()

	return cfg, nil
}

// Invalidate limpia la entrada cacheada de un asistente (llamar justo
// antes de aplicar un cambio de config desde el panel).
func (r *Resolver) Invalidate(assistantID uint) {
	r.mu.Lock()
	delete(r.cache, assistantID)
	r.mu.Unlock()
}

func toConfig(row database.Assistant) agentpkg.Config {
	var enabledTools []string
	if strings.TrimSpace(row.EnabledTools) != "" {
		enabledTools = strings.Split(row.EnabledTools, ",")
	}
	return agentpkg.Config{
		ID:                   row.ID,
		Name:                 row.Name,
		Locale:               row.DefaultLocale,
		Active:               row.Active && row.Status == "active",
		LLMProvider:          row.LLMProvider,
		LLMModel:             row.LLMModel,
		LLMBaseURL:           row.LLMBaseURL,
		LLMAPIKey:            row.LLMAPIKey,
		EmbedProvider:        row.EmbedProvider,
		EmbedModel:           row.EmbedModel,
		EmbedBaseURL:         row.EmbedBaseURL,
		EmbedAPIKey:          row.EmbedAPIKey,
		SystemPromptOverride: row.SystemPromptOverride,
		PersonalityOverride:  row.PersonalityOverride,
		KnowledgeBaseID:      row.ID, // v1: kb_id == assistant_id (ver knowledge.ScopeFor)
		AgentType:            row.AgentType,
		EnabledTools:         enabledTools,
		MaxToolRounds:        row.MaxToolRounds,
	}
}

// PlatformAssistantID resuelve "el asistente de plataforma activo" — en v1
// solo existe uno (owner_kind="platform"), igual que en el original. Si no
// existe todavía, lo crea (EnsureBendeyAssistant-equivalente).
func PlatformAssistantID(ctx context.Context, db *gorm.DB) (uint, error) {
	var row database.Assistant
	err := db.WithContext(ctx).
		Where("owner_kind = ? AND status = ?", "platform", "active").
		Order("id ASC").
		First(&row).Error
	if err == nil {
		return row.ID, nil
	}
	if err != gorm.ErrRecordNotFound {
		return 0, fmt.Errorf("service: resolver asistente de plataforma: %w", err)
	}

	row = database.Assistant{
		Name:          "Mistiq",
		Slug:          "mistiq-comercial",
		OwnerKind:     "platform",
		Status:        "active",
		DefaultLocale: "es",
		LLMProvider:   "openai",
		AgentType:     "commercial",
		Active:        true,
	}
	if err := db.WithContext(ctx).Create(&row).Error; err != nil {
		return 0, fmt.Errorf("service: crear asistente de plataforma: %w", err)
	}
	return row.ID, nil
}
