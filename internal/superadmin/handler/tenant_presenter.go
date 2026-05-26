package handler

import (
	"tukifac/config"
	"tukifac/pkg/database"
	"tukifac/pkg/domains"

	"github.com/gofiber/fiber/v3"
)

// enrichTenantMap añade host/url del tenant según APP_DOMAIN (raíz), sin modelo slug.app.*.
func enrichTenantMap(t *database.Tenant) fiber.Map {
	m := fiber.Map{
		"id":                 t.ID,
		"name":               t.Name,
		"slug":               t.Slug,
		"db_name":            t.DBName,
		"plan":               t.Plan,
		"status":             t.Status,
		"email":              t.Email,
		"phone":              t.Phone,
		"ruc":                t.RUC,
		"rubro":              t.Rubro,
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
