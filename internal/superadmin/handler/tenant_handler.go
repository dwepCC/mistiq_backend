package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"tukifac/config"
	companysvc "tukifac/internal/company/service"
	"tukifac/internal/superadmin/service"
	"tukifac/pkg/database"
	"tukifac/pkg/facturador"
	"tukifac/pkg/fiscal"
	"tukifac/pkg/saas"

	"github.com/gofiber/fiber/v3"
	"gorm.io/gorm"
)

type TenantHandler struct {
	svc *service.TenantService
}

func NewTenantHandler() *TenantHandler {
	return &TenantHandler{svc: service.NewTenantService()}
}

// GET /api/superadmin/tenants?q=&status=&region_id=&provincia_id=
func (h *TenantHandler) ListAPI(c fiber.Ctx) error {
	tenants, err := h.svc.List(c.Query("q"), c.Query("status"), c.Query("region_id"), c.Query("provincia_id"))
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	// Incluir billing_enabled por tenant para la columna Modo SUNAT
	billingByTenant, _ := h.svc.BillingEnabledByTenantIDs(tenantIDs(tenants))
	out := make([]fiber.Map, 0, len(tenants))
	for _, t := range tenants {
		m := enrichTenantMap(&t)
		m["billing_enabled"] = billingByTenant[t.ID]
		out = append(out, m)
	}
	return c.JSON(fiber.Map{"data": out})
}

func tenantIDs(tenants []database.Tenant) []uint {
	ids := make([]uint, 0, len(tenants))
	for _, t := range tenants {
		ids = append(ids, t.ID)
	}
	return ids
}

// GET /api/superadmin/tenants/:id
func (h *TenantHandler) GetAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	tenant, err := h.svc.GetByID(uint(id))
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Tenant no encontrado"})
	}
	modules, _ := h.svc.GetModules(uint(id))
	return c.JSON(fiber.Map{"data": tenant, "modules": modules})
}

// POST /api/superadmin/tenants
func (h *TenantHandler) CreateAPI(c fiber.Ctx) error {
	var input service.CreateTenantInput
	if err := c.Bind().JSON(&input); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "JSON inválido"})
	}
	tenant, err := h.svc.Create(input)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"success": true,
		"data":    enrichTenantMap(tenant),
	})
}

// POST /api/superadmin/tenants/:id/destroy-complete — elimina tenant, BD y archivos (requiere clave de operaciones).
func (h *TenantHandler) DestroyCompleteAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	var body service.DestroyTenantInput
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "JSON inválido"})
	}
	res, err := h.svc.DestroyTenantComplete(uint(id), body)
	if err != nil {
		code := fiber.StatusBadRequest
		if err == saas.ErrOperationsKeyNotConfigured || err == saas.ErrOperationsKeyInvalid {
			code = fiber.StatusForbidden
		}
		return c.Status(code).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{
		"success": true,
		"message": "Tenant eliminado por completo (facturador SUNAT/Lycet no modificado)",
		"result":  res,
	})
}

// PUT /api/superadmin/tenants/:id
func (h *TenantHandler) UpdateAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	var input database.Tenant
	if err := c.Bind().JSON(&input); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "JSON inválido"})
	}
	if err := h.svc.Update(uint(id), input); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true})
}

// PATCH /api/superadmin/tenants/:id/status
func (h *TenantHandler) ToggleStatusAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	var body struct {
		Status string `json:"status"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "JSON inválido"})
	}

	if err := h.svc.SetStatus(uint(id), body.Status); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true})
}

// GET /api/superadmin/tenants/:id/modules
func (h *TenantHandler) GetModulesAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	modules, err := h.svc.GetModules(uint(id))
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": modules})
}

// POST /api/superadmin/tenants/:id/modules
func (h *TenantHandler) SetModuleAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	var body struct {
		ModuleKey string `json:"module_key"`
		Enabled   bool   `json:"enabled"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "JSON inválido"})
	}
	if err := h.svc.SetModule(uint(id), body.ModuleKey, body.Enabled); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true})
}

// POST /api/superadmin/tenants/:id/migrate — ejecuta migraciones del tenant (columnas/tablas nuevas).
func (h *TenantHandler) MigrateAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	if err := h.svc.RunMigrations(uint(id)); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true, "message": "Migraciones ejecutadas"})
}

// POST /api/superadmin/tenants/migrate-all — LEGACY; deshabilitado en producción.
func (h *TenantHandler) MigrateAllAPI(c fiber.Ctx) error {
	if err := service.GuardMigrateAllProduction(); err != nil {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": err.Error()})
	}
	summary, err := h.svc.RunMigrationsAll()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	resp := fiber.Map{
		"success":  len(summary.Failed) == 0,
		"migrated": len(summary.Success),
		"failed":   len(summary.Failed),
	}
	if len(summary.Failed) > 0 {
		failed := make([]string, 0, len(summary.Failed))
		for _, f := range summary.Failed {
			failed = append(failed, f.Slug)
		}
		resp["failed_tenants"] = failed
	}
	return c.JSON(resp)
}

