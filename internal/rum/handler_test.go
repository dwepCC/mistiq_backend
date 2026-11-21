package rum

import (
	"bytes"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"

	"tukifac/pkg/logger"

	"github.com/gofiber/fiber/v3"
)

func captureLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	buf := &bytes.Buffer{}
	prev := logger.L
	logger.L = slog.New(slog.NewJSONHandler(buf, nil))
	t.Cleanup(func() { logger.L = prev })
	return buf
}

func post(t *testing.T, body string, headers map[string]string) (int, string) {
	t.Helper()
	buf := captureLogs(t)
	app := fiber.New()
	RegisterPublicRoutes(app.Group("/api"))
	req := httptest.NewRequest("POST", "/api/public/rum", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, buf.String()
}

func TestHandler_LogsSanitizedSample(t *testing.T) {
	body := `{"protocol":"h2","net_type":"4g","mobile":true,"app":"tenant",
	 "nav":{"dns":12.34,"connect":110,"tls":120,"ttfb":300,"load":900},
	 "views":[{"route":"/sales/pos","settle_ms":640,"api_calls":9,"first":true},{"route":"/x?token=SECRETO","settle_ms":1,"api_calls":1}],
	 "api":[{"route":"/api/products","n":12,"p50":30,"p95":80,"max":120,"ttfb_p50":25,"kb_avg":3.2},{"route":"/api/p/1?a=b","n":1,"p50":1,"p95":1,"max":1}]}`
	code, out := post(t, body, map[string]string{
		"CF-Ray":           "a467e11e6a26559c-GIG",
		"CF-IPCountry":     "PE",
		"User-Agent":       "Mozilla/5.0 (secreto-ua)",
		"Authorization":    "Bearer token-secreto",
		"X-Tenant-Slug":    "doriconta",
		"CF-Connecting-IP": "190.236.1.2",
	})
	if code != 204 {
		t.Fatalf("status %d", code)
	}
	for _, want := range []string{`"msg":"rum_sample"`, `"cf_colo":"GIG"`, `"country":"PE"`, `"protocol":"h2"`, `"connect_ms":110`, `"/sales/pos"`, `"/api/products"`} {
		if !strings.Contains(out, want) {
			t.Errorf("falta %s en: %s", want, out)
		}
	}
	// Privacidad y saneado.
	for _, forbidden := range []string{"secreto", "190.236.1.2", "Mozilla", "token", "SECRETO", "a=b"} {
		if strings.Contains(out, forbidden) {
			t.Errorf("no debe aparecer %q en el registro: %s", forbidden, out)
		}
	}
}

func TestHandler_IgnoresGarbageAndOversize(t *testing.T) {
	if code, out := post(t, `no es json`, nil); code != 204 || out != "" {
		t.Errorf("json inválido: %d %q", code, out)
	}
	if code, out := post(t, `{}`, nil); code != 204 || out != "" {
		t.Errorf("vacío no debe registrar: %d %q", code, out)
	}
	big := `{"nav":{"dns":1},"x":"` + strings.Repeat("a", maxBodyBytes) + `"}`
	if code, out := post(t, big, nil); code != 204 || out != "" {
		t.Errorf("cuerpo gigante debe ignorarse: %d len=%d", code, len(out))
	}
	// valores absurdos se recortan
	_, out := post(t, `{"nav":{"dns":-5,"connect":99999999,"load":1}}`, nil)
	if !strings.Contains(out, `"dns_ms":0`) || !strings.Contains(out, `"connect_ms":600000`) {
		t.Errorf("clamp: %s", out)
	}
}

func TestEnrichmentHelpers(t *testing.T) {
	if ipFamily("190.1.2.3") != "v4" || ipFamily("2800:200:1:2::/64") != "v6" || ipFamily("") != "" {
		t.Error("ipFamily")
	}
	if coloFromRay("a467e11e6a26559c-GIG") != "GIG" || coloFromRay("sin-guion-largo-ZZZZZZ") != "" {
		t.Error("coloFromRay")
	}
}
