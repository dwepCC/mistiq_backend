package service

import (
	"encoding/json"
	"errors"
	"math"
	"strings"

	salessvc "tukifac/internal/sales/service"
	"tukifac/pkg/money"
)

// QuotationPaymentRef es un método de pago de REFERENCIA de la cotización: cómo piensa pagar el
// cliente. No es un cobro: no toca caja, cuentas ni saldos, y la cotización no se convierte en
// pago. Solo se guarda y se muestra en el documento (y precarga la conversión a venta).
type QuotationPaymentRef struct {
	Method    string  `json:"method"`
	Amount    float64 `json:"amount"`
	Reference string  `json:"reference,omitempty"`
}

const (
	maxQuotationPaymentRefs   = 6
	maxQuotationPayMethodLen  = 30
	maxQuotationPayReferenceN = 60
)

// normalizePaymentRefs valida y serializa la lista (JSON para tenant_quotations.payment_methods_json).
// Devuelve "" cuando no hay ninguna. Descarta filas sin método; el monto es opcional (0 = sin monto).
func normalizePaymentRefs(in []QuotationPaymentRef) (string, error) {
	out := make([]QuotationPaymentRef, 0, len(in))
	for _, p := range in {
		method := strings.TrimSpace(p.Method)
		if method == "" {
			continue
		}
		if len(method) > maxQuotationPayMethodLen {
			return "", errors.New("método de pago inválido")
		}
		if math.IsNaN(p.Amount) || math.IsInf(p.Amount, 0) || p.Amount < 0 {
			return "", errors.New("el monto del método de pago no es válido")
		}
		ref := strings.TrimSpace(p.Reference)
		if r := []rune(ref); len(r) > maxQuotationPayReferenceN {
			ref = string(r[:maxQuotationPayReferenceN])
		}
		out = append(out, QuotationPaymentRef{Method: method, Amount: money.RoundSunat(p.Amount), Reference: ref})
	}
	if len(out) == 0 {
		return "", nil
	}
	if len(out) > maxQuotationPaymentRefs {
		return "", errors.New("máximo 6 métodos de pago de referencia")
	}
	b, err := json.Marshal(out)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// paymentRefsToPrint convierte el JSON guardado en las filas de referencia del documento impreso.
func paymentRefsToPrint(raw string) []salessvc.PrintPayment {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var refs []QuotationPaymentRef
	if err := json.Unmarshal([]byte(raw), &refs); err != nil {
		return nil
	}
	out := make([]salessvc.PrintPayment, 0, len(refs))
	for _, r := range refs {
		out = append(out, salessvc.PrintPayment{Method: r.Method, Amount: r.Amount, Reference: r.Reference})
	}
	return out
}
