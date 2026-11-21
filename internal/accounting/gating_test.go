package accounting

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"tukifac/internal/accounting/handler"
	"tukifac/pkg/middleware"

	"github.com/glebarez/sqlite"
	"github.com/gofiber/fiber/v3"
	"gorm.io/gorm"
)

// Cada endpoint del módulo exige el módulo «accounting» (plan) Y su permiso propio.
func TestRoutesRequireModuleAndPermission(t *testing.T) {
	type ep struct{ method, path, perm string }
	endpoints := []ep{
		{"GET", "/accounting/registers/sales", "accounting.sales_register"},
		{"GET", "/accounting/registers/purchases", "accounting.purchases_register"},
		{"GET", "/accounting/summary", "accounting.summary_report"},
		{"GET", "/accounting/formats", "accounting.export_formats"},
		{"GET", "/accounting/formats/export", "accounting.export_formats"},
		{"GET", "/accounting/ledger", "accounting.ledger"},
		{"GET", "/accounting/settings", "accounting.settings"},
		{"PUT", "/accounting/settings", "accounting.settings"},
		{"GET", "/accounting/sire/settings", "accounting.settings"},
		{"PUT", "/accounting/sire/settings", "accounting.settings"},
		{"GET", "/accounting/sire/sale/periods", "accounting.sire_view"},
		{"GET", "/accounting/sire/purchase/tickets", "accounting.sire_view"},
		{"POST", "/accounting/sire/sale/query", "accounting.sire_view"},
		{"POST", "/accounting/sire/sale/tickets", "accounting.sire_manage"},
		{"POST", "/accounting/sire/sale/accept", "accounting.sire_manage"},
	}
	// BD vacía (sin tablas): los handlers responden 400 en vez de entrar en pánico.
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	build := func(modules, perms []string) *fiber.App {
		app := fiber.New()
		app.Use(func(c fiber.Ctx) error {
			c.Locals("tenantDB", db)
			c.Locals("tenant_claims", &middleware.TenantClaims{Modules: modules, Permissions: perms})
			return c.Next()
		})
		RegisterRoutesWith(app, handler.New())
		return app
	}
	status := func(app *fiber.App, e ep) int {
		req := httptest.NewRequest(e.method, e.path, nil)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp.StatusCode
	}
	for _, e := range endpoints {
		// Sin el módulo en el plan: 403 aunque tenga el permiso.
		if got := status(build([]string{"sales"}, []string{e.perm}), e); got != http.StatusForbidden {
			t.Errorf("%s %s sin módulo: %d, quería 403", e.method, e.path, got)
		}
		// Con el módulo pero sin el permiso: 403.
		if got := status(build([]string{"accounting"}, []string{"sales.view"}), e); got != http.StatusForbidden {
			t.Errorf("%s %s sin permiso: %d, quería 403", e.method, e.path, got)
		}
		// Con el módulo y OTRO permiso de contabilidad: 403 (no están agrupados).
		other := "accounting.ledger"
		if e.perm == other {
			other = "accounting.settings"
		}
		if got := status(build([]string{"accounting"}, []string{other}), e); got != http.StatusForbidden {
			t.Errorf("%s %s con otro permiso de contabilidad: %d, quería 403", e.method, e.path, got)
		}
		// Con módulo y permiso propio: pasa los middlewares (el handler responde 400/200 con la BD vacía, nunca 403).
		if got := status(build([]string{"accounting"}, []string{e.perm}), e); got == http.StatusForbidden {
			t.Errorf("%s %s con módulo y permiso: 403", e.method, e.path)
		}
	}
}
