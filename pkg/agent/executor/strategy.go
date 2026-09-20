package executor

import (
	"context"

	"tukifac/pkg/agent/bizctx"
	"tukifac/pkg/agent/providers"
)

const defaultMaxRounds = 3

// ReAct implementa el bucle "razona → llama herramientas → responde":
// llama al LLM, si devuelve tool_calls las ejecuta en Go una por una
// (nunca las ejecuta el modelo), reinyecta los resultados como mensajes
// role=tool, y vuelve a llamar — hasta MaxRounds.
type ReAct struct {
	LLM      providers.LLM
	Executor *Executor
}

func NewReAct(llm providers.LLM, exec *Executor) *ReAct {
	return &ReAct{LLM: llm, Executor: exec}
}

// Run corre el bucle y devuelve el mensaje final del asistente (sin
// tool_calls pendientes) más el uso de tokens ACUMULADO de todas las
// llamadas al LLM en este turno.
func (r *ReAct) Run(ctx context.Context, bc bizctx.Context, model string, messages []providers.Message, tools []providers.ToolSpec, maxRounds int) (providers.Message, providers.Usage, error) {
	if maxRounds <= 0 {
		maxRounds = defaultMaxRounds
	}

	var totalUsage providers.Usage

	for round := 0; round < maxRounds; round++ {
		resp, err := r.LLM.Chat(ctx, providers.ChatRequest{
			Model:    model,
			Messages: messages,
			Tools:    tools,
		})
		if err != nil {
			return providers.Message{}, totalUsage, err
		}
		totalUsage.PromptTokens += resp.Usage.PromptTokens
		totalUsage.CompletionTokens += resp.Usage.CompletionTokens
		totalUsage.TotalTokens += resp.Usage.TotalTokens

		if len(resp.Message.ToolCalls) == 0 {
			return resp.Message, totalUsage, nil
		}

		// El modelo pidió tools: agregar su mensaje (con los tool_calls) al
		// historial de la request, ejecutar cada una en Go, y reinyectar
		// los resultados como mensajes role=tool antes de la siguiente
		// vuelta.
		messages = append(messages, resp.Message)
		for _, tc := range resp.Message.ToolCalls {
			result := r.Executor.Run(ctx, bc, tc.Name, tc.Arguments)
			messages = append(messages, providers.Message{
				Role:       providers.RoleTool,
				Name:       tc.Name,
				ToolCallID: tc.ID,
				Content:    result,
			})
		}
	}

	// Se agotaron las rondas sin una respuesta final sin tool_calls: pedir
	// una última respuesta de texto, sin ofrecer tools, para no dejar al
	// cliente sin nada.
	resp, err := r.LLM.Chat(ctx, providers.ChatRequest{Model: model, Messages: messages})
	if err != nil {
		return providers.Message{}, totalUsage, err
	}
	totalUsage.PromptTokens += resp.Usage.PromptTokens
	totalUsage.CompletionTokens += resp.Usage.CompletionTokens
	totalUsage.TotalTokens += resp.Usage.TotalTokens
	return resp.Message, totalUsage, nil
}
