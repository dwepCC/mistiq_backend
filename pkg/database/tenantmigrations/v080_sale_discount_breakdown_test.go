package tenantmigrations

import "testing"

func TestV080SaleDiscountBreakdown_Idempotent(t *testing.T) {
	mig := V080SaleDiscountBreakdown{}
	if mig.Version() != 80 {
		t.Fatalf("version=%d", mig.Version())
	}
	if mig.Name() != "sale_discount_breakdown" {
		t.Fatalf("name=%s", mig.Name())
	}
}
