package paymentcondition

import "strings"

const (
	CodeCash   = "cash"
	CodeCredit = "credit"
	NameCash   = "Contado"
	NameCredit = "Crédito"
)

// IsCreditCode indica condición comercial a crédito (CxC).
func IsCreditCode(code string) bool {
	c := strings.TrimSpace(strings.ToLower(code))
	return c == CodeCredit || c == "credito"
}

// IsCashCode indica condición contado.
func IsCashCode(code string) bool {
	return strings.TrimSpace(strings.ToLower(code)) == CodeCash
}
