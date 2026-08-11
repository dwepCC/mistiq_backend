package docseries

import (
	"fmt"
	"strings"
)

// SeriesDocumentType define un tipo documental configurable como serie (fuente única de verdad).
type SeriesDocumentType struct {
	ID               string `json:"id"`
	DocType          string `json:"doc_type"`
	Label            string `json:"label"`
	DocumentCode     string `json:"document_code"`
	Category         string `json:"category"`
	CategoryLabel    string `json:"category_label"`
	SeriesPrefixHint string `json:"series_prefix_hint"`
	Electronic       bool   `json:"electronic"`
	SunatNumbering   bool   `json:"sunat_numbering"`
	FormSelectable   bool   `json:"form_selectable"`
	RestaurantForm   bool   `json:"restaurant_form"`
	RequiresSunat    bool   `json:"requires_sunat"`
}

// SunatCode devuelve el código almacenado en BD (compatibilidad interna).
func (t SeriesDocumentType) SunatCode() string {
	return t.DocumentCode
}

var seriesDocumentCatalog = []SeriesDocumentType{
	{
		ID: "nota_venta", DocType: "NOTA DE VENTA", Label: "Nota de venta",
		DocumentCode: "00", Category: "venta", CategoryLabel: "Venta", SeriesPrefixHint: "NV",
		Electronic: false, SunatNumbering: false, FormSelectable: true, RestaurantForm: true,
	},
	{
		ID: "factura", DocType: "FACTURA", Label: "Factura",
		DocumentCode: "01", Category: "venta", CategoryLabel: "Venta", SeriesPrefixHint: "F",
		Electronic: true, SunatNumbering: true, FormSelectable: true, RestaurantForm: true, RequiresSunat: true,
	},
	{
		ID: "boleta", DocType: "BOLETA", Label: "Boleta",
		DocumentCode: "03", Category: "venta", CategoryLabel: "Venta", SeriesPrefixHint: "B",
		Electronic: true, SunatNumbering: true, FormSelectable: true, RestaurantForm: true, RequiresSunat: true,
	},
	{
		ID: "nota_credito", DocType: "NOTA_CREDITO", Label: "Nota de crédito",
		DocumentCode: "07", Category: "nota_credito", CategoryLabel: "Nota crédito", SeriesPrefixHint: "FC / BC",
		Electronic: true, SunatNumbering: true, FormSelectable: true, RestaurantForm: true, RequiresSunat: true,
	},
	{
		ID: "nota_debito", DocType: "NOTA_DEBITO", Label: "Nota de débito",
		DocumentCode: "08", Category: "nota_debito", CategoryLabel: "Nota débito", SeriesPrefixHint: "FD",
		Electronic: true, SunatNumbering: true, FormSelectable: true, RestaurantForm: true, RequiresSunat: true,
	},
	{
		ID: "guia_remision", DocType: "GUIA_REMISION", Label: "Guía de remisión remitente",
		DocumentCode: "09", Category: "guia_remision", CategoryLabel: "Guía remitente", SeriesPrefixHint: "T",
		Electronic: true, SunatNumbering: true, FormSelectable: true, RestaurantForm: true, RequiresSunat: true,
	},
	{
		ID: "guia_transportista", DocType: "GUIA_TRANSPORTISTA", Label: "Guía transportista",
		DocumentCode: "31", Category: "guia_transportista", CategoryLabel: "Guía transportista", SeriesPrefixHint: "V",
		Electronic: true, SunatNumbering: true, FormSelectable: true, RestaurantForm: false, RequiresSunat: true,
	},
	{
		ID: "retencion", DocType: "RETENCION", Label: "Comprobante de retención",
		DocumentCode: "20", Category: "retencion", CategoryLabel: "Retención", SeriesPrefixHint: "R",
		Electronic: true, SunatNumbering: true, FormSelectable: true, RestaurantForm: false, RequiresSunat: true,
	},
	{
		ID: "percepcion", DocType: "PERCEPCION", Label: "Comprobante de percepción",
		DocumentCode: "40", Category: "percepcion", CategoryLabel: "Percepción", SeriesPrefixHint: "P",
		Electronic: true, SunatNumbering: true, FormSelectable: true, RestaurantForm: false, RequiresSunat: true,
	},
	{
		ID: "cotizacion", DocType: "Cotización", Label: "Cotización",
		DocumentCode: "QT", Category: "cotizacion", CategoryLabel: "Cotización", SeriesPrefixHint: "COT",
		Electronic: false, SunatNumbering: false, FormSelectable: true,
	},
	{
		ID: "ingreso_inventario", DocType: "INGRESO_INVENTARIO", Label: "Ingreso de inventario",
		DocumentCode: "00", Category: "almacen", CategoryLabel: "Almacén", SeriesPrefixHint: "ING",
		Electronic: false, SunatNumbering: false, FormSelectable: true,
	},
	{
		ID: "egreso_inventario", DocType: "EGRESO_INVENTARIO", Label: "Egreso de inventario",
		DocumentCode: "00", Category: "almacen", CategoryLabel: "Almacén", SeriesPrefixHint: "EGR",
		Electronic: false, SunatNumbering: false, FormSelectable: true,
	},
}

