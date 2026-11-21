package middleware

import (
	"net"
	"strings"

	"github.com/gofiber/fiber/v3"
)

// Rangos oficiales de Cloudflare (https://www.cloudflare.com/ips-v4 y /ips-v6). Cambian muy rara vez; si
// Cloudflare publica rangos nuevos hay que agregarlos aquí (un rango que falte NO es un riesgo de seguridad:
// esas conexiones simplemente se limitan por la IP del edge, como hasta ahora).
var cloudflareCIDRs = []string{
	"173.245.48.0/20", "103.21.244.0/22", "103.22.200.0/22", "103.31.4.0/22", "141.101.64.0/18",
	"108.162.192.0/18", "190.93.240.0/20", "188.114.96.0/20", "197.234.240.0/22", "198.41.128.0/17",
	"162.158.0.0/15", "104.16.0.0/13", "104.24.0.0/14", "172.64.0.0/13", "131.0.72.0/22",
	"2400:cb00::/32", "2606:4700::/32", "2803:f800::/32", "2405:b500::/32", "2405:8100::/32",
	"2a06:98c0::/29", "2c0f:f248::/32",
}

var cloudflareNets = func() []*net.IPNet {
	nets := make([]*net.IPNet, 0, len(cloudflareCIDRs))
	for _, cidr := range cloudflareCIDRs {
		if _, n, err := net.ParseCIDR(cidr); err == nil {
			nets = append(nets, n)
		}
	}
	return nets
}()

// IsCloudflareIP indica si ip pertenece a los rangos publicados por Cloudflare.
func IsCloudflareIP(ip string) bool {
	parsed := net.ParseIP(strings.TrimSpace(ip))
	if parsed == nil {
		return false
	}
	for _, n := range cloudflareNets {
		if n.Contains(parsed) {
			return true
		}
	}
	return false
}

// lastIPOfChain devuelve el último elemento de una cadena "a, b, c" (c.IP() con ProxyHeader=X-Forwarded-For
// puede devolver la cadena completa cuando algún elemento no se puede validar).
func lastIPOfChain(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndex(s, ","); i >= 0 {
		s = s[i+1:]
	}
	return strings.TrimSpace(s)
}

// clientIPFrom decide la IP real del cliente a partir del par que realmente se conectó al origen (peer) y del
// header CF-Connecting-IP.
//
// CF-Connecting-IP solo se acepta si el peer ES un edge de Cloudflare: Cloudflare siempre lo escribe él mismo.
// Un cliente que llega directo al origen (el origen responde en su IP pública) puede mandar ese header con lo
// que quiera, así que en ese caso se ignora y manda el peer. Un peer de Cloudflare sin header válido también
// cae al peer (nunca a un valor sin verificar).
func clientIPFrom(peer, cfConnectingIP string) string {
	peer = strings.TrimSpace(peer)
	if IsCloudflareIP(peer) {
		if ip := net.ParseIP(strings.TrimSpace(cfConnectingIP)); ip != nil {
			return normalizeRateLimitIP(ip)
		}
	}
	if ip := net.ParseIP(peer); ip != nil {
		return normalizeRateLimitIP(ip)
	}
	return peer
}

// normalizeRateLimitIP: las IPv6 de un mismo cliente rotan dentro de su /64 (direcciones temporales), así que
// se agrupan por /64 para que rotar la dirección no evada el límite. Las IPv4 quedan tal cual.
func normalizeRateLimitIP(ip net.IP) string {
	if v4 := ip.To4(); v4 != nil {
		return v4.String()
	}
	masked := ip.Mask(net.CIDRMask(64, 128))
	if masked == nil {
		return ip.String()
	}
	return masked.String() + "/64"
}

// ClientIP IP real del cliente para rate limit y registros: ver clientIPFrom.
func ClientIP(c fiber.Ctx) string {
	peer := lastIPOfChain(c.IP())
	if peer == "" {
		// Con ProxyHeader configurado y un par de confianza SIN cabecera X-Forwarded-For, Fiber devuelve "": se usa la
		// dirección real de la conexión para no agrupar a todos esos clientes bajo una clave vacía.
		if ip := c.RequestCtx().RemoteIP(); ip != nil {
			peer = ip.String()
		}
	}
	return clientIPFrom(peer, c.Get("CF-Connecting-IP"))
}
