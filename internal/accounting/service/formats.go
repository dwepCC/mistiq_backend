package service

import (
	"fmt"
	"strings"
)

// Formatos de exportación a sistemas contables. Todos parten del MISMO asiento (SalesVouchers) o del
// MISMO desglose por comprobante (SaleDoc), de modo que los importes y cuentas son idénticos entre
// formatos y lo único que cambia es la presentación.

// Export resultado de una exportación: una tabla (Excel) o un texto plano (.txt de ancho fijo).
type Export struct {
	Table    *Table   `json:"table,omitempty"`
	Text     string   `json:"text,omitempty"`
	FileName string   `json:"file_name"`
	Warnings []string `json:"warnings,omitempty"`
}

// Formats formatos disponibles (clave → nombre).
var Formats = []struct{ Key, Name string }{
	{"concar", "CONCAR"},
	{"concar_simple", "CONCAR simple"},
	{"siscont_txt", "SISCONT (.txt)"},
	{"siscont_excel", "SISCONT (Excel)"},
	{"foxcont", "FOXCONT"},
	{"contasis", "CONTASIS"},
	{"adsoft", "ADSOFT"},
	{"sumerius", "SUMERIUS"},
}

func f2(v float64) float64 { return round2(v) }

// ExportFormat genera el archivo de ventas del mes para el sistema contable indicado.
func (s *Service) ExportFormat(format, month string) (*Export, error) {
	from, to, err := ParseMonth(month)
	if err != nil {
		return nil, err
	}
	docs, warns, err := s.SalesDocs(from, to)
	if err != nil {
		return nil, err
	}
	acc, err := s.ResolvedAccounts()
	if err != nil {
		return nil, err
	}
	co := s.Company()
	ex := &Export{Warnings: warns}
	name := func(ext string) string { return fmt.Sprintf("ventas-%s-%s.%s", format, month, ext) }

	switch format {
	case "concar", "concar_simple":
		vs, w := SalesVouchers(docs, acc)
		ex.Warnings = append(ex.Warnings, w...)
		ex.Table = renderConcar(vs, acc, format == "concar_simple")
		ex.Table.FileName = name("xlsx")
	case "siscont_txt":
		vs, w := SalesVouchers(docs, acc)
		ex.Warnings = append(ex.Warnings, w...)
		ex.Text = renderSiscontTxt(vs)
		ex.FileName = name("txt")
		return ex, nil
	case "siscont_excel":
		ex.Table = renderSiscontExcel(docs, acc)
	case "foxcont":
		ex.Table = renderFoxcont(docs)
	case "contasis":
		ex.Table = renderContasis(docs, acc)
	case "adsoft":
		ex.Table = renderAdsoft(docs)
	case "sumerius":
		ex.Table = renderSumerius(docs, acc)
	default:
		return nil, fmt.Errorf("formato desconocido: %s", format)
	}
	ex.Table.FileName = name("xlsx")
	ex.Table.Warnings = ex.Warnings
	if len(ex.Table.Preamble) == 0 && format != "contasis" {
		ex.Table.Preamble = nil
	}
	_ = co
	ex.FileName = ex.Table.FileName
	return ex, nil
}

func shortDoc(code string, concarNC bool) string {
	switch code {
	case "01":
		return "FT"
	case "03":
		return "BV"
	case "07":
		if concarNC {
			return "NA"
		}
		return "NC"
	case "08":
		return "ND"
	}
	return ""
}

func monthOf(d SaleDoc) string { return fmt.Sprintf("%02d", int(d.IssueDate.Month())) }

func trunc(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}

// ── CONCAR / CONCAR simple ───────────────────────────────────────────────────────────────────────

