// Package tax centraliza la lógica tributaria del sistema para Perú.
// Elimina valores hardcodeados de IGV y aplica correctamente los tipos
// de afectación SUNAT (Catálogo N°07) por producto.
package tax

import (
	"strings"

	"tukifac/pkg/database"
	"tukifac/pkg/money"

	"gorm.io/gorm"
)

// Config contiene la configuración tributaria activa de la empresa.
type Config struct {
	// TaxRate es la tasa de IGV configurada en la empresa (p.ej. 18, 10, 0).
	TaxRate float64
	// IgvRegime identifica el régimen: "standard", "reduced" o "exonerated".
	IgvRegime string
	// TaxBenefitZone indica si la empresa opera en zona amazónica/selva con exoneración.
	TaxBenefitZone bool
}

// DefaultConfig retorna la configuración tributaria por defecto (IGV 18% régimen general).
func DefaultConfig() Config {
	return Config{TaxRate: 18, IgvRegime: "standard"}
}

// LoadFromDB carga la configuración tributaria activa desde la base de datos del tenant.
// Si no hay registro, retorna DefaultConfig().
func LoadFromDB(db *gorm.DB) Config {
	var cfg database.TenantCompanyConfig
	if err := db.Select("tax_rate, igv_regime, tax_benefit_zone").First(&cfg).Error; err != nil {
		return DefaultConfig()
	}
	taxRate := cfg.TaxRate
	if taxRate <= 0 {
		taxRate = 18
	}
	igvRegime := cfg.IgvRegime
	if igvRegime == "" {
		igvRegime = "standard"
	}
	return Config{
		TaxRate:        taxRate,
		IgvRegime:      igvRegime,
		TaxBenefitZone: cfg.TaxBenefitZone,
	}
}

// EffectiveRate retorna la tasa de IGV efectiva para un item dado su tipo de afectación.
//
// Reglas SUNAT Catálogo N°07:
//   - "10" Gravado Operación Onerosa         → usa la tasa configurada de empresa
//   - "11"-"17" Gravado (retiros, bonif.)    → tasa de empresa
//   - "20" Exonerado                         → 0%
//   - "30" Inafecto                          → 0%
//   - "40" Exportación                       → 0%
//   - ""  (no especificado)                  → usa tasa de empresa (comportamiento legacy)
func (c Config) EffectiveRate(igvAffectationType string) float64 {
	switch igvAffectationType {
	case "20", "30", "40": // Exonerado, Inafecto, Exportación
		return 0
	default: // "10" y cualquier otro gravado
		if c.TaxBenefitZone {
			// En zona de beneficio tributario los gravados pueden quedar en 0
			// si así fue configurado por el régimen "exonerated"
			if c.IgvRegime == "exonerated" {
				return 0
			}
		}
		return c.TaxRate
	}
}

// IsGravado retorna true si el tipo de afectación genera IGV.
func IsGravado(igvAffectationType string) bool {
	switch igvAffectationType {
	case "20", "30", "40":
		return false
	default:
		return true
	}
}

// IsBonificacionGravada — Catálogo N°07 código 15: bonificación gravada en la misma factura.
// El cliente no paga el ítem; la base/IGV se informan a SUNAT con valor referencial.
func IsBonificacionGravada(igvAffectationType string) bool {
	return strings.TrimSpace(igvAffectationType) == "15"
}

// IsGravadoOperacionNoOnerosa identifica gravados en operaciones no onerosas (cat. N°07: 11–16).
// Equivale al bloque calculateTotal() del sistema legacy para retiros/bonificaciones gravadas:
// el IGV es referencial (TaxSubtotal) y el impuesto cobrable de línea (total_taxes) queda en 0.
func IsGravadoOperacionNoOnerosa(igvAffectationType string) bool {
	switch strings.TrimSpace(igvAffectationType) {
	case "11", "12", "13", "14", "15", "16":
		return true
	default:
		return false
	}
}

// LineChargeableTotalImpuestos es el impuesto cobrable de línea → Greenter detail.TotalImpuestos
// (legacy document_items.total_taxes → InvoiceLine/TaxTotal/cbc:TaxAmount).
//
// Legacy calculateTotal para 11–16:
//
//	total_taxes = total_value - total_value_partial + ICBPER
//
// con total_value_partial = (total_value/qty)*qty → cobrable 0 (+ bolsa plástica si aplica).
// total_igv referencial no se incluye en total_taxes.
func LineChargeableTotalImpuestos(igvAffectationType string, referentialIGV, plasticBagTax float64) float64 {
	if IsGravadoOperacionNoOnerosa(igvAffectationType) {
		return money.RoundSunat(plasticBagTax)
	}
	return money.RoundSunat(referentialIGV + plasticBagTax)
}

// SunatIgvTypeLabel retorna la descripción del tipo de afectación SUNAT.
func SunatIgvTypeLabel(code string) string {
	labels := map[string]string{
		"10": "Gravado – Op. Onerosa",
		"11": "Gravado – Retiro por premio",
		"12": "Gravado – Retiro por donación",
		"13": "Gravado – Retiro",
		"14": "Gravado – Retiro por publicidad",
		"15": "Gravado – Bonificaciones",
		"16": "Gravado – Retiro por trabajadores",
		"17": "Gravado – IVAP",
		"20": "Exonerado – Op. Onerosa",
		"21": "Exonerado – Transferencia gratuita",
		"30": "Inafecto – Op. Onerosa",
		"31": "Inafecto – Retiro por bonificación",
		"32": "Inafecto – Retiro",
		"33": "Inafecto – Retiro por muestras médicas",
		"34": "Inafecto – Retiro a trabajadores",
		"35": "Inafecto – FISE",
		"36": "Inafecto – Subsidio al transportista",
		"40": "Exportación de bienes o servicios",
	}
	if l, ok := labels[code]; ok {
		return l
	}
	return "Gravado – Op. Onerosa" // default
}

// CalcItem calcula subtotal, impuesto y total para un ítem de venta/compra.
// price es el precio que se ingresó; si priceIncludesIgv es true, lo descompone.
func CalcItem(price, quantity, discount float64, igvAffectationType string, priceIncludesIgv bool, taxCfg Config) (subtotal, taxAmount, total float64) {
	rate := taxCfg.EffectiveRate(igvAffectationType)
	gross := quantity*price - discount

	if rate == 0 {
		subtotal = gross
		taxAmount = 0
		total = gross
		return
	}

	if priceIncludesIgv {
		// Precio ya lleva IGV: descomponer
		subtotal = gross / (1 + rate/100)
		taxAmount = subtotal * (rate / 100)
	} else {
		// Precio sin IGV: agregar
		subtotal = gross
		taxAmount = gross * (rate / 100)
	}
	total = subtotal + taxAmount
	subtotal = money.RoundSunat(subtotal)
	taxAmount = money.RoundSunat(taxAmount)
	total = money.RoundSunat(total)
	return
}

// CalcItemPayableTotal importe cobrable al cliente (bonificación gravada 15 → 0).
func CalcItemPayableTotal(price, quantity, discount float64, igvAffectationType string, priceIncludesIgv bool, taxCfg Config) float64 {
	if IsBonificacionGravada(igvAffectationType) {
		return 0
	}
	_, _, total := CalcItem(price, quantity, discount, igvAffectationType, priceIncludesIgv, taxCfg)
	return total
}
