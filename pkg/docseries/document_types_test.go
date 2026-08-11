package docseries

import "testing"

func TestValidateSeriesDocumentType_rejectsInconsistentCode(t *testing.T) {
	err := ValidateSeriesDocumentType("BOLETA", "01", "venta")
	if err == nil {
		t.Fatal("Boleta + código 01 debe rechazarse")
	}
}

func TestValidateSeriesDocumentType_acceptsConsistentFactura(t *testing.T) {
	if err := ValidateSeriesDocumentType("FACTURA", "01", "venta"); err != nil {
		t.Fatalf("Factura + 01: %v", err)
	}
}

func TestNormalizeSeriesDocumentInput_derivesCanonicalFields(t *testing.T) {
	doc, code, cat, err := NormalizeSeriesDocumentInput("Boleta")
	if err != nil {
		t.Fatal(err)
	}
	if doc != "BOLETA" || code != "03" || cat != "venta" {
		t.Fatalf("got doc=%q code=%q cat=%q", doc, code, cat)
	}
}

func TestResolveDocumentType_legacyLabels(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"NOTA DE CRÉDITO", "NOTA_CREDITO"},
		{"GUÍA DE REMISIÓN", "GUIA_REMISION"},
		{"NOTA DE VENTA", "NOTA DE VENTA"},
	}
	for _, c := range cases {
		def, err := ResolveDocumentType(c.in)
		if err != nil {
			t.Fatalf("%q: %v", c.in, err)
		}
		if def.DocType != c.want {
			t.Fatalf("%q: got doc_type %q want %q", c.in, def.DocType, c.want)
		}
	}
}

func TestListFormDocumentTypes_withoutSunatOnlyInternalDocs(t *testing.T) {
	types := ListFormDocumentTypes(false, false)
	if len(types) != 4 {
		t.Fatalf("want 4 tipos internos (nota_venta, cotizacion, inventario), got %d", len(types))
	}
	ids := map[string]bool{}
	for _, item := range types {
		ids[item.ID] = true
	}
	for _, want := range []string{"nota_venta", "cotizacion", "ingreso_inventario", "egreso_inventario"} {
		if !ids[want] {
			t.Fatalf("falta tipo %s en %v", want, types)
		}
	}
}

func TestListFormDocumentTypes_withSunatIncludesQuotation(t *testing.T) {
	types := ListFormDocumentTypes(true, false)
	found := false
	for _, item := range types {
		if item.ID == "cotizacion" {
			found = true
			if item.RequiresSunat {
				t.Fatal("cotizacion no debe requerir SUNAT")
			}
		}
	}
	if !found {
		t.Fatal("cotizacion debe estar disponible con SUNAT habilitado")
	}
}

func TestListFormDocumentTypes_restaurantSubset(t *testing.T) {
	types := ListFormDocumentTypes(true, true)
	for _, item := range types {
		if !item.RestaurantForm {
			t.Fatalf("tipo %s no es restaurant_form", item.ID)
		}
	}
}

func TestCategoryLabels_includesInventoryAndQuotation(t *testing.T) {
	labels := CategoryLabels()
	if labels["almacen"] == "" || labels["cotizacion"] == "" {
		t.Fatalf("labels=%v", labels)
	}
}