var concarColumns = []string{
	"Sub Diario", "Número de Comprobante", "Fecha de Comprobante", "Código de Moneda", "Glosa Principal",
	"Tipo de Cambio", "Tipo de Conversión", "Flag de Conversión de Moneda", "Fecha Tipo de Cambio",
	"Cuenta Contable", "Código de Anexo", "Código de Centro de Costo", "Debe / Haber", "Importe Original",
	"Importe en Dólares", "Importe en Soles", "Tipo de Documento", "Número de Documento", "Fecha de Documento",
	"Fecha de Vencimiento", "Código de Area", "Glosa Detalle", "Código de Anexo Auxiliar", "Medio de Pago",
	"Tipo de Documento de Referencia", "Número de Documento Referencia", "Fecha Documento Referencia",
	"Nro Máq. Registradora Tipo Doc. Ref.", "Base Imponible Documento Referencia", "IGV Documento Provisión",
	"Tipo Referencia en estado MQ", "Número Serie Caja Registradora", "Fecha de Operación", "Tipo de Tasa",
	"Tasa Detracción/Percepción", "Importe Base Detracción/Percepción Dólares",
	"Importe Base Detracción/Percepción Soles", "Tipo Cambio para 'F'", "Importe de IGV sin derecho crédito fiscal",
}

func renderConcar(vs []Voucher, acc Accounts, simple bool) *Table {
	cols := append([]string{}, concarColumns...)
	if simple {
		cols = append(cols, "Tasa IGV")
	}
	t := &Table{Sheet: "Concar", Columns: cols}
	for _, v := range vs {
		d := v.Doc
		cur := "MN"
		if d.Currency == "USD" {
			cur = "US"
		}
		due := d.DueDate
		dueStr := any(nil)
		if due != nil {
			dueStr = ymd(*due)
		} else if simple && (d.SunatCode == "07" || d.SunatCode == "08") {
			dueStr = ymd(d.IssueDate)
		}
		gloss := fmt.Sprintf("VENTAS DEL DIA %s", d.IssueDate.Format("02012006"))
		detail := d.FullNumber
		if simple {
			gloss = trunc(d.ContactName, 40)
			detail = trunc(d.ContactName+" "+d.FullNumber, 30)
		}
		var refType, refNum, refDate any
		var refBase, refIgv any
		if d.Affected != nil {
			refType = shortDoc(d.Affected.SunatCode, false)
			refNum = d.Affected.Series + "-" + d.Affected.Number
			refDate = datePtr(d.Affected.Date)
			if d.Affected.Taxable > 0 {
				refBase, refIgv = f2(d.Affected.Taxable), f2(d.Affected.Igv)
			}
		}
		sub := acc.SubdiarioSales
		for _, l := range v.Lines {
			orig := f2(l.Amount)
			var usd, pen any
			if !simple {
				if d.Currency == "USD" {
					usd, pen = orig, f2(l.Amount*d.ExchangeRate)
				} else {
					usd, pen = f2(l.Amount/d.ExchangeRate), orig
				}
			}
			tc := any(f2(d.ExchangeRate))
			if simple {
				tc = 0.0
			}
			row := []any{
				sub, monthOf(d) + fmt.Sprintf("%04d", v.Index), ymd(d.IssueDate), cur, gloss,
				tc, "V", "S", nil,
				l.Account, d.ContactDocNumber, nil, l.Side, orig,
				usd, pen, shortDoc(d.SunatCode, !simple), d.FullNumber, ymd(d.IssueDate),
				dueStr, nil, detail, nil, nil,
				refType, refNum, refDate,
				nil, refBase, refIgv,
				nil, nil, nil, nil,
				nil, nil, nil, nil, nil,
			}
			if simple {
				row = append(row, 18.0)
			}
			t.Rows = append(t.Rows, row)
		}
	}
	return t
}

// ── SISCONT (.txt de ancho fijo, 358 caracteres) ─────────────────────────────────────────────────

func padL(s string, n int) string { // alinea a la derecha, trunca a n
	r := []rune(s)
	if len(r) >= n {
		return string(r[len(r)-n:])
	}
	return strings.Repeat(" ", n-len(r)) + s
}

func blank(n int) string { return strings.Repeat(" ", n) }

