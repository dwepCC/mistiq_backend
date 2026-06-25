package middleware

import (
	"net/http/httptest"
	"sync"
	"testing"

	"tukifac/config"
	"tukifac/pkg/database"
	"tukifac/pkg/tenantctx"

	"github.com/gofiber/fiber/v3"
)

func TestValidateTenantBinding_rejectsMissingTenantID(t *testing.T) {
	config.AppConfig = &config.Config{JWTSecret: "test-secret"}

	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		tenant := &database.Tenant{ID: 1, Slug: "empresa-a", DBName: "saas_tenant_empresa_a"}
		tenantctx.Bind(c, tenant, nil)
		c.Locals("tenant_claims", &TenantClaims{
			TenantSlug: "empresa-a",
			TenantDB:   "saas_tenant_empresa_a",
			TenantID:   0,
			Type:       "tenant",
		})
		return c.Next()
	})
	app.Get("/api/test", ValidateTenantBinding(), func(c fiber.Ctx) error {
		return c.SendString("ok")
	})

	req := httptest.NewRequest("GET", "/api/test", nil)
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != fiber.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp.StatusCode)
	}
}

func TestConcurrentTenantLocalsIsolation(t *testing.T) {
	config.AppConfig = &config.Config{AppEnv: "production", AppDomain: "tukifac.com"}

	const n = 100
	var wg sync.WaitGroup
	wg.Add(n)

	app := fiber.New()
	app.Get("/t/:slug", func(c fiber.Ctx) error {
		slug := c.Params("slug")
		tenant := &database.Tenant{
			ID:     uint(len(slug)),
			Slug:   slug,
			DBName: "saas_tenant_" + slug,
		}
		tenantctx.Bind(c, tenant, nil)
		c.Locals("marker", slug)

		gotSlug := tenant.Slug
		if s := tenantctx.Slug(c); s != "" {
			gotSlug = s
		}
		gotMarker, _ := c.Locals("marker").(string)
		if gotSlug != slug || gotMarker != slug {
			t.Errorf("locals bleed: want %q got slug=%q marker=%q", slug, gotSlug, gotMarker)
		}
		return c.SendString(slug)
	})

	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			slug := "tenant-a"
			if i%2 == 1 {
				slug = "tenant-b"
			}
			req := httptest.NewRequest("GET", "/t/"+slug, nil)
			resp, err := app.Test(req)
			if err != nil {
				t.Error(err)
				return
			}
			if resp.StatusCode != fiber.StatusOK {
				t.Errorf("status %d for %s", resp.StatusCode, slug)
			}
		}(i)
	}

	wg.Wait()
}
