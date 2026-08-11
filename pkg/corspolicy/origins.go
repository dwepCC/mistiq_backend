package corspolicy

import (
	"net/url"
	"strings"

	"tukifac/config"
	"tukifac/pkg/domains"
)

// Matcher decide si un Origin del navegador puede recibir Access-Control-Allow-Origin.
type Matcher struct {
	exact     map[string]struct{}
	baseHosts []string // dominio raíz → permite https://tenant1.tukifac.com
	allowHTTP bool
}

// NewMatcher construye reglas desde .env (sin dominios hardcodeados).
func NewMatcher(cfg *config.Config) *Matcher {
	m := &Matcher{
		exact:     make(map[string]struct{}),
		allowHTTP: cfg.IsDev(),
	}

	addExact := func(raw string) {
		if o := normalizeOrigin(raw); o != "" {
			m.exact[o] = struct{}{}
		}
	}

	addExact(cfg.FrontendURL)
	addExact(cfg.CentralFrontendURL)
	addExact(cfg.APIPublicURL)

	for _, raw := range cfg.CORSExtraOrigins {
		addExact(raw)
	}

	// Solo el dominio raíz habilita tenants por subdominio (*.tukifac.com).
	if root := domains.NormalizeRootDomain(cfg.AppDomain); root != "" && root != "localhost" {
		m.baseHosts = append(m.baseHosts, root)
	}

	if cfg.IsDev() {
		for _, o := range devLocalhostOrigins() {
			addExact(o)
		}
	}

	// Tauri / Capacitor: orígenes del WebView empaquetado (no son sitios web arbitrarios).
	for _, o := range nativeShellOrigins() {
		addExact(o)
	}

	return m
}

// BaseHosts dominio raíz para CORS de subdominios tenant.
func (m *Matcher) BaseHosts() []string {
	return append([]string(nil), m.baseHosts...)
}

// ExactCount orígenes exactos (app, api, localhost…).
func (m *Matcher) ExactCount() int {
	return len(m.exact)
}

func devLocalhostOrigins() []string {
	return []string{
		"http://localhost:3000",
		"http://localhost:5173",
		"http://localhost:5174",
		"http://localhost:5175",
		"http://localhost:4173",
		"http://localhost:4174",
		"http://127.0.0.1:3000",
		"http://127.0.0.1:5173",
	}
}

// nativeShellOrigins — WebView de Tauri/Capacitor en builds de producción.
func nativeShellOrigins() []string {
	return []string{
		"tauri://localhost",
		"http://tauri.localhost",
		"https://tauri.localhost",
		"https://localhost",
		"http://localhost",
		"capacitor://localhost",
		"ionic://localhost",
	}
}

// Allow devuelve true si el Origin debe recibir Access-Control-Allow-Origin.
func (m *Matcher) Allow(origin string) bool {
	origin = normalizeOrigin(origin)
	if origin == "" || origin == "null" {
		return false
	}

	if _, ok := m.exact[origin]; ok {
		return true
	}

	u, err := url.Parse(origin)
	if err != nil || u.Hostname() == "" {
		return false
	}

	scheme := strings.ToLower(u.Scheme)
	if scheme != "https" && scheme != "http" {
		return false
	}
	if scheme == "http" && !m.allowHTTP {
		return false
	}

	host := strings.ToLower(u.Hostname())

	// Desarrollo: Vite en localhost y subdominios tenant (angel.localhost:5173).
	if m.allowHTTP && (host == "localhost" || strings.HasSuffix(host, ".localhost")) {
		return true
	}

	for _, base := range m.baseHosts {
		if host == base || strings.HasSuffix(host, "."+base) {
			return true
		}
	}
	return false
}

func normalizeOrigin(o string) string {
	o = strings.TrimSpace(o)
	o = strings.TrimRight(o, "/")
	return o
}