func renderSiscontTxt(vs []Voucher) string {
	var sb strings.Builder
	for _, v := range vs {
		d := v.Doc
		cur := "S"
		if d.Currency == "USD" {
			cur = "D"
		}
		docType := "03"
		if d.SunatCode == "01" {
			docType = "01"
		}
		date := d.IssueDate.Format("02/01/06")
		due := date
		if d.DueDate != nil {
			due = d.DueDate.Format("02/01/06")
		}
		detail := trunc(d.ContactName+", "+docType+" "+d.FullNumber, 30)
		number := d.Series + "-" + fmt.Sprintf("%015s", d.Number)
		tc := fmt.Sprintf("%010.7f", d.ExchangeRate)
		customer := padL(d.ContactDocNumber, 11)
		var base, igv float64
		for _, l := range v.Lines {
			switch l.Concept {
			case ConceptIgv:
				igv = l.Amount
			case ConceptTaxed:
				base = l.Amount
			}
		}
		for _, l := range v.Lines {
			// Siscont solo conoce total, IGV y un ingreso: los demás ingresos van como líneas de ingreso.
			baseCol, igvCol := blank(12), blank(12)
			flag := " "
			if l.Concept == ConceptIgv {
				flag = "V"
				baseCol = padL(fmt.Sprintf("%012.2f", base), 12)
				igvCol = padL(fmt.Sprintf("%012.2f", igv), 12)
			}
			line := "02" + // 001-002
				fmt.Sprintf("%04d", v.Index%10000) + // 003-006
				date + // 007-014
				fmt.Sprintf("%-10s", trunc(l.Account, 10)) + // 015-024
				fmt.Sprintf("%012.2f", l.Amount) + // 025-036
				l.Side + // 037
				cur + // 038
				tc + // 039-048
				docType + // 049-050
				fmt.Sprintf("%-20s", trunc(number, 20)) + // 051-070
				due + // 071-078
				customer + // 079-089
				blank(10) + blank(4) + blank(10) + // 090-113
				flag + // 114
				date + // 115-122
				baseCol + blank(12) + blank(12) + blank(12) + igvCol + // 123-182
				customer + // 183-193
				" " + // 194
				padL(d.ContactDocNumber, 40) + // 195-234
				padL(detail, 30) + // 235-264
				d.ContactDocType + // 265
				blank(3) // 266-268
			line += blank(20) + blank(20) + blank(20) + blank(20) + blank(2) + blank(8) // 269-358
			sb.WriteString(line + "\r\n")
		}
	}
	return sb.String()
}

// ── SISCONT (Excel, una fila por comprobante) ────────────────────────────────────────────────────

func renderSiscontExcel(docs []SaleDoc, acc Accounts) *Table {
	cols := []string{
		"Vou.Origen", "Vou.Numero", "Vou.Fecha", "Doc", "Numero", "Fec.Doc", "Fec.Venc.", "Codigo", "Valor Exp.", "B.Imponible",
		"Inafecto", "Exonerado", "I.S.C.", "IGV", "OTROS TRIB.", "IMP. BOLSA", "Moneda", "TC", "Glosa", "Cta Ingreso", "Cta IGV",
		"Cta O. Trib.", "Cta x Cobrar", "C.Costo", "Presupuesto", "R.Doc", "R.numero", "R.Fecha", "RUC", "R.Social", "Tipo",
		"Tip.Doc.Iden", "Medio de Pago", "Apellido 1", "Apellido 2", "Nombre", "P.origen", "P.vou", "P.fecha", "P.fecha D.",
		"P.fecha V.", "P.cta cob", "P.m.pago", "P.doc", "P.num doc", "P.moneda", "P.tc", "P.monto", "P.glosa", "P.fe",
		"Retencion 0/1", "PDB ndes", "CodTasa", "Ind.Ret", "B.Imp", "IGV.",
	}
	t := &Table{Sheet: "Siscont", Columns: cols}
	for i, d := range docs {
		cur, recv, igvAcc, inc := "S", acc.ReceivablePEN, acc.IgvSalesPEN, acc.TaxedPEN
		if d.Currency == "USD" {
			cur, recv, igvAcc, inc = "D", acc.ReceivableUSD, acc.IgvSalesUSD, acc.TaxedUSD
		}
		o := d.Orig
		if d.Voided {
			o = Breakdown{}
		}
		due := d.IssueDate
		if d.DueDate != nil {
			due = *d.DueDate
		}
		t.Rows = append(t.Rows, []any{
			"02", i + 1, ymd(d.IssueDate), d.SunatCode, d.FullNumber, ymd(d.IssueDate), ymd(due), d.ContactDocNumber,
			f2(o.Exportation), f2(o.Taxed), f2(o.Unaffected), f2(o.Exonerated), f2(o.Isc), f2(o.Igv), 0.0, f2(o.Icbper),
			cur, f2(d.ExchangeRate), "VENTA", inc, igvAcc, "", recv, "", "", "", "", "",
			d.ContactDocNumber, d.ContactName, "2", d.ContactDocType, "", "", "", "",
			"04", "", "", "", "", "1011", "008", "", "", "", "", "", "VENTA", "O101", "", "", "", "", "", "",
		})
	}
	return t
}

