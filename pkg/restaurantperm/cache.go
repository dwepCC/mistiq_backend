package restaurantperm

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"tukifac/pkg/tenantcache"
)

const cacheTTL = 15 * time.Minute

// PermissionSchemaVersion incrementar al cambiar plantillas EmployeeTypeToKeys / LegacyRoleToKeys.
const PermissionSchemaVersion uint = 2

type memEntry struct {
	keys    []string
	expires time.Time
}

var (
	memMu sync.RWMutex
	mem   = map[string]memEntry{}
)

// cacheKey namespaced por slug (evita colisión tenantID=0 o IDs reutilizados entre tenants).
func cacheKey(tenantSlug string, tenantID, userID, version uint) string {
	slug := strings.TrimSpace(tenantSlug)
	if slug == "" {
		slug = fmt.Sprintf("id%d", tenantID)
	}
	return fmt.Sprintf("%stenant:%s:rp:%d:%d:v%d:ps%d", tenantcache.TenantKeyPrefix, slug, tenantID, userID, version, PermissionSchemaVersion)
}

func memKey(tenantSlug string, tenantID, userID, version uint) string {
	return cacheKey(tenantSlug, tenantID, userID, version)
}

// GetCached devuelve permisos cacheados (Redis primero, memoria local).
func GetCached(tenantSlug string, tenantID, userID, version uint) ([]string, bool) {
	key := cacheKey(tenantSlug, tenantID, userID, version)
	if rdb := tenantcache.RDB(); rdb != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		raw, err := rdb.Get(ctx, key).Bytes()
		if err == nil && len(raw) > 0 {
			var keys []string
			if json.Unmarshal(raw, &keys) == nil {
				return keys, true
			}
		}
	}
	memMu.RLock()
	e, ok := mem[memKey(tenantSlug, tenantID, userID, version)]
	memMu.RUnlock()
	if ok && time.Now().Before(e.expires) {
		return e.keys, true
	}
	return nil, false
}

// SetCached guarda permisos en Redis y memoria.
func SetCached(tenantSlug string, tenantID, userID, version uint, keys []string) {
	key := cacheKey(tenantSlug, tenantID, userID, version)
	if rdb := tenantcache.RDB(); rdb != nil {
		raw, _ := json.Marshal(keys)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = rdb.Set(ctx, key, raw, cacheTTL).Err()
	}
	memMu.Lock()
	mem[memKey(tenantSlug, tenantID, userID, version)] = memEntry{keys: keys, expires: time.Now().Add(cacheTTL)}
	memMu.Unlock()
}

// InvalidateUser borra entradas conocidas de versión (best-effort).
func InvalidateUser(tenantSlug string, tenantID, userID, version uint) {
	key := cacheKey(tenantSlug, tenantID, userID, version)
	if rdb := tenantcache.RDB(); rdb != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = rdb.Del(ctx, key).Err()
	}
	memMu.Lock()
	delete(mem, memKey(tenantSlug, tenantID, userID, version))
	memMu.Unlock()
}

// InvalidateTenantMem limpia entradas en memoria del proceso para un slug (best-effort).
func InvalidateTenantMem(tenantSlug string) {
	tenantSlug = strings.TrimSpace(tenantSlug)
	if tenantSlug == "" {
		return
	}
	prefix := fmt.Sprintf("%stenant:%s:rp:", tenantcache.TenantKeyPrefix, tenantSlug)
	memMu.Lock()
	for k := range mem {
		if strings.HasPrefix(k, prefix) {
			delete(mem, k)
		}
	}
	memMu.Unlock()
}
