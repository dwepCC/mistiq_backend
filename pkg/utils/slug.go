package utils

import (
	"errors"
	"regexp"
	"strings"
	"unicode"

	"golang.org/x/text/transform"
	"golang.org/x/text/unicode/norm"
)

var nonAlphanumeric = regexp.MustCompile(`[^a-z0-9]+`)

// NormalizeSubdomain deja solo letras minúsculas y números (sin guiones).
// Útil para que el usuario elija un subdominio corto: empresadeplasticos → empresadeplasticos.tukifac.com
// Devuelve error si queda vacío o si tiene longitud inválida (2-63 caracteres).
func NormalizeSubdomain(s string) (string, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	// Quitar todo lo que no sea a-z0-9
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	out := b.String()
	if len(out) < 2 {
		return "", errors.New("el subdominio debe tener al menos 2 caracteres")
	}
	if len(out) > 63 {
		return "", errors.New("el subdominio no puede superar 63 caracteres")
	}
	return out, nil
}

// Slugify convierte un string arbitrario en un slug URL-friendly.
func Slugify(s string) string {
	// Normalizar y eliminar acentos
	t := transform.Chain(norm.NFD, transform.RemoveFunc(func(r rune) bool {
		return unicode.Is(unicode.Mn, r)
	}), norm.NFC)
	result, _, _ := transform.String(t, s)

	result = strings.ToLower(result)
	result = nonAlphanumeric.ReplaceAllString(result, "-")
	result = strings.Trim(result, "-")
	return result
}

// ExtractSubdomain extrae el subdominio de un host dado el dominio raíz (APP_DOMAIN / ROOT_DOMAIN).
// Ej: host="empresa1.tukifac.com", domain="tukifac.com" → "empresa1"
// Ej: host="api.tukifac.com", domain="tukifac.com" → "api" (luego IsReservedSubdomain lo ignora)
// Ej: host="empresa1.localhost", domain="localhost" → "empresa1"
func ExtractSubdomain(host, appDomain string) string {
	// Eliminar puerto
	if idx := strings.LastIndex(host, ":"); idx != -1 {
		host = host[:idx]
	}

	// Eliminar el dominio base
	suffix := "." + strings.TrimPrefix(appDomain, ".")
	if strings.HasSuffix(host, suffix) {
		sub := strings.TrimSuffix(host, suffix)
		if strings.Contains(sub, ".") {
			// subdomain más profundo, tomar solo el primer segmento
			parts := strings.SplitN(sub, ".", 2)
			return parts[0]
		}
		return sub
	}

	// Para desarrollo local con subdominios en localhost
	if strings.HasSuffix(host, ".localhost") {
		sub := strings.TrimSuffix(host, ".localhost")
		return sub
	}

	return ""
}
