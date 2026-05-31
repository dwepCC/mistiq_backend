package middleware

import (
	"testing"

	"tukifac/config"
)

func TestResolveTenantSlug_prodSubdomainWins(t *testing.T) {
	cfg := &config.Config{AppEnv: "production", AppDomain: "bendey.cloud"}
	slug, reason := resolveTenantSlug("empresa1.bendey.cloud", "empresa1", "", "/api/products", cfg)
	if reason != "" || slug != "empresa1" {
		t.Fatalf("got slug=%q reason=%q", slug, reason)
	}
}

func TestResolveTenantSlug_prodHeaderMismatchBlocked(t *testing.T) {
	cfg := &config.Config{AppEnv: "production", AppDomain: "bendey.cloud"}
	_, reason := resolveTenantSlug("empresa1.bendey.cloud", "empresa2", "", "/api/products", cfg)
	if reason != "header_subdomain_mismatch" {
		t.Fatalf("expected mismatch, got %q", reason)
	}
}

func TestResolveTenantSlug_prodAppSameSlug(t *testing.T) {
	cfg := &config.Config{AppEnv: "production", AppDomain: "bendey.cloud"}
	slug, reason := resolveTenantSlug("empresa1.bendey.cloud", "empresa1", "", "/api/login", cfg)
	if reason != "" || slug != "empresa1" {
		t.Fatalf("app flow failed slug=%q reason=%q", slug, reason)
	}
}

func TestResolveTenantSlug_devHeaderFirst(t *testing.T) {
	cfg := &config.Config{AppEnv: "development", AppDomain: "localhost"}
	slug, _ := resolveTenantSlug("localhost:5175", "demo", "", "/api/login", cfg)
	if slug != "demo" {
		t.Fatalf("expected demo, got %q", slug)
	}
}

func TestResolveTenantSlug_centralHostLoginFallback(t *testing.T) {
	cfg := &config.Config{
		AppEnv:             "production",
		AppDomain:          "bendey.cloud",
		ReservedSubdomains: []string{"api", "app", "www", "admin", "central"},
	}
	slug, reason := resolveTenantSlug("api.bendey.cloud", "empresa1", "", "/api/login", cfg)
	if slug != "empresa1" || reason != "central_host_header_fallback" {
		t.Fatalf("legacy fallback slug=%q reason=%q", slug, reason)
	}
}

func TestValidateTenantJWTClaims_rejectsLegacy(t *testing.T) {
	config.AppConfig = &config.Config{AppEnv: "production"}
	if err := validateTenantJWTClaims(&TenantClaims{TenantID: 0, TenantSlug: "a", TenantDB: "b"}); err == nil {
		t.Fatal("expected error for missing tenant_id")
	}
	if err := validateTenantJWTClaims(&TenantClaims{TenantID: 1, TenantSlug: "a", TenantDB: "b", TenantVersion: 0}); err == nil {
		t.Fatal("expected error for missing tenant_version in prod")
	}
	if err := validateTenantJWTClaims(&TenantClaims{TenantID: 1, TenantSlug: "a", TenantDB: "b", TenantVersion: 1}); err != nil {
		t.Fatalf("valid token rejected: %v", err)
	}
}