// getTenantDBByID obtiene el *gorm.DB del tenant por su ID (para uso en sunat/sync).
// El caller debe invocar database.ReleaseTenantDB(dbName) con defer al terminar.
func (h *TenantHandler) getTenantDBByID(id uint) (*gorm.DB, string, error) {
	tenant, err := h.svc.GetByID(id)
	if err != nil {
		return nil, "", err
	}
	db, err := database.GetTenantDB(tenant.DBName)
	if err != nil {
		return nil, "", err
	}
	return db, tenant.DBName, nil
}

// mapEnvToFacturadorAmbiente mapea sunat_env_mode interno (demo/production) al ambiente del facturador (produccion/pruebas).
func mapEnvToFacturadorAmbiente(sunatEnvMode string) string {
	return fiscal.SunatEnvToFacturadorAmbiente(sunatEnvMode)
}

// GET /api/superadmin/tenants/:id/sunat-config — configuración SUNAT del tenant (desde su BD).
func (h *TenantHandler) GetSunatConfigAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	tenantDB, dbName, err := h.getTenantDBByID(uint(id))
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Tenant no encontrado"})
	}
	defer database.ReleaseTenantDB(dbName)
	cfg, err := companysvc.NewCompanyService(tenantDB).GetConfig()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	// Sincronizar sunat_env_mode en central para que el listado lo muestre (backfill)
	if cfg.SunatEnvMode != "" {
		database.CentralDB.Model(&database.Tenant{}).Where("id = ?", id).Update("sunat_env_mode", cfg.SunatEnvMode)
	}
	sendMode := strings.TrimSpace(cfg.SendMode)
	if sendMode == "" {
		sendMode = "sunat_direct"
	}
	provider := strings.TrimSpace(cfg.FiscalProvider)
	connStatus := strings.TrimSpace(cfg.FiscalConnectionStatus)
	connType := strings.TrimSpace(cfg.FiscalConnectionType)
	pseBaseURLConfigured := false
	pseTokenConfigured := false
	solConfigured := false
	certificateConfigured := false
	greClientConfigured := false
	greClientID := ""
	sunatSolUser := ""
	pseUser := ""
	certificateFile := ""
	logoFile := ""
	pseBaseURL := ""
	if config.AppConfig.FacturadorBaseURL != "" && cfg.RUC != "" {
		if entry, err := facturador.Shared().GetEmpresa(cfg.RUC); err == nil && entry != nil {
			sunatSolUser = strings.TrimSpace(entry.SOLUser)
			pseUser = strings.TrimSpace(entry.PSEUser)
			certificateFile = strings.TrimSpace(entry.Certificate)
			logoFile = strings.TrimSpace(entry.Logo)
			pseBaseURL = strings.TrimSpace(entry.PSEBaseURL)
		}
		if st, err := facturador.Shared().GetEmpresaFiscalStatus(cfg.RUC); err == nil && st != nil {
			connStatus = st.ConnectionStatus
			if st.SendMode != "" {
				sendMode = st.SendMode
			}
			if st.Provider != "" {
				provider = st.Provider
			}
			if st.ConnectionType != "" {
				connType = st.ConnectionType
			}
			pseBaseURLConfigured = st.PSEBaseURLConfigured
			pseTokenConfigured = st.PSETokenConfigured
			solConfigured = st.SOLConfigured
			certificateConfigured = st.CertificateConfigured
			greClientConfigured = st.GreClientConfigured
			greClientID = strings.TrimSpace(st.GreClientID)
		}
	}
	return c.JSON(fiber.Map{
		"sunat_enabled":          cfg.SunatEnabled,
		"automatic_send":         cfg.AutomaticSend,
		"sunat_env_mode":         fiscal.NormalizeSunatEnvMode(cfg.SunatEnvMode),
		"tax_rate":               cfg.TaxRate,
		"igv_regime":             cfg.IgvRegime,
		"tax_benefit_zone":       cfg.TaxBenefitZone,
		"ruc":                    cfg.RUC,
		"business_name":          cfg.BusinessName,
		"send_mode":              sendMode,
		"fiscal_provider":        provider,
		"connection_type":        connType,
		"connection_status":      connStatus,
		"fiscal_last_sync_at":    cfg.FiscalLastSyncAt,
		"sunat_connected":        cfg.SunatConnected,
		"pse_base_url_configured": pseBaseURLConfigured,
		"pse_base_url":           pseBaseURL,
		"pse_token_configured":   pseTokenConfigured,
		"sol_configured":         solConfigured,
		"certificate_configured": certificateConfigured,
		"sunat_sol_user":         sunatSolUser,
		"pse_user":                 pseUser,
		"certificate_file":       certificateFile,
		"logo_file":              logoFile,
		"logo_configured":        logoFile != "",
		"gre_client_configured":  greClientConfigured,
		"gre_client_id":          greClientID,
	})
}

