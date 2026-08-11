package tenantmigrations

import "testing"

func TestParseMariaDBVersion(t *testing.T) {
	tests := []struct {
		raw                 string
		major, minor, patch int
	}{
		{"10.11.6-MariaDB", 10, 11, 6},
		{"5.5.5-10.11.6-MariaDB", 10, 11, 6},
		{"10.5.22-MariaDB-1:10.5.22+maria~ubu2004", 10, 5, 22},
	}
	for _, tc := range tests {
		maj, min, pat := parseMariaDBVersion(tc.raw)
		if maj != tc.major || min != tc.minor || pat != tc.patch {
			t.Fatalf("%q: got %d.%d.%d want %d.%d.%d", tc.raw, maj, min, pat, tc.major, tc.minor, tc.patch)
		}
	}
}

func TestDBCapabilitiesSupports(t *testing.T) {
	cases := []struct {
		cap dbCapabilities
		fn  bool
		gen bool
	}{
		{dbCapabilities{Engine: engineMySQL, Major: 8, Minor: 0, Patch: 12}, false, true},
		{dbCapabilities{Engine: engineMySQL, Major: 8, Minor: 0, Patch: 13}, true, true},
		{dbCapabilities{Engine: engineMySQL, Major: 5, Minor: 7, Patch: 6}, false, true},
		{dbCapabilities{Engine: engineMariaDB, Major: 10, Minor: 5, Patch: 0}, false, true},
		{dbCapabilities{Engine: engineMariaDB, Major: 10, Minor: 6, Patch: 0}, true, true},
		{dbCapabilities{Engine: engineMariaDB, Major: 10, Minor: 1, Patch: 0}, false, false},
		{dbCapabilities{Engine: engineSQLite}, false, false},
	}
	for _, tc := range cases {
		if got := tc.cap.SupportsFunctionalPartialUnique(); got != tc.fn {
			t.Fatalf("%v functional: got %v want %v", tc.cap, got, tc.fn)
		}
		if got := tc.cap.SupportsGeneratedColumnUnique(); got != tc.gen {
			t.Fatalf("%v generated: got %v want %v", tc.cap, got, tc.gen)
		}
	}
}

func TestDescribeOpenSessionConstraint(t *testing.T) {
	strat, sql := DescribeOpenSessionConstraint(dbCapabilities{Engine: engineMariaDB, Major: 10, Minor: 5})
	if strat != "generated_column" {
		t.Fatalf("MariaDB 10.5 strategy: got %s", strat)
	}
	if sql == "" {
		t.Fatal("expected SQL reference")
	}
	strat, _ = DescribeOpenSessionConstraint(dbCapabilities{Engine: engineMySQL, Major: 8, Minor: 0, Patch: 13})
	if strat != "functional_index" {
		t.Fatalf("MySQL 8.0.13 strategy: got %s", strat)
	}
}
