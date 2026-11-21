package handler

import (
	"net"
	"net/http"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"tukifac/pkg/database"
	"tukifac/pkg/middleware"

	"github.com/gofiber/fiber/v3"
	"github.com/golang-jwt/jwt/v5"
)

// lastExpUnixNano guarda el exp (ya truncado a segundos, como en un JWT real) del último stream abierto.
var lastExpUnixNano atomic.Int64

// startSSEServerWithExp sirve el SSE con claims cuyo exp es now+/exp/ segundos (0 = sin exp).
func startSSEServerWithExp(t *testing.T) string {
	t.Helper()
	app := fiber.New()
	app.Get("/events/:expMs", func(c fiber.Ctx) error {
		ms, _ := strconv.Atoi(c.Params("expMs"))
		cl := &middleware.TenantClaims{Type: "tenant"}
		if ms > 0 {
			cl.RegisteredClaims = jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Duration(ms) * time.Millisecond))}
			lastExpUnixNano.Store(cl.ExpiresAt.UnixNano())
		}
		c.Locals("tenant", &database.Tenant{ID: 1})
		c.Locals("tenant_claims", cl)
		return (&BillingHandler{}).BillingEventsSSE(c)
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = app.Listener(ln, fiber.ListenConfig{DisableStartupMessage: true}) }()
	t.Cleanup(func() { _ = app.Shutdown() })
	return "http://" + ln.Addr().String()
}

// waitClosed espera a que el canal de líneas se cierre (el servidor terminó el stream) y devuelve cuánto tardó.
func (c *sseClient) waitClosed(d time.Duration, start time.Time) (time.Duration, bool) {
	timeout := time.After(d)
	for {
		select {
		case _, ok := <-c.lines:
			if !ok {
				return time.Since(start), true
			}
		case <-timeout:
			return time.Since(start), false
		}
	}
}

// El stream se cierra cuando vence el token (exp a 2 s): ni antes ni mucho después.
func TestBillingEventsSSE_ClosesWhenTokenExpires(t *testing.T) {
	sseKeepaliveInterval = 100 * time.Millisecond
	defer func() { sseKeepaliveInterval = 25 * time.Second }()
	setupHub(t)
	base := startSSEServerWithExp(t)

	start := time.Now()
	cl := openSSE(t, base+"/events/2000")
	if !cl.waitLine("retry: 3000", 2*time.Second) {
		t.Fatal("no llegó el preámbulo SSE")
	}
	// Antes del vencimiento sigue vivo.
	if !cl.waitLine(": keepalive", 1*time.Second) {
		t.Fatal("el stream debe seguir abierto antes de vencer el token")
	}
	if time.Since(start) >= time.Duration(lastExpUnixNano.Load()-start.UnixNano())-100*time.Millisecond {
		t.Fatal("prueba mal temporizada")
	}
	elapsed, closed := cl.waitClosed(5*time.Second, start)
	if !closed {
		t.Fatal("el stream no se cerró al vencer el token")
	}
	expIn := time.Duration(lastExpUnixNano.Load() - start.UnixNano())
	if elapsed < expIn-50*time.Millisecond {
		t.Fatalf("el stream se cerró antes de vencer el token: %v (exp en %v)", elapsed, expIn)
	}
	if elapsed > expIn+1500*time.Millisecond {
		t.Fatalf("el stream se cerró demasiado tarde: %v", elapsed)
	}
	if n := waitSubs(0, 2*time.Second); n != 0 {
		t.Fatalf("la suscripción debe liberarse al cerrar: %d", n)
	}
}

// Con un token de larga vida el tope duro (sseMaxLifetime) también cierra el stream.
func TestBillingEventsSSE_ClosesAtMaxLifetime(t *testing.T) {
	sseKeepaliveInterval = 100 * time.Millisecond
	sseMaxLifetime = 1 * time.Second
	defer func() { sseKeepaliveInterval = 25 * time.Second; sseMaxLifetime = 55 * time.Minute }()
	setupHub(t)
	base := startSSEServerWithExp(t)

	start := time.Now()
	cl := openSSE(t, base+"/events/3600000") // exp a 1 h
	elapsed, closed := cl.waitClosed(5*time.Second, start)
	if !closed {
		t.Fatal("el tope duro de vida no cerró el stream")
	}
	if elapsed < 900*time.Millisecond || elapsed > 2500*time.Millisecond {
		t.Fatalf("cierre fuera de la ventana esperada (~1 s): %v", elapsed)
	}
}

func TestSSEDeadline(t *testing.T) {
	now := time.Now()
	if got := sseDeadline(now, nil); !got.Equal(now.Add(55 * time.Minute)) {
		t.Fatalf("sin claims: tope duro de 55 min, got %v", got.Sub(now))
	}
	soon := &middleware.TenantClaims{RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(now.Add(10 * time.Minute))}}
	if got := sseDeadline(now, soon); got.Sub(now) > 10*time.Minute+time.Second || got.Sub(now) < 9*time.Minute {
		t.Fatalf("debe respetar el exp del token (10 min), got %v", got.Sub(now))
	}
	late := &middleware.TenantClaims{RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(now.Add(24 * time.Hour))}}
	if got := sseDeadline(now, late); !got.Equal(now.Add(55 * time.Minute)) {
		t.Fatalf("exp lejano: manda el tope de 55 min, got %v", got.Sub(now))
	}
}

func TestBillingEventsSSE_SecurityHeaders(t *testing.T) {
	setupHub(t)
	base := startSSEServerWithExp(t)
	resp, err := http.Get(base + "/events/1500")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if got := resp.Header.Get("Cache-Control"); got != "no-store, no-cache, no-transform" {
		t.Errorf("Cache-Control = %q", got)
	}
	if got := resp.Header.Get("Referrer-Policy"); got != "no-referrer" {
		t.Errorf("Referrer-Policy = %q", got)
	}
	if got := resp.Header.Get("Content-Type"); got != "text/event-stream" {
		t.Errorf("Content-Type = %q", got)
	}
}
