package sunat

import "testing"

func TestNormalizeUnit(t *testing.T) {
	cases := []struct {
		unit, typ, want string
	}{
		{"", "product", "NIU"},
		{"UND", "product", "NIU"},
		{"und", "product", "NIU"},
		{"NIU", "product", "NIU"},
		{"KGM", "product", "KGM"},
		{"LT", "product", "LTR"},
		{"lt", "product", "LTR"},
		{"LITRO", "product", "LTR"},
		{"KG", "product", "KGM"},
		{"CAJ", "product", "BX"},
		{"CJA", "product", "BX"},
		{"PAQ", "product", "PK"},
		{"INVALIDO_XYZ", "product", "NIU"},
		{"", "service", "ZZ"},
		{"UND", "service", "ZZ"},
		{"NIU", "service", "ZZ"},
		{"LT", "service", "ZZ"},
		{"ZZ", "", "ZZ"},
		{"UND", "", "NIU"},
	}
	for _, c := range cases {
		if got := NormalizeUnit(c.unit, c.typ); got != c.want {
			t.Fatalf("NormalizeUnit(%q, %q) = %q, want %q", c.unit, c.typ, got, c.want)
		}
	}
}

func TestNormalizeUnit_LT_not_passed_through(t *testing.T) {
	if got := NormalizeUnit("LT", "product"); got != "LTR" {
		t.Fatalf("LT must map to LTR for SUNAT, got %q", got)
	}
}

func TestNormalizeUnit_CJA_PAQ_mapToCatalog(t *testing.T) {
	if got := NormalizeUnit("CJA", "product"); got != "BX" {
		t.Fatalf("CJA → BX, got %q", got)
	}
	if got := NormalizeUnit("PAQ", "product"); got != "PK" {
		t.Fatalf("PAQ → PK, got %q", got)
	}
	if got := NormalizeUnit("BLT", "product"); got != "BE" {
		t.Fatalf("BLT → BE, got %q", got)
	}
}

func TestIsValidUnitCode_catalog03(t *testing.T) {
	for _, code := range []string{"LTR", "NIU", "BX", "PK", "BE"} {
		if !IsValidUnitCode(code) {
			t.Fatalf("%q should be valid catalog 03", code)
		}
	}
	for _, code := range []string{"LT", "CJA", "PAQ", "BLT"} {
		if IsValidUnitCode(code) {
			t.Fatalf("%q should NOT be valid catalog 03", code)
		}
	}
}

func TestSystemUnitCodes_allInCatalog03(t *testing.T) {
	for _, code := range SystemUnitCodes() {
		if !IsValidUnitCode(code) {
			t.Fatalf("system unit %q not in catalog 03", code)
		}
	}
}
