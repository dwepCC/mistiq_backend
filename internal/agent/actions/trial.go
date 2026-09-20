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

// CreateTrialTenant crea una cuenta REAL de Mistiq en modo prueba. Primera
// acción Sensitive del catálogo: exige confirmación explícita del cliente
// (policies.Confirmation) y tiene su propio kill-switch — DESACTIVADO por
// defecto tanto a nivel de instancia (Assistant.EnableTrialTenant) como de
// plataforma (ASSISTANT_ENABLE_TRIAL_TENANT), hay que prender ambos
// explícitamente para que esta acción pueda ejecutarse de verdad.
type CreateTrialTenant struct {
	Cfg      *config.Config
	Consulta *consultaService.ConsultaService
	Tenants  *superadminService.TenantService
}

func NewCreateTrialTenant(cfg *config.Config, consulta *consultaService.ConsultaService, tenants *superadminService.TenantService) *CreateTrialTenant {
	return &CreateTrialTenant{Cfg: cfg, Consulta: consulta, Tenants: tenants}
}

func (CreateTrialTenant) Name() string { return "commercial_create_trial_tenant" }
func (CreateTrialTenant) Description() string {
	return "Crea una cuenta de PRUEBA real de Mistiq para el cliente (RUC, email y teléfono ya validados). Es una acción irreversible: antes de llamarla, confirma explícitamente con el cliente el RUC/razón social y que quiere empezar la prueba."
}
func (CreateTrialTenant) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","required":["ruc","email","phone","plan_name"],"properties":{
		"name":{"type":"string","description":"razón social; si no la tienes, se resuelve automáticamente por RUC"},
		"ruc":{"type":"string"},"email":{"type":"string"},"phone":{"type":"string"},
		"plan_name":{"type":"string","description":"nombre exacto del plan (ver commercial_lookup_plans)"}
	}}`)
}
func (CreateTrialTenant) Meta() actions.Meta {
	return actions.Meta{Sensitive: true, RequiredPermission: "assistant.create_trial_tenant"}
}

func (a *CreateTrialTenant) Execute(ctx context.Context, bc bizctx.Context, raw json.RawMessage) (actions.Result, error) {
	if !a.enabled(ctx, bc.AssistantID) {
		return actions.Result{Message: "Ahora mismo no puedo crear cuentas de prueba automáticamente; deriva al cliente con un asesor humano."}, nil
	}

	args, err := decodeCreateTenantArgs(raw)
	if err != nil {
		return actions.Result{}, err
	}

	tenant, password, err := createTenantForChat(ctx, a.Consulta, a.Tenants, args, 1)
	if err != nil {
		return actions.Result{}, err
	}
	if err := linkLeadToTenant(ctx, bc, tenant.ID, "trial_registered"); err != nil {
		return actions.Result{}, fmt.Errorf("vincular lead a la cuenta creada: %w", err)
	}

	return actions.Result{
		Message: fmt.Sprintf(
			"¡Listo! Tu cuenta de prueba de Mistiq ya está creada.\nUsuario: %s\nContraseña: %s\nEmpieza en: https://%s.mistiq.cloud",
			args.Email, password, tenant.Slug,
		),
		AuditSummary: fmt.Sprintf("tenant #%d (%s) creado en modo prueba", tenant.ID, tenant.Slug),
	}, nil
}

// enabled: kill-switch de dos niveles — CUALQUIERA de los dos en true
// habilita la acción (el de plataforma sirve de interruptor general por
// ops sin tocar BD; el de la instancia deja prenderlo/apagarlo desde el
// panel sin redeploy). Ambos en false (el default) la mantiene bloqueada.
func (a *CreateTrialTenant) enabled(ctx context.Context, assistantID uint) bool {
	if a.Cfg.AssistantEnableTrialTenant {
		return true
	}
	var row database.Assistant
	if err := database.CentralDB.WithContext(ctx).First(&row, assistantID).Error; err != nil {
		return false
	}
	return row.EnableTrialTenant
}
