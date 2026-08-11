package tenantmigrations

import "testing"

func TestMigrationsUpToSparseVersions(t *testing.T) {
	migs := MigrationsUpTo(0, MaxVersion())
	if len(migs) == 0 {
		t.Fatal("expected registered migrations")
	}
	if migs[0].Version() != MinVersion() {
		t.Fatalf("first migration = V%d, want V%d", migs[0].Version(), MinVersion())
	}
	seen := make(map[int]struct{})
	for _, m := range migs {
		if _, ok := seen[m.Version()]; ok {
			t.Fatalf("duplicate version V%d", m.Version())
		}
		seen[m.Version()] = struct{}{}
	}
	if _, ok := seen[1]; !ok {
		t.Fatal("expected V001 baseline in range from 0")
	}
	if _, ok := seen[59]; !ok {
		t.Fatal("expected V059 in range to MaxVersion")
	}
	if _, ok := seen[79]; !ok {
		t.Fatal("expected V080 in range to MaxVersion")
	}
}

func TestMinVersionIsBaseline(t *testing.T) {
	if MinVersion() != 1 {
		t.Fatalf("MinVersion = %d, want 1", MinVersion())
	}
}
