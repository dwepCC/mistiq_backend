package saas

import (
	"encoding/json"
	"strings"
	"time"

	"tukifac/pkg/database"
)

const DefaultTimezone = "America/Lima"

// PlatformSettings DTO para API (panel central).
type PlatformSettings struct {
	ReminderDays                   []int                 `json:"reminder_days"`
	GracePeriodDays                int                   `json:"grace_period_days"`
	ReconnectionFee                float64               `json:"reconnection_fee"`
	AutoSuspendEnabled             bool                  `json:"auto_suspend_enabled"`
	ProvisionalReactivationEnabled bool                  `json:"provisional_reactivation_enabled"`
	ProvisionalHours               int                   `json:"provisional_hours"`
	StrikeMax                      int                   `json:"strike_max"`
	CronEvalHour                   int                   `json:"cron_eval_hour"`
	CronEvalMinute                 int                   `json:"cron_eval_minute"`
	Timezone                       string                `json:"timezone"`
	PaymentMethods                 []PaymentMethodConfig `json:"payment_methods"`
	BankAccounts                   []BankAccountConfig   `json:"bank_accounts"`
	YapeQRURL                      string                `json:"yape_qr_url"`
	PlinQRURL                      string                `json:"plin_qr_url"`
	PortalURLOverride              string                `json:"portal_url_override"` // vacío = flujo interno /subscription
	Support                        SupportConfig         `json:"support"`
	OperationsKeyConfigured        bool                  `json:"operations_key_configured"`
	UpdatedAt                      string                `json:"updated_at,omitempty"`
}

// SupportConfig contacto para tenants.
type SupportConfig struct {
	WhatsApp string `json:"whatsapp"`
	Email    string `json:"email"`
	Phone    string `json:"phone"`
}

type PaymentMethodConfig struct {
	Key     string `json:"key"`
	Label   string `json:"label"`
	Enabled bool   `json:"enabled"`
}

type BankAccountConfig struct {
	Bank          string `json:"bank"`
	AccountNumber string `json:"account_number"`
	CCI           string `json:"cci"`
	Holder        string `json:"holder"`
	Currency      string `json:"currency"`
	Enabled       bool   `json:"enabled"`
}

// PaymentConfigView métodos/cuentas visibles para el tenant (solo activos).
type PaymentConfigView struct {
	Methods       []PaymentMethodConfig `json:"methods"`
	BankAccounts  []BankAccountConfig   `json:"bank_accounts"`
	YapeQRURL     string                `json:"yape_qr_url"`
	PlinQRURL     string                `json:"plin_qr_url"`
	PortalOverride string               `json:"portal_url_override,omitempty"`
	UseInternalHub bool                 `json:"use_internal_hub"`
}

func defaultSettings() PlatformSettings {
	return PlatformSettings{
		ReminderDays:                   []int{7, 5, 3, 1},
		GracePeriodDays:                3,
		ReconnectionFee:                50,
		AutoSuspendEnabled:             true,
		ProvisionalReactivationEnabled: true,
		ProvisionalHours:               12,
		StrikeMax:                      2,
		CronEvalHour:                   0,
		CronEvalMinute:                 5,
		Timezone:                       DefaultTimezone,
		PaymentMethods: []PaymentMethodConfig{
			{Key: "yape", Label: "Yape", Enabled: true},
			{Key: "plin", Label: "Plin", Enabled: true},
			{Key: "transfer", Label: "Transferencia", Enabled: true},
			{Key: "deposit", Label: "Depósito", Enabled: true},
		},
		BankAccounts: []BankAccountConfig{},
		PortalURLOverride: "",
	}
}