// PUT /api/superadmin/tenants/:id/sunat-config — actualiza configuración SUNAT del tenant.
func (h *TenantHandler) UpdateSunatConfigAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	tenantDB, dbName, err := h.getTenantDBByID(uint(id))
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Tenant no encontrado"})
	}
	defer database.ReleaseTenantDB(dbName)
	var body struct {
		SunatEnabled   bool    `json:"sunat_enabled"`
		AutomaticSend  *bool   `json:"automatic_send"`
		SunatSolUser   string  `json:"sunat_sol_user"`
		SunatSolPass   string  `json:"sunat_sol_pass"`
		Certificate    string  `json:"certificate"`
		SunatEnvMode   string  `json:"sunat_env_mode"`
		TaxRate        float64 `json:"tax_rate"`
		IgvRegime      string  `json:"igv_regime"`
		TaxBenefitZone bool    `json:"tax_benefit_zone"`
		SendMode       string  `json:"send_mode"`
		PSEProvider    string  `json:"pse_provider"`
		FiscalProvider string  `json:"fiscal_provider"`
		ConnectionType string  `json:"connection_type"`
		PSEBaseURL     string  `json:"pse_base_url"`
		PSEToken       string  `json:"pse_token"`
		PSEUser        string  `json:"pse_user"`
		PSEPassword    string  `json:"pse_password"`
		GreClientID    string  `json:"gre_client_id"`
		GreClientSecret string `json:"gre_client_secret"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "JSON inválido"})
	}
	body.SunatEnvMode = fiscal.NormalizeSunatEnvMode(body.SunatEnvMode)
	var tenant database.Tenant
	_ = database.CentralDB.Select("id", "slug").First(&tenant, id).Error
	svc := companysvc.NewCompanyService(tenantDB).WithSaaSContext(uint(id), tenant.Slug)

	sendMode := strings.TrimSpace(body.SendMode)
	provider := strings.TrimSpace(body.FiscalProvider)
	if provider == "" {
		provider = strings.TrimSpace(body.PSEProvider)
	}
	connType := strings.TrimSpace(body.ConnectionType)
	if strings.ToLower(strings.TrimSpace(sendMode)) == "pse" {
		connType = "bearer"
		if provider == "" {
			provider = "validapse"
		}
		if fiscal.ResolvePSEBaseURL(provider) == "" {
			provider = "validapse"
		}
	} else {
		connType = "bearer"
		provider = "sunat"
	}

	cfg, err := svc.GetConfig()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	if fiscal.NormalizeSunatEnvMode(cfg.SunatEnvMode) == "production" && body.SunatEnvMode == "demo" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "No se puede volver a pruebas: el tenant ya está en producción",
		})
	}

	psePassword := strings.TrimSpace(body.PSEPassword)
	pseToken := strings.TrimSpace(body.PSEToken)
	if strings.ToLower(strings.TrimSpace(sendMode)) == "pse" && pseToken == "" {
		pseToken = psePassword
	}
	pseBaseURL := strings.TrimSpace(body.PSEBaseURL)
	if strings.ToLower(strings.TrimSpace(sendMode)) == "pse" && pseBaseURL == "" {
		pseBaseURL = fiscal.ResolvePSEBaseURL(provider)
	}

	if err := svc.SaveFiscalMetadataCentral(
		sendMode, provider, connType, body.SunatEnvMode,
		body.SunatEnabled,
		body.TaxRate, body.IgvRegime, body.TaxBenefitZone,
		body.AutomaticSend,
	); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}

	certB64 := ""
	if strings.TrimSpace(body.Certificate) != "" {
		certB64 = facturador.PEMToBase64(body.Certificate)
	}

	if config.AppConfig.FacturadorBaseURL != "" && config.AppConfig.FacturadorToken != "" {
		status, syncErr := svc.SyncFiscalToFacturador(companysvc.FiscalSyncInput{
			SendMode:        sendMode,
			Provider:        provider,
			ConnectionType:  connType,
			SOLUser:         body.SunatSolUser,
			SOLPass:         body.SunatSolPass,
			CertificateB64:  certB64,
			PSEBaseURL:      pseBaseURL,
			PSEUser:         body.PSEUser,
			PSEPassword:     psePassword,
			PSEToken:        pseToken,
			GreClientID:     body.GreClientID,
			GreClientSecret: body.GreClientSecret,
			Enabled:         body.SunatEnabled,
		})
		if syncErr != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": syncErr.Error()})
		}
		now := time.Now()
		database.CentralDB.Model(&database.Tenant{}).Where("id = ?", id).Updates(map[string]interface{}{
			"sunat_connected_at": now,
			"sunat_env_mode":     body.SunatEnvMode,
		})
		if status != nil {
			if cfg, _ := svc.GetConfig(); cfg != nil && cfg.RUC != "" {
				_ = facturador.Shared().PatchAmbiente(cfg.RUC, mapEnvToFacturadorAmbiente(body.SunatEnvMode))
			}
		}
	} else if body.SunatEnvMode != "" {
		database.CentralDB.Model(&database.Tenant{}).Where("id = ?", id).Update("sunat_env_mode", body.SunatEnvMode)
	}
	return c.JSON(fiber.Map{"success": true})
}

// POST /api/superadmin/tenants/:id/fiscal-test-connection
func (h *TenantHandler) TestFiscalConnectionAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	tenantDB, dbName, err := h.getTenantDBByID(uint(id))
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Tenant no encontrado"})
	}
	defer database.ReleaseTenantDB(dbName)
	cfg, err := companysvc.NewCompanyService(tenantDB).GetConfig()
	if err != nil || cfg.RUC == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "RUC no configurado"})
	}
	if config.AppConfig.FacturadorBaseURL == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "facturador no configurado"})
	}
	result, err := facturador.Shared().TestFiscalConnection(cfg.RUC)
	if err != nil {
		return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{"error": err.Error()})
	}
	_ = tenantDB.Model(&database.TenantCompanyConfig{}).Where("1=1").Updates(map[string]interface{}{
		"fiscal_connection_status": result.ConnectionStatus,
		"sunat_connected":          result.ConnectionStatus == "connected",
	}).Error
	return c.JSON(result)
}

// PATCH /api/superadmin/tenants/:id/sunat-env — cambia modo pruebas/producción del tenant.
func (h *TenantHandler) PatchSunatEnvAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	var body struct {
		SunatEnvMode string `json:"sunat_env_mode"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "JSON inválido"})
	}
	body.SunatEnvMode = fiscal.NormalizeSunatEnvMode(body.SunatEnvMode)
	if body.SunatEnvMode != "demo" && body.SunatEnvMode != "production" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "sunat_env_mode debe ser demo o production"})
	}
	tenantDB, dbName, err := h.getTenantDBByID(uint(id))
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Tenant no encontrado"})
	}
	defer database.ReleaseTenantDB(dbName)
	svc := companysvc.NewCompanyService(tenantDB)
	cfg, err := svc.GetConfig()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	if fiscal.NormalizeSunatEnvMode(cfg.SunatEnvMode) == "production" && body.SunatEnvMode == "demo" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{
			"error": "No se puede volver a pruebas: el tenant ya está en producción",
		})
	}
	if err := svc.SaveFiscalMetadataCentral(
		cfg.SendMode, cfg.FiscalProvider, cfg.FiscalConnectionType, body.SunatEnvMode,
		cfg.SunatEnabled,
		float64(cfg.TaxRate), cfg.IgvRegime, cfg.TaxBenefitZone,
		nil,
	); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	database.CentralDB.Model(&database.Tenant{}).Where("id = ?", id).Update("sunat_env_mode", body.SunatEnvMode)
	// Actualizar ambiente en Lycet (PATCH /api/v1/empresas/{ruc}/ambiente)
	if cfg, _ := svc.GetConfig(); cfg != nil && cfg.RUC != "" && config.AppConfig.FacturadorBaseURL != "" && config.AppConfig.FacturadorToken != "" {
		_ = facturador.Shared().PatchAmbiente(cfg.RUC, mapEnvToFacturadorAmbiente(body.SunatEnvMode))
	}

	return c.JSON(fiber.Map{"success": true, "sunat_env_mode": body.SunatEnvMode})
}

