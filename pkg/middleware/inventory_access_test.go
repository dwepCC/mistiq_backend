package middleware

import (
	"io"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
)

func newInventoryAccessApp(claims *TenantClaims, restaurantPerms []string, guard fiber.Handler) *fiber.App {
	app := fiber.New()
	app.Get("/protegida", func(c fiber.Ctx) error {
		if claims != nil {
			c.Locals("tenant_claims", claims)
		}
		if restaurantPerms != nil {
			set := make(map[string]struct{}, len(restaurantPerms))
			for _, p := range restaurantPerms {
				set[p] = struct{}{}
			}
			c.Locals(restaurantPermsLocal, set)
		}
		return c.Next()
	}, guard, func(c fiber.Ctx) error {
		return c.SendString("ok")
	})
	return app
}

func statusForInventoryAccess(t *testing.T, claims *TenantClaims, restaurantPerms []string, guard fiber.Handler) int {
	t.Helper()
	resp, err := newInventoryAccessApp(claims, restaurantPerms, guard).Test(httptest.NewRequest("GET", "/protegida", nil))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

// Regresión real detectada en producción: el POS de Tukichef consulta stock-summary/movements
// para mostrar disponibilidad de productos — un staff de PIN nunca tiene inventory.view en su
// JWT, así que RequirePermission a secas lo rechazaba siempre.
func TestRequireInventoryViewAccess(t *testing.T) {
	cases := []struct {
		name            string
		claims          *TenantClaims
		restaurantPerms []string
		want            int
	}{
		{
			name:   "usuario ERP con inventory.view",
			claims: &TenantClaims{Permissions: []string{"inventory.view"}},
			want:   fiber.StatusOK,
		},
		{
			name:   "usuario ERP sin inventory.view",
			claims: &TenantClaims{Permissions: []string{"sales.view"}},
			want:   fiber.StatusForbidden,
		},
		{
			name:            "mesero de Tukichef con o.c puede ver stock",
			claims:          &TenantClaims{AuthMethod: "pin", EmployeeType: "waiter", Permissions: nil},
			restaurantPerms: []string{"o.c"},
			want:            fiber.StatusOK,
		},
		{
			name:            "delivery de Tukichef sin ningun permiso de catalogo queda afuera",
			claims:          &TenantClaims{AuthMethod: "pin", EmployeeType: "driver", Permissions: nil},
			restaurantPerms: []string{"d.v", "d.u"},
			want:            fiber.StatusForbidden,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := statusForInventoryAccess(t, tc.claims, tc.restaurantPerms, RequireInventoryViewAccess()); got != tc.want {
				t.Fatalf("status = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestRequireInventoryAdjustAccess(t *testing.T) {
	cases := []struct {
		name            string
		claims          *TenantClaims
		restaurantPerms []string
		want            int
	}{
		{
			name:   "usuario ERP con inventory.adjust",
			claims: &TenantClaims{Permissions: []string{"inventory.adjust"}},
			want:   fiber.StatusOK,
		},
		{
			name:            "encargado de Tukichef con g.p (ProductsManage) puede ajustar",
			claims:          &TenantClaims{AuthMethod: "pin", EmployeeType: "admin", Permissions: nil},
			restaurantPerms: []string{"g.p"},
			want:            fiber.StatusOK,
		},
		{
			name:            "mesero de Tukichef sin g.p no puede ajustar",
			claims:          &TenantClaims{AuthMethod: "pin", EmployeeType: "waiter", Permissions: nil},
			restaurantPerms: []string{"o.c", "t.v"},
			want:            fiber.StatusForbidden,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := statusForInventoryAccess(t, tc.claims, tc.restaurantPerms, RequireInventoryAdjustAccess()); got != tc.want {
				t.Fatalf("status = %d, want %d", got, tc.want)
			}
		})
	}
}
