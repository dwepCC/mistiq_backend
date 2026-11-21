package handler

import (
	"bufio"
	"encoding/json"
	"fmt"
	"time"

	"tukifac/internal/sales/branchguard"
	"tukifac/pkg/billingevents"
	"tukifac/pkg/database"
	"tukifac/pkg/middleware"

	"github.com/gofiber/fiber/v3"
)

// sseKeepaliveInterval cada cuánto se escribe un comentario SSE para mantener viva la conexión
// (Cloudflare corta conexiones inactivas) y para detectar al cliente que ya se fue. Variable para tests.
var sseKeepaliveInterval = 25 * time.Second

// sseMaxLifetime es el tope duro de vida de un stream: al vencer, el servidor lo cierra y el cliente reconecta,
// con lo que se vuelven a evaluar permisos, sucursal y versión de sesión. Variable para tests.
var sseMaxLifetime = 55 * time.Minute

// sseDeadline devuelve el instante en que debe cerrarse el stream: el vencimiento del JWT o el tope duro, lo que
// ocurra primero.
func sseDeadline(now time.Time, claims *middleware.TenantClaims) time.Time {
	dl := now.Add(sseMaxLifetime)
	if claims != nil && claims.ExpiresAt != nil && claims.ExpiresAt.Time.Before(dl) {
		dl = claims.ExpiresAt.Time
	}
	return dl
}

// BillingEventsSSE GET /api/billing/events — SSE autenticado por tenant.
func (h *BillingHandler) BillingEventsSSE(c fiber.Ctx) error {
	tenant, ok := c.Locals("tenant").(*database.Tenant)
	if !ok || tenant == nil {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "tenant requerido"})
	}

	c.Set("Content-Type", "text/event-stream")
	c.Set("Cache-Control", "no-store, no-cache, no-transform")
	c.Set("Referrer-Policy", "no-referrer")
	c.Set("Connection", "keep-alive")
	c.Set("X-Accel-Buffering", "no")

	// Los eventos son por tenant y traen solo el sale_id: se filtran por la sucursal de la venta
	// para que un usuario no reciba estados (y mensajes SUNAT) de comprobantes de otra sucursal.
	// Se captura la regla ahora: dentro del stream writer c ya no es seguro.
	tdb := db(c)
	readable := branchguard.ReadableFunc(c)
	branchOf := map[uint]uint{}
	allowEvent := func(data []byte) bool {
		if tdb == nil {
			return true
		}
		var ev struct {
			SaleID uint `json:"sale_id"`
		}
		if json.Unmarshal(data, &ev) != nil || ev.SaleID == 0 {
			return false
		}
		b, ok := branchOf[ev.SaleID]
		if !ok {
			v, err := branchguard.SaleBranch(tdb, ev.SaleID)
			if err != nil {
				return false
			}
			if len(branchOf) > 5000 {
				branchOf = map[uint]uint{}
			}
			branchOf[ev.SaleID], b = v, v
		}
		return readable(b)
	}

	ch, unsub := billingevents.Subscribe(tenant.ID)
	// El contexto se captura AHORA: dentro del stream writer c ya no es seguro (Fiber lo reutiliza
	// cuando el handler retorna).
	ctx := c.Context()
	claims, _ := c.Locals("tenant_claims").(*middleware.TenantClaims)
	deadline := time.NewTimer(time.Until(sseDeadline(time.Now(), claims)))

	return c.SendStreamWriter(func(w *bufio.Writer) {
		// unsub se ejecuta aquí, cuando el stream REALMENTE termina (cliente desconectado, apagado del
		// servidor o error de escritura). Antes era un `defer` del handler: corría al retornar este
		// método —antes de que el writer empezara—, cerraba el canal y el stream moría a los pocos
		// milisegundos; el navegador reconectaba cada 3 s (retry) y nunca recibía eventos.
		defer unsub()
		defer deadline.Stop()

		_, _ = fmt.Fprintf(w, "retry: 3000\n\n")
		if err := w.Flush(); err != nil {
			return
		}

		ping := time.NewTicker(sseKeepaliveInterval)
		defer ping.Stop()

		for {
			select {
			case <-deadline.C:
				// Token vencido o tope de vida: se cierra para que el cliente reconecte con credenciales vigentes.
				return
			case <-ctx.Done():
				return
			case data, ok := <-ch:
				if !ok {
					return
				}
				if !allowEvent(data) {
					continue
				}
				_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", billingevents.EventStatusUpdated, data)
				if err := w.Flush(); err != nil {
					return
				}
			case <-ping.C:
				_, _ = fmt.Fprint(w, ": keepalive\n\n")
				if err := w.Flush(); err != nil {
					return
				}
			}
		}
	})
}
