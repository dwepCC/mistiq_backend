package middleware

import (
	"strings"

	"tukifac/config"
	"tukifac/pkg/utils"
)

// Versión mínima del claim tenant_version en JWT (tokens legacy sin versión se rechazan en prod).
const MinTenantJWTVersion uint = 1

// resolveTenantSlug aplica política host/header según entorno.
//
// Caso A — Web producción: {slug}.tukifac.com → solo subdominio (header ignorado salvo validación).
// Caso B — App/Tauri/Capacitor: request a {slug}.tukifac.com + X-Tenant-Slug redundante → host manda.
// Caso C — Dev localhost: X-Tenant-Slug o subdominio .localhost.
//
// Hosts centrales (api, app): sin tenant salvo rutas de bootstrap (login legacy con header).
func resolveTenantSlug(host, headerSlug, cookieSlug, querySlug, path string, cfg *config.Config) (slug string, blockReason string) {
	headerSlug = strings.TrimSpace(headerSlug)
	querySlug = strings.TrimSpace(querySlug)
	subdomainSlug := utils.ExtractSubdomain(host, cfg.AppDomain)

	if isLocalDevHost(host) {
		if headerSlug != "" {
			return headerSlug, ""
		}
		if querySlug != "" && !cfg.IsReservedSubdomain(querySlug) {
			return querySlug, ""
		}
		if subdomainSlug != "" && !cfg.IsReservedSubdomain(subdomainSlug) {
			return subdomainSlug, ""
		}
		if cookieSlug != "" && cfg.IsDev() {
			return strings.TrimSpace(cookieSlug), ""
		}
		return "", ""
	}

	// Producción / staging
	if subdomainSlug != "" && !cfg.IsReservedSubdomain(subdomainSlug) {
		if headerSlug != "" && headerSlug != subdomainSlug {
			return "", "header_subdomain_mismatch"
		}
		return subdomainSlug, ""
	}

	// Host central (api.tukifac.com, app.tukifac.com) — la app NO debería operar aquí salvo bootstrap.
	if headerSlug != "" && allowHeaderFallbackOnCentralHost(path) {
		return headerSlug, "central_host_header_fallback"
	}

	return "", ""
}

func isLocalDevHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if idx := strings.LastIndex(host, ":"); idx != -1 {
		host = host[:idx]
	}
	if host == "localhost" || host == "127.0.0.1" {
		return true
	}
	return strings.HasSuffix(host, ".localhost")
}

func allowHeaderFallbackOnCentralHost(path string) bool {
	switch path {
	case "/api/login":
		return true
	default:
		return strings.HasPrefix(path, "/api/restaurant/auth/")
	}
}
