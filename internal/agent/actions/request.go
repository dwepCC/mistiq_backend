package actions

import (
	"context"
	"encoding/json"
	"fmt"

	"tukifac/pkg/agent/actions"
	"tukifac/pkg/agent/bizctx"
	"tukifac/pkg/database"
)

// CreateRequest cubre pedidos que no encajan en lead/demo/training (p. ej.
// "quiero que me llamen", "necesito una cotización especial") sin forzar
// al modelo a elegir mal entre las otras acciones.
type CreateRequest struct{}

func NewCreateRequest() *CreateRequest { return &CreateRequest{} }

func (CreateRequest) Name() string { return "commercial_create_request" }
func (CreateRequest) Description() string {
	return "Registra un pedido del cliente que no es agendar una demo/capacitación ni un lead simple (p. ej. pedir que lo llamen, una cotización especial). Usa esta acción solo cuando ninguna otra encaje."
}
func (CreateRequest) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","required":["name","detail"],"properties":{
		"name":{"type":"string"},"phone":{"type":"string"},"email":{"type":"string"},
		"detail":{"type":"string","description":"qué está pidiendo el cliente"}
	}}`)
}
func (CreateRequest) Meta() actions.Meta { return actions.Meta{} }

type createRequestArgs struct {
	Name   string `json:"name"`
	Phone  string `json:"phone"`
	Email  string `json:"email"`
	Detail string `json:"detail"`
}

func (a *CreateRequest) Execute(ctx context.Context, bc bizctx.Context, raw json.RawMessage) (actions.Result, error) {
	var args createRequestArgs
	if err := decodeArgs(raw, &args); err != nil {
		return actions.Result{}, err
	}
	if err := requireNonEmpty("name", args.Name); err != nil {
		return actions.Result{}, err
	}
	if err := requireNonEmpty("detail", args.Detail); err != nil {
		return actions.Result{}, err
	}

	lead, err := findOrCreateLead(ctx, bc, "request")
	if err != nil {
		return actions.Result{}, err
	}
	updates := map[string]any{"kind": "request", "name": args.Name, "interest": args.Detail}
	if args.Phone != "" {
		updates["phone"] = args.Phone
	}
	if args.Email != "" {
		updates["email"] = args.Email
	}
	if err := database.CentralDB.WithContext(ctx).Model(&database.AssistantLead{}).
		Where("id = ?", lead.ID).Updates(updates).Error; err != nil {
		return actions.Result{}, fmt.Errorf("registrar pedido: %w", err)
	}
	return actions.Result{Message: "Pedido registrado, un asesor lo va a revisar."}, nil
}
