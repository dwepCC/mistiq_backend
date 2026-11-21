package service

import (
	"fmt"
	"math"
)

// Concept concepto de una línea de asiento de venta.
type Concept string

const (
	ConceptTotal       Concept = "total"
	ConceptIgv         Concept = "igv"
	ConceptTaxed       Concept = "taxed"
	ConceptExonerated  Concept = "exonerated"
	ConceptUnaffected  Concept = "unaffected"
	ConceptExportation Concept = "exportation"
	ConceptIcbper      Concept = "icbper"
	// ConceptServiceCharge recargo al consumo: va al pasivo con los trabajadores, nunca a ingresos.
	ConceptServiceCharge Concept = "service_charge"
)

// VoucherLine una línea Debe/Haber de un asiento. Amount es POSITIVO y está en la moneda del
// comprobante; Side indica el lado.
type VoucherLine struct {
	Concept Concept
	Account string
	Side    string // "D" | "H"
	Amount  float64
}

// Voucher asiento de venta de un comprobante.
type Voucher struct {
	Doc   SaleDoc
	Index int // correlativo dentro del archivo (1..n)
	Lines []VoucherLine
}

// SalesVouchers genera el asiento de cada comprobante. Es el ÚNICO lugar donde se decide qué cuenta y
// qué lado lleva cada concepto; los formatos de los sistemas contables solo lo presentan. Garantiza que
// cada asiento cuadra (Σ Debe = Σ Haber): una diferencia de redondeo se absorbe en la línea de ingreso
// más grande y una mayor a 5 céntimos se devuelve como advertencia.
//
//	Factura/boleta/nota de débito:  D cuenta por cobrar (total) · H IGV · H ingresos por tipo de operación
//	Nota de crédito:                H cuenta por cobrar (total) · D IGV · D ingresos (todo invertido)
//	Anulado: el asiento se conserva con importes en cero (el número queda usado).
func SalesVouchers(docs []SaleDoc, acc Accounts) ([]Voucher, []string) {
	var warns []string
	out := make([]Voucher, 0, len(docs))
	for i, d := range docs {
		v := Voucher{Doc: d, Index: i + 1}
		usd := d.Currency == "USD"
		recv, igvAcc, taxedAcc := acc.ReceivablePEN, acc.IgvSalesPEN, acc.TaxedPEN
		if usd {
			recv, igvAcc, taxedAcc = acc.ReceivableUSD, acc.IgvSalesUSD, acc.TaxedUSD
		}
		debitSide, creditSide := "D", "H"
		if d.IsCreditNote() {
			debitSide, creditSide = "H", "D"
		}
		b := d.Orig
		if d.Voided {
			b = Breakdown{}
		}
		type rev struct {
			c   Concept
			acc string
			amt float64
		}
		revs := []rev{
			{ConceptTaxed, taxedAcc, b.Taxed},
			{ConceptExonerated, acc.Exonerated, b.Exonerated},
			{ConceptUnaffected, acc.Unaffected, b.Unaffected},
			{ConceptExportation, acc.Exportation, b.Exportation},
			{ConceptIcbper, acc.Icbper, b.Icbper},
		}
		// Cuadre: total = Σ ingresos + IGV (+ISC). Si no, la diferencia (redondeo) va a la línea mayor.
		sum := b.Igv + b.Isc + b.OtherCharges
		big := 0
		for k, r := range revs {
			sum += r.amt
			if r.amt > revs[big].amt {
				big = k
			}
		}
		if diff := round2(b.Total - sum); diff != 0 {
			if math.Abs(diff) > 0.05 {
				warns = append(warns, fmt.Sprintf("%s: el asiento no cuadra por %.2f (total %.2f vs. partidas %.2f)", d.FullNumber, diff, b.Total, sum))
			}
			revs[big].amt = round2(revs[big].amt + diff)
		}
		v.Lines = append(v.Lines, VoucherLine{ConceptTotal, recv, debitSide, round2(b.Total)})
		v.Lines = append(v.Lines, VoucherLine{ConceptIgv, igvAcc, creditSide, round2(b.Igv)})
		for _, r := range revs {
			// Se omiten los conceptos en cero salvo el gravado, para que un anulado conserve su línea.
			if r.amt == 0 && r.c != ConceptTaxed {
				continue
			}
			if d.Voided && r.c != ConceptTaxed {
				continue
			}
			v.Lines = append(v.Lines, VoucherLine{r.c, r.acc, creditSide, round2(r.amt)})
		}
		if b.OtherCharges != 0 {
			v.Lines = append(v.Lines, VoucherLine{ConceptServiceCharge, pick(acc.ServiceCharge, ServiceChargeAccount), creditSide, round2(b.OtherCharges)})
		}
		out = append(out, v)
	}
	return out, warns
}

// VoucherBalanced verifica Σ Debe = Σ Haber (usado por las pruebas y como red de seguridad).
func VoucherBalanced(v Voucher) bool {
	var d, h float64
	for _, l := range v.Lines {
		if l.Side == "D" {
			d += l.Amount
		} else {
			h += l.Amount
		}
	}
	return round2(d) == round2(h)
}
