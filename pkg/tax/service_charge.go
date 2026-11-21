package tax

import (
	"errors"
	"fmt"

	"tukifac/pkg/database"
	"tukifac/pkg/money"

	"gorm.io/gorm"
)

// Recargo al consumo (DL 25988, 5.ª disposición complementaria). Lo fija el establecimiento de hospedaje o de expendio de comidas y bebidas
// en acuerdo con sus trabajadores, hasta 13 % del valor de los servicios; no integra la base imponible del IGV y no es ingreso del negocio.
const (
	// MaxServiceChargeRate tope legal (porcentaje).
	MaxServiceChargeRate = 13.0
	// DefaultServiceChargeRate porcentaje que se propone al activarlo (práctica habitual; el tenant lo cambia).
	DefaultServiceChargeRate = 10.0
	// ServiceChargeCode código del catálogo 53 de SUNAT: «Recargo al consumo y/o propinas» (cargo global que no afecta la base del IGV).
	ServiceChargeCode = "46"
)

// ValidServiceChargeRate indica si el porcentaje está dentro de lo permitido (0 < tasa <= 13).
func ValidServiceChargeRate(rate float64) bool {
	return rate > 0 && rate <= MaxServiceChargeRate
}

// ServiceChargeAmount recargo sobre la base indicada (valor de venta sin IGV, ya con el descuento global aplicado), redondeado a céntimos (es un monto cobrado al cliente y va en el XML con 2 decimales).
// Devuelve 0 si la tasa no es válida o la base no es positiva: nunca cobra por encima del tope legal.
func ServiceChargeAmount(base, rate float64) float64 {
	if base <= 0 || !ValidServiceChargeRate(rate) {
		return 0
	}
	return money.RoundDisplay(base * rate / 100)
}

// ErrServiceChargeDisabled la venta pidió recargo al consumo pero el tenant no lo tiene activado.
var ErrServiceChargeDisabled = errors.New("el recargo al consumo no está activado en la configuración de la empresa")

// LoadServiceChargeFromDB lee del tenant si el recargo está activado y con qué porcentaje.
func LoadServiceChargeFromDB(db *gorm.DB) (enabled bool, rate float64) {
	var cfg database.TenantCompanyConfig
	if err := db.Select("service_charge_enabled", "service_charge_rate").First(&cfg).Error; err != nil {
		return false, 0
	}
	return cfg.ServiceChargeEnabled, cfg.ServiceChargeRate
}

// ResolveServiceChargeRate decide el porcentaje a cobrar en una venta. El porcentaje SIEMPRE sale de la configuración del tenant, nunca de lo que
// mande el cliente (el cajero solo elige cobrarlo o no). apply=false → 0 sin consultar. apply=true con el recargo apagado → error, para que
// una pantalla desactualizada no cobre ni calle un recargo que el negocio no activó.
func ResolveServiceChargeRate(db *gorm.DB, apply bool) (float64, error) {
	if !apply {
		return 0, nil
	}
	enabled, rate := LoadServiceChargeFromDB(db)
	if !enabled {
		return 0, ErrServiceChargeDisabled
	}
	if !ValidServiceChargeRate(rate) {
		return 0, fmt.Errorf("el porcentaje de recargo al consumo configurado (%.2f) no es válido: debe ser mayor que 0 y no pasar de %.0f%%", rate, MaxServiceChargeRate)
	}
	return rate, nil
}

// AddServiceCharge suma el recargo al total. base es el valor de venta sin IGV (con descuentos); el IGV no se toca: el recargo no integra la
// base imponible. Devuelve el recargo y el nuevo total.
func AddServiceCharge(base, total, rate float64) (charge, newTotal float64) {
	charge = ServiceChargeAmount(base, rate)
	return charge, money.RoundSunat(total + charge)
}
