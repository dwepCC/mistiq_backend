package tenantmigrations

import "testing"

func TestV081FinancialMovementReversal_Meta(t *testing.T) {
	mig := V081FinancialMovementReversal{}
	if mig.Version() != 81 {
		t.Fatalf("version=%d", mig.Version())
	}
	if mig.Name() != "financial_movement_reversal" {
		t.Fatalf("name=%s", mig.Name())
	}
}
