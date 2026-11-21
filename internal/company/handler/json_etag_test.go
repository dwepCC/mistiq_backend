package handler

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"tukifac/pkg/database"

	"github.com/gofiber/fiber/v3"
)

func TestJSONWithETag_200Then304ThenChanged(t *testing.T) {
	payload := fiber.Map{"data": []string{"a", "b"}}
	app := fiber.New()
	app.Get("/x", func(c fiber.Ctx) error { return jsonWithETag(c, payload) })

	resp, err := app.Test(httptest.NewRequest("GET", "/x", nil))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	etag := resp.Header.Get("ETag")
	if resp.StatusCode != 200 || etag == "" || !strings.HasPrefix(etag, `W/"`) {
		t.Fatalf("primera respuesta: %d etag=%q", resp.StatusCode, etag)
	}
	if got := resp.Header.Get("Cache-Control"); got != "private, no-cache" {
		t.Fatalf("Cache-Control = %q", got)
	}
	if !strings.Contains(resp.Header.Get("Content-Type"), "application/json") || !strings.Contains(string(body), `"data"`) {
		t.Fatalf("el cuerpo debe ser el mismo JSON de antes: %s", body)
	}

	// Revalidación: mismo contenido → 304 sin cuerpo.
	req := httptest.NewRequest("GET", "/x", nil)
	req.Header.Set("If-None-Match", etag)
	resp2, _ := app.Test(req)
	b2, _ := io.ReadAll(resp2.Body)
	if resp2.StatusCode != 304 || len(b2) != 0 {
		t.Fatalf("revalidación: %d, cuerpo=%d bytes (esperado 304 vacío)", resp2.StatusCode, len(b2))
	}

	// El navegador puede mandar el validador sin el prefijo W/ o dentro de una lista.
	req = httptest.NewRequest("GET", "/x", nil)
	req.Header.Set("If-None-Match", `"zzz", `+strings.TrimPrefix(etag, "W/"))
	resp3, _ := app.Test(req)
	if resp3.StatusCode != 304 {
		t.Fatalf("lista/sin W/: %d", resp3.StatusCode)
	}

	// Cambia el contenido (ej. se subió otro logo) → ETag distinto y 200 completo.
	payload["data"] = []string{"a", "b", "c"}
	req = httptest.NewRequest("GET", "/x", nil)
	req.Header.Set("If-None-Match", etag)
	resp4, _ := app.Test(req)
	if resp4.StatusCode != 200 || resp4.Header.Get("ETag") == etag {
		t.Fatalf("tras cambiar el contenido: %d etag=%q", resp4.StatusCode, resp4.Header.Get("ETag"))
	}
}

func TestAttachLogoDataURL_DoesNotDuplicateEmbeddedLogo(t *testing.T) {
	embedded := "data:image/png;base64,AAAA"
	cfg := &database.TenantCompanyConfig{LogoURL: embedded}
	attachLogoDataURL("12345678901", cfg)
	if cfg.LogoDataURL != "" {
		t.Fatalf("logo_url ya es data:, no debe duplicarse en logo_data_url (len=%d)", len(cfg.LogoDataURL))
	}
	b := &database.TenantBranch{ID: 2, LogoURL: embedded}
	attachBranchLogoDataURL("12345678901", b)
	if b.LogoDataURL != "" {
		t.Fatal("el logo de sucursal embebido tampoco debe duplicarse")
	}
}
