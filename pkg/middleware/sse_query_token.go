package middleware

import (
	"log"
	"os"
	"strings"
	"sync/atomic"

	"github.com/gofiber/fiber/v3"
)

// SSEEventsPath es la ÚNICA ruta que puede autenticarse con ?access_token= (EventSource no admite cabeceras).
const SSEEventsPath = "/api/billing/events"

// sseQueryTokenConns cuenta las conexiones que usaron el puente ?access_token= desde el arranque del proceso.
// Cuando deje de crecer se apaga el puente (SSE_ALLOW_QUERY_TOKEN=0) y se borra este archivo.
var sseQueryTokenConns atomic.Uint64

// SSEQueryTokenConnections devuelve cuántas conexiones SSE usaron el puente ?access_token= en este proceso.
func SSEQueryTokenConnections() uint64 { return sseQueryTokenConns.Load() }

// sseQueryTokenAllowed: puente de compatibilidad para apps ya instaladas que abren EventSource con el token en la
// URL. Activo por defecto; SSE_ALLOW_QUERY_TOKEN=0 lo apaga (los clientes nuevos usan la cabecera Authorization).
func sseQueryTokenAllowed() bool {
	return strings.TrimSpace(os.Getenv("SSE_ALLOW_QUERY_TOKEN")) != "0"
}

// queryAccessToken devuelve el token del query SOLO para GET /api/billing/events y solo con el puente activo.
// En cualquier otra ruta el parámetro se ignora (la petición cae en "Token no proporcionado"). Si count es true
// contabiliza y registra el uso (sin el valor del token).
func queryAccessToken(c fiber.Ctx, count bool) string {
	if c.Method() != fiber.MethodGet || strings.TrimRight(c.Path(), "/") != SSEEventsPath || !sseQueryTokenAllowed() {
		return ""
	}
	tok := strings.TrimSpace(c.Query("access_token"))
	if tok == "" {
		return ""
	}
	if count {
		n := sseQueryTokenConns.Add(1)
		log.Printf("[sse] conexión con access_token en la URL (puente de compatibilidad, total=%d)", n)
	}
	return tok
}
