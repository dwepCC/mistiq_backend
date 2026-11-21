// Package rum recibe telemetría mínima de rendimiento medida en el navegador del usuario (Real User Monitoring):
// tiempos de conexión, de carga de vistas y de llamadas API. Sirve para saber, con clientes reales, qué parte de
// la lentitud es de red (p. ej. IPv6 con pérdidas hacia Cloudflare) y qué parte es nuestra.
//
// Privacidad: NO se guarda la IP, ni el user-agent, ni ningún identificador de usuario/dispositivo, ni query
// strings ni IDs de ruta. Solo cifras de tiempo, la familia de IP (v4/v6), el datacenter de Cloudflare, el país
// que ya calcula Cloudflare y el slug del tenant (identificador de empresa, no de persona).
package rum

import (
	"encoding/json"
	"log/slog"
	"regexp"
	"strings"

	"tukifac/pkg/logger"
	"tukifac/pkg/middleware"

	"github.com/gofiber/fiber/v3"
)

const (
	maxBodyBytes = 16 * 1024
	maxRoutes    = 40
	maxAPIKeys   = 40
	maxMs        = 10 * 60 * 1000
)

var (
	routeRe = regexp.MustCompile(`^/[a-zA-Z0-9_:\-/]{0,80}$`)
	protoRe = regexp.MustCompile(`^(h2|h3|http/1\.1|http/1\.0|)$`)
	netRe   = regexp.MustCompile(`^(slow-2g|2g|3g|4g|)$`)
	coloRe  = regexp.MustCompile(`^[A-Z]{3,4}$`)
	ctryRe  = regexp.MustCompile(`^[A-Z]{2}$`)
)

// Payload es lo que envía el navegador. Todo es opcional; lo inválido se descarta, nunca se rechaza la petición.
type Payload struct {
	Nav      *NavTiming `json:"nav,omitempty"`
	Views    []ViewTime `json:"views,omitempty"`
	API      []APIStat  `json:"api,omitempty"`
	Protocol string     `json:"protocol,omitempty"` // h2 | h3 | http/1.1 (nextHopProtocol)
	NetType  string     `json:"net_type,omitempty"` // effectiveType de Network Information API
	Mobile   bool       `json:"mobile,omitempty"`
	App      string     `json:"app,omitempty"` // "tenant" | "restaurant" (opcional)
}

// NavTiming tiempos de la carga de documento (ms).
type NavTiming struct {
	DNS     float64 `json:"dns"`
	Connect float64 `json:"connect"` // TCP (+TLS si TLS no se informa aparte)
	TLS     float64 `json:"tls"`
	TTFB    float64 `json:"ttfb"`
	Load    float64 `json:"load"`
}

// ViewTime tiempo hasta que una vista terminó de cargar sus datos (ms).
type ViewTime struct {
	Route    string  `json:"route"`
	SettleMs float64 `json:"settle_ms"`
	APICalls int     `json:"api_calls"`
	First    bool    `json:"first,omitempty"` // carga completa de página (no navegación interna)
}

// APIStat resumen por ruta de API normalizada (sin IDs ni query).
type APIStat struct {
	Route string  `json:"route"`
	N     int     `json:"n"`
	P50   float64 `json:"p50"`
	P95   float64 `json:"p95"`
	Max   float64 `json:"max"`
	TTFB  float64 `json:"ttfb_p50"`
	KB    float64 `json:"kb_avg"`
}

func clamp(v float64) float64 {
	if v < 0 || v != v {
		return 0
	}
	if v > maxMs {
		return maxMs
	}
	return float64(int(v*10)) / 10
}

func okRoute(s string) bool { return routeRe.MatchString(s) }

// ipFamily "v6" | "v4" a partir de la IP real del cliente ya normalizada por middleware.ClientIP.
func ipFamily(clientIP string) string {
	if strings.Contains(clientIP, ":") {
		return "v6"
	}
	if clientIP == "" {
		return ""
	}
	return "v4"
}

