package receiptdesign

import "testing"

func TestNormalizeA4(t *testing.T) {
	cases := map[string]string{
		"":                                     DefaultA4,
		"  ":                                   DefaultA4,
		"classic":                              "classic",
		" Classic ":                            "classic",
		"modern":                               "modern",
		"compact_2":                            "compact_2",
		"con espacio":                          DefaultA4,
		"'; DROP TABLE x;--":                   DefaultA4,
		"áéí":                                  DefaultA4,
		"abcdefghijklmnopqrstuvwxyz0123456789": DefaultA4, // > 30
	}
	for in, want := range cases {
		if got := NormalizeA4(in); got != want {
			t.Errorf("NormalizeA4(%q) = %q, quiero %q", in, got, want)
		}
	}
}
