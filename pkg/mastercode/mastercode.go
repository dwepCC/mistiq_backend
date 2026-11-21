// Package mastercode implementa el código de canje de UN SOLO USO del acceso maestro (soporte).
//
// Antes el superadmin abría el ERP del tenant con una URL de página que llevaba el JWT completo (?master_sso=<jwt>): el token
// quedaba en el historial del navegador, en logs de proxy/CDN y podía filtrarse por Referer. Ahora la URL lleva solo un código
// aleatorio de 30 segundos y un solo uso; el ERP del tenant lo canjea por el JWT mediante POST (el token viaja en el cuerpo).
//
// El código se guarda como hash (SHA-256) junto con el slug del tenant al que pertenece: canjearlo en otro tenant lo quema y falla.
// Con Redis (producción) el canje es atómico (GETDEL) y vale entre instancias; sin Redis (desarrollo) se usa memoria local.
package mastercode

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
	"time"

	"tukifac/pkg/tenantcache"
)

// TTL vida del código: lo justo para abrir la pestaña y canjearlo.
var TTL = 30 * time.Second

const keyPrefix = "tukifac:master_code:"

type entry struct {
	Slug  string `json:"slug"`
	Token string `json:"token"`
}

type memEntry struct {
	entry
	exp time.Time
}

var (
	memMu sync.Mutex
	mem   = map[string]memEntry{}
)

func hashCode(code string) string {
	sum := sha256.Sum256([]byte(code))
	return hex.EncodeToString(sum[:])
}

// Issue guarda token asociado a slug y devuelve el código de un solo uso (32 bytes aleatorios, base64url).
func Issue(ctx context.Context, slug, token string) (string, error) {
	if slug == "" || token == "" {
		return "", errors.New("mastercode: slug y token requeridos")
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	code := base64.RawURLEncoding.EncodeToString(raw)
	key := hashCode(code)
	e := entry{Slug: slug, Token: token}

	if rdb := tenantcache.RDB(); rdb != nil {
		b, _ := json.Marshal(e)
		if err := rdb.Set(ctx, keyPrefix+key, b, TTL).Err(); err != nil {
			return "", err
		}
		return code, nil
	}
	memMu.Lock()
	defer memMu.Unlock()
	purgeLocked(time.Now())
	mem[key] = memEntry{entry: e, exp: time.Now().Add(TTL)}
	return code, nil
}

// Redeem canjea el código por el token. Un solo uso: tras el primer intento (válido o no) el código deja de existir.
// Devuelve ok=false si no existe, venció, ya se usó o pertenece a otro tenant.
func Redeem(ctx context.Context, slug, code string) (string, bool) {
	if slug == "" || code == "" || len(code) > 128 {
		return "", false
	}
	key := hashCode(code)

	var e entry
	if rdb := tenantcache.RDB(); rdb != nil {
		raw, err := rdb.GetDel(ctx, keyPrefix+key).Bytes()
		if err != nil || json.Unmarshal(raw, &e) != nil {
			return "", false
		}
	} else {
		memMu.Lock()
		me, ok := mem[key]
		delete(mem, key)
		memMu.Unlock()
		if !ok || time.Now().After(me.exp) {
			return "", false
		}
		e = me.entry
	}
	if e.Slug != slug {
		return "", false
	}
	return e.Token, true
}

func purgeLocked(now time.Time) {
	for k, v := range mem {
		if now.After(v.exp) {
			delete(mem, k)
		}
	}
}
