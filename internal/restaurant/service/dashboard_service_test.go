package service

import "testing"

func TestDashboardDayKey(t *testing.T) {
	cases := map[string]string{
		"2026-05-26":           "2026-05-26",
		"2026-05-26T00:00:00Z": "2026-05-26",
		" 2026-05-26 ":         "2026-05-26",
	}
	for in, want := range cases {
		if got := dashboardDayKey(in); got != want {
			t.Fatalf("dashboardDayKey(%q) = %q, want %q", in, got, want)
		}
	}
}
