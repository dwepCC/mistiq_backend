package service

import (
	"testing"

	"tukifac/pkg/database"
)

func TestFormatPackLabel(t *testing.T) {
	cases := []struct {
		factor float64
		sym    string
		want   string
	}{
		{12, "und", "12 UND"},
		{2.5, "KG", "2.5 KG"},
		{100, " kg ", "100 KG"},
		{1, "UND", ""},    // unidad base
		{0.001, "KG", ""}, // fraccionaria (gramo)
		{12, "", ""},      // sin símbolo no hay etiqueta útil
	}
	for _, c := range cases {
		if got := FormatPackLabel(c.factor, c.sym); got != c.want {
			t.Errorf("FormatPackLabel(%v,%q)=%q, want %q", c.factor, c.sym, got, c.want)
		}
	}
}

func TestResolvePackLabel(t *testing.T) {
	db := setupPrintDataTestDB(t)
	if err := db.AutoMigrate(&database.TenantProduct{}, &database.TenantProductSaleUnit{}, &database.TenantUnit{}); err != nil {
		t.Fatal(err)
	}
	db.Create(&database.TenantUnit{Code: "NIU", Name: "Unidades", Symbol: "UND", Active: true})
	prod := database.TenantProduct{Name: "Galletas", Code: "G1", Unit: "NIU", Type: "product"}
	if err := db.Create(&prod).Error; err != nil {
		t.Fatal(err)
	}
	caja := database.TenantProductSaleUnit{ProductID: prod.ID, Name: "Caja", Unit: "BX", ConversionFactor: 12, Price1: 10, Active: true}
	base := database.TenantProductSaleUnit{ProductID: prod.ID, Name: "Unidad", Unit: "NIU", ConversionFactor: 1, IsBase: true, Price1: 1, Active: true}
	gramo := database.TenantProductSaleUnit{ProductID: prod.ID, Name: "Gramo", Unit: "GRM", ConversionFactor: 0.001, Price1: 1, Active: true}
	for _, su := range []*database.TenantProductSaleUnit{&caja, &base, &gramo} {
		if err := db.Create(su).Error; err != nil {
			t.Fatal(err)
		}
	}
	pid := prod.ID
	if got := ResolvePackLabel(db, &pid, &caja.ID); got != "12 UND" {
		t.Fatalf("caja de 12: got %q", got)
	}
	if got := ResolvePackLabel(db, &pid, &base.ID); got != "" {
		t.Fatalf("unidad base no lleva paréntesis, got %q", got)
	}
	if got := ResolvePackLabel(db, &pid, &gramo.ID); got != "" {
		t.Fatalf("fraccionaria no lleva paréntesis, got %q", got)
	}
	if got := ResolvePackLabel(db, &pid, nil); got != "" {
		t.Fatalf("sin unidad de venta: got %q", got)
	}

	// Se congela al crear la línea: cambiar el factor después no altera una etiqueta ya calculada (se guarda el texto, no el factor).
	label := ResolvePackLabel(db, &pid, &caja.ID)
	db.Model(&caja).Update("conversion_factor", 24)
	if label != "12 UND" {
		t.Fatal("la etiqueta ya calculada no cambia")
	}
	// Una línea que ya trae su etiqueta (comprobante emitido desde una nota de venta) la conserva.
	if got := resolveItemPackLabel(db, SaleItemInput{ProductID: &pid, SaleUnitID: &caja.ID, PackLabel: "12 UND"}); got != "12 UND" {
		t.Fatalf("debe respetar la de la nota: got %q", got)
	}
}
