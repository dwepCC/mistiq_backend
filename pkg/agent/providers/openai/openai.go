// Package openai implementa providers.LLM contra la API de OpenAI (Chat
// Completions + Embeddings), vía net/http directo (sin SDK) para no traer
// una dependencia nueva solo por esto.
package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"tukifac/pkg/agent/providers"
)

const (
	defaultBaseURL    = "https://api.openai.com/v1"
	defaultChatModel  = "gpt-4o-mini"
	defaultEmbedModel = "text-embedding-3-small"
)

type Client struct {
	apiKey     string
	baseURL    string
	chatModel  string
	embedModel string
	http       *http.Client
}

var _ providers.LLM = (*Client)(nil)

func New(apiKey, baseURL, chatModel, embedModel string, timeout time.Duration) *Client {
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	if chatModel == "" {
		chatModel = defaultChatModel
	}
	if embedModel == "" {
		embedModel = defaultEmbedModel
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &Client{
		apiKey:     apiKey,
		baseURL:    baseURL,
		chatModel:  chatModel,
		embedModel: embedModel,
		http:       &http.Client{Timeout: timeout},
	}
}

func (c *Client) Name() string { return "openai" }

// --- wire format OpenAI Chat Completions ---

type wireMessage struct {
	Role       string         `json:"role"`
	Content    string         `json:"content,omitempty"`
	Name       string         `json:"name,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
	ToolCalls  []wireToolCall `json:"tool_calls,omitempty"`
}

type wireToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function wireToolCallFunc `json:"function"`
}

type wireToolCallFunc struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type wireTool struct {
	Type     string       `json:"type"`
	Function wireFunction `json:"function"`
}

type wireFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type wireChatRequest struct {
	Model       string        `json:"model"`
	Messages    []wireMessage `json:"messages"`
	Tools       []wireTool    `json:"tools,omitempty"`
	Temperature float64       `json:"temperature,omitempty"`
}

type wireChatChoice struct {
	Message wireMessage `json:"message"`
}

type wireUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type wireChatResponse struct {
	Choices []wireChatChoice `json:"choices"`
	Usage   wireUsage        `json:"usage"`
	Error   *wireError       `json:"error,omitempty"`
}

type wireError struct {
	Message string `json:"message"`
}

func toWireMessages(msgs []providers.Message) []wireMessage {
	out := make([]wireMessage, 0, len(msgs))
	for _, m := range msgs {
		wm := wireMessage{
			Role:       string(m.Role),
			Content:    m.Content,
			Name:       m.Name,
			ToolCallID: m.ToolCallID,
		}
		for _, tc := range m.ToolCalls {
			wm.ToolCalls = append(wm.ToolCalls, wireToolCall{
				ID:   tc.ID,
				Type: "function",
				Function: wireToolCallFunc{
					Name:      tc.Name,
					Arguments: string(tc.Arguments),
				},
			})
		}
		out = append(out, wm)
	}
	return out
}

func toWireTools(specs []providers.ToolSpec) []wireTool {
	if len(specs) == 0 {
		return nil
	}
	out := make([]wireTool, 0, len(specs))
	for _, s := range specs {
		out = append(out, wireTool{
			Type: "function",
			Function: wireFunction{
				Name:        s.Name,
				Description: s.Description,
				Parameters:  s.Parameters,
			},
		})
	}
	return out
}

func (c *Client) Chat(ctx context.Context, req providers.ChatRequest) (providers.ChatResponse, error) {
	model := req.Model
	if model == "" {
		model = c.chatModel
	}
	body := wireChatRequest{
		Model:       model,
		Messages:    toWireMessages(req.Messages),
		Tools:       toWireTools(req.Tools),
		Temperature: req.Temperature,
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return providers.ChatResponse{}, fmt.Errorf("openai: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(raw))
	if err != nil {
		return providers.ChatResponse{}, fmt.Errorf("openai: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return providers.ChatResponse{}, fmt.Errorf("openai: request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return providers.ChatResponse{}, fmt.Errorf("openai: read response: %w", err)
	}

	var wireResp wireChatResponse
	if err := json.Unmarshal(respBody, &wireResp); err != nil {
		return providers.ChatResponse{}, fmt.Errorf("openai: unmarshal response (status %d): %w", resp.StatusCode, err)
	}
	if wireResp.Error != nil {
		return providers.ChatResponse{}, fmt.Errorf("openai: api error: %s", wireResp.Error.Message)
	}
	if resp.StatusCode != http.StatusOK {
		return providers.ChatResponse{}, fmt.Errorf("openai: status %d", resp.StatusCode)
	}
	if len(wireResp.Choices) == 0 {
		return providers.ChatResponse{}, fmt.Errorf("openai: respuesta sin choices")
	}

	choice := wireResp.Choices[0].Message
	msg := providers.Message{
		Role:    providers.Role(choice.Role),
		Content: choice.Content,
	}
	for _, tc := range choice.ToolCalls {
		msg.ToolCalls = append(msg.ToolCalls, providers.ToolCall{
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: json.RawMessage(tc.Function.Arguments),
		})
	}

	return providers.ChatResponse{
		Message: msg,
		Usage: providers.Usage{
			PromptTokens:     wireResp.Usage.PromptTokens,
			CompletionTokens: wireResp.Usage.CompletionTokens,
			TotalTokens:      wireResp.Usage.TotalTokens,
		},
	}, nil
}

type wireEmbedRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type wireEmbedItem struct {
	Embedding []float32 `json:"embedding"`
	Index     int       `json:"index"`
}

type wireEmbedResponse struct {
	Data  []wireEmbedItem `json:"data"`
	Error *wireError      `json:"error,omitempty"`
}

func (c *Client) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	body := wireEmbedRequest{Model: c.embedModel, Input: texts}
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("openai: marshal embed request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/embeddings", bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("openai: build embed request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("openai: embed request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("openai: read embed response: %w", err)
	}

	var wireResp wireEmbedResponse
	if err := json.Unmarshal(respBody, &wireResp); err != nil {
		return nil, fmt.Errorf("openai: unmarshal embed response (status %d): %w", resp.StatusCode, err)
	}
	if wireResp.Error != nil {
		return nil, fmt.Errorf("openai: embed api error: %s", wireResp.Error.Message)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("openai: embed status %d", resp.StatusCode)
	}

	out := make([][]float32, len(texts))
	for _, item := range wireResp.Data {
		if item.Index >= 0 && item.Index < len(out) {
			out[item.Index] = item.Embedding
		}
	}
	return out, nil
}
