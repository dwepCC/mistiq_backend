package salecontext

import (
	"fmt"
	"strings"

	"tukifac/pkg/money"
	"tukifac/pkg/salecurrency"
)

// AutoSuggestIGVRetention indica si las reglas SUNAT sugieren activar retención (sin umbral S/ 700; eso se valida al guardar).
func AutoSuggestIGVRetention(sunatDocCode string, contact *ContactSnapshot, _saleTotal float64, _currency string, _exchangeRate *float64) bool {
	if sunatDocCode != "01" {
		return false
	}
	if contact == nil {
		return false
	}
	if contact.EsAgenteDePercepcion {
		return false
	}
	docType := normalizeDocType(contact.DocType, contact.DocNumber)
	if docType != "6" {
		return false
	}
	return contact.EsAgenteDeRetencion
}

// EvaluateIGVRetention calcula retención IGV y neto a cobrar (vendedor).
func EvaluateIGVRetention(in RetentionEvalInput) RetentionEvalResult {
	currency := strings.TrimSpace(in.Currency)
	if currency == "" {
		currency = "PEN"
	}
	total := money.RoundSunat(in.SaleTotal)
	totalPEN := salecurrency.TotalInPEN(currency, total, in.ExchangeRate)
	auto := AutoSuggestIGVRetention(in.SunatDocCode, in.Contact, total, currency, in.ExchangeRate)

	hasRetention := in.RequestedRetention
	manualOverride := in.ManualOverride
	if !manualOverride {
		hasRetention = auto
	}

	res := RetentionEvalResult{
		HasIgvRetention:            hasRetention,
		IgvRetentionManualOverride: manualOverride,
		AutoSuggested:              auto,
		RatePercent:                IGVRetentionRate * 100,
		BaseAmount:                 total,
		NetCollectible:             total,
		Source:                     SourceAutoRule,
	}
	if manualOverride {
		res.Source = SourceManualOverride
	}

	if !hasRetention {
		res.Applicable = false
		res.Reason = "Retención IGV desactivada"
		return res
	}

	if in.SunatDocCode != "01" {
		res.Applicable = false
		res.Reason = "La retención IGV aplica principalmente a facturas (01)"
		return res
	}
	if totalPEN <= IGVRetentionThreshold {
		res.Applicable = false
		if strings.EqualFold(currency, salecurrency.CurrencyUSD) && (in.ExchangeRate == nil || *in.ExchangeRate <= 0) {
			res.Reason = fmt.Sprintf("El importe en USD (US$ %.2f) requiere tipo de cambio para evaluar el umbral de S/ %.2f", total, IGVRetentionThreshold)
		} else {
			res.Reason = fmt.Sprintf("El importe total equivalente (S/ %.2f) no supera S/ %.2f", totalPEN, IGVRetentionThreshold)
		}
		return res
	}
	if in.Contact == nil {
		res.Applicable = false
		res.Reason = "Se requiere un cliente para evaluar retención"
		return res
	}
	if in.Contact.EsAgenteDePercepcion {
		res.Applicable = false
		res.Reason = "Operación excluida: cliente agente de percepción"
		return res
	}
	docType := normalizeDocType(in.Contact.DocType, in.Contact.DocNumber)
	if docType != "6" {
		res.Applicable = false
		res.Reason = "La retención IGV requiere cliente con RUC"
		return res
	}
	if !in.Contact.EsAgenteDeRetencion && !manualOverride {
		res.Applicable = false
		res.Reason = "El cliente no está registrado como agente de retención"
		return res
	}

	amount := money.RoundSunat(total * IGVRetentionRate)
	res.Applicable = true
	res.Reason = "Retención IGV del 3% sobre el importe total de la operación"
	res.ObligationAmount = amount
	res.NetCollectible = money.RoundSunat(total - amount)
	return res
}

func normalizeDocType(docType, docNumber string) string {
	dt := strings.TrimSpace(docType)
	switch strings.ToUpper(dt) {
	case "RUC", "6":
		return "6"
	case "DNI", "1":
		return "1"
	}
	num := strings.TrimSpace(docNumber)
	if len(num) == 11 {
		return "6"
	}
	if len(num) == 8 {
		return "1"
	}
	return dt
}

// ResolveRequestedRetention combina input explícito con sugerencia automática.
func ResolveRequestedRetention(input *FiscalContextInput, sunatDocCode string, contact *ContactSnapshot, saleTotal float64, currency string, exchangeRate *float64) (requested bool, manualOverride bool) {
	if input == nil {
		return AutoSuggestIGVRetention(sunatDocCode, contact, saleTotal, currency, exchangeRate), false
	}
	manualOverride = input.IgvRetentionManualOverride
	if input.HasIgvRetention != nil {
		return *input.HasIgvRetention, manualOverride
	}
	return AutoSuggestIGVRetention(sunatDocCode, contact, saleTotal, currency, exchangeRate), false
}
