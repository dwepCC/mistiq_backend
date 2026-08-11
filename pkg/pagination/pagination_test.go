package pagination

import "testing"

func TestNormalize(t *testing.T) {
	page, perPage := Normalize(0, 999)
	if page != 1 || perPage != 25 {
		t.Fatalf("expected 1,25 got %d,%d", page, perPage)
	}
	page, perPage = Normalize(2, 20)
	if page != 2 || perPage != 20 {
		t.Fatalf("expected 2,20 got %d,%d", page, perPage)
	}
}

func TestTotalPages(t *testing.T) {
	if TotalPages(250, 25) != 10 {
		t.Fatalf("expected 10 pages")
	}
	if TotalPages(0, 25) != 0 {
		t.Fatalf("expected 0 pages for empty")
	}
}
