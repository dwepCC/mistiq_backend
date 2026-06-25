package service

import (
	"errors"
	"strings"
	"time"

	"tukifac/config"
	"tukifac/pkg/database"
	"tukifac/pkg/facturador"
	"tukifac/pkg/fiscal"

	"gorm.io/gorm"
)

// FiscalSyncInput credenciales transitivas panel central → facturador (no se persisten en tenant ERP).
type FiscalSyncInput struct {
	SendMode       string
	Provider       string
	ConnectionType string
	SOLUser        string
	SOLPass        string
	CertificateB64 string
	LogoB64        string
	CertPassword   string
	PSEBaseURL     string
	PSEUser        string
	PSEPassword    string
	PSEToken       string
	PSESecondary   string
	GreClientID    string
	GreClientSecret string
	Enabled        bool
}

func normalizeSendMode(mode string) string {
	m := strings.ToLower(strings.TrimSpace(mode))
	if m == "pse" {
		return "pse"
	}
	if m == "" {
		return "sunat_direct"
	}
	return m
}

// SyncFiscalToFacturador sincroniza SSOT en facturador. Secretos solo via input (request panel).
func (s *CompanyService) SyncFiscalToFacturador(input FiscalSyncInput) (*facturador.FiscalCompanyStatus, error) {
	if config.AppConfig.FacturadorBaseURL == "" || config.AppConfig.FacturadorToken == "" {
		return nil, errors.New("facturador no configurado")
	}
	var cfg database.TenantCompanyConfig
	if err := s.db.First(&cfg).Error; err != nil {
		return nil, errors.New("configure primero los datos de la empresa")
	}
	if cfg.RUC == "" {
		return nil, errors.New("RUC requerido")
	}

	sendMode := normalizeSendMode(input.SendMode)
	if sendMode == "sunat_direct" && strings.TrimSpace(input.SendMode) == "" {
		sendMode = normalizeSendMode(cfg.SendMode)
	}
	provider := strings.TrimSpace(input.Provider)
	if provider == "" {
		provider = strings.TrimSpace(cfg.FiscalProvider)
	}
	if provider == "" {
		if sendMode == "pse" {
			provider = "validapse"
		} else {
			provider = "sunat"
		}
	}
	provider = fiscal.NormalizePSEProvider(provider)
	if sendMode != "pse" && provider == "validapse" && strings.TrimSpace(input.Provider) == "" && strings.TrimSpace(cfg.FiscalProvider) == "" {
		provider = "sunat"
	}
	connType := strings.TrimSpace(input.ConnectionType)
	if sendMode == "pse" {
		connType = "bearer"
	} else if connType == "" {
		connType = strings.TrimSpace(cfg.FiscalConnectionType)
	}
	if connType == "" {
		connType = "bearer"
	}
	if sendMode == "sunat_direct" {
		provider = "sunat"
	}
	if sendMode == "pse" && fiscal.ResolvePSEBaseURL(provider) == "" {
		provider = "validapse"
	}

	pseBaseURL := strings.TrimSpace(input.PSEBaseURL)
	if sendMode == "pse" && pseBaseURL == "" {
		pseBaseURL = fiscal.ResolvePSEBaseURL(provider)
	}
	pseToken := strings.TrimSpace(input.PSEToken)
	psePassword := strings.TrimSpace(input.PSEPassword)
	if sendMode == "pse" && pseToken == "" {
		pseToken = psePassword
	}
	ambiente := fiscal.SunatEnvToFacturadorAmbiente(cfg.SunatEnvMode)
	solUser := strings.TrimSpace(input.SOLUser)
	solPass := strings.TrimSpace(input.SOLPass)

	autoSend := cfg.AutomaticSend
	emailOn := true
	retryOn := true
	enabled := input.Enabled || cfg.SunatEnabled

	payload := facturador.FiscalCompanySyncPayload{
		RUC:            cfg.RUC,
		TenantID:       s.tenantID,
		TenantSlug:     s.tenantSlug,
		SendMode:       sendMode,
		Provider:       provider,
		ConnectionType: connType,
		Ambiente:       ambiente,
		CertificateB64: strings.TrimSpace(input.CertificateB64),
		CertPassword:   input.CertPassword,
		LogoB64:        input.LogoB64,
		PSEBaseURL:     pseBaseURL,
		PSEUser:        input.PSEUser,
		PSEPassword:    psePassword,
		PSEToken:       pseToken,
		PSESecondary:   input.PSESecondary,
		AutomaticSend:  &autoSend,
		EmailEnabled:   &emailOn,
		RetryEnabled:   &retryOn,
		Enabled:        &enabled,
	}
	// Credenciales SOL: solo si el usuario las envió explícitamente en la petición.
	// Sin fallback MODDATOS — Lycet conserva los valores persistidos en actualizaciones parciales.
	if solUser != "" {
		payload.SOLUser = solUser
	}
	if solPass != "" {
		payload.SOLPass = solPass
	}
	if ambiente == "produccion" {
		payload.GreClientID = strings.TrimSpace(input.GreClientID)
		payload.GreClientSecret = strings.TrimSpace(input.GreClientSecret)
	}

	status, err := facturador.Shared().CompanySync(payload)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	_ = s.db.Model(&cfg).Updates(map[string]interface{}{
		"send_mode":                sendMode,
		"fiscal_provider":          provider,
		"fiscal_connection_type":   connType,
		"fiscal_connection_status": status.ConnectionStatus,
		"fiscal_last_sync_at":      now,
		"sunat_connected":          status.ConnectionStatus == "connected",
	}).Error

	return status, nil
}

// SaveFiscalMetadataCentral persiste solo metadatos fiscales en tenant ERP (sin secretos).
func (s *CompanyService) SaveFiscalMetadataCentral(
	sendMode, provider, connectionType, envMode string,
	enabled bool,
	taxRate float64,
	igvRegime string,
	taxBenefitZone bool,
	automaticSend *bool,
) error {
	var existing database.TenantCompanyConfig
	if err := s.db.First(&existing).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		return errors.New("configure primero los datos generales de la empresa")
	}
	if taxRate <= 0 {
		taxRate = 18
	}
	if igvRegime == "" {
		igvRegime = "standard"
	}
	updates := map[string]interface{}{
		"sunat_enabled":          enabled,
		"sunat_env_mode":         fiscal.NormalizeSunatEnvMode(envMode),
		"send_mode":              normalizeSendMode(sendMode),
		"fiscal_provider":        strings.TrimSpace(provider),
		"fiscal_connection_type": strings.TrimSpace(connectionType),
		"tax_rate":               taxRate,
		"igv_regime":             igvRegime,
		"tax_benefit_zone":       taxBenefitZone,
	}
	if automaticSend != nil {
		updates["automatic_send"] = *automaticSend
	}
	return s.db.Model(&existing).Updates(updates).Error
}
