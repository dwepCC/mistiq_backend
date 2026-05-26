package tenantrubro

import "testing"

func TestNormalize(t *testing.T) {
	if Normalize("gastronomico") != Gastronomico {
		t.Fatal("expected gastronomico")
	}
	if Normalize("GASTRONOMICO") != Gastronomico {
		t.Fatal("case insensitive")
	}
	if Normalize("") != General {
		t.Fatal("empty -> general")
	}
	if Normalize("retail") != General {
		t.Fatal("unknown -> general")
	}
}

func TestLabel(t *testing.T) {
	if Label(Gastronomico) != "Gastronómico" {
		t.Fatal("label gastronomico")
	}
	if Label(General) != "General" {
		t.Fatal("label general")
	}
}