// POST /api/superadmin/tenants/:id/sync-facturador — sincroniza configuración del tenant con el facturador (Lycet).
func (h *TenantHandler) SyncFacturadorAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	tenantDB, dbName, err := h.getTenantDBByID(uint(id))
	if err != nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Tenant no encontrado"})
	}
	defer database.ReleaseTenantDB(dbName)
	var body struct {
		CertificateBase64   string `json:"certificate_base64"`
		PrivateKeyBase64    string `json:"private_key_base64"`
		PfxBase64           string `json:"pfx_base64"`
		CertificatePassword string `json:"certificate_password"`
		LogoBase64          string `json:"logo_base64"`
		SolUser             string `json:"sol_user"`
		SolPass             string `json:"sol_pass"`
	}
	_ = c.Bind().JSON(&body)
	var tenant database.Tenant
	_ = database.CentralDB.Select("id", "slug").First(&tenant, id).Error
	svc := companysvc.NewCompanyService(tenantDB).WithSaaSContext(uint(id), tenant.Slug)
	hasFiles := body.CertificateBase64 != "" || body.PrivateKeyBase64 != "" || body.LogoBase64 != "" || body.PfxBase64 != ""
	if hasFiles {
		if err := svc.SyncFacturadorConfigWithFiles(body.CertificateBase64, body.PrivateKeyBase64, body.LogoBase64, body.SolUser, body.SolPass, body.CertificatePassword, body.PfxBase64); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
		}
	} else {
		if err := svc.SyncFacturadorConfigWithFiles("", "", "", body.SolUser, body.SolPass, "", ""); err != nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
		}
	}
	now := time.Now()
	database.CentralDB.Model(&database.Tenant{}).Where("id = ?", id).Update("sunat_connected_at", now)
	return c.JSON(fiber.Map{"success": true, "message": "Configuración sincronizada con el facturador"})
}

