package service

import "testing"

func TestBarcodeCandidates_UPC12ToEAN13(t *testing.T) {
	cands := BarcodeCandidates("123456789012")
	found := false
	for _, c := range cands {
		if c == "0123456789012" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected EAN-13 variant with leading 0, got %v", cands)
	}
}

func TestBarcodeCandidates_stripsControlChars(t *testing.T) {
	cands := BarcodeCandidates("775123\x0d")
	if len(cands) == 0 || cands[0] != "775123" {
		t.Fatalf("unexpected candidates: %v", cands)
	}
}
