package tax

import "testing"

func TestServiceChargeAmount(t *testing.T) {
	cases := []struct {
		base, rate, want float64
	}{
		{100, 10, 10},
		{100, 13, 13},
		{84.75, 10, 8.48},  // 8.475 → redondeo SUNAT
		{100, 13.01, 0},    // por encima del tope legal: no se cobra
		{100, 0, 0},
		{100, -5, 0},
		{0, 10, 0},
		{-10, 10, 0},
	}
	for _, c := range cases {
		if got := ServiceChargeAmount(c.base, c.rate); got != c.want {
			t.Errorf("ServiceChargeAmount(%v, %v) = %v, quiero %v", c.base, c.rate, got, c.want)
		}
	}
	if !ValidServiceChargeRate(13) || ValidServiceChargeRate(13.5) || ValidServiceChargeRate(0) {
		t.Error("ValidServiceChargeRate fuera del rango 0 < r <= 13")
	}
}