// GET /api/superadmin/tenants/conectados-sunat — empresas en facturador Lycet (SUNAT y PSE).
// Alias: GET /api/superadmin/tenants/conectados-facturador
func (h *TenantHandler) ListConectadosSunatAPI(c fiber.Ctx) error {
	if config.AppConfig.FacturadorBaseURL == "" || config.AppConfig.FacturadorToken == "" {
		return c.JSON(fiber.Map{"data": []interface{}{}})
	}
	client := facturador.Shared()
	empresasLycet, err := client.ListEmpresas()
	if err != nil {
		return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{"error": "no se pudo obtener la lista del facturador: " + err.Error(), "data": []interface{}{}})
	}
	// RUCs en Lycet pueden venir como keys del mapa (map[RUC]Entry) o desde array; ListEmpresas ya normaliza a mapa por RUC.
	// Obtener todos los tenants de la BD central con RUC para cruzar
	var tenantsByRuc map[string]database.Tenant
	{
		var tenants []database.Tenant
		if err := database.CentralDB.Where("ruc != '' AND ruc IS NOT NULL").Find(&tenants).Error; err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
		}
		tenantsByRuc = make(map[string]database.Tenant)
		for _, t := range tenants {
			tenantsByRuc[t.RUC] = t
		}
	}
	type item struct {
		ID               uint       `json:"id"` // 0 si no hay tenant en tukifac con este RUC
		Name             string     `json:"name"`
		Slug             string     `json:"slug"`
		RUC              string     `json:"ruc"`
		SunatConnectedAt *time.Time `json:"sunat_connected_at"`
		EnLycet          bool       `json:"en_lycet"`
		AmbienteLycet    string     `json:"ambiente_lycet,omitempty"`
		SendMode         string     `json:"send_mode,omitempty"`
		Provider         string     `json:"provider,omitempty"`
		ConexionTipo       string     `json:"conexion_tipo"` // SUNAT | PSE
		ConnectionStatus   string     `json:"connection_status,omitempty"`
		PseConfigured      bool       `json:"pse_configured"`
		Enabled          bool       `json:"enabled"`
	}
	out := make([]item, 0, len(empresasLycet))
	for ruc, entry := range empresasLycet {
		if ruc == "" {
			continue
		}
		t, hasTenant := tenantsByRuc[ruc]
		ambiente := entry.Ambiente
		if ambiente == "" {
			ambiente = "pruebas"
		}
		conexion := facturador.ConnectionType(entry)
		pseConfigured := strings.TrimSpace(entry.PSEUser) != "" || conexion == "PSE"
		slug := ""
		if hasTenant {
			slug = t.Slug
		} else if strings.TrimSpace(entry.TenantSlug) != "" {
			slug = strings.TrimSpace(entry.TenantSlug)
		}
		if hasTenant {
			out = append(out, item{
				ID:               t.ID,
				Name:             t.Name,
				Slug:             slug,
				RUC:              ruc,
				SunatConnectedAt: t.SunatConnectedAt,
				EnLycet:          true,
				AmbienteLycet:    ambiente,
				SendMode:         entry.SendMode,
				Provider:         entry.Provider,
				ConexionTipo:       conexion,
				ConnectionStatus:   entry.ConnectionStatus,
				PseConfigured:    pseConfigured,
				Enabled:          entry.Enabled,
			})
		} else {
			out = append(out, item{
				ID:               0,
				Name:             "",
				Slug:             slug,
				RUC:              ruc,
				SunatConnectedAt: nil,
				EnLycet:          true,
				AmbienteLycet:    ambiente,
				SendMode:         entry.SendMode,
				Provider:         entry.Provider,
				ConexionTipo:       conexion,
				ConnectionStatus:   entry.ConnectionStatus,
				PseConfigured:    pseConfigured,
				Enabled:          entry.Enabled,
			})
		}
	}
	// Ordenar por RUC para orden estable
	sort.Slice(out, func(i, j int) bool { return out[i].RUC < out[j].RUC })
	return c.JSON(fiber.Map{"data": out})
}

