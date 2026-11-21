package handler

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"tukifac/pkg/database"
	"tukifac/pkg/mastercode"
	"tukifac/pkg/tenantctx"

	"github.com/gofiber/fiber/v3"
)

func exchangeApp(slug string) *fiber.App {
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		if slug != "" {
			c.Locals(tenantctx.LocalTenant, &database.Tenant{ID: 7, Slug: slug})
		}
		return c.Next()
	})
	app.Post("/api/auth/master-exchange", NewAuthHandler().MasterExchangeAPI)
	return app
}

func exchange(t *testing.T, app *fiber.App, body string) (int, string) {
	t.Helper()
	req := httptest.NewRequest("POST", "/api/auth/master-exchange", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// El código del acceso maestro se canjea una sola vez, solo en el tenant para el que se emitió, y el JWT viaja en el cuerpo.
func TestMasterExchange(t *testing.T) {
	ctx := context.Background()
	acme := exchangeApp("acme")

	code, _ := mastercode.Issue(ctx, "acme", "jwt-del-tenant")
	st, body := exchange(t, acme, `{"code":"`+code+`"}`)
	var out map[string]string
	_ = json.Unmarshal([]byte(body), &out)
	if st != 200 || out["token"] != "jwt-del-tenant" {
		t.Fatalf("canje válido: %d %s", st, body)
	}
	if st, _ := exchange(t, acme, `{"code":"`+code+`"}`); st != 401 {
		t.Errorf("segundo canje: %d, quiere 401", st)
	}

	// Código de otro tenant: 401 y queda quemado.
	c2, _ := mastercode.Issue(ctx, "acme", "tok2")
	if st, _ := exchange(t, exchangeApp("otra"), `{"code":"`+c2+`"}`); st != 401 {
		t.Errorf("canje en otro tenant: %d, quiere 401", st)
	}
	if st, _ := exchange(t, acme, `{"code":"`+c2+`"}`); st != 401 {
		t.Errorf("el código canjeado en otro tenant debía quedar quemado: %d", st)
	}

	// Sin contexto de empresa, sin código, código inventado.
	if st, _ := exchange(t, exchangeApp(""), `{"code":"x"}`); st != 400 {
		t.Errorf("sin tenant: %d, quiere 400", st)
	}
	if st, _ := exchange(t, acme, `{}`); st != 400 {
		t.Errorf("sin código: %d, quiere 400", st)
	}
	if st, _ := exchange(t, acme, `{"code":"inventado"}`); st != 401 {
		t.Errorf("código inventado: %d, quiere 401", st)
	}
}
