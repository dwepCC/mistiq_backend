package service

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"tukifac/pkg/database"
)

// Table salida tabular genérica: el navegador la convierte a Excel (hucre) como el resto de reportes.
// Las celdas son string, float64 o nil.
type Table struct {
	FileName string   `json:"file_name"`
	Sheet    string   `json:"sheet"`
	Preamble []string `json:"preamble"` // líneas sobre la cabecera (empresa, RUC, título del formato)
	// NoHeader: formatos que se importan sin fila de títulos (p. ej. Contasis).
	NoHeader bool     `json:"no_header"`
	Columns  []string `json:"columns"`
	Rows     [][]any  `json:"rows"`
	Footer   [][]any  `json:"footer,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

// Company datos de la empresa para los encabezados.
type Company struct{ Name, RUC string }

func (s *Service) Company() Company {
	var c database.TenantCompanyConfig
	s.db.First(&c)
	name := strings.TrimSpace(c.BusinessName)
	return Company{Name: name, RUC: strings.TrimSpace(c.RUC)}
}

func ymd(t time.Time) string { return t.Format("02/01/2006") }

func datePtr(t *time.Time) any {
	if t == nil || t.IsZero() {
		return nil
	}
	return ymd(*t)
}

// sign devuelve -1 para notas de crédito (los registros las informan en negativo) y 1 para el resto.
func sign(sunat string) float64 {
	if sunat == "07" {
		return -1
	}
	return 1
}

func statusLabel(d SaleDoc) string {
	if d.Voided {
		return "ANULADO"
	}
	switch d.BillingStatus {
	case "accepted":
		return "ACEPTADO"
	case "sent":
		return "ENVIADO"
	case "pending", "":
		return "PENDIENTE"
	}
	return strings.ToUpper(d.BillingStatus)
}

// SalesRegister Registro de Ventas e Ingresos (formato 14.1) de un mes. Importes en soles; las notas
// de crédito van en negativo; los anulados conservan su fila con importes en cero.
func (s *Service) SalesRegister(month string, withStatus bool) (*Table, error) {
	from, to, err := ParseMonth(month)
	if err != nil {
		return nil, err
	}
	docs, warns, err := s.SalesDocs(from, to)
	if err != nil {
		return nil, err
	}
	co := s.Company()
	cols := []string{
		"N°", "Fecha de emisión", "Fecha de vencimiento", "Tipo CP", "Serie", "Número",
		"Tipo doc. identidad", "N° doc. identidad", "Cliente",
		"Exportación", "Base imponible gravada", "Exonerada", "Inafecta", "ISC", "IGV", "ICBPER", "Otros tributos", "Importe total",
		"Moneda", "Tipo de cambio", "Total en moneda original",
		"Fecha doc. referencia", "Tipo doc. referencia", "Serie referencia", "Número referencia",
	}
	if withStatus {
		cols = append(cols, "Estado")
	}
	cols = append(cols, "Observación")
	t := &Table{
		FileName: fmt.Sprintf("registro-ventas-%s.xlsx", month), Sheet: "Registro de ventas", Columns: cols, Warnings: warns,
		Preamble: []string{
			fmt.Sprintf("%s — RUC %s", co.Name, co.RUC),
			fmt.Sprintf("FORMATO 14.1: REGISTRO DE VENTAS E INGRESOS — PERIODO %s", month),
			"Importes en soles. Notas de crédito en negativo. Anulados con importes en cero.",
		},
	}
	var tot Breakdown
	for i, d := range docs {
		sg := sign(d.SunatCode)
		p := d.PEN
		row := []any{
			i + 1, ymd(d.IssueDate), datePtr(d.DueDate), d.SunatCode, d.Series, d.Number,
			d.ContactDocType, d.ContactDocNumber, d.ContactName,
			sg * p.Exportation, sg * p.Taxed, sg * p.Exonerated, sg * p.Unaffected, sg * p.Isc, sg * p.Igv, sg * p.Icbper, sg * p.OtherCharges, sg * p.Total,
			d.Currency, d.ExchangeRate, sg * d.Orig.Total,
		}
		if d.Affected != nil {
			row = append(row, datePtr(d.Affected.Date), d.Affected.SunatCode, d.Affected.Series, d.Affected.Number)
		} else {
			row = append(row, nil, nil, nil, nil)
		}
		if withStatus {
			row = append(row, statusLabel(d))
		}
		row = append(row, d.Observation)
		t.Rows = append(t.Rows, row)
		tot.Exportation += sg * p.Exportation
		tot.Taxed += sg * p.Taxed
		tot.Exonerated += sg * p.Exonerated
		tot.Unaffected += sg * p.Unaffected
		tot.Isc += sg * p.Isc
		tot.Igv += sg * p.Igv
		tot.Icbper += sg * p.Icbper
		tot.OtherCharges += sg * p.OtherCharges
		tot.Total += sg * p.Total
	}
	foot := make([]any, len(cols))
	foot[0] = "TOTALES"
	foot[8] = fmt.Sprintf("%d comprobante(s)", len(docs))
	foot[9], foot[10], foot[11], foot[12] = round2(tot.Exportation), round2(tot.Taxed), round2(tot.Exonerated), round2(tot.Unaffected)
	foot[13], foot[14], foot[15], foot[16], foot[17] = round2(tot.Isc), round2(tot.Igv), round2(tot.Icbper), round2(tot.OtherCharges), round2(tot.Total)
	t.Footer = [][]any{foot}
	return t, nil
}

// PurchasesRegister Registro de Compras (formato 8.1) de un mes, en soles.
func (s *Service) PurchasesRegister(month string) (*Table, error) {
	from, to, err := ParseMonth(month)
	if err != nil {
		return nil, err
	}
	docs, warns, err := s.PurchaseDocs(from, to)
	if err != nil {
		return nil, err
	}
	co := s.Company()
	cols := []string{
		"N°", "Fecha de emisión", "Fecha de vencimiento", "Tipo CP", "Serie", "Número",
		"Tipo doc. proveedor", "N° doc. proveedor", "Proveedor",
		"Base imponible gravada", "IGV", "Exonerada", "Inafecta", "ISC", "Otros tributos", "Importe total",
		"Moneda", "Total en moneda original", "Observación",
	}
	t := &Table{
		FileName: fmt.Sprintf("registro-compras-%s.xlsx", month), Sheet: "Registro de compras", Columns: cols, Warnings: warns,
		Preamble: []string{
			fmt.Sprintf("%s — RUC %s", co.Name, co.RUC),
			fmt.Sprintf("FORMATO 8.1: REGISTRO DE COMPRAS — PERIODO %s", month),
			"Importes en soles. Notas de crédito en negativo.",
		},
	}
	var tot Breakdown
	for i, d := range docs {
		sg := sign(d.SunatCode)
		p := d.PEN
		t.Rows = append(t.Rows, []any{
			i + 1, ymd(d.IssueDate), datePtr(d.DueDate), d.SunatCode, d.Series, d.Number,
			d.ContactDocType, d.ContactDocNumber, d.ContactName,
			sg * p.Taxed, sg * p.Igv, sg * p.Exonerated, sg * p.Unaffected, sg * p.Isc, 0.0, sg * p.Total,
			d.Currency, sg * d.Orig.Total, d.Observation,
		})
		tot.Taxed += sg * p.Taxed
		tot.Igv += sg * p.Igv
		tot.Exonerated += sg * p.Exonerated
		tot.Unaffected += sg * p.Unaffected
		tot.Isc += sg * p.Isc
		tot.Total += sg * p.Total
	}
	foot := make([]any, len(cols))
	foot[0] = "TOTALES"
	foot[8] = fmt.Sprintf("%d comprobante(s)", len(docs))
	foot[9], foot[10], foot[11], foot[12], foot[13], foot[15] = round2(tot.Taxed), round2(tot.Igv), round2(tot.Exonerated), round2(tot.Unaffected), round2(tot.Isc), round2(tot.Total)
	t.Footer = [][]any{foot}
	return t, nil
}

// ── Reporte resumido de ventas ───────────────────────────────────────────────────────────────────

type SummaryRow struct {
	Type        string  `json:"type"` // FAC | BV | NC | ND
	SunatCode   string  `json:"sunat_code"`
	Series      string  `json:"series"`
	Quantity    int     `json:"quantity"`
	FirstNumber string  `json:"first_number"`
	LastNumber  string  `json:"last_number"`
	TaxableBase float64 `json:"taxable_base"` // valor de venta (gravado + exonerado + inafecto + exportación)
	Igv         float64 `json:"igv"`
	Icbper      float64 `json:"icbper"`
	Total       float64 `json:"total"`
	Gaps        string  `json:"gaps"` // números faltantes dentro del rango (vacío si es correlativo)
}

type VoidedRow struct {
	Type      string  `json:"type"`
	SunatCode string  `json:"sunat_code"`
	Series    string  `json:"series"`
	Numbers   string  `json:"numbers"`
	Quantity  int     `json:"quantity"`
	Total     float64 `json:"total"`
}

type SummaryReport struct {
	From            string       `json:"from"`
	To              string       `json:"to"`
	Confirmed       []SummaryRow `json:"confirmed"`
	Voided          []VoidedRow  `json:"voided"`
	ConfirmedTotals SummaryRow   `json:"confirmed_totals"`
	VoidedTotal     float64      `json:"voided_total"`
	// NotAccepted: comprobantes del rango que aún no están aceptados por SUNAT (pendientes o enviados).
	NotAccepted int      `json:"not_accepted"`
	Warnings    []string `json:"warnings,omitempty"`
}

func typeLabel(code string) string {
	switch code {
	case "01":
		return "FAC"
	case "03":
		return "BV"
	case "07":
		return "NC"
	case "08":
		return "ND"
	}
	return code
}

// SummaryReport reporte resumido por serie. «Confirmados» = aceptados por SUNAT y no anulados; las
// notas de crédito se informan aparte (tipo NC) y RESTAN en el total general.
func (s *Service) SummaryReport(from, to time.Time) (*SummaryReport, error) {
	docs, warns, err := s.SalesDocs(from, to.AddDate(0, 0, 1))
	if err != nil {
		return nil, err
	}
	type key struct{ code, series string }
	acc := map[key]*SummaryRow{}
	nums := map[key][]int{}
	voided := map[key]*VoidedRow{}
	voidedNums := map[key][]int{}
	rep := &SummaryReport{From: from.Format("2006-01-02"), To: to.Format("2006-01-02"), Warnings: warns}
	for _, d := range docs {
		k := key{d.SunatCode, d.Series}
		n := 0
		fmt.Sscanf(d.Number, "%d", &n)
		if d.Voided {
			v := voided[k]
			if v == nil {
				v = &VoidedRow{Type: typeLabel(d.SunatCode), SunatCode: d.SunatCode, Series: d.Series}
				voided[k] = v
			}
			v.Quantity++
			v.Total += d.Orig.Total * d.ExchangeRate
			voidedNums[k] = append(voidedNums[k], n)
			continue
		}
		if d.BillingStatus != "accepted" {
			rep.NotAccepted++
			continue
		}
		r := acc[k]
		if r == nil {
			r = &SummaryRow{Type: typeLabel(d.SunatCode), SunatCode: d.SunatCode, Series: d.Series}
			acc[k] = r
		}
		sg := sign(d.SunatCode)
		p := d.PEN
		r.Quantity++
		r.TaxableBase += sg * (p.Taxed + p.Exonerated + p.Unaffected + p.Exportation)
		r.Igv += sg * p.Igv
		r.Icbper += sg * p.Icbper
		r.Total += sg * p.Total
		nums[k] = append(nums[k], n)
	}
	keys := make([]key, 0, len(acc))
	for k := range acc {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].code != keys[j].code {
			return keys[i].code < keys[j].code
		}
		return keys[i].series < keys[j].series
	})
	for _, k := range keys {
		r := acc[k]
		ns := nums[k]
		sort.Ints(ns)
		r.FirstNumber, r.LastNumber = fmt.Sprint(ns[0]), fmt.Sprint(ns[len(ns)-1])
		r.Gaps = missingRanges(ns, voidedNums[k])
		r.TaxableBase, r.Igv, r.Icbper, r.Total = round2(r.TaxableBase), round2(r.Igv), round2(r.Icbper), round2(r.Total)
		rep.Confirmed = append(rep.Confirmed, *r)
		rep.ConfirmedTotals.Quantity += r.Quantity
		rep.ConfirmedTotals.TaxableBase += r.TaxableBase
		rep.ConfirmedTotals.Igv += r.Igv
		rep.ConfirmedTotals.Icbper += r.Icbper
		rep.ConfirmedTotals.Total += r.Total
	}
	t := &rep.ConfirmedTotals
	t.TaxableBase, t.Igv, t.Icbper, t.Total = round2(t.TaxableBase), round2(t.Igv), round2(t.Icbper), round2(t.Total)
	vkeys := make([]key, 0, len(voided))
	for k := range voided {
		vkeys = append(vkeys, k)
	}
	sort.Slice(vkeys, func(i, j int) bool {
		if vkeys[i].code != vkeys[j].code {
			return vkeys[i].code < vkeys[j].code
		}
		return vkeys[i].series < vkeys[j].series
	})
	for _, k := range vkeys {
		v := voided[k]
		ns := voidedNums[k]
		sort.Ints(ns)
		parts := make([]string, len(ns))
		for i, n := range ns {
			parts[i] = fmt.Sprint(n)
		}
		v.Numbers = strings.Join(parts, "; ")
		v.Total = round2(v.Total)
		rep.VoidedTotal += v.Total
		rep.Voided = append(rep.Voided, *v)
	}
	rep.VoidedTotal = round2(rep.VoidedTotal)
	return rep, nil
}

// missingRanges lista los números que faltan entre el primero y el último confirmados, sin contar los
// anulados (esos aparecen en su propia tabla).
func missingRanges(confirmed, voided []int) string {
	if len(confirmed) < 2 {
		return ""
	}
	have := map[int]bool{}
	for _, n := range confirmed {
		have[n] = true
	}
	for _, n := range voided {
		have[n] = true
	}
	var gaps []string
	for n := confirmed[0]; n <= confirmed[len(confirmed)-1]; n++ {
		if !have[n] {
			gaps = append(gaps, fmt.Sprint(n))
			if len(gaps) >= 30 {
				gaps = append(gaps, "…")
				break
			}
		}
	}
	return strings.Join(gaps, ", ")
}
