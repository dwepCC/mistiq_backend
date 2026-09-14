package service

import (
	"os"
	"testing"
	"time"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func setupPaymentServiceDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := "file:payment_service_test.db?_journal_mode=WAL&_busy_timeout=15000"
	t.Cleanup(func() { os.Remove("payment_service_test.db") })
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	database.CentralDB = db
	if err := db.AutoMigrate(&database.Tenant{}, &database.SaasPayment{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	for _, tbl := range []string{"saas_payments", "tenants"} {
		db.Exec("DELETE FROM " + tbl)
	}
	return db
}

// Búsqueda por empresa/RUC y rango de fechas (created_at), más paginación — mismo criterio que
// ListInvoices, para "ver pagos pendientes" de una empresa concreta sin traer todo el listado.
func TestPaymentServiceList_searchAndDateRange(t *testing.T) {
	db := setupPaymentServiceDB(t)

	acme := database.Tenant{Name: "ACME SAC", Slug: "acme", RUC: "20123456789", Status: database.TenantStatusActive}
	db.Create(&acme)
	otra := database.Tenant{Name: "Otra Empresa", Slug: "otra", RUC: "10987654321", Status: database.TenantStatusActive}
	db.Create(&otra)

	mk := func(tenantID uint, status string, created time.Time) database.SaasPayment {
		return database.SaasPayment{TenantID: tenantID, Amount: 99, Status: status, CreatedAt: created}
	}
	p1 := mk(acme.ID, database.SaasPayPendingReview, time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC))
	db.Create(&p1)
	p2 := mk(acme.ID, database.SaasPayApproved, time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC))
	db.Create(&p2)
	p3 := mk(otra.ID, database.SaasPayPendingReview, time.Date(2026, 8, 15, 10, 0, 0, 0, time.UTC))
	db.Create(&p3)

	svc := NewPaymentService()

	t.Run("busca por nombre de empresa", func(t *testing.T) {
		rows, total, err := svc.List(PaymentListParams{Query: "ACME"})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if total != 2 || len(rows) != 2 {
			t.Fatalf("total=%d len=%d, se esperaban 2 (solo ACME)", total, len(rows))
		}
	})

	t.Run("busca por RUC", func(t *testing.T) {
		rows, total, err := svc.List(PaymentListParams{Query: "10987654321"})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if total != 1 || len(rows) != 1 || rows[0].TenantRUC != "10987654321" {
			t.Fatalf("total=%d len=%d, se esperaba 1 (Otra Empresa por RUC)", total, len(rows))
		}
	})

	t.Run("combina status con búsqueda sin ambigüedad de columna", func(t *testing.T) {
		rows, total, err := svc.List(PaymentListParams{Status: "pending_review", Query: "ACME"})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if total != 1 || len(rows) != 1 {
			t.Fatalf("total=%d len=%d, se esperaba 1 (ACME en pending_review)", total, len(rows))
		}
	})

	t.Run("filtra por rango de fechas (created_at)", func(t *testing.T) {
		rows, total, err := svc.List(PaymentListParams{DateFrom: "2026-08-01", DateTo: "2026-08-31"})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if total != 2 || len(rows) != 2 {
			t.Fatalf("total=%d len=%d, se esperaban 2 (los de agosto)", total, len(rows))
		}
	})

	t.Run("pagina resultados", func(t *testing.T) {
		for i := 0; i < 8; i++ {
			p := mk(acme.ID, database.SaasPayApproved, time.Date(2026, 12, 1+i, 10, 0, 0, 0, time.UTC))
			db.Create(&p)
		}
		page1, total1, err := svc.List(PaymentListParams{Page: 1, PerPage: 10})
		if err != nil {
			t.Fatalf("List página 1: %v", err)
		}
		if total1 != 11 || len(page1) != 10 {
			t.Fatalf("página 1: total=%d len=%d, se esperaban total=11 len=10", total1, len(page1))
		}
		page2, total2, err := svc.List(PaymentListParams{Page: 2, PerPage: 10})
		if err != nil {
			t.Fatalf("List página 2: %v", err)
		}
		if total2 != 11 || len(page2) != 1 {
			t.Fatalf("página 2: total=%d len=%d, se esperaban total=11 len=1", total2, len(page2))
		}
	})
}
