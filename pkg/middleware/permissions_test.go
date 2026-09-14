package middleware

import (
	"io"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
)

// newRequirePermissionApp monta la ruta protegida inyectando los claims que dejaría
// TenantAuthAPI, para probar el guard aislado del parseo del JWT.
func newRequirePermissionApp(claims *TenantClaims, permission string) *fiber.App {
	app := fiber.New()
	app.Get("/protegida", func(c fiber.Ctx) error {
		if claims != nil {
			c.Locals("tenant_claims", claims)
		}
		return c.Next()
	}, RequirePermission(permission), func(c fiber.Ctx) error {
		return c.SendString("ok")
	})
	return app
}

func statusForPermission(t *testing.T, claims *TenantClaims, permission string) int {
	t.Helper()
	resp, err := newRequirePermissionApp(claims, permission).Test(httptest.NewRequest("GET", "/protegida", nil))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

// RequirePermission debe usar el mismo criterio que tenantHasPermission (match exacto +
// implicaciones, p. ej. "{modulo}.manage" concede todas las acciones del módulo). Antes hacía
// match exacto por su cuenta, así que un rol con "inventory.manage" podía crear/editar documentos
// de inventario pero no ver el listado (RequirePermission("inventory.view") lo rechazaba) — el
// mismo permiso "funcionaba" para RequireSalesAccess/RequireCashbankAccess (que sí usan
// tenantHasPermission) y no para el resto de los módulos.
func TestRequirePermission(t *testing.T) {
	cases := []struct {
		name       string
		claims     *TenantClaims
		permission string
		want       int
	}{
		{
			name:       "sin contexto de autenticacion",
			claims:     nil,
			permission: "contacts.view",
			want:       fiber.StatusForbidden,
		},
		{
			name:       "permisos nil",
			claims:     &TenantClaims{Permissions: nil},
			permission: "contacts.view",
			want:       fiber.StatusForbidden,
		},
		{
			name:       "match exacto concede",
			claims:     &TenantClaims{Permissions: []string{"contacts.view"}},
			permission: "contacts.view",
			want:       fiber.StatusOK,
		},
		{
			name:       "sin el permiso exacto ni implicito, rechaza",
			claims:     &TenantClaims{Permissions: []string{"contacts.view"}},
			permission: "contacts.delete",
			want:       fiber.StatusForbidden,
		},
		{
			name:       "{modulo}.manage implica todas las acciones del modulo",
			claims:     &TenantClaims{Permissions: []string{"inventory.manage"}},
			permission: "inventory.view",
			want:       fiber.StatusOK,
		},
		{
			name:       "cashbank.open implica cashbank.view",
			claims:     &TenantClaims{Permissions: []string{"cashbank.open"}},
			permission: "cashbank.view",
			want:       fiber.StatusOK,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := statusForPermission(t, tc.claims, tc.permission); got != tc.want {
				t.Fatalf("status = %d, want %d", got, tc.want)
			}
		})
	}
}
