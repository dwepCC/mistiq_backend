package handler

import (
	"context"
	"net"
	"testing"
	"time"

	"tukifac/internal/sales/branchguard/branchtest"
	"tukifac/pkg/billingevents"
	"tukifac/pkg/database"

	"github.com/gofiber/fiber/v3"
)

// El stream SSE es por tenant y solo trae el sale_id: un usuario no debe recibir los estados de
// comprobantes de otra sucursal; el administrador recibe los de todas.
func TestBillingEventsSSE_BranchIsolation(t *testing.T) {
	sseKeepaliveInterval = 80 * time.Millisecond
	defer func() { sseKeepaliveInterval = 25 * time.Second }()
	setupHub(t)
	f := newBillFixture(t)

	serve := func(a branchtest.Actor) string {
		app := branchtest.NewApp(f.db, a)
		app.Get("/events", func(c fiber.Ctx) error {
			c.Locals("tenant", &database.Tenant{ID: 1})
			return (&BillingHandler{}).BillingEventsSSE(c)
		})
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		go func() { _ = app.Listener(ln, fiber.ListenConfig{DisableStartupMessage: true}) }()
		t.Cleanup(func() { _ = app.Shutdown() })
		return "http://" + ln.Addr().String() + "/events"
	}

	vend := openSSE(t, serve(branchtest.Vendedor1))
	admin := openSSE(t, serve(branchtest.Admin))
	for _, cl := range []*sseClient{vend, admin} {
		if !cl.waitLine("retry: 3000", 10*time.Second) {
			t.Fatal("no llegó el preámbulo SSE")
		}
	}
	if n := waitSubs(2, 10*time.Second); n != 2 {
		t.Fatalf("suscripciones activas = %d, want 2", n)
	}

	// Primero el evento ajeno (S2) y después el propio (S1): si el filtro fallara, el vendedor
	// recibiría "sale_id":<S2> antes que el propio.
	billingevents.PublishStatusUpdated(context.Background(), billingevents.NewStatusUpdated(1, f.sale2, "rejected", "", "secreto-S2"))
	billingevents.PublishStatusUpdated(context.Background(), billingevents.NewStatusUpdated(1, f.sale1, "accepted", "", "propio-S1"))

	if !vend.waitLine("propio-S1", 10*time.Second) {
		t.Fatal("el vendedor debía recibir el evento de su propia sucursal")
	}
	if vend.waitLine("secreto-S2", 500*time.Millisecond) {
		t.Fatal("el vendedor recibió el evento de otra sucursal")
	}
	if !admin.waitLine("secreto-S2", 10*time.Second) {
		t.Fatal("el administrador debía recibir el evento de S2")
	}
}
