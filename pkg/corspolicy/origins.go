package corspolicy

import (
	"net/url"
	"strings"

	"tukifac/config"
)

// Matcher decide si un Origin del navegador puede recibir Access-Control-Allow-Origin.
type Matcher struct {
	exact      map[string]struct{}
	baseHosts  []string
	allowHTTP  bool
}

// NewMatcher construye reglas desde variables de entorno (FRONTEND_URL, APP_DOMAIN, etc.).
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

	for _, raw := range cfg.CORSExtraOrigins {
		addExact(raw)
	}

	// Origen del API (p. ej. https://api.tukifac.com) derivado del frontend app.*
	if apiOrigin := deriveAPIOrigin(cfg.FrontendURL); apiOrigin != "" {
		addExact(apiOrigin)
	}
	if apiOrigin := deriveAPIOrigin(cfg.CentralFrontendURL); apiOrigin != "" {
		addExact(apiOrigin)
	}

	addHost := func(host string) {
		host = normalizeHost(host)
		if host == "" || host == "localhost" {
			return
		}
		for _, h := range m.baseHosts {
			if h == host {
				return
			}
		}
		m.baseHosts = append(m.baseHosts, host)
	}

	addHost(hostFromURL(cfg.FrontendURL))
	addHost(hostFromURL(cfg.CentralFrontendURL))
	addHost(normalizeHost(cfg.AppDomain))

	// Localhost en desarrollo
	if cfg.IsDev() {
		for _, o := range devLocalhostOrigins() {
			addExact(o)
		}
	}

	return m
}

// BaseHosts devuelve hosts usados para subdominios (*.app.ejemplo.com).
func (m *Matcher) BaseHosts() []string {
	return append([]string(nil), m.baseHosts...)
}

// ExactCount cantidad de orígenes exactos configurados (para logs de arranque).
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
		"tauri://localhost",
		"http://tauri.localhost",
		"https://tauri.localhost",
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

func normalizeHost(h string) string {
	h = strings.TrimSpace(h)
	h = strings.TrimPrefix(h, "https://")
	h = strings.TrimPrefix(h, "http://")
	if i := strings.IndexByte(h, '/'); i >= 0 {
		h = h[:i]
	}
	if i := strings.IndexByte(h, ':'); i >= 0 {
		h = h[:i]
	}
	return strings.ToLower(strings.TrimSpace(h))
}

func hostFromURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return normalizeHost(u.Hostname())
}

// deriveAPIOrigin: app.tukifac.com → https://api.tukifac.com
func deriveAPIOrigin(frontendURL string) string {
	host := hostFromURL(frontendURL)
	if host == "" || !strings.HasPrefix(host, "app.") {
		return ""
	}
	return "https://api." + strings.TrimPrefix(host, "app.")
}
