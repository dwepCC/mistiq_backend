package taxregime

import "testing"

func TestNRUSCannotEmitFactura(t *testing.T) {
	p := For("nrus")
	if p.CanEmitFactura() {
		t.Fatal("NRUS no debe poder emitir factura (01)")
	}
	if p.CanEmit("01") {
		t.Fatal("NRUS.CanEmit(01) debe ser false")
	}
	if !p.CanEmitBoleta() {
		t.Fatal("NRUS debe poder emitir boleta (03)")
	}
	if !p.CanEmitNotaCredito() || !p.CanEmitNotaDebito() {
		t.Fatal("NRUS debe poder emitir NC/ND (sobre boletas)")
	}
	if !p.CanEmitNoteAffecting("03") {
		t.Fatal("NRUS debe poder emitir nota sobre boleta (03)")
	}
	if p.CanEmitNoteAffecting("01") {
		t.Fatal("NRUS no debe poder emitir nota sobre factura (01)")
	}
	if p.ShowIgvBreakdown {
		t.Fatal("NRUS no debe mostrar desglose de IGV en impreso")
	}
}

func TestGeneralCanEmitAll(t *testing.T) {
	p := For("general")
	if !p.CanEmitFactura() || !p.CanEmitBoleta() {
		t.Fatal("General debe poder emitir factura y boleta")
	}
	if !p.ShowIgvBreakdown {
		t.Fatal("General debe mostrar desglose de IGV")
	}
}

func TestDefaultsToGeneral(t *testing.T) {
	for _, in := range []string{"", "  ", "desconocido", "GENERAL", "General"} {
		if got := Normalize(in); got != General && in != "" && in != "  " && in != "desconocido" {
			// GENERAL/General deben normalizar a general
			t.Fatalf("Normalize(%q) = %q, se esperaba general", in, got)
		}
		if !For(in).CanEmitFactura() {
			t.Fatalf("For(%q) debe comportarse como General (puede factura)", in)
		}
	}
}

// Invariante de negocio: ningún régimen puede quedar sin poder emitir boleta.
func TestEveryRegimeCanEmitBoleta(t *testing.T) {
	for r := range registry {
		if !For(string(r)).CanEmitBoleta() {
			t.Fatalf("régimen %q no puede emitir boleta (03)", r)
		}
	}
}

func TestCapabilitiesShape(t *testing.T) {
	caps := CapabilitiesFor("nrus")
	if len(caps.AllowedSaleDocCodes) != 2 || caps.AllowedSaleDocCodes[0] != "00" || caps.AllowedSaleDocCodes[1] != "03" {
		t.Fatalf("NRUS allowed_sale_doc_codes = %v, se esperaba [00 03]", caps.AllowedSaleDocCodes)
	}
	caps = CapabilitiesFor("general")
	if len(caps.AllowedSaleDocCodes) != 3 {
		t.Fatalf("General allowed_sale_doc_codes = %v, se esperaba 3 (00,03,01)", caps.AllowedSaleDocCodes)
	}
}
