package middleware

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"tukifac/config"

	"github.com/gofiber/fiber/v3"
	"github.com/golang-jwt/jwt/v5"
)

func TestClientIPFrom(t *testing.T) {
	cases := []struct {
		name, peer, cf, want string
	}{
		{"peer de Cloudflare + CF-Connecting-IP válido → IP real", "172.64.222.34", "190.236.1.2", "190.236.1.2"},
		{"conexión directa al origen con CF-Connecting-IP falsificado → se ignora", "200.10.20.30", "1.1.1.1", "200.10.20.30"},
		{"peer de Cloudflare sin header → peer", "172.64.222.34", "", "172.64.222.34"},
		{"peer de Cloudflare con header basura → peer", "172.64.222.34", "no-es-ip", "172.64.222.34"},
		{"IPv6 de cliente se agrupa por /64", "2606:4700::1", "2800:200:1234:5678:aaaa:bbbb:cccc:dddd", "2800:200:1234:5678::/64"},
		{"peer IPv6 de Cloudflare reconocido", "2606:4700:3036::6815:1a31", "190.236.1.2", "190.236.1.2"},
		{"peer IPv6 directo (no Cloudflare) con header falso → peer /64", "2800:200:1:2:3:4:5:6", "9.9.9.9", "2800:200:1:2::/64"},
	}
	for _, tc := range cases {
		if got := clientIPFrom(tc.peer, tc.cf); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestLastIPOfChain(t *testing.T) {
	if got := lastIPOfChain("2a02:4780:75:29c1::1, 162.158.155.68"); got != "162.158.155.68" {
		t.Errorf("cadena: %q", got)
	}
	if got := lastIPOfChain("  10.0.0.1 "); got != "10.0.0.1" {
		t.Errorf("simple: %q", got)
	}
}

func testToken(t *testing.T, tenantID uint, secret string) string {
	t.Helper()
	claims := &TenantClaims{TenantID: tenantID, Type: "tenant"}
	claims.ExpiresAt = jwt.NewNumericDate(time.Now().Add(time.Hour))
	s, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestRateLimitKey_TenantOnlyFromVerifiedJWT(t *testing.T) {
	config.AppConfig = &config.Config{JWTSecret: "s3cret-test"}
	app := fiber.New()
	var got string
	app.Get("/k", func(c fiber.Ctx) error { got = RateLimitKey(c); return c.SendString("ok") })

	do := func(auth string, extra map[string]string) string {
		req := httptest.NewRequest("GET", "/k", nil)
		if auth != "" {
			req.Header.Set("Authorization", "Bearer "+auth)
		}
		for k, v := range extra {
			req.Header.Set(k, v)
		}
		if _, err := app.Test(req); err != nil {
			t.Fatal(err)
		}
		return got
	}
	base := do("", nil)
	if base == "" {
		t.Fatal("la clave no puede ser vacía")
	}
	a := do(testToken(t, 7, "s3cret-test"), nil)
	b := do(testToken(t, 8, "s3cret-test"), nil)
	if a == b || a == base || b == base {
		t.Fatalf("tenants distintos y anónimo deben tener claves distintas: base=%q a=%q b=%q", base, a, b)
	}
	// Token firmado con otro secreto: no cuenta como tenant (no se puede fabricar cupos).
	if forged := do(testToken(t, 9, "otro-secreto"), nil); forged != base {
		t.Fatalf("un JWT no verificable no debe aportar tenant: %q vs %q", forged, base)
	}
	// Cabeceras controlables por el cliente no cambian la clave.
	if spoof := do("", map[string]string{"X-Tenant-Slug": "otro", "CF-Connecting-IP": "8.8.8.8"}); spoof != base {
		t.Fatalf("X-Tenant-Slug / CF-Connecting-IP falsificados no deben alterar la clave: %q vs %q", spoof, base)
	}
}

// Dos tenants detrás de la misma IP no comparten cupo; el mismo tenant sí se limita a sí mismo.
func TestApplyRateLimits_TenantsDoNotShareBucket(t *testing.T) {
	config.AppConfig = &config.Config{JWTSecret: "s3cret-test", RateLimitEnabled: true, RateLimitGlobal: 3, RateLimitAuth: 100, RateLimitPassword: 100, RateLimitBilling: 100, RateLimitUpload: 100, RateLimitPublicConsult: 100}
	app := fiber.New()
	ApplyRateLimits(app)
	app.Get("/api/algo", func(c fiber.Ctx) error { return c.SendString("ok") })

	call := func(tok string) int {
		req := httptest.NewRequest("GET", "/api/algo", nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp.StatusCode
	}
	ta, tb := testToken(t, 1, "s3cret-test"), testToken(t, 2, "s3cret-test")
	for i := 0; i < 3; i++ {
		if c := call(ta); c != 200 {
			t.Fatalf("tenant A petición %d: %d", i+1, c)
		}
	}
	if c := call(ta); c != 429 {
		t.Fatalf("tenant A debe quedar limitado en la 4ª: %d", c)
	}
	if c := call(tb); c != 200 {
		t.Fatalf("tenant B no debe verse afectado por el cupo de A: %d", c)
	}
}

// Cadena real de producción: Cliente → Cloudflare → NPM → backend (conexión TCP real por loopback, que Fiber
// trata como proxy de confianza igual que la red privada de Docker). Las cabeceras reproducen lo que NPM envía:
//   - hosts de tenant (*.tukifac.com, /api/): X-Forwarded-For = $remote_addr (sobrescrito, una sola IP)
//   - api.tukifac.com: X-Forwarded-For = $proxy_add_x_forwarded_for (lo que mande el cliente + $remote_addr al final)
//   - CF-Connecting-IP pasa tal cual (NPM no lo toca).
func TestClientIP_RealProxyChain(t *testing.T) {
	app := fiber.New(fiber.Config{
		ProxyHeader: fiber.HeaderXForwardedFor,
		TrustProxy:  true,
		TrustProxyConfig: fiber.TrustProxyConfig{
			Loopback:  true,
			Private:   true,
			LinkLocal: true,
		},
	})
	app.Get("/ip", func(c fiber.Ctx) error { return c.SendString(ClientIP(c)) })
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = app.Listener(ln, fiber.ListenConfig{DisableStartupMessage: true}) }()
	t.Cleanup(func() { _ = app.Shutdown() })

	cases := []struct {
		name, xff, cf, want string
	}{
		{"vía Cloudflare, host de tenant", "172.64.222.42", "190.236.1.2", "190.236.1.2"},
		{"vía Cloudflare, host api (XFF con cadena)", "190.236.1.2, 172.64.222.42", "190.236.1.2", "190.236.1.2"},
		{"vía Cloudflare, XFF falsificado por el cliente al inicio", "6.6.6.6, 190.236.1.2, 172.64.222.42", "190.236.1.2", "190.236.1.2"},
		{"vía Cloudflare, cliente IPv6 → /64", "162.158.155.68", "2800:200:1:2:aaaa:bbbb:cccc:dddd", "2800:200:1:2::/64"},
		{"DIRECTO al origen, host de tenant, CF-Connecting-IP falsificado", "200.10.20.30", "1.1.1.1", "200.10.20.30"},
		{"DIRECTO al origen, host api, XFF con IP de Cloudflare falsa al inicio y header falso", "172.64.222.1, 200.10.20.30", "1.1.1.1", "200.10.20.30"},
		{"DIRECTO al origen sin CF-Connecting-IP", "200.10.20.30", "", "200.10.20.30"},
		{"vía Cloudflare pero sin CF-Connecting-IP → cae al edge (comportamiento anterior)", "172.64.222.42", "", "172.64.222.42"},
	}
	for _, tc := range cases {
		req, _ := http.NewRequest("GET", "http://"+ln.Addr().String()+"/ip", nil)
		req.Header.Set("X-Forwarded-For", tc.xff)
		if tc.cf != "" {
			req.Header.Set("CF-Connecting-IP", tc.cf)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if string(b) != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, b, tc.want)
		}
	}
}

func TestVerifiedTenantID_ExpiredAndWrongTypeAreIgnored(t *testing.T) {
	config.AppConfig = &config.Config{JWTSecret: "s3cret-test"}
	app := fiber.New()
	var got uint
	app.Get("/t", func(c fiber.Ctx) error { got = verifiedTenantID(c); return c.SendString("ok") })
	call := func(tok string) uint {
		req := httptest.NewRequest("GET", "/t", nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		if _, err := app.Test(req); err != nil {
			t.Fatal(err)
		}
		return got
	}
	mk := func(typ string, exp time.Time) string {
		claims := &TenantClaims{TenantID: 5, Type: typ}
		claims.ExpiresAt = jwt.NewNumericDate(exp)
		s, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte("s3cret-test"))
		return s
	}
	if id := call(mk("tenant", time.Now().Add(time.Hour))); id != 5 {
		t.Fatalf("token válido: %d", id)
	}
	if id := call(mk("tenant", time.Now().Add(-time.Hour))); id != 0 {
		t.Fatalf("token vencido no debe aportar tenant: %d", id)
	}
	if id := call(mk("superadmin", time.Now().Add(time.Hour))); id != 0 {
		t.Fatalf("token de otro tipo no debe aportar tenant: %d", id)
	}
	// alg=none y HS512 (algoritmo no permitido) tampoco
	none := jwt.NewWithClaims(jwt.SigningMethodNone, &TenantClaims{TenantID: 5, Type: "tenant"})
	s, _ := none.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if id := call(s); id != 0 {
		t.Fatalf("alg=none no debe aceptarse: %d", id)
	}
}

// Costo de verificar el JWT en cada petición para la clave del rate limit (token realista con 120 permisos).
func BenchmarkVerifyJWTForRateLimit(b *testing.B) {
	claims := &TenantClaims{TenantID: 7, Type: "tenant", Permissions: make([]string, 120)}
	for i := range claims.Permissions {
		claims.Permissions[i] = "modulo.accion"
	}
	claims.ExpiresAt = jwt.NewNumericDate(time.Now().Add(time.Hour))
	tok, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte("s3cret-test"))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		c := &TenantClaims{}
		t, err := jwt.ParseWithClaims(tok, c, func(*jwt.Token) (interface{}, error) { return []byte("s3cret-test"), nil }, jwt.WithValidMethods([]string{"HS256"}))
		if err != nil || !t.Valid {
			b.Fatal(err)
		}
	}
}
