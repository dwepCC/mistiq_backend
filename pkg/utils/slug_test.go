package utils

import "testing"

func TestExtractSubdomainTukifac(t *testing.T) {
	root := "tukifac.com"
	cases := []struct {
		host string
		want string
	}{
		{"tenant1.tukifac.com", "tenant1"},
		{"api.tukifac.com", "api"},
		{"app.tukifac.com", "app"},
		{"www.tukifac.com", "www"},
		{"tukifac.com", ""},
		{"localhost", ""},
		{"empresa.localhost", "empresa"},
	}
	for _, tc := range cases {
		if got := ExtractSubdomain(tc.host, root); got != tc.want {
			t.Errorf("ExtractSubdomain(%q, %q) = %q, want %q", tc.host, root, got, tc.want)
		}
	}
}
