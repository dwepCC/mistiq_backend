package utils

import "testing"

func TestExtractSubdomainTukifac(t *testing.T) {
	root := "bendey.cloud"
	cases := []struct {
		host string
		want string
	}{
		{"tenant1.bendey.cloud", "tenant1"},
		{"api.bendey.cloud", "api"},
		{"app.bendey.cloud", "app"},
		{"www.bendey.cloud", "www"},
		{"bendey.cloud", ""},
		{"localhost", ""},
		{"empresa.localhost", "empresa"},
	}
	for _, tc := range cases {
		if got := ExtractSubdomain(tc.host, root); got != tc.want {
			t.Errorf("ExtractSubdomain(%q, %q) = %q, want %q", tc.host, root, got, tc.want)
		}
	}
}
