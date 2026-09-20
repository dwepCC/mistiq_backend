// Package memory guarda y recupera el historial de una conversación,
// combinando Redis (caliente, ventana reciente con TTL) y MySQL
// (permanente) detrás de un único puerto — el orquestador no sabe cuál de
// las dos fuentes respondió.
package memory

import (
	"context"
	"time"
)

type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
	RoleAgent     Role = "agent" // respuesta de un asesor humano
)

// Turn es un mensaje persistido del historial. Los contadores de tokens
// vienen del proveedor real (nunca estimados con una heurística tipo
// len/4) — solo se llenan en turnos `assistant`.
type Turn struct {
	ID               uint
	ConversationID   uint
	Role             Role
	Content          string
	ToolName         string
	ToolCallID       string
	ChannelMsgID     string
	ReplyToID        uint
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
	CreatedAt        time.Time
}

// Memory es el puerto que usa el orquestador.
type Memory interface {
	// LoadContext trae hasta `window` turnos más recientes, en orden
	// cronológico ascendente.
	LoadContext(ctx context.Context, conversationID uint, window int) ([]Turn, error)
	// AppendTurn persiste un turno nuevo (siempre en MySQL; best-effort en
	// Redis si está disponible).
	AppendTurn(ctx context.Context, t Turn) (Turn, error)
	// MarkNeedsHuman marca la conversación como necesitando un asesor
	// humano. `reason` queda reservado para auditoría futura.
	MarkNeedsHuman(ctx context.Context, conversationID uint, reason string) error
}
