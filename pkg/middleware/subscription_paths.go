package middleware

import (
	"strings"

	"tukifac/pkg/database"
)

// Rutas permitidas sin suscripción operativa activa (pagos / resumen).
var subscriptionExemptPrefixes = []string{
	"/api/login",
	"/api/subscription",
	"/health",
	"/metrics",
}

// IsSubscriptionExemptPath indica si la ruta es del módulo de pagos/suscripción o login.
func IsSubscriptionExemptPath(path string) bool {
	path = strings.TrimSpace(path)
	for _, p := range subscriptionExemptPrefixes {
		if strings.HasPrefix(path, p) {
			return true
		}
	}
	return false
}

// reportsReadPaths rutas de LECTURA (GET, coincidencia exacta) que usa el módulo de Reportes y su
// cascarón (menú, cabecera, permisos). Con la cuenta suspendida por falta de pago el ERP queda cerrado,
// salvo esto: el cliente puede seguir consultando sus reportes mientras regulariza. Todo lo que no esté
// aquí sigue respondiendo 402.
var reportsReadPaths = map[string]struct{}{
	// Cascarón: configuración de la empresa, módulos y permisos de la sesión.
	"/api/company/config":              {},
	"/api/company/sunat":               {},
	"/api/company/series":              {},
	"/api/company/branches":            {},
	"/api/session/modules":             {},
	"/api/session/capabilities":        {},
	"/api/billing/notification-counts": {},
	// Datos de los reportes.
	"/api/sales":                      {},
	"/api/sales/by-product":           {},
	"/api/sales/profit-detail":        {},
	"/api/purchases":                  {},
	"/api/products":                   {},
	"/api/categories":                 {},
	"/api/payment-methods":            {},
	"/api/inventory/movements":        {},
	"/api/inventory/operation-types":  {},
	"/api/cashbank/reports/movements": {},
}

// IsReportsReadPath true si la petición es una lectura (GET) permitida del módulo de Reportes.
func IsReportsReadPath(method, path string) bool {
	if !strings.EqualFold(method, "GET") {
		return false
	}
	_, ok := reportsReadPaths[strings.TrimRight(strings.TrimSpace(path), "/")]
	return ok
}

// IsSubscriptionHubPath rutas del Billing Hub (/api/subscription/*).
func IsSubscriptionHubPath(path string) bool {
	return strings.HasPrefix(strings.TrimSpace(path), "/api/subscription")
}

// IsSubscriptionPaymentSubmit POST comprobante SaaS.
func IsSubscriptionPaymentSubmit(path, method string) bool {
	return strings.EqualFold(method, "POST") && strings.HasPrefix(strings.TrimSpace(path), "/api/subscription/payments")
}

// TenantAllowsBillingHubRead suspended/blocked/active pueden leer hub.
func TenantAllowsBillingHubRead(tenant *database.Tenant) bool {
	if tenant == nil {
		return false
	}
	switch tenant.Status {
	case database.TenantStatusActive, database.TenantStatusSuspended, database.TenantStatusBlocked:
		return true
	default:
		return false
	}
}
