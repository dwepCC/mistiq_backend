package actions

import (
	"context"
	"encoding/json"
	"fmt"

	"tukifac/pkg/agent/actions"
	"tukifac/pkg/agent/bizctx"
	"tukifac/pkg/agent/memory"
)

// RequestHandoff marca la conversación como needs_human — el motor deja de
// responder hasta que un asesor la tome (ver
// pkg/agent/orchestrator: conv.Status != "bot" => Silent). NO es Sensitive:
// no mueve dinero ni es irreversible, un asesor humano siempre puede
// retomarla o reabrirla.
type RequestHandoff struct {
	Memory memory.Memory
}

func NewRequestHandoff(mem memory.Memory) *RequestHandoff { return &RequestHandoff{Memory: mem} }

func (RequestHandoff) Name() string { return "commercial_request_handoff" }
func (RequestHandoff) Description() string {
	return "Deriva la conversación a un asesor humano y deja de responder automáticamente. Úsala cuando el cliente lo pida explícitamente, o cuando la consulta se sale de lo que puedes resolver de forma confiable."
}
func (RequestHandoff) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","required":["reason"],"properties":{
		"reason":{"type":"string","description":"por qué se deriva, breve"}
	}}`)
}
func (RequestHandoff) Meta() actions.Meta { return actions.Meta{Idempotent: true} }

type handoffArgs struct {
	Reason string `json:"reason"`
}

func (a *RequestHandoff) Execute(ctx context.Context, bc bizctx.Context, raw json.RawMessage) (actions.Result, error) {
	var args handoffArgs
	if err := decodeArgs(raw, &args); err != nil {
		return actions.Result{}, err
	}
	if err := requireNonEmpty("reason", args.Reason); err != nil {
		return actions.Result{}, err
	}
	if err := a.Memory.MarkNeedsHuman(ctx, bc.ConversationID, args.Reason); err != nil {
		return actions.Result{}, fmt.Errorf("derivar a un asesor: %w", err)
	}
	return actions.Result{
		Message:      "Listo, un asesor va a continuar la conversación. Avísale al cliente que en breve lo atienden.",
		AuditSummary: "handoff: " + args.Reason,
	}, nil
}
