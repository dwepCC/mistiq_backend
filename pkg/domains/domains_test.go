package domains

import "testing"

func TestTenantHost(t *testing.T) {
	if got := TenantHost("doricontdemo", "bendey.cloud"); got != "doricontdemo.bendey.cloud" {
		t.Fatalf("got %q", got)
	}
	if got := TenantURL("doricontdemo", "bendey.cloud"); got != "https://doricontdemo.bendey.cloud" {
		t.Fatalf("got %q", got)
	}
	if got := TenantHost("demo", "localhost"); got != "" {
		t.Fatalf("localhost host should be empty, got %q", got)
	}
}

func TestResolveTenantAPIURL(t *testing.T) {
	if got := ResolveTenantAPIURL("demo", "bendey.cloud", "http://localhost:3000"); got != "https://demo.bendey.cloud" {
		t.Fatalf("prod: got %q", got)
	}
	if got := ResolveTenantAPIURL("demo", "localhost", "http://localhost:3000"); got != "http://localhost:3000" {
		t.Fatalf("local fallback: got %q", got)
	}
}
