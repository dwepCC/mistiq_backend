package middleware

import (
	"io"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v3"
)

// newMasterAccessApp monta la ruta protegida inyectando los claims que dejaría
// TenantAuthAPI, para probar el guard aislado del parseo del JWT.
func newMasterAccessApp(claims *TenantClaims) *fiber.App {
	app := fiber.New()
	app.Get("/protegida", func(c fiber.Ctx) error {
		if claims != nil {
			c.Locals("tenant_claims", claims)
		}
		return c.Next()
	}, RequireMasterAccess(), func(c fiber.Ctx) error {
		return c.SendString("ok")
	})
	return app
}

func statusFor(t *testing.T, claims *TenantClaims) int {
	t.Helper()
	resp, err := newMasterAccessApp(claims).Test(httptest.NewRequest("GET", "/protegida", nil))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	return resp.StatusCode
}

// La sesión de soporte es la única que pasa. Cualquier otra combinación queda
// fuera: un tenant no debe poder alcanzar acciones fiscales irreversibles ni
// aunque su rol tenga todos los permisos.
func TestRequireMasterAccess(t *testing.T) {
	cases := []struct {
		name   string
		claims *TenantClaims
		want   int
	}{
		{
			name:   "acceso maestro completo",
			claims: &TenantClaims{Impersonated: true, AuthMethod: AuthMethodMasterAccess},
			want:   fiber.StatusOK,
		},
		{
			name:   "sesion normal del tenant",
			claims: &TenantClaims{AuthMethod: "pwd"},
			want:   fiber.StatusForbidden,
		},
		{
			// El claim impersonated sin el auth_method correspondiente indica un
			// token manipulado o un flujo que no es acceso maestro.
			name:   "impersonated sin auth_method",
			claims: &TenantClaims{Impersonated: true, AuthMethod: "pwd"},
			want:   fiber.StatusForbidden,
		},
		{
			name:   "auth_method sin impersonated",
			claims: &TenantClaims{AuthMethod: AuthMethodMasterAccess},
			want:   fiber.StatusForbidden,
		},
		{
			name:   "sesion por pin",
			claims: &TenantClaims{AuthMethod: "pin"},
			want:   fiber.StatusForbidden,
		},
		{
			name:   "sin claims en contexto",
			claims: nil,
			want:   fiber.StatusForbidden,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := statusFor(t, tc.claims); got != tc.want {
				t.Fatalf("status esperado %d, got %d", tc.want, got)
			}
		})
	}
}

func TestIsMasterAccess(t *testing.T) {
	app := fiber.New()
	var got bool
	app.Get("/x", func(c fiber.Ctx) error {
		c.Locals("tenant_claims", &TenantClaims{Impersonated: true, AuthMethod: AuthMethodMasterAccess})
		got = IsMasterAccess(c)
		return c.SendString("ok")
	})
	resp, err := app.Test(httptest.NewRequest("GET", "/x", nil))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	if !got {
		t.Fatal("IsMasterAccess debía detectar la sesión de soporte")
	}
}
