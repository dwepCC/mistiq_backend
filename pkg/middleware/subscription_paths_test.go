package middleware

import "testing"

// Con la cuenta suspendida solo se abre la LECTURA de lo que usa el módulo de Reportes; nada de
// escritura ni de otras pantallas del ERP.
func TestIsReportsReadPath(t *testing.T) {
	cases := []struct {
		method, path string
		want         bool
	}{
		{"GET", "/api/sales", true},
		{"GET", "/api/sales/", true},
		{"GET", "/api/sales/by-product", true},
		{"GET", "/api/sales/profit-detail", true},
		{"GET", "/api/purchases", true},
		{"GET", "/api/inventory/movements", true},
		{"GET", "/api/cashbank/reports/movements", true},
		{"GET", "/api/company/config", true},
		{"get", "/api/products", true},
		// Escrituras: nunca.
		{"POST", "/api/sales", false},
		{"PUT", "/api/company/config", false},
		{"DELETE", "/api/products", false},
		// Lecturas fuera del módulo de Reportes: siguen cerradas.
		{"GET", "/api/sales/12", false},
		{"GET", "/api/sales/12/pdf", false},
		{"GET", "/api/users", false},
		{"GET", "/api/cashbank/sessions", false},
		{"GET", "/api/billing/invoices", false},
		{"GET", "/api/dashboard/summary", false},
		{"GET", "/api/products/302/sale-units/1", false},
	}
	for _, c := range cases {
		if got := IsReportsReadPath(c.method, c.path); got != c.want {
			t.Errorf("IsReportsReadPath(%s %s) = %v, want %v", c.method, c.path, got, c.want)
		}
	}
}
