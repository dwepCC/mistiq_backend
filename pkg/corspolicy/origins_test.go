package corspolicy

import (
	"testing"

	"tukifac/config"
	"tukifac/pkg/domains"
)

func TestMatcherTukifacRootDomain(t *testing.T) {
	cfg := &config.Config{
		AppEnv:             "production",
		AppDomain:          "bendey.cloud",
		APIPublicURL:       "https://api.bendey.cloud",
		FrontendURL:        "https://app.bendey.cloud",
		CentralFrontendURL: "https://app.bendey.cloud",
		ReservedSubdomains: domains.MergeReserved(nil),
	}
	m := NewMatcher(cfg)

	for _, o := range []string{
		"https://app.bendey.cloud",
		"https://api.bendey.cloud",
		"https://tenant1.bendey.cloud",
		"https://empresa.bendey.cloud",
	} {
		if !m.Allow(o) {
			t.Errorf("expected allowed: %s", o)
		}
	}

	for _, o := range []string{
		"https://app.tukifac.cloud",
		"http://app.bendey.cloud",
	} {
		if m.Allow(o) {
			t.Errorf("expected denied: %s", o)
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
		"http://angel.localhost:5173",
		"http://empresa.localhost:5174",
	} {
		if !m.Allow(o) {
			t.Errorf("expected allowed in dev: %s", o)
		}
	}
}

func TestMatcherProductionAllowsNativeShell(t *testing.T) {
	cfg := &config.Config{
		AppEnv:             "production",
		AppDomain:          "bendey.cloud",
		APIPublicURL:       "https://api.bendey.cloud",
		FrontendURL:        "https://app.bendey.cloud",
		CentralFrontendURL: "https://app.bendey.cloud",
		ReservedSubdomains: domains.MergeReserved(nil),
	}
	m := NewMatcher(cfg)

	for _, o := range []string{
		"tauri://localhost",
		"https://tauri.localhost",
		"https://localhost",
		"capacitor://localhost",
	} {
		if !m.Allow(o) {
			t.Errorf("expected native shell allowed in production: %s", o)
		}
	}
}

func TestMatcherProductionDeniesLocalhostSubdomains(t *testing.T) {
	cfg := &config.Config{
		AppEnv:             "production",
		AppDomain:          "bendey.cloud",
		APIPublicURL:       "https://api.bendey.cloud",
		FrontendURL:        "https://app.bendey.cloud",
		CentralFrontendURL: "https://app.bendey.cloud",
		ReservedSubdomains: domains.MergeReserved(nil),
	}
	m := NewMatcher(cfg)
	if m.Allow("http://angel.localhost:5173") {
		t.Error("production must not allow angel.localhost")
	}
}