// coloFromRay "a467e11e6a26559c-GIG" → "GIG".
func coloFromRay(ray string) string {
	if i := strings.LastIndex(ray, "-"); i >= 0 {
		c := strings.ToUpper(strings.TrimSpace(ray[i+1:]))
		if coloRe.MatchString(c) {
			return c
		}
	}
	return ""
}

// Handler POST /api/public/rum — siempre responde 204 (nunca debe afectar al usuario).
func Handler(c fiber.Ctx) error {
	body := c.Body()
	if len(body) == 0 || len(body) > maxBodyBytes {
		return c.SendStatus(fiber.StatusNoContent)
	}
	var p Payload
	if err := json.Unmarshal(body, &p); err != nil {
		return c.SendStatus(fiber.StatusNoContent)
	}

	attrs := []any{slog.String("rum", "1")}
	if slug, ok := c.Locals("tenant_slug").(string); ok && slug != "" && len(slug) <= 60 {
		attrs = append(attrs, slog.String("tenant", slug))
	}
	// Enriquecimiento del lado servidor: lo que el navegador no puede saber de su propia conexión.
	if fam := ipFamily(middleware.ClientIP(c)); fam != "" {
		attrs = append(attrs, slog.String("ip_family", fam))
	}
	if colo := coloFromRay(c.Get("CF-Ray")); colo != "" {
		attrs = append(attrs, slog.String("cf_colo", colo))
	}
	if cc := strings.ToUpper(strings.TrimSpace(c.Get("CF-IPCountry"))); ctryRe.MatchString(cc) {
		attrs = append(attrs, slog.String("country", cc))
	}
	if protoRe.MatchString(p.Protocol) && p.Protocol != "" {
		attrs = append(attrs, slog.String("protocol", p.Protocol))
	}
	if netRe.MatchString(p.NetType) && p.NetType != "" {
		attrs = append(attrs, slog.String("net_type", p.NetType))
	}
	if p.Mobile {
		attrs = append(attrs, slog.Bool("mobile", true))
	}
	if p.App == "tenant" || p.App == "restaurant" {
		attrs = append(attrs, slog.String("app", p.App))
	}
	if n := p.Nav; n != nil {
		attrs = append(attrs,
			slog.Float64("dns_ms", clamp(n.DNS)),
			slog.Float64("connect_ms", clamp(n.Connect)),
			slog.Float64("tls_ms", clamp(n.TLS)),
			slog.Float64("ttfb_ms", clamp(n.TTFB)),
			slog.Float64("load_ms", clamp(n.Load)),
		)
	}
	views := make([]ViewTime, 0, len(p.Views))
	for _, v := range p.Views {
		if len(views) >= maxRoutes {
			break
		}
		if !okRoute(v.Route) {
			continue
		}
		views = append(views, ViewTime{Route: v.Route, SettleMs: clamp(v.SettleMs), APICalls: min(max(v.APICalls, 0), 500), First: v.First})
	}
	if len(views) > 0 {
		attrs = append(attrs, slog.Any("views", views))
	}
	apis := make([]APIStat, 0, len(p.API))
	for _, a := range p.API {
		if len(apis) >= maxAPIKeys {
			break
		}
		if !okRoute(a.Route) || a.N <= 0 {
			continue
		}
		apis = append(apis, APIStat{Route: a.Route, N: min(a.N, 100000), P50: clamp(a.P50), P95: clamp(a.P95), Max: clamp(a.Max), TTFB: clamp(a.TTFB), KB: clamp(a.KB)})
	}
	if len(apis) > 0 {
		attrs = append(attrs, slog.Any("api", apis))
	}
	if p.Nav == nil && len(views) == 0 && len(apis) == 0 {
		return c.SendStatus(fiber.StatusNoContent)
	}
	logger.L.Info("rum_sample", attrs...)
	return c.SendStatus(fiber.StatusNoContent)
}

// RegisterPublicRoutes registra POST /api/public/rum (sin login: también mide la pantalla de inicio de sesión).
func RegisterPublicRoutes(api fiber.Router) {
	api.Post("/public/rum", Handler)
}
