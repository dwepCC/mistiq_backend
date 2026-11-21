package mastercode

import (
	"context"
	"strings"
	"testing"
	"time"

	"tukifac/pkg/tenantcache"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestIssueRedeemSingleUse(t *testing.T) {
	ctx := context.Background()
	code, err := Issue(ctx, "acme", "jwt-secreto")
	if err != nil || len(code) < 40 {
		t.Fatalf("Issue: code=%q err=%v", code, err)
	}
	if strings.Contains(code, "jwt-secreto") {
		t.Fatal("el código no debe contener el token")
	}
	if tok, ok := Redeem(ctx, "acme", code); !ok || tok != "jwt-secreto" {
		t.Fatalf("primer canje: tok=%q ok=%v", tok, ok)
	}
	if _, ok := Redeem(ctx, "acme", code); ok {
		t.Fatal("el código se pudo canjear dos veces")
	}
}

func TestRedeemWrongTenantBurnsCode(t *testing.T) {
	ctx := context.Background()
	code, _ := Issue(ctx, "acme", "tok")
	if _, ok := Redeem(ctx, "otra", code); ok {
		t.Fatal("un código de otro tenant no debe canjearse")
	}
	// El intento fallido lo quema: ni siquiera el tenant correcto puede usarlo después.
	if _, ok := Redeem(ctx, "acme", code); ok {
		t.Fatal("el código debía quedar quemado tras un canje en otro tenant")
	}
}

func TestRedeemExpiredOrUnknown(t *testing.T) {
	ctx := context.Background()
	old := TTL
	TTL = 30 * time.Millisecond
	defer func() { TTL = old }()
	code, _ := Issue(ctx, "acme", "tok")
	time.Sleep(60 * time.Millisecond)
	if _, ok := Redeem(ctx, "acme", code); ok {
		t.Fatal("un código vencido no debe canjearse")
	}
	for _, bad := range []string{"", "no-existe", strings.Repeat("x", 500)} {
		if _, ok := Redeem(ctx, "acme", bad); ok {
			t.Fatalf("código inválido %q aceptado", bad)
		}
	}
}

func TestIssueRequiresSlugAndToken(t *testing.T) {
	if _, err := Issue(context.Background(), "", "t"); err == nil {
		t.Error("slug vacío debe fallar")
	}
	if _, err := Issue(context.Background(), "s", ""); err == nil {
		t.Error("token vacío debe fallar")
	}
}

func TestCodesAreUnique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		c, _ := Issue(context.Background(), "a", "t")
		if seen[c] {
			t.Fatal("código repetido")
		}
		seen[c] = true
	}
}

// Con Redis (producción) el canje es atómico y de un solo uso incluso entre instancias.
func TestRedisSingleUseAndTTL(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()
	defer tenantcache.SetClientForTests(rdb)()

	ctx := context.Background()
	code, err := Issue(ctx, "acme", "jwt")
	if err != nil {
		t.Fatal(err)
	}
	keys := mr.Keys()
	if len(keys) != 1 || strings.Contains(keys[0], code) {
		t.Fatalf("la clave de Redis no debe contener el código en claro: %v", keys)
	}
	if ttl := mr.TTL(keys[0]); ttl <= 0 || ttl > 30*time.Second {
		t.Errorf("TTL del código en Redis: %v, quiere (0, 30s]", ttl)
	}
	if tok, ok := Redeem(ctx, "acme", code); !ok || tok != "jwt" {
		t.Fatalf("primer canje: %q %v", tok, ok)
	}
	if _, ok := Redeem(ctx, "acme", code); ok {
		t.Fatal("doble canje con Redis")
	}
	if len(mr.Keys()) != 0 {
		t.Errorf("tras el canje no debe quedar la clave: %v", mr.Keys())
	}
	// Otro tenant: falla y quema.
	c2, _ := Issue(ctx, "acme", "jwt2")
	if _, ok := Redeem(ctx, "otra", c2); ok {
		t.Fatal("canje cross-tenant con Redis")
	}
	if _, ok := Redeem(ctx, "acme", c2); ok {
		t.Fatal("el código debía quedar quemado")
	}
}
