package actions

import (
	"fmt"

	"tukifac/config"
	consultaService "tukifac/internal/consulta/service"
	plansService "tukifac/internal/plans/service"
	superadminService "tukifac/internal/superadmin/service"
	"tukifac/pkg/agent/actions"
	"tukifac/pkg/agent/memory"
)

// Names es el catálogo de nombres de acciones comerciales — usado como
// Tools del AgentType "commercial" en internal/agent/wiring.go.
func Names() []string {
	return []string{
		"commercial_create_lead",
		"commercial_qualify_lead",
		"commercial_schedule_demo",
		"commercial_schedule_training",
		"commercial_create_request",
		"commercial_lookup_plans",
		"commercial_validate_ruc",
		"commercial_check_ruc_has_account",
		"commercial_notify_team",
		"commercial_request_handoff",
		"commercial_deliver_payment_instructions",
		"commercial_report_payment_sent",
		"commercial_check_payment_status",
		"commercial_create_trial_tenant",
		"commercial_create_tenant_and_subscribe",
	}
}

// RegisterAll construye e inscribe todas las acciones comerciales en el
// Registry del motor. Cableado desde internal/agent/wiring.go.
func RegisterAll(reg *actions.Registry, cfg *config.Config, mem memory.Memory) error {
	consulta := consultaService.NewConsultaService()
	tenants := superadminService.NewTenantService()
	plans := plansService.NewPlanService()

	all := []actions.Action{
		NewCreateLead(),
		NewQualifyLead(),
		NewScheduleDemo(),
		NewScheduleTraining(),
		NewCreateRequest(),
		NewLookupPlans(plans),
		NewValidateRUC(consulta),
		NewCheckRUCHasAccount(),
		NewNotifyTeam(cfg),
		NewRequestHandoff(mem),
		NewDeliverPaymentInstructions(),
		NewReportPaymentSent(mem),
		NewCheckPaymentStatus(),
		NewCreateTrialTenant(cfg, consulta, tenants),
		NewCreateTenantAndSubscribe(cfg, consulta, tenants),
	}
	for _, a := range all {
		if err := reg.Register(a); err != nil {
			return fmt.Errorf("actions: registrar %q: %w", a.Name(), err)
		}
	}
	return nil
}
