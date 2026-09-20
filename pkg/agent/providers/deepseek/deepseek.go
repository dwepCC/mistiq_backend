// Package deepseek implementa providers.LLM contra DeepSeek, reutilizando
// el transporte del cliente OpenAI apuntado a otro base URL: la API de chat
// de DeepSeek es compatible con la de OpenAI (mismo wire format). DeepSeek
// no expone endpoint de embeddings propio, así que Embed delega
// opcionalmente a un cliente OpenAI de respaldo si hay clave configurada;
// sin ella, degrada con providers.ErrNotConfigured (el RAG cae a búsqueda
// por palabra clave, el chat sigue funcionando).
package deepseek

import (
	"context"
	"time"

	"tukifac/pkg/agent/providers"
	"tukifac/pkg/agent/providers/openai"
)

const (
	defaultBaseURL = "https://api.deepseek.com"
	// defaultChatModel: DeepSeek necesita su PROPIO default — si se deja
	// pasar un chatModel vacío directo a openai.New(), ese paquete aplica
	// SU default ("gpt-4o-mini"), que DeepSeek rechaza de inmediato
	// ("The supported API model names are ..., but you passed
	// gpt-4o-mini."). Bug real encontrado verificando con una cuenta
	// DeepSeek real sin modelo configurado explícitamente.
	//
	// "deepseek-flash" (no "deepseek-chat") confirmado contra la
	// documentación oficial vigente (https://api-docs.deepseek.com/) el
	// 2026-09-20 — nombres de modelo más antiguos quedaron obsoletos.
	defaultChatModel = "deepseek-flash"
)

type Client struct {
	chat     *openai.Client
	fallback *openai.Client // opcional, para Embed
}

var _ providers.LLM = (*Client)(nil)

// New crea el cliente DeepSeek. fallbackAPIKey puede ir vacío: en ese caso
// Embed siempre devuelve providers.ErrNotConfigured. fallbackBaseURL/
// fallbackEmbedModel son del proveedor de respaldo (típicamente OpenAI
// real, no DeepSeek) — antes se ignoraban y el respaldo siempre usaba los
// defaults de openai.New() sin importar qué hubiera configurado el
// usuario para embeddings específicamente.
func New(apiKey, baseURL, chatModel string, fallbackAPIKey, fallbackBaseURL, fallbackEmbedModel string, timeout time.Duration) *Client {
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	if chatModel == "" {
		chatModel = defaultChatModel
	}
	c := &Client{
		chat: openai.New(apiKey, baseURL, chatModel, "", timeout),
	}
	if fallbackAPIKey != "" {
		c.fallback = openai.New(fallbackAPIKey, fallbackBaseURL, "", fallbackEmbedModel, timeout)
	}
	return c
}

func (c *Client) Name() string { return "deepseek" }

func (c *Client) Chat(ctx context.Context, req providers.ChatRequest) (providers.ChatResponse, error) {
	return c.chat.Chat(ctx, req)
}

func (c *Client) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if c.fallback == nil {
		return nil, providers.ErrNotConfigured
	}
	return c.fallback.Embed(ctx, texts)
}
