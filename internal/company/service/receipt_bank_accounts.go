package service

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
)

// EncodeReceiptBankAccountIDs serializa IDs para tenant_company_configs.receipt_bank_account_ids.
func EncodeReceiptBankAccountIDs(ids []uint) string {
	if len(ids) == 0 {
		return "[]"
	}
	b, err := json.Marshal(ids)
	if err != nil {
		return "[]"
	}
	return string(b)
}

// ParseReceiptBankAccountIDsJSON acepta [1,2], ["1","2"] o null (sin filtro → todas).
func ParseReceiptBankAccountIDsJSON(raw json.RawMessage) ([]uint, error) {
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" {
		// No enviado: guardar lista vacía explícita no; el caller siempre envía array desde el panel.
		// Tratar como ninguna cuenta seleccionada solo si es "[]".
		return []uint{}, nil
	}
	var asUint []uint
	if err := json.Unmarshal(raw, &asUint); err == nil {
		return asUint, nil
	}
	var asFloat []float64
	if err := json.Unmarshal(raw, &asFloat); err == nil {
		out := make([]uint, 0, len(asFloat))
		for _, f := range asFloat {
			if f > 0 {
				out = append(out, uint(f))
			}
		}
		return out, nil
	}
	var asStr []string
	if err := json.Unmarshal(raw, &asStr); err == nil {
		out := make([]uint, 0, len(asStr))
		for _, part := range asStr {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			n, err := strconv.ParseUint(part, 10, 64)
			if err != nil || n == 0 {
				return nil, errors.New("receipt_bank_account_ids inválido")
			}
			out = append(out, uint(n))
		}
		return out, nil
	}
	return nil, errors.New("receipt_bank_account_ids debe ser un arreglo de IDs")
}

// DecodeReceiptBankAccountIDs interpreta la configuración guardada.
// configured=false: sin filtro (mostrar todas las cuentas activas, compatibilidad).
// configured=true: usar ids (puede estar vacío = no mostrar ninguna).
func DecodeReceiptBankAccountIDs(raw string) (ids []uint, configured bool) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return nil, false
	}
	var out []uint
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return nil, false
	}
	return out, true
}

func bankAccountIDAllowed(id uint, selected []uint, configured bool) bool {
	if !configured {
		return true
	}
	for _, sid := range selected {
		if sid == id {
			return true
		}
	}
	return false
}
