package service

import (
	"encoding/json"
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