var seriesDocumentByID map[string]SeriesDocumentType
var seriesDocumentAlias map[string]string

func init() {
	seriesDocumentByID = make(map[string]SeriesDocumentType, len(seriesDocumentCatalog))
	seriesDocumentAlias = map[string]string{
		"NOTA_DE_VENTA":              "nota_venta",
		"NOTA_DE_CREDITO":            "nota_credito",
		"NOTA_DE_DEBITO":             "nota_debito",
		"NOTA_CREDITO":               "nota_credito",
		"NOTA_DEBITO":                "nota_debito",
		"GUIA_DE_REMISION":           "guia_remision",
		"GUIA_REMISION":              "guia_remision",
		"GUIA_TRANSPORTISTA":         "guia_transportista",
		"GUIA_DE_REMISION_REMITENTE": "guia_remision",
		"RETENCION":                  "retencion",
		"PERCEPCION":                 "percepcion",
		"COTIZACION":                 "cotizacion",
		"COTIZACIÓN":                 "cotizacion",
		"INGRESO_INVENTARIO":         "ingreso_inventario",
		"EGRESO_INVENTARIO":          "egreso_inventario",
	}
	for _, t := range seriesDocumentCatalog {
		seriesDocumentByID[t.ID] = t
		seriesDocumentAlias[normalizeDocTypeKey(t.DocType)] = t.ID
		seriesDocumentAlias[normalizeDocTypeKey(t.ID)] = t.ID
	}
}

func normalizeDocTypeKey(s string) string {
	s = strings.ToUpper(strings.TrimSpace(s))
	replacer := strings.NewReplacer("Á", "A", "É", "E", "Í", "I", "Ó", "O", "Ú", "U", "Ü", "U")
	s = replacer.Replace(s)
	s = strings.ReplaceAll(s, " ", "_")
	s = strings.ReplaceAll(s, "-", "_")
	return s
}

// ResolveDocumentType resuelve un tipo documental (nombre canónico, etiqueta o alias legacy).
func ResolveDocumentType(docType string) (SeriesDocumentType, error) {
	key := normalizeDocTypeKey(docType)
	if key == "" {
		return SeriesDocumentType{}, fmt.Errorf("tipo de documento requerido")
	}
	if id, ok := seriesDocumentAlias[key]; ok {
		if def, found := seriesDocumentByID[id]; found {
			return def, nil
		}
	}
	return SeriesDocumentType{}, fmt.Errorf("tipo de documento no reconocido: %q", docType)
}

// ValidateSeriesDocumentType comprueba coherencia entre tipo, código documental y categoría.
func ValidateSeriesDocumentType(docType, documentCode, category string) error {
	def, err := ResolveDocumentType(docType)
	if err != nil {
		return err
	}
	dc := strings.TrimSpace(documentCode)
	cat := strings.TrimSpace(strings.ToLower(category))
	if dc != "" && dc != def.DocumentCode {
		return fmt.Errorf(
			"el código documental %s no corresponde al tipo %s (esperado %s)",
			dc, def.Label, def.DocumentCode,
		)
	}
	if cat != "" && cat != def.Category {
		return fmt.Errorf(
			"la categoría %q no corresponde al tipo documental %s (esperada %q)",
			category, def.Label, def.Category,
		)
	}
	return nil
}

// NormalizeSeriesDocumentInput devuelve valores canónicos a partir del tipo documental.
func NormalizeSeriesDocumentInput(docType string) (canonicalDocType, documentCode, category string, err error) {
	def, err := ResolveDocumentType(docType)
	if err != nil {
		return "", "", "", err
	}
	return def.DocType, def.DocumentCode, def.Category, nil
}

// ListFormDocumentTypes tipos disponibles en el formulario de series.
func ListFormDocumentTypes(sunatEnabled, restaurantOnly bool) []SeriesDocumentType {
	out := make([]SeriesDocumentType, 0, len(seriesDocumentCatalog))
	for _, t := range seriesDocumentCatalog {
		if !t.FormSelectable {
			continue
		}
		if restaurantOnly && !t.RestaurantForm {
			continue
		}
		if !sunatEnabled && t.RequiresSunat {
			continue
		}
		out = append(out, t)
	}
	return out
}

// CategoryLabels mapa categoría → etiqueta UI desde el catálogo completo.
func CategoryLabels() map[string]string {
	out := map[string]string{"compra": "Compra"}
	for _, t := range seriesDocumentCatalog {
		if t.CategoryLabel != "" {
			out[t.Category] = t.CategoryLabel
		}
	}
	return out
}

// DocumentTypeBySunatAndCategory resuelve un tipo legacy almacenado.
func DocumentTypeBySunatAndCategory(documentCode, category string) (SeriesDocumentType, bool) {
	dc := strings.TrimSpace(documentCode)
	cat := strings.TrimSpace(strings.ToLower(category))
	for _, t := range seriesDocumentCatalog {
		if t.DocumentCode == dc && t.Category == cat {
			return t, true
		}
	}
	return SeriesDocumentType{}, false
}
