package agent

import "context"

// Config es la fila `assistants` mapeada a lo que el motor necesita en runtime.
type Config struct {
	ID     uint
	Name   string
	Locale string
	Active bool

	LLMProvider string
	LLMModel    string
	LLMBaseURL  string
	LLMAPIKey   string

	EmbedProvider string
	EmbedModel    string
	EmbedBaseURL  string
	EmbedAPIKey   string

	SystemPromptOverride string
	PersonalityOverride  string

	KnowledgeBaseID uint

	AgentType     string
	EnabledTools  []string
	MaxToolRounds int
}

// Resolver resuelve la Config vigente de un asistente por ID. Implementado
// fuera de este paquete (capa HTTP/servicio), con caché a su criterio.
type Resolver interface {
	Resolve(ctx context.Context, assistantID uint) (Config, error)
}
