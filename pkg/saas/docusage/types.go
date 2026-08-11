package docusage

import "strings"

// DocumentUsageView resumen para Billing Hub y APIs.
//
// Ojo con las dos fechas: la cuota del plan (PlanLimit) se renueva cada mes en
// QuotaPeriodEnd, mientras que los paquetes comprados duran hasta BillingCycleEnd, que
// es el fin de la suscripción pagada.
type DocumentUsageView struct {
	IsUnlimited      bool   `json:"is_unlimited"`
	PlanLimit        int    `json:"plan_limit"`
	PlanUsed         int    `json:"plan_used"`
	PlanRemaining    int    `json:"plan_remaining"`
	PackageBonus     int    `json:"package_bonus"`
	PackageUsed      int    `json:"package_used"`
	PackageRemaining int    `json:"package_remaining"`
	TotalAvailable   int    `json:"total_available"`
	TotalConsumed    int    `json:"total_consumed"`
	UsagePercent     int    `json:"usage_percent"`
	WarningLevel     string `json:"warning_level"` // none | low | high | exhausted
	WarningMessage   string `json:"warning_message,omitempty"`
	CanEmit          bool   `json:"can_emit"`
	BillingCycleID   uint   `json:"billing_cycle_id,omitempty"`
	// Fin de la suscripción pagada; hasta aquí valen los paquetes comprados.
	BillingCycleEnd string `json:"billing_cycle_end,omitempty"`

	// Período mensual de cuota vigente.
	QuotaPeriodID uint `json:"quota_period_id,omitempty"`
	// Día en que el cupo del plan vuelve a estar completo (YYYY-MM-DD).
	QuotaPeriodEnd string `json:"quota_period_end,omitempty"`
	// Índice del mes dentro de la suscripción: "mes 2 de 6".
	QuotaPeriodIndex int `json:"quota_period_index,omitempty"`
	QuotaPeriodTotal int `json:"quota_period_total,omitempty"`
}

// ReserveInput intento de emisión (cuenta aunque SUNAT falle).
type ReserveInput struct {
	TenantID       uint
	DocumentType   string
	DocumentID     uint
	DocumentNumber string
	Source         string
	MetadataJSON   string
}

// SunatCodeToDocType mapea código SUNAT a tipo de dominio.
func SunatCodeToDocType(code string) string {
	switch strings.TrimSpace(code) {
	case "01":
		return "invoice"
	case "03":
		return "receipt"
	case "07":
		return "credit_note"
	case "08":
		return "debit_note"
	case "09":
		return "guide_remitter"
	case "31":
		return "guide_carrier"
	case "20":
		return "retention"
	case "40":
		return "perception"
	default:
		return "electronic"
	}
}

// IsCountableSunatCode indica si el código SUNAT consume cupo.
func IsCountableSunatCode(code string) bool {
	switch strings.TrimSpace(code) {
	case "00", "":
		return false
	default:
		return true
	}
}
