package saas

import (
	"sync"
	"time"
)

// tenantViewCacheTTL ventana breve de caché para GetTenantView.
//
// Cada request ERP evaluaba GetTenantView dos veces (TenantAuthAPI + SubscriptionGate), y cada
// evaluación hace ~7-8 consultas a la BD central (settings, tenant, suscripción, pagos, plan,
// ciclo). En el burst de arranque eso saturaba el pool central. Con esta ventana, solo la primera
// evaluación por tenant consulta la BD; el resto (dentro de la ventana) usa el valor cacheado.
//
// La ventana es corta a propósito y, sobre todo, se invalida de inmediato en cualquier cambio de
// suscripción/pago/estado vía InvalidateTenantCache. Por eso un tenant bloqueado o vencido se
// refleja al instante en esos flujos; la ventana solo aplica mientras el estado no cambia.
const tenantViewCacheTTL = 10 * time.Second

type cachedTenantView struct {
	view      TenantSubscriptionView
	expiresAt time.Time
}

var (
	tenantViewCacheMu sync.RWMutex
	tenantViewCache   = make(map[uint]cachedTenantView)
)

func getCachedTenantView(tenantID uint) (TenantSubscriptionView, bool) {
	tenantViewCacheMu.RLock()
	entry, ok := tenantViewCache[tenantID]
	tenantViewCacheMu.RUnlock()
	if !ok || time.Now().After(entry.expiresAt) {
		return TenantSubscriptionView{}, false
	}
	return entry.view, true
}

func setCachedTenantView(tenantID uint, v TenantSubscriptionView) {
	tenantViewCacheMu.Lock()
	tenantViewCache[tenantID] = cachedTenantView{view: v, expiresAt: time.Now().Add(tenantViewCacheTTL)}
	tenantViewCacheMu.Unlock()
}

// clearCachedTenantView descarta el valor cacheado de un tenant. Se llama desde
// InvalidateTenantCache para que cambios de suscripción/pago/estado se reflejen sin esperar el TTL.
func clearCachedTenantView(tenantID uint) {
	tenantViewCacheMu.Lock()
	delete(tenantViewCache, tenantID)
	tenantViewCacheMu.Unlock()
}
