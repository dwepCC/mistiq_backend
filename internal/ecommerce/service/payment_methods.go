package service

import (
	"encoding/json"
	"fmt"
	"strings"
)

// PaymentMethod medio de pago que la tienda muestra al cliente al armar el pedido. Es solo
// informativo (el cliente paga por fuera y manda la captura por WhatsApp): el sistema no cobra
// ni valida pagos.
type PaymentMethod struct {
	Type    string `json:"type"` // yape | plin | bank | cash | other
	Label   string `json:"label"`
	Enabled bool   `json:"enabled"`
	Holder  string `json:"holder"`  // titular
	Account string `json:"account"` // celular (yape/plin) o número de cuenta
	CCI     string `json:"cci"`
	Bank    string `json:"bank"`
	Note    string `json:"note"`
}

const maxPaymentMethods = 10

var validPaymentTypes = map[string]bool{"yape": true, "plin": true, "bank": true, "cash": true, "other": true}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// NormalizePaymentMethods valida y limpia lo que envía el panel antes de guardarlo.
func NormalizePaymentMethods(in []PaymentMethod) ([]PaymentMethod, error) {
	if len(in) > maxPaymentMethods {
		return nil, fmt.Errorf("máximo %d medios de pago", maxPaymentMethods)
	}
	out := make([]PaymentMethod, 0, len(in))
	for _, m := range in {
		m.Type = strings.ToLower(strings.TrimSpace(m.Type))
		if !validPaymentTypes[m.Type] {
			return nil, fmt.Errorf("tipo de medio de pago inválido: %q", m.Type)
		}
		m.Label = clip(m.Label, 60)
		m.Holder = clip(m.Holder, 120)
		m.Account = clip(m.Account, 60)
		m.CCI = clip(m.CCI, 40)
		m.Bank = clip(m.Bank, 60)
		m.Note = clip(m.Note, 200)
		out = append(out, m)
	}
	return out, nil
}

// ParsePaymentMethods lee el JSON guardado; vacío o corrupto = sin medios de pago.
func ParsePaymentMethods(raw string) []PaymentMethod {
	out := []PaymentMethod{}
	if strings.TrimSpace(raw) == "" {
		return out
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return []PaymentMethod{}
	}
	return out
}

// EnabledPaymentMethods los que la tienda pública debe ofrecer.
func EnabledPaymentMethods(raw string) []PaymentMethod {
	out := []PaymentMethod{}
	for _, m := range ParsePaymentMethods(raw) {
		if m.Enabled {
			out = append(out, m)
		}
	}
	return out
}
