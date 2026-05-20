package corspolicy

import (
	"testing"

	"tukifac/config"
)

func TestMatcherProductionTukifac(t *testing.T) {
	cfg := &config.Config{
		AppEnv:             "production",
		AppDomain:          "api.tukifac.com",
		FrontendURL:        "https://app.tukifac.com",
		CentralFrontendURL: "https://app.tukifac.com",
	}
	m := NewMatcher(cfg)

	allowed := []string{
		"https://app.tukifac.com",
		"https://api.tukifac.com",
		"https://empresa.app.tukifac.com",
	}
	for _, o := range allowed {
		if !m.Allow(o) {
			t.Errorf("expected allowed: %s", o)
		}
	}

	denied := []string{
		"https://evil.tukifac.com",
		"https://app.tukifac.cloud",
		"http://app.tukifac.com",
		"",
	}
	for _, o := range denied {
		if m.Allow(o) {
			t.Errorf("expected denied: %q", o)
		}
	}
}

func TestMatcherDevLocalhost(t *testing.T) {
	cfg := &config.Config{
		AppEnv:             "development",
		AppDomain:          "localhost",
		FrontendURL:        "http://localhost:5173",
		CentralFrontendURL: "http://localhost:5174",
	}
	m := NewMatcher(cfg)

	for _, o := range []string{
		"http://localhost:3000",
		"http://localhost:5173",
		"http://127.0.0.1:5173",
	} {
		if !m.Allow(o) {
			t.Errorf("expected allowed in dev: %s", o)
		}
	}
}

func TestNormalizeOrigin(t *testing.T) {
	if got := normalizeOrigin("https://app.tukifac.com/"); got != "https://app.tukifac.com" {
		t.Fatalf("got %q", got)
	}
}
