// Package providers define el puerto único que el motor usa para hablar con
// cualquier proveedor LLM (interfaz LLM), agnóstico de OpenAI/DeepSeek/etc.
package providers

import (
	"context"
	"encoding/json"
	"errors"
)

// ErrNotConfigured indica que la operación pedida (típicamente Embed) no
// tiene credenciales/soporte configurado. El llamador debe degradar con
// gracia (p. ej. el RAG cae a búsqueda por palabra clave), no fallar duro.
var ErrNotConfigured = errors.New("providers: operación no configurada")

type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// ToolCall es una invocación de herramienta que el modelo pidió ejecutar.
type ToolCall struct {
	ID        string
	Name      string
	Arguments json.RawMessage
}

// Message es un turno agnóstico del proveedor.
type Message struct {
	Role       Role
	Content    string
	Name       string     // nombre de la tool, solo si Role==RoleTool
	ToolCallID string     // id de la tool call que este mensaje responde, solo si Role==RoleTool
	ToolCalls  []ToolCall // solo si Role==RoleAssistant y el modelo pidió tools
}

// ToolSpec es la especificación de una herramienta expuesta al modelo
// (function calling), construida a partir de actions.Action.
type ToolSpec struct {
	Name        string
	Description string
	Parameters  json.RawMessage // JSON-Schema
}

type Usage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

type ChatRequest struct {
	Model       string
	Messages    []Message
	Tools       []ToolSpec
	Temperature float64
}

type ChatResponse struct {
	Message Message
	Usage   Usage
}

// LLM es el puerto único del motor hacia un proveedor. Toda implementación
// (OpenAI, DeepSeek, ...) vive detrás de esta interfaz — el motor nunca
// conoce el wire format real del proveedor.
type LLM interface {
	Chat(ctx context.Context, req ChatRequest) (ChatResponse, error)
	Embed(ctx context.Context, texts []string) ([][]float32, error)
	Name() string
}
