package salecontext

import (
	"fmt"
	"strings"

	"tukifac/pkg/facturador"
	"tukifac/pkg/money"
)

const (
	igvRetentionPDFExtraAmount = "Retención IGV (3%)"
	igvRetentionPDFExtraNet     = "Neto a cobrar"
)

func igvRetentionObservacion(amount, net float64, currency string) string {
	return fmt.Sprintf("%s: %s. %s: %s.",
		igvRetentionPDFExtraAmount,
		formatRetentionMoney(amount, currency),
		igvRetentionPDFExtraNet,
		formatRetentionMoney(net, currency),
	)
}

func formatRetentionMoney(amount float64, currency string) string {
	cur := strings.TrimSpace(currency)
	if cur == "" {
		cur = "PEN"
	}
	prefix := "S/ "
	if cur == "USD" {
		prefix = "US$ "
	}
	return fmt.Sprintf("%s%.2f", prefix, money.RoundSunat(amount))
}

func appendIGVRetentionObservacion(payload *facturador.InvoicePayload, note string) {
	if payload == nil || strings.TrimSpace(note) == "" {
		return
	}
	if obs := strings.TrimSpace(payload.Observacion); obs != "" {
		payload.Observacion = obs + " | " + note
		return
	}
	payload.Observacion = note
}

// BuildIGVRetentionPDFExtras filas para parameters.user.extras en POST /invoice/pdf (solo representación impresa).
func BuildIGVRetentionPDFExtras(amount, net float64, currency string) []facturador.InvoicePDFExtra {
	if amount <= 0 {
		return nil
	}
	return []facturador.InvoicePDFExtra{
		{Name: igvRetentionPDFExtraAmount, Value: formatRetentionMoney(amount, currency)},
		{Name: igvRetentionPDFExtraNet, Value: formatRetentionMoney(net, currency)},
	}
}

func buildIGVRetentionPDFParameters(amount, net float64, currency string) *facturador.InvoicePDFParameters {
	extras := BuildIGVRetentionPDFExtras(amount, net, currency)
	if len(extras) == 0 {
		return nil
	}
	return &facturador.InvoicePDFParameters{
		User: facturador.InvoicePDFUserParameters{Extras: extras},
	}
}

// applyIGVRetentionToInvoicePayload informa retención IGV en XML (observacion) y PDF Lycet (parameters.user.extras).
func applyIGVRetentionToInvoicePayload(payload *facturador.InvoicePayload, amount, net float64, currency string) {
	if payload == nil || amount <= 0 {
		return
	}
	appendIGVRetentionObservacion(payload, igvRetentionObservacion(amount, net, currency))
	payload.Parameters = buildIGVRetentionPDFParameters(amount, net, currency)
}
