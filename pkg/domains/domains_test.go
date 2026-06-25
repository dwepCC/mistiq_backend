package domains

import "testing"

func TestTenantHost(t *testing.T) {
	if got := TenantHost("doricontdemo", "tukifac.com"); got != "doricontdemo.tukifac.com" {
		t.Fatalf("got %q", got)
	}
	if got := TenantURL("doricontdemo", "tukifac.com"); got != "https://doricontdemo.tukifac.com" {
		t.Fatalf("got %q", got)
	}
	if got := TenantHost("demo", "localhost"); got != "" {
		t.Fatalf("localhost host should be empty, got %q", got)
	}
}

func TestResolveTenantAPIURL(t *testing.T) {
	if got := ResolveTenantAPIURL("demo", "tukifac.com", "http://localhost:3000"); got != "https://demo.tukifac.com" {
		t.Fatalf("prod: got %q", got)
	}
	if got := ResolveTenantAPIURL("demo", "localhost", "http://localhost:3000"); got != "http://localhost:3000" {
		t.Fatalf("local fallback: got %q", got)
	}
}
