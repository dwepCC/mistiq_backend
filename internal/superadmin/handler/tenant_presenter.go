package handler

import (
	"tukifac/config"
	"tukifac/internal/superadmin/service"
	"tukifac/pkg/database"
	"tukifac/pkg/domains"

	"github.com/gofiber/fiber/v3"
)

// enrichTenantMap añade host/url del tenant según APP_DOMAIN (raíz), sin modelo slug.app.*.
func enrichTenantMap(t *database.Tenant) fiber.Map {
	m := fiber.Map{
		"id":      t.ID,
		"name":    t.Name,
		"slug":    t.Slug,
		"db_name": t.DBName,
		"plan":    t.Plan,
		// plan_id/plan_name se rellenan aparte (ver withPlanRef): el panel necesita la
		// identidad del plan para preseleccionarlo, no solo el nombre suelto.
		"status": t.Status,
		"email":  t.Email,
		"phone":  t.Phone,
		"ruc":    t.RUC,
		"rubro":  t.Rubro,
		// Sin taxpayer_regime el formulario de edición caía siempre a "general" al reabrirse,
		// aunque el valor sí se hubiera guardado: parecía que la edición no funcionaba.
		"taxpayer_regime":    t.TaxpayerRegime,
		"address":            t.Address,
		"ubigeo":             t.Ubigeo,
		"sunat_env_mode":     t.SunatEnvMode,
		"sunat_connected_at": t.SunatConnectedAt,
		"trial_ends_at":      t.TrialEndsAt,
		"created_at":         t.CreatedAt,
		"updated_at":         t.UpdatedAt,
	}
	if cfg := config.AppConfig; cfg != nil {
		m["root_domain"] = cfg.AppDomain
		m["tenant_host"] = domains.TenantHost(t.Slug, cfg.AppDomain)
		m["tenant_url"] = domains.TenantURL(t.Slug, cfg.AppDomain)
	}
	return m
}

// withPlanRef añade el plan resuelto contra el catálogo. plan_id 0 significa que el plan
// guardado no corresponde a ningún plan del catálogo (dato heredado): el panel lo muestra
// como tal en vez de preseleccionar uno equivocado.
func withPlanRef(m fiber.Map, ref service.TenantPlanRef) fiber.Map {
	m["plan_id"] = ref.PlanID
	if ref.PlanName != "" {
		m["plan_name"] = ref.PlanName
	}
	return m
}
