package fiscal

import "testing"

func TestResolvePSEBaseURL_validapse(t *testing.T) {
	if got := ResolvePSEBaseURL("validapse"); got != "https://app.validapse.com" {
		t.Fatalf("validapse URL = %q", got)
	}
}

func TestResolvePSEBaseURL_sunatNotPseProvider(t *testing.T) {
	if got := ResolvePSEBaseURL("sunat"); got != "" {
		t.Fatalf("sunat no es proveedor PSE, URL debe ser vacía: %q", got)
	}
}

func TestNormalizePSEProvider_defaults(t *testing.T) {
	if got := NormalizePSEProvider(""); got != "validapse" {
		t.Fatalf("empty -> validapse, got %q", got)
	}
	if got := NormalizePSEProvider("sunat"); got != "sunat" {
		t.Fatalf("sunat se conserva, got %q", got)
	}
}
