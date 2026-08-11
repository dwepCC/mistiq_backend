package prepayment

import "testing"

func TestAffectationGroupFromItem(t *testing.T) {
	cases := map[string]string{
		"10": AffectationGravado,
		"15": AffectationGravado,
		"20": AffectationExonerado,
		"21": AffectationExonerado,
		"30": AffectationInafecto,
		"31": AffectationInafecto,
	}
	for aff, want := range cases {
		if got := AffectationGroupFromItem(aff); got != want {
			t.Fatalf("aff %s: got %q want %q", aff, got, want)
		}
	}
}

func TestValidateItemsAffectationGroup(t *testing.T) {
	if err := ValidateItemsAffectationGroup(AffectationGravado, []string{"10", "10"}); err != nil {
		t.Fatal(err)
	}
	if err := ValidateItemsAffectationGroup(AffectationGravado, []string{"10", "20"}); err == nil {
		t.Fatal("expected mixed affectation error")
	}
}