// ── FOXCONT ──────────────────────────────────────────────────────────────────────────────────────

func renderFoxcont(docs []SaleDoc) *Table {
	t := &Table{Sheet: "Foxcont", Columns: []string{
		"FECHAE", "FECHAV", "TIPOC", "SERIE", "NUMERO", "TIPODOC", "DOCUMENTO", "NOMBRE", "BASEI", "IGV", "ICBPER", "EXONERADO", "RETENCION", "TOTAL",
	}}
	for _, d := range docs {
		due := d.IssueDate
		if d.DueDate != nil {
			due = *d.DueDate
		}
		p := d.PEN // Foxcont no trae moneda: siempre soles
		num := fmt.Sprintf("%07s", d.Number)
		if d.Voided {
			t.Rows = append(t.Rows, []any{ymd(d.IssueDate), ymd(due), d.SunatCode, d.Series, num, "0", "0", "ANULADA", 0.0, 0.0, 0.0, 0.0, 0.0, 0.0})
			continue
		}
		t.Rows = append(t.Rows, []any{
			ymd(d.IssueDate), ymd(due), d.SunatCode, d.Series, num, d.ContactDocType, d.ContactDocNumber, d.ContactName,
			f2(p.Taxed), f2(p.Igv), f2(p.Icbper), f2(p.Exonerated), 0.0, f2(p.Total),
		})
	}
	return t
}

// ── CONTASIS (sin fila de títulos, 42 columnas) ──────────────────────────────────────────────────

func renderContasis(docs []SaleDoc, acc Accounts) *Table {
	t := &Table{Sheet: "Contasis", NoHeader: true}
	for _, d := range docs {
		cur, taxedAcc, recvAcc := "S", acc.TaxedPEN, acc.ReceivablePEN
		if d.Currency == "USD" {
			cur, taxedAcc, recvAcc = "D", acc.TaxedUSD, acc.ReceivableUSD
		}
		due := d.IssueDate
		if d.DueDate != nil {
			due = *d.DueDate
		}
		o := d.Orig
		if d.Voided {
			o = Breakdown{}
		}
		var rd, rt, rs, rn any
		if d.Affected != nil {
			rd, rt, rs, rn = datePtr(d.Affected.Date), d.Affected.SunatCode, d.Affected.Series, fmt.Sprintf("%013s", d.Affected.Number)
		}
		cond, method := any(nil), any(nil)
		switch d.PaymentCondition {
		case "cash":
			cond, method = "CON", "008"
		case "credit":
			cond, method = "CRE", "005"
		}
		t.Rows = append(t.Rows, []any{
			ymd(d.IssueDate), ymd(due), d.SunatCode, "00" + d.Series, fmt.Sprintf("%013s", d.Number),
			d.ContactDocType, d.ContactDocNumber, d.ContactName,
			f2(o.Exportation), f2(o.Taxed), f2(o.Exonerated), f2(o.Unaffected), f2(o.Isc), f2(o.Igv), 0.0, f2(o.Total),
			f2(d.ExchangeRate), rd, rt, rs, rn, cur, nil, ymd(due), cond,
			nil, nil, taxedAcc, nil, recvAcc, nil, nil, nil, nil, nil, nil, nil,
			18.0, d.Observation, method, nil, nil,
		})
	}
	return t
}

// ── ADSOFT ───────────────────────────────────────────────────────────────────────────────────────

