package middleware

import (
	"io"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
)

// newContactsAccessApp inyecta los claims y (opcionalmente) el set de permisos de restaurante que
// dejaría LoadRestaurantPermissions, para probar el guard aislado de la carga real (que pega a la
// BD vía pkg/restaurantperm/staff — fuera del alcance de este test).
func newContactsAccessApp(claims *TenantClaims, restaurantPerms []string, action string) *fiber.App {
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
	}, RequireContactsAccess(action), func(c fiber.Ctx) error {
		return c.SendString("ok")
	})
	return app
}

func statusForContactsAccess(t *testing.T, claims *TenantClaims, restaurantPerms []string, action string) int {
	t.Helper()
	resp, err := newContactsAccessApp(claims, restaurantPerms, action).Test(httptest.NewRequest("GET", "/protegida", nil))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

// Regresión real detectada en producción: un mozo/cajero de Tukichef logueado por PIN nunca tiene
// claims.Permissions poblado (usa pkg/restaurantperm, no TenantPermission) — RequirePermission a
// secas lo rechazaba siempre, dejando sin clientes visibles el POS de Tukichef. Debe pasar por
// o.c (OrdersCreate), igual que ya bridgea sales.create/cashbank en RequireSalesAccess.
func TestRequireContactsAccess(t *testing.T) {
	cases := []struct {
		name            string
		claims          *TenantClaims
		restaurantPerms []string
		action          string
		want            int
	}{
		{
			name:   "usuario ERP con contacts.view",
			claims: &TenantClaims{Permissions: []string{"contacts.view"}},
			action: "view",
			want:   fiber.StatusOK,
		},
		{
			name:   "usuario ERP sin ningun permiso de contactos",
			claims: &TenantClaims{Permissions: []string{"sales.view"}},
			action: "view",
			want:   fiber.StatusForbidden,
		},
		{
			name:            "staff PIN de Tukichef con o.c (crear pedido) puede ver contactos",
			claims:          &TenantClaims{AuthMethod: "pin", EmployeeType: "waiter", Permissions: nil},
			restaurantPerms: []string{"o.c"},
			action:          "view",
			want:            fiber.StatusOK,
		},
		{
			name:            "staff PIN de Tukichef con o.c puede crear un cliente rapido",
			claims:          &TenantClaims{AuthMethod: "pin", EmployeeType: "cashier", Permissions: nil},
			restaurantPerms: []string{"o.c"},
			action:          "create",
			want:            fiber.StatusOK,
		},
		{
			name:            "staff PIN de Tukichef sin o.c (ej. cocinero) queda afuera",
			claims:          &TenantClaims{AuthMethod: "pin", EmployeeType: "cook", Permissions: nil},
			restaurantPerms: []string{"k.v", "k.u"},
			action:          "view",
			want:            fiber.StatusForbidden,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := statusForContactsAccess(t, tc.claims, tc.restaurantPerms, tc.action); got != tc.want {
				t.Fatalf("status = %d, want %d", got, tc.want)
			}
		})
	}
}
