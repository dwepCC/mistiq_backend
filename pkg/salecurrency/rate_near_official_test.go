package salecurrency

import (
	"testing"
	"time"

	"tukifac/pkg/dailyfx"
)

func TestEnsureRateNearOfficial(t *testing.T) {
	old := dailyfx.Lookup
	defer func() { dailyfx.Lookup = old }()
	dailyfx.Lookup = func(day string) (float64, bool, bool) { return 3.40, false, true }
	day := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	f := func(v float64) *float64 { return &v }

	if err := EnsureRateNearOfficial("USD", f(3.45), day); err != nil {
		t.Errorf("TC 3.45 (+1.5%%) debe pasar: %v", err)
	}
	if err := EnsureRateNearOfficial("USD", f(10), day); err == nil {
		t.Error("TC 10 debe rechazarse")
	}
	if err := EnsureRateNearOfficial("USD", f(1), day); err == nil {
		t.Error("TC 1 debe rechazarse")
	}
	if err := EnsureRateNearOfficial("PEN", f(10), day); err != nil {
		t.Errorf("en soles no aplica: %v", err)
	}
	dailyfx.Lookup = nil
	if err := EnsureRateNearOfficial("USD", f(10), day); err != nil {
		t.Errorf("sin proveedor de TC no aplica: %v", err)
	}
}
