package database

import (
	"testing"

	"tukifac/pkg/sunat"
)

// Toda unidad del catálogo sembrado debe sobrevivir a sunat.NormalizeUnit: si no, un producto en
// Millar (MIL), Hora (HUR), Saco (SA)… se declara como NIU al vender o cotizar.
func TestDefaultUnitsCatalogCodesAreValidSunatUnits(t *testing.T) {
	for _, def := range defaultUnitsCatalog {
		if !sunat.IsValidUnitCode(def.Code) {
			t.Errorf("%s (%s) está en el catálogo sembrado pero sunat.IsValidUnitCode lo rechaza", def.Code, def.Name)
		}
		if def.Code == "ZZ" {
			continue
		}
		if got := sunat.NormalizeUnit(def.Code, "product"); got != def.Code {
			t.Errorf("NormalizeUnit(%q) = %q, quería %q", def.Code, got, def.Code)
		}
	}
}
