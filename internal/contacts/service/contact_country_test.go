package service

import "testing"

func TestNormalizeCountry(t *testing.T) {
	for in, want := range map[string]string{"us": "US", " co ": "CO", "PE": "", "pe": "", "": "", "USA": "", "1a": "", "É1": ""} {
		if got := NormalizeCountry(in); got != want {
			t.Errorf("NormalizeCountry(%q)=%q, want %q", in, got, want)
		}
	}
}
