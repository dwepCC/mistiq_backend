package saas

import (
	"testing"
	"time"

	"tukifac/pkg/database"
)

// Búsqueda por empresa/RUC y rango de fechas (due_date), más paginación — lo que necesita el
// panel central para "ver pagos pendientes" de una empresa concreta sin traer todo el listado.
func TestListInvoices_searchByTenantAndDateRange(t *testing.T) {
	db := setupApprovePaymentDB(t)

	acme := database.Tenant{Name: "ACME SAC", Slug: "acme", RUC: "20123456789", Status: database.TenantStatusActive}
	db.Create(&acme)
	otra := database.Tenant{Name: "Otra Empresa", Slug: "otra", RUC: "10987654321", Status: database.TenantStatusActive}
	db.Create(&otra)

	mk := func(tenantID uint, due time.Time) database.SaasBillingCycle {
		return database.SaasBillingCycle{
			TenantID: tenantID, SubscriptionID: 1, PlanID: 1,
			PeriodStart: due, PeriodEnd: due.AddDate(0, 1, 0), DueDate: due,
			Amount: 99, Currency: "PEN", Status: database.SaasInvoicePending,
		}
	}
	c1 := mk(acme.ID, time.Date(2026, 8, 10, 0, 0, 0, 0, lima()))
	db.Create(&c1)
	c2 := mk(acme.ID, time.Date(2026, 9, 20, 0, 0, 0, 0, lima()))
	db.Create(&c2)
	c3 := mk(otra.ID, time.Date(2026, 8, 15, 0, 0, 0, 0, lima()))
	db.Create(&c3)

	t.Run("busca por nombre de empresa", func(t *testing.T) {
		rows, total, err := ListInvoices(ListInvoicesParams{Query: "ACME"})
		if err != nil {
			t.Fatalf("ListInvoices: %v", err)
		}
		if total != 2 || len(rows) != 2 {
			t.Fatalf("total=%d len=%d, se esperaban 2 (solo ACME)", total, len(rows))
		}
	})

	t.Run("busca por RUC", func(t *testing.T) {
		rows, total, err := ListInvoices(ListInvoicesParams{Query: "10987654321"})
		if err != nil {
			t.Fatalf("ListInvoices: %v", err)
		}
		if total != 1 || len(rows) != 1 || rows[0].TenantRUC != "10987654321" {
			t.Fatalf("total=%d len=%d, se esperaba 1 (Otra Empresa por RUC)", total, len(rows))
		}
	})

	t.Run("filtra por rango de fechas (due_date)", func(t *testing.T) {
		rows, total, err := ListInvoices(ListInvoicesParams{DateFrom: "2026-08-01", DateTo: "2026-08-31"})
		if err != nil {
			t.Fatalf("ListInvoices: %v", err)
		}
		if total != 2 || len(rows) != 2 {
			t.Fatalf("total=%d len=%d, se esperaban 2 (los que vencen en agosto)", total, len(rows))
		}
	})

	t.Run("pagina resultados", func(t *testing.T) {
		// pagination.Normalize solo acepta 10/20/25/50/100 como per_page — con 3 filas en total
		// alcanza y sobra para probar el corte real de la página.
		rows, total, err := ListInvoices(ListInvoicesParams{Page: 1, PerPage: 10})
		if err != nil {
			t.Fatalf("ListInvoices: %v", err)
		}
		if total != 3 || len(rows) != 3 {
			t.Fatalf("total=%d len=%d, se esperaban 3 en total", total, len(rows))
		}

		// Se agregan 8 cobros más (11 en total) para que per_page=10 sí recorte la página 1.
		for i := 0; i < 8; i++ {
			c := mk(acme.ID, time.Date(2026, 12, 1+i, 0, 0, 0, 0, lima()))
			db.Create(&c)
		}

		page1, total1, err := ListInvoices(ListInvoicesParams{Page: 1, PerPage: 10})
		if err != nil {
			t.Fatalf("ListInvoices página 1: %v", err)
		}
		if total1 != 11 || len(page1) != 10 {
			t.Fatalf("página 1: total=%d len=%d, se esperaban total=11 len=10", total1, len(page1))
		}
		page2, total2, err := ListInvoices(ListInvoicesParams{Page: 2, PerPage: 10})
		if err != nil {
			t.Fatalf("ListInvoices página 2: %v", err)
		}
		if total2 != 11 || len(page2) != 1 {
			t.Fatalf("página 2: total=%d len=%d, se esperaban total=11 len=1 (el restante)", total2, len(page2))
		}
	})
}
