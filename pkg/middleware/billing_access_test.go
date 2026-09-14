package middleware

import (
	"io"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
)

func newBillingAccessApp(claims *TenantClaims, restaurantPerms []string, tenantPerm, action string) *fiber.App {
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
	}, RequireBillingAccess(tenantPerm, action), func(c fiber.Ctx) error {
		return c.SendString("ok")
	})
	return app
}

func statusForBillingAccess(t *testing.T, claims *TenantClaims, restaurantPerms []string, tenantPerm, action string) int {
	t.Helper()
	resp, err := newBillingAccessApp(claims, restaurantPerms, tenantPerm, action).Test(httptest.NewRequest("GET", "/protegida", nil))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

// Regresión real detectada en producción: un cajero de Tukichef logueado por PIN nunca tiene
// claims.Permissions poblado — RequirePermission("billing.send") a secas lo rechazaba siempre,
// dejándolo sin poder cobrar/emitir el comprobante de su propia mesa.
func TestRequireBillingAccess(t *testing.T) {
	cases := []struct {
		name            string
		claims          *TenantClaims
		restaurantPerms []string
		tenantPerm      string
		action          string
		want            int
	}{
		{
			name:       "usuario ERP con billing.send",
			claims:     &TenantClaims{Permissions: []string{"billing.send"}},
			tenantPerm: "billing.send",
			action:     "send",
			want:       fiber.StatusOK,
		},
		{
			name:       "usuario ERP sin billing.send",
			claims:     &TenantClaims{Permissions: []string{"sales.view"}},
			tenantPerm: "billing.send",
			action:     "send",
			want:       fiber.StatusForbidden,
		},
		{
			name:            "cajero de Tukichef con o.ch puede enviar/cobrar",
			claims:          &TenantClaims{AuthMethod: "pin", EmployeeType: "cashier", Permissions: nil},
			restaurantPerms: []string{"o.ch"},
			tenantPerm:      "billing.send",
			action:          "send",
			want:            fiber.StatusOK,
		},
		{
			name:            "mozo de Tukichef sin o.ch no puede enviar",
			claims:          &TenantClaims{AuthMethod: "pin", EmployeeType: "waiter", Permissions: nil},
			restaurantPerms: []string{"o.c", "t.v"},
			tenantPerm:      "billing.send",
			action:          "send",
			want:            fiber.StatusForbidden,
		},
		{
			name:            "cajero de Tukichef con o.cx puede anular con nota de credito",
			claims:          &TenantClaims{AuthMethod: "pin", EmployeeType: "cashier", Permissions: nil},
			restaurantPerms: []string{"o.cx"},
			tenantPerm:      "billing.credit_note",
			action:          "credit_note",
			want:            fiber.StatusOK,
		},
		{
			name:            "cajero de Tukichef con o.ch (cobrar) NO alcanza para anular",
			claims:          &TenantClaims{AuthMethod: "pin", EmployeeType: "cashier", Permissions: nil},
			restaurantPerms: []string{"o.ch"},
			tenantPerm:      "billing.credit_note",
			action:          "credit_note",
			want:            fiber.StatusForbidden,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := statusForBillingAccess(t, tc.claims, tc.restaurantPerms, tc.tenantPerm, tc.action); got != tc.want {
				t.Fatalf("status = %d, want %d", got, tc.want)
			}
		})
	}
}
