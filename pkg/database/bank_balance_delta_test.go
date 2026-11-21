package database

import "testing"

func TestBankMovementBalanceDelta(t *testing.T) {
	pen := 380.0
	cases := []struct {
		name  string
		m     TenantBankMovement
		delta float64
		want  float64
		ok    bool
	}{
		{"soles sin snapshot", TenantBankMovement{Amount: 50}, 50, 50, true},
		{"USD con TC suma el equivalente", TenantBankMovement{Amount: 100, Currency: "USD", AmountPEN: &pen}, 100, 380, true},
		{"USD con TC resta el equivalente", TenantBankMovement{Amount: 100, Currency: "USD", AmountPEN: &pen}, -100, -380, true},
		{"USD sin TC no se puede valuar", TenantBankMovement{Amount: 100, Currency: "USD"}, 100, 100, false},
	}
	for _, c := range cases {
		got, ok := c.m.BalanceDelta(c.delta)
		if got != c.want || ok != c.ok {
			t.Errorf("%s: got (%v,%v) want (%v,%v)", c.name, got, ok, c.want, c.ok)
		}
	}
}
