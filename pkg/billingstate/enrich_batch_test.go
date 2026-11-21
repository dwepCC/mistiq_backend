package billingstate

import (
	"fmt"
	"testing"
	"time"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// EnrichSalesBillingStatus corrige en memoria y persiste los desvíos agrupados por estado destino.
func TestEnrichSalesBillingStatus_PersisteDesviosEnLote(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&database.TenantSale{}, &database.TenantInvoice{}); err != nil {
		t.Fatal(err)
	}
	mk := func(num string, billing string, pipeline string) uint {
		s := database.TenantSale{SeriesID: 1, DocType: "BOLETA", Number: num, BranchID: 1, UserID: 1, IssueDate: time.Now(),
			Total: 10, Status: "paid", PaymentMethod: "cash", BillingStatus: billing}
		if err := db.Create(&s).Error; err != nil {
			t.Fatal(err)
		}
		if pipeline != "" {
			inv := database.TenantInvoice{SaleID: s.ID, PipelineStatus: pipeline}
			if err := db.Create(&inv).Error; err != nil {
				t.Fatal(err)
			}
		}
		return s.ID
	}
	// Dos ventas aceptadas por SUNAT que seguían "pending" en la cabecera, una ya consistente y una
	// sin invoice (queda pending).
	a1 := mk("1", BillingPending, SUNAT_ACCEPTED)
	a2 := mk("2", BillingPending, SUNAT_ACCEPTED)
	ok := mk("3", BillingAccepted, SUNAT_ACCEPTED)
	none := mk("4", BillingPending, "")

	var sales []database.TenantSale
	if err := db.Order("id").Find(&sales).Error; err != nil {
		t.Fatal(err)
	}
	EnrichSalesBillingStatus(db, sales)

	want := map[uint]string{a1: BillingAccepted, a2: BillingAccepted, ok: BillingAccepted, none: BillingPending}
	for _, s := range sales {
		if s.BillingStatus != want[s.ID] {
			t.Errorf("en memoria venta %d = %s, se esperaba %s", s.ID, s.BillingStatus, want[s.ID])
		}
	}
	var persisted []database.TenantSale
	if err := db.Order("id").Find(&persisted).Error; err != nil {
		t.Fatal(err)
	}
	for _, s := range persisted {
		if s.BillingStatus != want[s.ID] {
			t.Errorf("persistido venta %d = %s, se esperaba %s", s.ID, s.BillingStatus, want[s.ID])
		}
	}
}
