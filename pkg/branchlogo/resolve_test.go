package branchlogo

import "testing"

func TestResolvePrintLogo(t *testing.T) {
	cases := []struct {
		name                           string
		bURL, bData, cURL, cData, want string
	}{
		{"sucursal con logo embebido", "/u/b.png", "data:b", "/u/c.png", "data:c", "data:b"},
		{"sucursal con logo no embebible conserva su URL", "/u/b.png", "", "/u/c.png", "data:c", "/u/b.png"},
		{"sucursal sin logo usa empresa embebida", "", "", "/u/c.png", "data:c", "data:c"},
		{"sucursal sin logo, empresa no embebible", "  ", "", "/u/c.png", "", "/u/c.png"},
		{"nada", "", "", "", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ResolvePrintLogo(tc.bURL, tc.bData, tc.cURL, tc.cData); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}