type validaPSEManagementEnvelope struct {
	Success bool            `json:"success"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

type validaPSEEmpresa struct {
	ID              int    `json:"id"`
	RUC             string `json:"ruc"`
	RazonSocial     string `json:"razon_social"`
	Servidor        string `json:"servidor"`
	FechaInicio     string `json:"fecha_inicio"`
	FechaFin        string `json:"fecha_fin"`
	SinLimiteFecha  bool   `json:"sin_limite_fecha"`
	Estado          string `json:"estado"`
	FirmasUsadas    int    `json:"firmas_usadas"`
	CredencialesCPE struct {
		UsuarioSecundaria string `json:"usuario_secundaria"`
		TokenAcceso       string `json:"token_acceso"`
	} `json:"credenciales_cpe"`
	OSE struct {
		Activo bool `json:"activo"`
	} `json:"ose"`
}

type validaPSEEmpresasList struct {
	Data []validaPSEEmpresa `json:"data"`
	Meta json.RawMessage    `json:"meta"`
}

type validaPSEClient struct {
	baseURL string
	token   string
	client  *http.Client
}

func newValidaPSEClient() *validaPSEClient {
	base := strings.TrimSpace(config.AppConfig.ValidaPSEManagementBaseURL)
	if base == "" {
		base = "https://app.validapse.com/api"
	}
	return &validaPSEClient{
		baseURL: strings.TrimRight(base, "/"),
		token:   strings.TrimSpace(config.AppConfig.ValidaPSEManagementToken),
		client:  &http.Client{Timeout: 20 * time.Second},
	}
}

func (c *validaPSEClient) do(method, path string, q url.Values, body io.Reader) (*validaPSEManagementEnvelope, int, error) {
	if strings.TrimSpace(c.token) == "" {
		return nil, 0, fmt.Errorf("ValidaPSE no configurado: define VALIDAPSE_MGMT_TOKEN")
	}
	u := c.baseURL + path
	if q != nil && len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequest(method, u, body)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(b))
		if msg == "" {
			msg = resp.Status
		}
		return nil, resp.StatusCode, fmt.Errorf("ValidaPSE respondió %d: %s", resp.StatusCode, msg)
	}

	var env validaPSEManagementEnvelope
	if err := json.Unmarshal(b, &env); err != nil {
		return nil, resp.StatusCode, err
	}
	return &env, resp.StatusCode, nil
}

func parseValidaPSEEmpresasList(raw json.RawMessage) ([]validaPSEEmpresa, interface{}, error) {
	b := bytes.TrimSpace(raw)
	if len(b) == 0 {
		return []validaPSEEmpresa{}, nil, nil
	}

	if b[0] == '[' {
		var items []validaPSEEmpresa
		if err := json.Unmarshal(b, &items); err != nil {
			return nil, nil, err
		}
		return items, nil, nil
	}

	var obj map[string]json.RawMessage
	if err := json.Unmarshal(b, &obj); err != nil {
		return nil, nil, err
	}

	if dataRaw, ok := obj["data"]; ok {
		d := bytes.TrimSpace(dataRaw)
		if len(d) > 0 && d[0] == '[' {
			var items []validaPSEEmpresa
			if err := json.Unmarshal(d, &items); err != nil {
				return nil, nil, err
			}
			var meta interface{}
			if metaRaw, ok := obj["meta"]; ok && len(bytes.TrimSpace(metaRaw)) > 0 {
				_ = json.Unmarshal(metaRaw, &meta)
			}
			return items, meta, nil
		}

		var inner map[string]json.RawMessage
		if err := json.Unmarshal(d, &inner); err == nil {
			if innerData, ok := inner["data"]; ok && len(bytes.TrimSpace(innerData)) > 0 {
				var items []validaPSEEmpresa
				if err := json.Unmarshal(innerData, &items); err != nil {
					return nil, nil, err
				}
				var meta interface{}
				if metaRaw, ok := inner["meta"]; ok && len(bytes.TrimSpace(metaRaw)) > 0 {
					_ = json.Unmarshal(metaRaw, &meta)
				} else {
					var metaObj map[string]interface{}
					if err := json.Unmarshal(d, &metaObj); err == nil {
						delete(metaObj, "data")
						meta = metaObj
					}
				}
				return items, meta, nil
			}
		}
	}

	list := validaPSEEmpresasList{}
	if err := json.Unmarshal(b, &list); err != nil {
		return nil, nil, err
	}
	var meta interface{}
	if len(list.Meta) > 0 {
		_ = json.Unmarshal(list.Meta, &meta)
	}
	return list.Data, meta, nil
}

func validaPSEServidorFromEnvMode(envMode string) string {
	m := strings.ToLower(strings.TrimSpace(envMode))
	if m == "production" {
		return "PRODUCCION"
	}
	return "DEMO"
}

func validaPSETenantFechaFin(tenantID uint) string {
	var sub database.SaasSubscription
	if err := database.CentralDB.
		Where("tenant_id = ? AND status IN ('active','trial')", tenantID).
		Order("created_at desc").
		First(&sub).Error; err != nil {
		return ""
	}
	if sub.EndDate.IsZero() {
		return ""
	}
	return sub.EndDate.Format("2006-01-02")
}

func validaPSEFindEmpresaIDByRUC(client *validaPSEClient, ruc string) (int, error) {
	ruc = strings.TrimSpace(ruc)
	if ruc == "" {
		return 0, fmt.Errorf("RUC inválido")
	}
	q := url.Values{}
	q.Set("per_page", "100")
	q.Set("search", ruc)
	env, _, err := client.do(http.MethodGet, "/empresas", q, nil)
	if err != nil {
		return 0, err
	}
	items, _, perr := parseValidaPSEEmpresasList(env.Data)
	if perr != nil {
		return 0, fmt.Errorf("respuesta inválida de ValidaPSE")
	}
	for _, it := range items {
		if strings.TrimSpace(it.RUC) == ruc {
			if it.ID != 0 {
				return it.ID, nil
			}
		}
	}
	return 0, fmt.Errorf("No existe una empresa en el PSE para este RUC")
}

func validaPSEGetEmpresa(client *validaPSEClient, empresaID int) (*validaPSEEmpresa, error) {
	if empresaID == 0 {
		return nil, fmt.Errorf("ID inválido")
	}
	env, _, err := client.do(http.MethodGet, fmt.Sprintf("/empresas/%d", empresaID), nil, nil)
	if err != nil {
		return nil, err
	}
	var detail validaPSEEmpresa
	if err := json.Unmarshal(env.Data, &detail); err != nil {
		return nil, fmt.Errorf("respuesta inválida de ValidaPSE")
	}
	return &detail, nil
}

func validaPSEUpdateEmpresa(client *validaPSEClient, empresaID int, body map[string]interface{}) error {
	if empresaID == 0 {
		return fmt.Errorf("ID inválido")
	}
	b, _ := json.Marshal(body)
	_, _, err := client.do(http.MethodPut, fmt.Sprintf("/empresas/%d", empresaID), nil, bytes.NewBuffer(b))
	return err
}

func validaPSEToggleEmpresa(client *validaPSEClient, empresaID int) error {
	if empresaID == 0 {
		return fmt.Errorf("ID inválido")
	}
	_, _, err := client.do(http.MethodPatch, fmt.Sprintf("/empresas/%d/toggle", empresaID), nil, nil)
	return err
}

func (h *TenantHandler) ListPSEEmpresasAPI(c fiber.Ctx) error {
	client := newValidaPSEClient()
	if strings.TrimSpace(client.token) == "" {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
			"error": "ValidaPSE no configurado en el servidor (falta VALIDAPSE_MGMT_TOKEN)",
			"data":  []interface{}{},
		})
	}

	q := url.Values{}
	if v := strings.TrimSpace(c.Query("page")); v != "" {
		q.Set("page", v)
	}
	if v := strings.TrimSpace(c.Query("per_page")); v != "" {
		q.Set("per_page", v)
	}
	if v := strings.TrimSpace(c.Query("search")); v != "" {
		q.Set("search", v)
	}
	if v := strings.TrimSpace(c.Query("estado")); v != "" {
		q.Set("estado", v)
	}
	if v := strings.TrimSpace(c.Query("servidor")); v != "" {
		q.Set("servidor", v)
	}

	env, _, err := client.do(http.MethodGet, "/empresas", q, nil)
	if err != nil {
		return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{"error": err.Error(), "data": []interface{}{}})
	}

	items, meta, err := parseValidaPSEEmpresasList(env.Data)
	if err != nil {
		return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{"error": "respuesta inválida de ValidaPSE", "data": []interface{}{}})
	}

	rucs := make([]string, 0, len(items))
	for _, it := range items {
		if strings.TrimSpace(it.RUC) != "" {
			rucs = append(rucs, strings.TrimSpace(it.RUC))
		}
	}

	tenantsByRuc := map[string]database.Tenant{}
	if len(rucs) > 0 {
		var tenants []database.Tenant
		if err := database.CentralDB.Where("ruc IN ?", rucs).Find(&tenants).Error; err == nil {
			for _, t := range tenants {
				if strings.TrimSpace(t.RUC) != "" {
					tenantsByRuc[strings.TrimSpace(t.RUC)] = t
				}
			}
		}
	}

	out := make([]fiber.Map, 0, len(items))
	for _, it := range items {
		m := fiber.Map{
			"id":               it.ID,
			"ruc":              it.RUC,
			"razon_social":     it.RazonSocial,
			"servidor":         it.Servidor,
			"estado":           it.Estado,
			"fecha_inicio":     it.FechaInicio,
			"fecha_fin":        it.FechaFin,
			"sin_limite_fecha": it.SinLimiteFecha,
			"firmas_usadas":    it.FirmasUsadas,
			"ose_activo":       it.OSE.Activo,
		}
		if t, ok := tenantsByRuc[strings.TrimSpace(it.RUC)]; ok {
			m["tenant"] = fiber.Map{
				"id":   t.ID,
				"name": t.Name,
				"slug": t.Slug,
			}
		} else {
			m["tenant"] = nil
		}
		out = append(out, m)
	}

	resp := fiber.Map{"data": out}
	if meta != nil {
		resp["meta"] = meta
	}
	return c.JSON(resp)
}

func (h *TenantHandler) GetPSEEmpresaAPI(c fiber.Ctx) error {
	id := strings.TrimSpace(c.Params("id"))
	if id == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	client := newValidaPSEClient()
	if strings.TrimSpace(client.token) == "" {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "ValidaPSE no configurado en el servidor (falta VALIDAPSE_MGMT_TOKEN)"})
	}
	env, _, err := client.do(http.MethodGet, "/empresas/"+id, nil, nil)
	if err != nil {
		return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{"error": err.Error()})
	}
	var it validaPSEEmpresa
	if err := json.Unmarshal(env.Data, &it); err != nil {
		return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{"error": "respuesta inválida de ValidaPSE"})
	}

	var tenant database.Tenant
	hasTenant := false
	ruc := strings.TrimSpace(it.RUC)
	if ruc != "" {
		if err := database.CentralDB.Where("ruc = ?", ruc).First(&tenant).Error; err == nil {
			hasTenant = true
		}
	}

	out := fiber.Map{
		"id":               it.ID,
		"ruc":              it.RUC,
		"razon_social":     it.RazonSocial,
		"servidor":         it.Servidor,
		"estado":           it.Estado,
		"fecha_inicio":     it.FechaInicio,
		"fecha_fin":        it.FechaFin,
		"sin_limite_fecha": it.SinLimiteFecha,
		"firmas_usadas":    it.FirmasUsadas,
		"ose_activo":       it.OSE.Activo,
	}
	if hasTenant {
		out["tenant"] = fiber.Map{"id": tenant.ID, "name": tenant.Name, "slug": tenant.Slug}
	} else {
		out["tenant"] = nil
	}
	return c.JSON(out)
}

func validaPSEDefaultCPEBaseURL(mgmtBaseURL string) string {
	u := strings.TrimRight(strings.TrimSpace(mgmtBaseURL), "/")
	if strings.HasSuffix(u, "/api") {
		u = strings.TrimSuffix(u, "/api")
	}
	return u
}

func (h *TenantHandler) CreatePSEEmpresaAPI(c fiber.Ctx) error {
	client := newValidaPSEClient()
	if strings.TrimSpace(client.token) == "" {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "ValidaPSE no configurado en el servidor (falta VALIDAPSE_MGMT_TOKEN)"})
	}
	var body map[string]interface{}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "JSON inválido"})
	}
	b, _ := json.Marshal(body)
	env, _, err := client.do(http.MethodPost, "/empresas", nil, bytes.NewBuffer(b))
	if err != nil {
		return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{"error": err.Error()})
	}
	var it validaPSEEmpresa
	_ = json.Unmarshal(env.Data, &it)
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{
		"success": env.Success,
		"message": env.Message,
		"data": fiber.Map{
			"id":                       it.ID,
			"ruc":                      it.RUC,
			"razon_social":             it.RazonSocial,
			"servidor":                 it.Servidor,
			"estado":                   it.Estado,
			"fecha_inicio":             it.FechaInicio,
			"fecha_fin":                it.FechaFin,
			"sin_limite_fecha":         it.SinLimiteFecha,
			"firmas_usadas":            it.FirmasUsadas,
			"ose_activo":               it.OSE.Activo,
			"token_acceso_configurado": strings.TrimSpace(it.CredencialesCPE.TokenAcceso) != "",
		},
	})
}

func (h *TenantHandler) UpdatePSEEmpresaAPI(c fiber.Ctx) error {
	id := strings.TrimSpace(c.Params("id"))
	if id == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	client := newValidaPSEClient()
	if strings.TrimSpace(client.token) == "" {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "ValidaPSE no configurado en el servidor (falta VALIDAPSE_MGMT_TOKEN)"})
	}
	var body map[string]interface{}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "JSON inválido"})
	}
	b, _ := json.Marshal(body)
	env, _, err := client.do(http.MethodPut, "/empresas/"+id, nil, bytes.NewBuffer(b))
	if err != nil {
		return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{"error": err.Error()})
	}
	var it validaPSEEmpresa
	_ = json.Unmarshal(env.Data, &it)
	return c.JSON(fiber.Map{
		"success": env.Success,
		"message": env.Message,
		"data": fiber.Map{
			"id":                       it.ID,
			"ruc":                      it.RUC,
			"razon_social":             it.RazonSocial,
			"servidor":                 it.Servidor,
			"estado":                   it.Estado,
			"fecha_inicio":             it.FechaInicio,
			"fecha_fin":                it.FechaFin,
			"sin_limite_fecha":         it.SinLimiteFecha,
			"firmas_usadas":            it.FirmasUsadas,
			"ose_activo":               it.OSE.Activo,
			"token_acceso_configurado": strings.TrimSpace(it.CredencialesCPE.TokenAcceso) != "",
		},
	})
}

func (h *TenantHandler) TogglePSEEmpresaAPI(c fiber.Ctx) error {
	id := strings.TrimSpace(c.Params("id"))
	if id == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
	}
	client := newValidaPSEClient()
	if strings.TrimSpace(client.token) == "" {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{"error": "ValidaPSE no configurado en el servidor (falta VALIDAPSE_MGMT_TOKEN)"})
	}
	env, _, err := client.do(http.MethodPatch, "/empresas/"+id+"/toggle", nil, nil)
	if err != nil {
		return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{"error": err.Error()})
	}
	var it validaPSEEmpresa
	_ = json.Unmarshal(env.Data, &it)
	return c.JSON(fiber.Map{
		"success": env.Success,
		"message": env.Message,
		"data": fiber.Map{
			"id":                       it.ID,
			"ruc":                      it.RUC,
			"razon_social":             it.RazonSocial,
			"servidor":                 it.Servidor,
			"estado":                   it.Estado,
			"fecha_inicio":             it.FechaInicio,
			"fecha_fin":                it.FechaFin,
			"sin_limite_fecha":         it.SinLimiteFecha,
			"firmas_usadas":            it.FirmasUsadas,
			"ose_activo":               it.OSE.Activo,
			"token_acceso_configurado": strings.TrimSpace(it.CredencialesCPE.TokenAcceso) != "",
		},
	})
}

