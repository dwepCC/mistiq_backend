package actions

import (
	"context"
	"encoding/json"
	"fmt"

	"tukifac/config"
	consultaService "tukifac/internal/consulta/service"
	superadminService "tukifac/internal/superadmin/service"
	"tukifac/pkg/agent/actions"
	"tukifac/pkg/agent/bizctx"
	"tukifac/pkg/database"
)

// CreateTenantAndSubscribe crea una cuenta PAGADA real (el cliente ya
// decidió contratar, no una prueba). Segunda acción Sensitive del catálogo
// — mismo patrón que CreateTrialTenant (confirmación explícita + kill-switch
// propio, default desactivado) y comparte la MISMA clave de lock por RUC
// (ver tenant.go) para no crear dos cuentas si el cliente dispara ambos
// caminos casi a la vez.
type CreateTenantAndSubscribe struct {
	Cfg      *config.Config
	Consulta *consultaService.ConsultaService
	Tenants  *superadminService.TenantService
}

func NewCreateTenantAndSubscribe(cfg *config.Config, consulta *consultaService.ConsultaService, tenants *superadminService.TenantService) *CreateTenantAndSubscribe {
	return &CreateTenantAndSubscribe{Cfg: cfg, Consulta: consulta, Tenants: tenants}
}

func (CreateTenantAndSubscribe) Name() string { return "commercial_create_tenant_and_subscribe" }
func (CreateTenantAndSubscribe) Description() string {
	return "Crea la cuenta PAGADA de Mistiq para el cliente, ya con el pago acordado/reportado (no una prueba gratuita). Es irreversible: confirma explícitamente RUC/razón social, plan y meses de suscripción con el cliente antes de llamarla."
}
func (CreateTenantAndSubscribe) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","required":["ruc","email","phone","plan_name","months"],"properties":{
		"name":{"type":"string"},"ruc":{"type":"string"},"email":{"type":"string"},"phone":{"type":"string"},
		"plan_name":{"type":"string"},
		"months":{"type":"integer","minimum":1,"description":"meses de suscripción contratados"}
	}}`)
}
func (CreateTenantAndSubscribe) Meta() actions.Meta {
	return actions.Meta{Sensitive: true, RequiredPermission: "assistant.create_paid_tenant"}
}

type createSubscribeArgs struct {
	createTenantArgs
	Months int `json:"months"`
}

func (a *CreateTenantAndSubscribe) Execute(ctx context.Context, bc bizctx.Context, raw json.RawMessage) (actions.Result, error) {
	if !a.enabled(ctx, bc.AssistantID) {
		return actions.Result{Message: "Ahora mismo no puedo crear cuentas contratadas automáticamente; deriva al cliente con un asesor humano."}, nil
	}

	var full createSubscribeArgs
	if err := decodeArgs(raw, &full); err != nil {
		return actions.Result{}, err
	}
	args := full.createTenantArgs
	if err := requireNonEmpty("ruc", args.RUC); err != nil {
		return actions.Result{}, err
	}
	if err := requireNonEmpty("email", args.Email); err != nil {
		return actions.Result{}, err
	}
	if err := requireNonEmpty("phone", args.Phone); err != nil {
		return actions.Result{}, err
	}
	if err := requireNonEmpty("plan_name", args.PlanName); err != nil {
		return actions.Result{}, err
	}
	months := full.Months
	if months <= 0 {
		months = 1
	}

	tenant, password, err := createTenantForChat(ctx, a.Consulta, a.Tenants, args, months)
	if err != nil {
		return actions.Result{}, err
	}
	if err := linkLeadToTenant(ctx, bc, tenant.ID, "in_contract"); err != nil {
		return actions.Result{}, fmt.Errorf("vincular lead a la cuenta creada: %w", err)
	}

	return actions.Result{
		Message: fmt.Sprintf(
			"¡Listo! Tu cuenta de Mistiq ya está activa por %d mes(es).\nUsuario: %s\nContraseña: %s\nEmpieza en: https://%s.mistiq.cloud",
			months, args.Email, password, tenant.Slug,
		),
		AuditSummary: fmt.Sprintf("tenant #%d (%s) creado con suscripción de %d mes(es)", tenant.ID, tenant.Slug, months),
	}, nil
}

func (a *CreateTenantAndSubscribe) enabled(ctx context.Context, assistantID uint) bool {
	if a.Cfg.AssistantEnablePaidContract {
		return true
	}
	var row database.Assistant
	if err := database.CentralDB.WithContext(ctx).First(&row, assistantID).Error; err != nil {
		return false
	}
	return row.EnablePaidContract
}
