package tenantrubro

import "strings"

const (
	General      = "general"
	Gastronomico = "gastronomico"
)

// Normalize devuelve un rubro válido o General por defecto.
func Normalize(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case Gastronomico:
		return Gastronomico
	default:
		return General
	}
}

func IsGastronomico(rubro string) bool {
	return Normalize(rubro) == Gastronomico
}

func Label(rubro string) string {
	if IsGastronomico(rubro) {
		return "Gastronómico"
	}
	return "General"
}
