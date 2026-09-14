package saas

import (
	"encoding/json"
	"strings"
	"time"

	"tukifac/pkg/database"
)

const DefaultTimezone = "America/Lima"

// DefaultPaymentWindowDays días para pagar un cobro recién emitido antes de darlo por vencido.
const DefaultPaymentWindowDays = 3

// PlatformSettings DTO para API (panel central).
type PlatformSettings struct {
	ReminderDays                   []int                 `json:"reminder_days"`
	GracePeriodDays                int                   `json:"grace_period_days"`
	PaymentWindowDays              int                   `json:"payment_window_days"`
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

// Kind de un método de pago: determina qué se le muestra al tenant al elegirlo.
const (
	PaymentMethodKindQR          = "qr"           // método tipo billetera: QR propio, opcional
	PaymentMethodKindBankAccount = "bank_account"  // método tipo depósito/transferencia: lista de cuentas bancarias
)

type PaymentMethodConfig struct {
	Key     string `json:"key"`
	Label   string `json:"label"`
	Enabled bool   `json:"enabled"`
	// Kind: "qr" o "bank_account". Determina si este método muestra su propio QR (QRURL) o
	// la lista compartida de cuentas bancarias (PlatformSettings.BankAccounts). Configs viejas
	// sin este campo se completan en LoadSettings (ver backfillPaymentMethodDefaults).
	Kind string `json:"kind"`
	// QRURL: imagen de QR propia de este método (solo aplica si Kind == "qr"). Antes de esto,
	// el QR de yape/plin vivía en dos campos sueltos (YapeQRURL/PlinQRURL) sin relación con la
	// lista de métodos — ver backfillPaymentMethodDefaults para la migración de esos datos viejos.
	QRURL string `json:"qr_url,omitempty"`
	// LogoURL: logo del método (ej. el ícono de Yape/Plin), se muestra junto al QR al tenant.
	LogoURL string `json:"logo_url,omitempty"`
	// ExtraInfo: texto libre (multilínea) con datos que el tenant necesita para pagar con este
	// método — ej. número de Yape/Plin y titular. Se muestra tal cual al costado del QR.
	ExtraInfo string `json:"extra_info,omitempty"`
}

type BankAccountConfig struct {
	// ID: identificador estable generado al crear la cuenta (el struct no tenía uno; se agrega
	// para poder asociarle un logo propio, igual que PaymentMethodConfig.Key). Cuentas guardadas
	// antes de este cambio no lo traen — se les asigna uno recién al guardarse de nuevo desde el
	// panel; mientras tanto simplemente no tienen logo propio (no rompe nada, es opcional).
	ID            string `json:"id,omitempty"`
	Bank          string `json:"bank"`
	AccountNumber string `json:"account_number"`
	CCI           string `json:"cci"`
	Holder        string `json:"holder"`
	Currency      string `json:"currency"`
	Enabled       bool   `json:"enabled"`
	// LogoURL: logo del banco, se muestra junto a los datos de la cuenta al tenant.
	LogoURL string `json:"logo_url,omitempty"`
	// ExtraInfo: texto libre (multilínea) con instrucciones adicionales para este depósito/
	// transferencia (ej. "Solo depósitos en agencia", horarios, etc.).
	ExtraInfo string `json:"extra_info,omitempty"`
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
		PaymentWindowDays:              DefaultPaymentWindowDays,
		ReconnectionFee:                50,
		AutoSuspendEnabled:             true,
		ProvisionalReactivationEnabled: true,
		ProvisionalHours:               12,
		StrikeMax:                      2,
		CronEvalHour:                   0,
		CronEvalMinute:                 5,
		Timezone:                       DefaultTimezone,
		PaymentMethods: []PaymentMethodConfig{
			{Key: "yape", Label: "Yape", Enabled: true, Kind: PaymentMethodKindQR},
			{Key: "plin", Label: "Plin", Enabled: true, Kind: PaymentMethodKindQR},
			{Key: "transfer", Label: "Transferencia", Enabled: true, Kind: PaymentMethodKindBankAccount},
			{Key: "deposit", Label: "Depósito", Enabled: true, Kind: PaymentMethodKindBankAccount},
		},
		BankAccounts: []BankAccountConfig{},
		PortalURLOverride: "",
	}
}

// backfillPaymentMethodDefaults completa Kind/QRURL en configs guardadas antes de que existieran
// estos campos (JSON viejo sin "kind"/"qr_url" simplemente los deserializa como ""). Sin esto,
// un tenant con configuración previa vería sus métodos sin QR ni cuentas bancarias hasta que un
// admin volviera a guardar el formulario. Muta in-place (methods comparte el array de out).
func backfillPaymentMethodDefaults(methods []PaymentMethodConfig, legacyYapeQR, legacyPlinQR string) {
	for i := range methods {
		m := &methods[i]
		if m.Kind == "" {
			switch strings.ToLower(strings.TrimSpace(m.Key)) {
			case "yape", "plin":
				m.Kind = PaymentMethodKindQR
			default:
				// Antes de este cambio, cualquier método que no fuera yape/plin ya se mostraba
				// como cuenta bancaria (era el único contenido disponible aparte del QR) — mismo
				// comportamiento por default acá.
				m.Kind = PaymentMethodKindBankAccount
			}
		}
		if m.Kind == PaymentMethodKindQR && m.QRURL == "" {
			switch strings.ToLower(strings.TrimSpace(m.Key)) {
			case "yape":
				m.QRURL = legacyYapeQR
			case "plin":
				m.QRURL = legacyPlinQR
			}
		}
	}
}

// PaymentMethodByKey busca un método (case-insensitive) en la config. nil si no existe.
func PaymentMethodByKey(methods []PaymentMethodConfig, key string) *PaymentMethodConfig {
	key = strings.ToLower(strings.TrimSpace(key))
	if key == "" {
		return nil
	}
	for i := range methods {
		if strings.ToLower(strings.TrimSpace(methods[i].Key)) == key {
			return &methods[i]
		}
	}
	return nil
}

// BankAccountByID busca una cuenta bancaria por ID (case-insensitive). nil si no existe.
func BankAccountByID(accounts []BankAccountConfig, id string) *BankAccountConfig {
	id = strings.ToLower(strings.TrimSpace(id))
	if id == "" {
		return nil
	}
	for i := range accounts {
		if strings.ToLower(strings.TrimSpace(accounts[i].ID)) == id {
			return &accounts[i]
		}
	}
	return nil
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
	// Filas creadas antes de que existiera el campo traen 0; ahí manda el default, no
	// «sin ventana» (que daría por vencido el cobro el mismo día de emitirlo).
	if row.PaymentWindowDays > 0 {
		out.PaymentWindowDays = row.PaymentWindowDays
	}
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
	backfillPaymentMethodDefaults(out.PaymentMethods, out.YapeQRURL, out.PlinQRURL)
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
	if in.PaymentWindowDays <= 0 {
		in.PaymentWindowDays = DefaultPaymentWindowDays
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
		PaymentWindowDays:              in.PaymentWindowDays,
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

// EffectivePaymentWindowDays días de ventana de pago, con default si no está configurado.
func EffectivePaymentWindowDays(cfg PlatformSettings) int {
	if cfg.PaymentWindowDays <= 0 {
		return DefaultPaymentWindowDays
	}
	return cfg.PaymentWindowDays
}

// EffectiveStrikeMax desde configuración.
func EffectiveStrikeMax(cfg PlatformSettings) int {
	if cfg.StrikeMax <= 0 {
		return 2
	}
	return cfg.StrikeMax
}
