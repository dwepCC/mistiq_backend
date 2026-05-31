package domains

import "strings"

// NormalizeHost quita esquema, puerto y path; devuelve host en minúsculas.
func NormalizeHost(raw string) string {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "https://")
	raw = strings.TrimPrefix(raw, "http://")
	if i := strings.IndexByte(raw, '/'); i >= 0 {
		raw = raw[:i]
	}
	if i := strings.IndexByte(raw, ':'); i >= 0 {
		raw = raw[:i]
	}
	return strings.ToLower(strings.TrimSpace(raw))
}

// NormalizeRootDomain es el dominio raíz de tenants (ej. bendey.cloud).
func NormalizeRootDomain(raw string) string {
	return NormalizeHost(raw)
}

// DefaultReservedSubdomains no se interpretan como slug de tenant (api, app, www…).
func DefaultReservedSubdomains() []string {
	return []string{"api", "app", "www", "admin", "central"}
}

// MergeReserved combina lista por defecto con la configurada en .env.
func MergeReserved(configured []string) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0, len(DefaultReservedSubdomains())+len(configured))
	add := func(s string) {
		s = strings.ToLower(strings.TrimSpace(s))
		if s == "" {
			return
		}
		if _, ok := seen[s]; ok {
			return
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	for _, s := range DefaultReservedSubdomains() {
		add(s)
	}
	for _, s := range configured {
		add(s)
	}
	return out
}

// IsReserved indica si el slug extraído del host no debe resolverse como tenant.
func IsReserved(slug string, reserved []string) bool {
	slug = strings.ToLower(strings.TrimSpace(slug))
	if slug == "" {
		return true
	}
	for _, r := range reserved {
		if slug == strings.ToLower(strings.TrimSpace(r)) {
			return true
		}
	}
	return false
}

// OriginFromHost construye https://host para CORS/API pública.
func OriginFromHost(host string) string {
	host = NormalizeHost(host)
	if host == "" {
		return ""
	}
	return "https://" + host
}

// TenantHost devuelve el host del tenant: {slug}.{root} → empresa1.bendey.cloud
func TenantHost(slug, rootDomain string) string {
	slug = strings.ToLower(strings.TrimSpace(slug))
	root := NormalizeRootDomain(rootDomain)
	if slug == "" || root == "" || root == "localhost" {
		return ""
	}
	return slug + "." + root
}

// TenantURL URL HTTPS del panel tenant (mismo SPA que FRONTEND_URL, otro subdominio).
func TenantURL(slug, rootDomain string) string {
	host := TenantHost(slug, rootDomain)
	if host == "" {
		return ""
	}
	return "https://" + host
}

// ResolveTenantAPIURL URL para clientes externos (Tukichef). En local (APP_DOMAIN=localhost)
// TenantURL queda vacío; usar fallbackOrigin (p. ej. http://localhost:3000 del monolito).
func ResolveTenantAPIURL(slug, rootDomain, fallbackOrigin string) string {
	if u := TenantURL(slug, rootDomain); u != "" {
		return u
	}
	fb := strings.TrimSpace(fallbackOrigin)
	if fb == "" {
		return ""
	}
	return strings.TrimRight(fb, "/")
}
