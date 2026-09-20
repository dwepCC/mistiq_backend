package actions

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"tukifac/pkg/agent/actions"
	"tukifac/pkg/agent/bizctx"
	"tukifac/pkg/database"
)

type scheduleArgs struct {
	Name        string `json:"name"`
	Phone       string `json:"phone"`
	Email       string `json:"email"`
	ScheduledAt string `json:"scheduled_at"` // "2006-01-02 15:04", hora del negocio
	Notes       string `json:"notes"`
}

func schedule(ctx context.Context, bc bizctx.Context, kind string, raw json.RawMessage) (actions.Result, error) {
	var args scheduleArgs
	if err := decodeArgs(raw, &args); err != nil {
		return actions.Result{}, err
	}
	if err := requireNonEmpty("name", args.Name); err != nil {
		return actions.Result{}, err
	}
	if err := requireNonEmpty("scheduled_at", args.ScheduledAt); err != nil {
		return actions.Result{}, err
	}

	lead, err := findOrCreateLead(ctx, bc, kind)
	if err != nil {
		return actions.Result{}, err
	}

	loc := time.FixedZone("America/Lima", -5*60*60)
	when, err := time.ParseInLocation("2006-01-02 15:04", args.ScheduledAt, loc)
	if err != nil {
		return actions.Result{}, fmt.Errorf("fecha/hora inválida (usa AAAA-MM-DD HH:MM): %w", err)
	}
	if when.Before(time.Now().In(loc)) {
		return actions.Result{}, fmt.Errorf("no se puede agendar en el pasado")
	}

	updates := map[string]any{
		"kind":         kind,
		"name":         args.Name,
		"scheduled_at": when,
	}
	if args.Phone != "" {
		updates["phone"] = args.Phone
	}
	if args.Email != "" {
		updates["email"] = args.Email
	}
	if args.Notes != "" {
		updates["notes"] = args.Notes
	}
	if err := database.CentralDB.WithContext(ctx).Model(&database.AssistantLead{}).
		Where("id = ?", lead.ID).Updates(updates).Error; err != nil {
		return actions.Result{}, fmt.Errorf("agendar: %w", err)
	}

	return actions.Result{
		Message:      fmt.Sprintf("Quedó agendado para el %s a las %s (hora de Perú).", when.Format("02/01/2006"), when.Format("15:04")),
		AuditSummary: fmt.Sprintf("lead #%d agendado (%s) para %s", lead.ID, kind, when.Format(time.RFC3339)),
	}, nil
}

type ScheduleDemo struct{}

func NewScheduleDemo() *ScheduleDemo { return &ScheduleDemo{} }

func (ScheduleDemo) Name() string { return "commercial_schedule_demo" }
func (ScheduleDemo) Description() string {
	return "Agenda una demostración en vivo del sistema con el cliente. Confirma nombre, teléfono/email y la fecha/hora exacta antes de llamarla."
}
func (ScheduleDemo) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","required":["name","scheduled_at"],"properties":{
		"name":{"type":"string"},"phone":{"type":"string"},"email":{"type":"string"},
		"scheduled_at":{"type":"string","description":"AAAA-MM-DD HH:MM, hora de Perú"},
		"notes":{"type":"string"}
	}}`)
}
func (ScheduleDemo) Meta() actions.Meta { return actions.Meta{} }
func (a *ScheduleDemo) Execute(ctx context.Context, bc bizctx.Context, raw json.RawMessage) (actions.Result, error) {
	return schedule(ctx, bc, "demo", raw)
}

type ScheduleTraining struct{}

func NewScheduleTraining() *ScheduleTraining { return &ScheduleTraining{} }

func (ScheduleTraining) Name() string { return "commercial_schedule_training" }
func (ScheduleTraining) Description() string {
	return "Agenda una capacitación con el cliente (ya contrató o está por hacerlo y necesita que le enseñen a usar el sistema)."
}
func (ScheduleTraining) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","required":["name","scheduled_at"],"properties":{
		"name":{"type":"string"},"phone":{"type":"string"},"email":{"type":"string"},
		"scheduled_at":{"type":"string","description":"AAAA-MM-DD HH:MM, hora de Perú"},
		"notes":{"type":"string"}
	}}`)
}
func (ScheduleTraining) Meta() actions.Meta { return actions.Meta{} }
func (a *ScheduleTraining) Execute(ctx context.Context, bc bizctx.Context, raw json.RawMessage) (actions.Result, error) {
	return schedule(ctx, bc, "training", raw)
}