func renderAdsoft(docs []SaleDoc) *Table {
	t := &Table{Sheet: "Adsoft", Preamble: []string{"REGISTRO DE VENTAS"}, Columns: []string{
		"SERIE", "NUMERO", "FECFAC", "FECVEN", "NRO_RUC", "NOMBRE", "TIPDOC", "TIPMON", "DETRAC", "IMP_VTA", "ISC", "ICBPER", "IMP_INA",
		"IMP_EXO", "IMP_EXP", "RECARGO", "IMP_IGV", "IMP_TOT", "ST", "SER_DQM", "NUM_DQM", "FEC_DQM", "TIP_DQM", "NRO_DNI", "PASAPORTE",
		"CTA_VTA", "TIP_CAM", "", "",
	}}
	for _, d := range docs {
		due := d.IssueDate
		if d.DueDate != nil {
			due = *d.DueDate
		}
		mon := "SOLES"
		if d.Currency == "USD" {
			mon = "DOLARES"
		}
		ruc, dni := any(nil), any(nil)
		if d.ContactDocType == "6" {
			ruc = d.ContactDocNumber
		}
		if d.ContactDocType == "1" {
			dni = d.ContactDocNumber
		}
		o := d.Orig
		st := any(nil)
		if d.Voided {
			o, st = Breakdown{}, "A"
		}
		// Documento mixto: cada base en su columna (el formato anterior descartaba la gravada).
		t.Rows = append(t.Rows, []any{
			d.Series, d.Number, ymd(d.IssueDate), ymd(due), ruc, d.ContactName, d.SunatCode, mon, nil,
			f2(o.Taxed), f2(o.Isc), f2(o.Icbper), f2(o.Unaffected), f2(o.Exonerated), f2(o.Exportation), nil, f2(o.Igv), f2(o.Total),
			st, nil, nil, nil, nil, dni, nil, nil, f2(d.ExchangeRate), nil, nil,
		})
	}
	return t
}

// ── SUMERIUS ─────────────────────────────────────────────────────────────────────────────────────

func renderSumerius(docs []SaleDoc, acc Accounts) *Table {
	cols := []string{
		"NUMERO", "FECHAE", "FECHAV", "TIPOC", "SERIE", "NUMERO", "NUMER2", "TIPODOC", "DOCUMENTO", "NOMBRE", "EXPORTACION", "BASEI",
		"ICBPER", "EXONERADO", "IGV", "RETENCION", "TOTAL", "TIPO DE CAMBIO", "FECHA NOTA DE CREDITO", "TIPO DE DOC", "SERIE - NUMERO",
		"CTA1", "CTA2", "CTA3", "CTA4", "CTA5", "CLOSA",
	}
	// Fila de cuentas y lado sobre las columnas de importes (EXPORTACION…TOTAL), tomadas de la configuración.
	accRow := make([]any, len(cols))
	sideRow := make([]any, len(cols))
	setAcc := func(i int, a, side string) { accRow[i], sideRow[i] = a, side }
	setAcc(10, acc.Exportation, "H")
	setAcc(11, acc.TaxedPEN, "H")
	setAcc(12, acc.Icbper, "H")
	setAcc(13, acc.Exonerated, "H")
	setAcc(14, acc.IgvSalesPEN, "H")
	setAcc(16, acc.ReceivablePEN, "D")
	t := &Table{Sheet: "Sumerius", Columns: cols, Preamble: []string{"ADVENIRFAC", "REPORTE DE VENTAS", "PARA SUMERIUS"}}
	t.Rows = append(t.Rows, accRow, sideRow)
	for _, d := range docs {
		due := d.IssueDate
		if d.DueDate != nil {
			due = *d.DueDate
		}
		o := d.Orig
		nm := d.ContactName
		tipo, doc := d.ContactDocType, d.ContactDocNumber
		if d.Voided {
			o, nm, tipo, doc = Breakdown{}, "ANULADA", "0", "0"
		}
		var rf, rt, rn any
		if d.Affected != nil {
			rf, rt, rn = datePtr(d.Affected.Date), d.Affected.SunatCode, d.Affected.Series+"-"+d.Affected.Number
		}
		t.Rows = append(t.Rows, []any{
			d.ID, ymd(d.IssueDate), ymd(due), d.SunatCode, d.Series, fmt.Sprintf("%07s", d.Number), nil, tipo, doc, nm,
			f2(o.Exportation), f2(o.Taxed), f2(o.Icbper), f2(o.Exonerated), f2(o.Igv), 0.0, f2(o.Total), f2(d.ExchangeRate),
			rf, rt, rn, acc.TaxedPEN, acc.Exonerated, acc.Unaffected, acc.IgvSalesPEN, acc.ReceivablePEN, "VENTA NACIONAL",
		})
	}
	return t
}
