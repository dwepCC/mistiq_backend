package actions

import (
	"context"
	"encoding/json"
	"fmt"

	"tukifac/pkg/agent/actions"
	"tukifac/pkg/agent/bizctx"
	"tukifac/pkg/agent/memory"
	"tukifac/pkg/database"
)

// DeliverPaymentInstructions entrega los datos de pago manual (Yape/Plin)
// configurados para este asistente. Nunca cobra ni valida nada — solo
// informa (ver docs/CHATBOT-AGENT-ARCHITECTURE.md §3.3).
type DeliverPaymentInstructions struct{}

func NewDeliverPaymentInstructions() *DeliverPaymentInstructions {
	return &DeliverPaymentInstructions{}
}

func (DeliverPaymentInstructions) Name() string { return "commercial_deliver_payment_instructions" }
func (DeliverPaymentInstructions) Description() string {
	return "Entrega los datos para pagar por Yape/Plin (número, titular). Úsala cuando el cliente ya decidió contratar y pregunta cómo pagar."
}
func (DeliverPaymentInstructions) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{}}`)
}
func (DeliverPaymentInstructions) Meta() actions.Meta {
	return actions.Meta{ReadOnly: true, Idempotent: true}
}

func (a *DeliverPaymentInstructions) Execute(ctx context.Context, bc bizctx.Context, raw json.RawMessage) (actions.Result, error) {
	var row database.Assistant
	if err := database.CentralDB.WithContext(ctx).First(&row, bc.AssistantID).Error; err != nil {
		return actions.Result{}, fmt.Errorf("leer datos de pago: %w", err)
	}
	if row.PaymentPlinNumber == "" {
		return actions.Result{Message: "Todavía no hay datos de pago manual configurados. Deriva a un asesor humano para coordinar el pago."}, nil
	}
	msg := fmt.Sprintf(
		"Puedes pagar por Yape o Plin al número %s (a nombre de %s). Cuando hagas el pago, mándame la captura o el número de operación por acá y avisamos al equipo para confirmarlo.",
		row.PaymentPlinNumber, row.PaymentHolderName,
	)
	return actions.Result{Message: msg}, nil
}

// ReportPaymentSent: el cliente AFIRMA haber pagado. Solo registra la
// afirmación y deriva a un humano — NUNCA valida el pago por su cuenta.
// No es Sensitive (no mueve dinero ni crea nada irreversible: es
// literalmente "anotar lo que dijo el cliente").
type ReportPaymentSent struct {
	Memory memory.Memory
}

func NewReportPaymentSent(mem memory.Memory) *ReportPaymentSent {
	return &ReportPaymentSent{Memory: mem}
}

func (ReportPaymentSent) Name() string { return "commercial_report_payment_sent" }
func (ReportPaymentSent) Description() string {
	return "Registra que el cliente DICE haber enviado el pago (nunca lo valida) y deriva la conversación a un asesor humano para que confirme el comprobante. Úsala en cuanto el cliente diga que ya pagó."
}
func (ReportPaymentSent) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{
		"note":{"type":"string","description":"detalle que dio el cliente (monto, hora, medio)"}
	}}`)
}
func (ReportPaymentSent) Meta() actions.Meta { return actions.Meta{Idempotent: true} }

type reportPaymentArgs struct {
	Note string `json:"note"`
}

func (a *ReportPaymentSent) Execute(ctx context.Context, bc bizctx.Context, raw json.RawMessage) (actions.Result, error) {
	var args reportPaymentArgs
	_ = decodeArgs(raw, &args) // nota es opcional; args vacío es válido

	lead, err := findOrCreateLead(ctx, bc, "lead")
	if err != nil {
		return actions.Result{}, err
	}
	updates := map[string]any{"status": "payment_reported"}
	if args.Note != "" {
		updates["notes"] = args.Note
	}
	if err := database.CentralDB.WithContext(ctx).Model(&database.AssistantLead{}).
		Where("id = ?", lead.ID).Updates(updates).Error; err != nil {
		return actions.Result{}, fmt.Errorf("registrar pago reportado: %w", err)
	}
	if err := a.Memory.MarkNeedsHuman(ctx, bc.ConversationID, "payment_reported"); err != nil {
		return actions.Result{}, fmt.Errorf("derivar a un asesor: %w", err)
	}
	return actions.Result{
		Message:      "Perfecto, registré que enviaste el pago. Un asesor va a confirmarlo en breve.",
		AuditSummary: fmt.Sprintf("lead #%d: pago reportado", lead.ID),
	}, nil
}

// CheckPaymentStatus consulta el estado registrado (solo lectura) — nunca
// confirma un pago por su cuenta, solo informa lo que un humano ya validó
// (o no) previamente.
type CheckPaymentStatus struct{}

func NewCheckPaymentStatus() *CheckPaymentStatus { return &CheckPaymentStatus{} }

func (CheckPaymentStatus) Name() string { return "commercial_check_payment_status" }
func (CheckPaymentStatus) Description() string {
	return "Consulta si el pago que el cliente reportó ya fue confirmado por un asesor. Úsala cuando el cliente pregunte si ya se procesó su pago."
}
func (CheckPaymentStatus) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{}}`)
}
func (CheckPaymentStatus) Meta() actions.Meta { return actions.Meta{ReadOnly: true, Idempotent: true} }

func (a *CheckPaymentStatus) Execute(ctx context.Context, bc bizctx.Context, raw json.RawMessage) (actions.Result, error) {
	var lead database.AssistantLead
	err := database.CentralDB.WithContext(ctx).
		Where("conversation_id = ?", bc.ConversationID).
		Order("id DESC").First(&lead).Error
	if err != nil {
		return actions.Result{Message: "No tengo ningún pago reportado en esta conversación todavía."}, nil
	}

	switch lead.Status {
	case "payment_validated":
		return actions.Result{Message: "Sí, tu pago ya fue confirmado por el equipo."}, nil
	case "payment_reported":
		return actions.Result{Message: "Tu pago está reportado y en revisión por el equipo, todavía no se confirma."}, nil
	default:
		return actions.Result{Message: "No tengo ningún pago reportado en esta conversación todavía."}, nil
	}
}