// LoadSettings carga configuración central (crea defaults si no existe).
func LoadSettings() (PlatformSettings, error) {
	out := defaultSettings()
	if database.CentralDB == nil {
		return out, nil
	}
	var row database.SaasPlatformSettings
	err := database.CentralDB.First(&row, 1).Error
	if err != nil {
		row = database.SaasPlatformSettings{ID: 1, StrikeMax: 2, CronEvalHour: 0, CronEvalMinute: 5}
		b, _ := json.Marshal(out.ReminderDays)
		row.ReminderDaysJSON = string(b)
		row.GracePeriodDays = out.GracePeriodDays
		row.ReconnectionFee = out.ReconnectionFee
		row.AutoSuspendEnabled = out.AutoSuspendEnabled
		row.ProvisionalReactivationEnabled = out.ProvisionalReactivationEnabled
		row.ProvisionalHours = out.ProvisionalHours
		pm, _ := json.Marshal(out.PaymentMethods)
		row.PaymentMethodsJSON = string(pm)
		_ = database.CentralDB.Create(&row).Error
		return out, nil
	}
	if row.ReminderDaysJSON != "" {
		_ = json.Unmarshal([]byte(row.ReminderDaysJSON), &out.ReminderDays)
	}
	out.GracePeriodDays = row.GracePeriodDays
	out.ReconnectionFee = row.ReconnectionFee
	out.AutoSuspendEnabled = row.AutoSuspendEnabled
	out.ProvisionalReactivationEnabled = row.ProvisionalReactivationEnabled
	out.ProvisionalHours = row.ProvisionalHours
	if row.StrikeMax > 0 {
		out.StrikeMax = row.StrikeMax
	}
	if row.CronEvalHour >= 0 && row.CronEvalHour <= 23 {
		out.CronEvalHour = row.CronEvalHour
	}
	if row.CronEvalMinute >= 0 && row.CronEvalMinute <= 59 {
		out.CronEvalMinute = row.CronEvalMinute
	}
	out.Timezone = DefaultTimezone
	if row.PaymentMethodsJSON != "" {
		_ = json.Unmarshal([]byte(row.PaymentMethodsJSON), &out.PaymentMethods)
	}
	if row.BankAccountsJSON != "" {
		_ = json.Unmarshal([]byte(row.BankAccountsJSON), &out.BankAccounts)
	}
	out.YapeQRURL = row.YapeQRURL
	out.PlinQRURL = row.PlinQRURL
	out.PortalURLOverride = strings.TrimSpace(row.PortalURL)
	out.Support = SupportConfig{
		WhatsApp: row.SupportWhatsApp,
		Email:    row.SupportEmail,
		Phone:    row.SupportPhone,
	}
	out.OperationsKeyConfigured = strings.TrimSpace(row.OperationsKeyHash) != ""
	if !row.UpdatedAt.IsZero() {
		out.UpdatedAt = row.UpdatedAt.UTC().Format(time.RFC3339)
	}
	return out, nil
}

// SaveSettings persiste configuración.
func SaveSettings(in PlatformSettings) error {
	if database.CentralDB == nil {
		return nil
	}
	if in.StrikeMax <= 0 {
		in.StrikeMax = 2
	}
	if in.ProvisionalHours <= 0 {
		in.ProvisionalHours = MaxProvisionalHours
	}
	if in.ProvisionalHours > MaxProvisionalHours {
		in.ProvisionalHours = MaxProvisionalHours
	}
	var existing database.SaasPlatformSettings
	_ = database.CentralDB.First(&existing, 1).Error

	rd, _ := json.Marshal(in.ReminderDays)
	pm, _ := json.Marshal(in.PaymentMethods)
	ba, _ := json.Marshal(in.BankAccounts)
	row := database.SaasPlatformSettings{
		ID:                             1,
		ReminderDaysJSON:               string(rd),
		GracePeriodDays:                in.GracePeriodDays,
		ReconnectionFee:                  in.ReconnectionFee,
		AutoSuspendEnabled:             in.AutoSuspendEnabled,
		ProvisionalReactivationEnabled: in.ProvisionalReactivationEnabled,
		ProvisionalHours:               in.ProvisionalHours,
		StrikeMax:                      in.StrikeMax,
		CronEvalHour:                   in.CronEvalHour,
		CronEvalMinute:                 in.CronEvalMinute,
		PaymentMethodsJSON:             string(pm),
		BankAccountsJSON:               string(ba),
		YapeQRURL:                      in.YapeQRURL,
		PlinQRURL:                      in.PlinQRURL,
		PortalURL:                      strings.TrimSpace(in.PortalURLOverride),
		SupportWhatsApp:                in.Support.WhatsApp,
		SupportEmail:                   in.Support.Email,
		SupportPhone:                   in.Support.Phone,
		OperationsKeyHash:              existing.OperationsKeyHash,
		UpdatedAt:                      time.Now(),
	}
	return database.CentralDB.Save(&row).Error
}

// TenantPaymentConfig solo métodos/cuentas activos para UI tenant.
func TenantPaymentConfig(cfg PlatformSettings) PaymentConfigView {
	methods := make([]PaymentMethodConfig, 0)
	for _, m := range cfg.PaymentMethods {
		if m.Enabled {
			methods = append(methods, m)
		}
	}
	banks := make([]BankAccountConfig, 0)
	for _, b := range cfg.BankAccounts {
		if b.Enabled {
			banks = append(banks, b)
		}
	}
	override := strings.TrimSpace(cfg.PortalURLOverride)
	return PaymentConfigView{
		Methods:        methods,
		BankAccounts:   banks,
		YapeQRURL:      cfg.YapeQRURL,
		PlinQRURL:      cfg.PlinQRURL,
		PortalOverride: override,
		UseInternalHub: true,
	}
}

// EffectiveStrikeMax desde configuración.
func EffectiveStrikeMax(cfg PlatformSettings) int {
	if cfg.StrikeMax <= 0 {
		return 2
	}
	return cfg.StrikeMax
}
