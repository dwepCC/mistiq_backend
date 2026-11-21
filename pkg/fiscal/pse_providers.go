package fiscal

import "strings"

// ResolvePSEBaseURL URL CPE por proveedor (no se pide en el panel central).
func ResolvePSEBaseURL(provider string) string {
	p := strings.ToLower(strings.TrimSpace(provider))
	if p == "" || p == "pse" {
		p = "validapse"
	}
	switch p {
	case "validapse":
		return "https://app.validapse.com"
	case "pseapp":
		return "https://pse.tukifac.com"
	default:
		return ""
	}
}

// NormalizePSEProvider default validapse cuando el tenant usa PSE sin proveedor explícito.
func NormalizePSEProvider(provider string) string {
	p := strings.ToLower(strings.TrimSpace(provider))
	if p == "" || p == "pse" {
		return "validapse"
	}
	return p
}
