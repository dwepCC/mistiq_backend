package actions

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"tukifac/pkg/agent/actions"
	"tukifac/pkg/agent/bizctx"
	"tukifac/pkg/agent/executor"
	"tukifac/pkg/database"
)

// findOrCreateLead busca el lead más reciente de esta conversación o crea
// uno nuevo — evita que cada tool call dentro del mismo hilo genere un lead
// duplicado (calificar, agendar, reportar pago, etc. todos operan sobre el
// mismo registro cuando ya existe uno).
func findOrCreateLead(ctx context.Context, bc bizctx.Context, kind string) (*database.AssistantLead, error) {
	var lead database.AssistantLead
	err := database.CentralDB.WithContext(ctx).
		Where("conversation_id = ?", bc.ConversationID).
		Order("id DESC").First(&lead).Error
	if err == nil {
		return &lead, nil
	}
	lead = database.AssistantLead{
		AssistantID:    bc.AssistantID,
		ConversationID: bc.ConversationID,
		Kind:           kind,
		Status:         "new",
	}
	if err := database.CentralDB.WithContext(ctx).Create(&lead).Error; err != nil {
		return nil, fmt.Errorf("crear lead: %w", err)
	}
	return &lead, nil
}

type CreateLead struct{}

func NewCreateLead() *CreateLead { return &CreateLead{} }

func (CreateLead) Name() string { return "commercial_create_lead" }
func (CreateLead) Description() string {
	return "Registra los datos de contacto de un prospecto interesado (nombre, teléfono, email, qué necesita). Úsala en cuanto el cliente dé sus datos, aunque todavía no haya decidido nada."
}
func (CreateLead) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","required":["name"],"properties":{
		"name":{"type":"string"},
		"phone":{"type":"string"},
		"email":{"type":"string"},
		"interest":{"type":"string","description":"qué necesita o qué le interesa del sistema"}
	}}`)
}
func (CreateLead) Meta() actions.Meta { return actions.Meta{} }

type createLeadArgs struct {
	Name     string `json:"name"`
	Phone    string `json:"phone"`
	Email    string `json:"email"`
	Interest string `json:"interest"`
}

func (a *CreateLead) Execute(ctx context.Context, bc bizctx.Context, raw json.RawMessage) (actions.Result, error) {
	var args createLeadArgs
	if err := decodeArgs(raw, &args); err != nil {
		return actions.Result{}, err
	}
	if err := requireNonEmpty("name", args.Name); err != nil {
		return actions.Result{}, err
	}

	lead, err := findOrCreateLead(ctx, bc, "lead")
	if err != nil {
		return actions.Result{}, err
	}
	updates := map[string]any{"name": args.Name}
	if args.Phone != "" {
		updates["phone"] = args.Phone
	}
	if args.Email != "" {
		updates["email"] = args.Email
	}
	if args.Interest != "" {
		updates["interest"] = args.Interest
	}
	if err := database.CentralDB.WithContext(ctx).Model(&database.AssistantLead{}).
		Where("id = ?", lead.ID).Updates(updates).Error; err != nil {
		return actions.Result{}, fmt.Errorf("actualizar lead: %w", err)
	}

	return actions.Result{
		Message:      "Datos del cliente registrados.",
		AuditSummary: fmt.Sprintf("lead #%d actualizado (name=%s)", lead.ID, args.Name),
	}, nil
}

type QualifyLead struct{}

func NewQualifyLead() *QualifyLead { return &QualifyLead{} }

func (QualifyLead) Name() string { return "commercial_qualify_lead" }
func (QualifyLead) Description() string {
	return "Marca qué tan avanzado está el interés del prospecto (hot/warm/cold) y agrega notas internas. Úsala cuando ya tengas suficiente info para evaluar qué tan cerca está de decidirse."
}
func (QualifyLead) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","required":["qualification"],"properties":{
		"qualification":{"type":"string","enum":["hot","warm","cold"]},
		"notes":{"type":"string"}
	}}`)
}
func (QualifyLead) Meta() actions.Meta { return actions.Meta{} }

type qualifyLeadArgs struct {
	Qualification string `json:"qualification"`
	Notes         string `json:"notes"`
}

func (a *QualifyLead) Execute(ctx context.Context, bc bizctx.Context, raw json.RawMessage) (actions.Result, error) {
	var args qualifyLeadArgs
	if err := decodeArgs(raw, &args); err != nil {
		return actions.Result{}, err
	}
	q := strings.ToLower(strings.TrimSpace(args.Qualification))
	if q != "hot" && q != "warm" && q != "cold" {
		return actions.Result{}, executor.Validation("qualification debe ser hot, warm o cold", nil)
	}

	lead, err := findOrCreateLead(ctx, bc, "lead")
	if err != nil {
		return actions.Result{}, err
	}
	updates := map[string]any{"qualification": q}
	if args.Notes != "" {
		updates["notes"] = args.Notes
	}
	if err := database.CentralDB.WithContext(ctx).Model(&database.AssistantLead{}).
		Where("id = ?", lead.ID).Updates(updates).Error; err != nil {
		return actions.Result{}, fmt.Errorf("calificar lead: %w", err)
	}
	return actions.Result{Message: "Lead calificado como " + q + "."}, nil
}
