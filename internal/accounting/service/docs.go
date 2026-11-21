// Package service implementa el módulo Contabilidad: lectura normalizada de ventas y compras,
// registros 14.1 / 8.1, reporte resumido, motor de asientos y formatos para sistemas contables,
// libro mayor y SIRE. Solo LEE los datos de los demás módulos.
package service

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"tukifac/pkg/database"

	"gorm.io/gorm"
)

type Service struct{ db *gorm.DB }

func New(db *gorm.DB) *Service { return &Service{db: db} }

func round2(v float64) float64 { return math.Round(v*100) / 100 }

// Breakdown desglose de un comprobante. Las notas de crédito conservan importes POSITIVOS aquí; el
// signo lo decide cada reporte (registros: negativo; asientos: se invierte Debe/Haber).
type Breakdown struct {
	Exportation float64
	Taxed       float64
	Exonerated  float64
	Unaffected  float64
	Isc         float64
	Igv         float64
	Icbper      float64
	// OtherCharges otros cargos que no forman parte de la base imponible (recargo al consumo, cat. 53 código 46). Suman al total pero
	// no a la base ni al IGV; en el registro de ventas van en «otros tributos y cargos».
	OtherCharges float64
	Total       float64
}

func (b Breakdown) scale(f float64) Breakdown {
	return Breakdown{
		Exportation: round2(b.Exportation * f), Taxed: round2(b.Taxed * f), Exonerated: round2(b.Exonerated * f),
		Unaffected: round2(b.Unaffected * f), Isc: round2(b.Isc * f), Igv: round2(b.Igv * f),
		Icbper: round2(b.Icbper * f), OtherCharges: round2(b.OtherCharges * f), Total: round2(b.Total * f),
	}
}

type DocRef struct {
	SunatCode string
	Series    string
	Number    string
	Date      *time.Time
	// Base e IGV del documento afectado (Concar los pide en las notas).
	Taxable float64
	Igv     float64
}

// SaleDoc comprobante de venta normalizado.
type SaleDoc struct {
	ID               uint
	SunatCode        string // 01 factura, 03 boleta, 07 nota de crédito, 08 nota de débito
	Series           string
	Number           string // solo el correlativo (sin serie)
	FullNumber       string // serie-correlativo
	IssueDate        time.Time
	DueDate          *time.Time
	ContactDocType   string // código SUNAT de identidad: 6 RUC, 1 DNI, 4 CE, 7 pasaporte, 0 sin documento
	ContactDocNumber string
	ContactName      string
	Currency         string
	ExchangeRate     float64
	Voided           bool
	BillingStatus    string
	Observation      string
	PaymentCondition string
	Orig             Breakdown // en la moneda del comprobante
	PEN              Breakdown // convertido a soles (ceros si está anulado)
	Affected         *DocRef
}

func (d SaleDoc) IsCreditNote() bool { return d.SunatCode == "07" }

// PurchaseDoc comprobante de compra normalizado.
type PurchaseDoc struct {
	ID               uint
	SunatCode        string
	Series           string
	Number           string
	IssueDate        time.Time
	DueDate          *time.Time
	ContactDocType   string
	ContactDocNumber string
	ContactName      string
	Currency         string
	Orig             Breakdown
	PEN              Breakdown
	ExchangeRate     float64
	Observation      string
}

func (d PurchaseDoc) IsCreditNote() bool { return d.SunatCode == "07" }

// ParseMonth interpreta «YYYY-MM» y devuelve [inicio, fin) del mes.
func ParseMonth(m string) (time.Time, time.Time, error) {
	t, err := time.Parse("2006-01", strings.TrimSpace(m))
	if err != nil {
		return time.Time{}, time.Time{}, fmt.Errorf("mes inválido (use AAAA-MM)")
	}
	return t, t.AddDate(0, 1, 0), nil
}

func saleSunatCode(docType string) string {
	switch strings.ToUpper(strings.TrimSpace(docType)) {
	case "FACTURA":
		return "01"
	case "BOLETA":
		return "03"
	case "NOTA_CREDITO":
		return "07"
	case "NOTA_DEBITO":
		return "08"
	}
	return ""
}

func purchaseSunatCode(docType string) string {
	t := strings.ToUpper(strings.TrimSpace(docType))
	switch {
	case t == "FACTURA":
		return "01"
	case t == "BOLETA":
		return "03"
	case strings.HasPrefix(t, "NOTA DE CR") || t == "NOTA_CREDITO":
		return "07"
	case t == "TICKET":
		return "12"
	}
	return ""
}

// identityCode traduce el tipo de documento del contacto al catálogo SUNAT 06.
func identityCode(docType, docNumber string) string {
	switch strings.ToUpper(strings.TrimSpace(docType)) {
	case "RUC":
		return "6"
	case "DNI":
		return "1"
	case "CE", "CARNET DE EXTRANJERIA", "CARNET_EXTRANJERIA":
		return "4"
	case "PASAPORTE", "PASSPORT":
		return "7"
	}
	if strings.TrimSpace(docNumber) == "" || strings.Trim(docNumber, "0") == "" {
		return "0"
	}
	return "0"
}

type classTotals struct{ taxed, exo, unaf, exp, tax float64 }

// splitByAffectation reparte las líneas por tipo de afectación (catálogo 07: 1x gravado, 2x
// exonerado, 3x inafecto, 4x exportación) y reconcilia con los totales de cabecera: si las líneas no
// suman la base de la cabecera (descuentos globales), se prorratea para que el comprobante cuadre.
func splitByAffectation(items []lineRow, headerBase, headerTax float64) classTotals {
	var c classTotals
	var sum float64
	for _, it := range items {
		sum += it.Subtotal
		switch {
		case strings.HasPrefix(it.Affectation, "2"):
			c.exo += it.Subtotal
		case strings.HasPrefix(it.Affectation, "3"):
			c.unaf += it.Subtotal
		case strings.HasPrefix(it.Affectation, "4"):
			c.exp += it.Subtotal
		default:
			c.taxed += it.Subtotal
		}
	}
	if len(items) == 0 || sum == 0 {
		return classTotals{taxed: headerBase, tax: headerTax}
	}
	if math.Abs(sum-headerBase) > 0.01 {
		f := headerBase / sum
		c.taxed, c.exo, c.unaf, c.exp = c.taxed*f, c.exo*f, c.unaf*f, c.exp*f
	}
	c.tax = headerTax
	return c
}

type lineRow struct {
	ParentID    uint
	Affectation string
	Subtotal    float64
	TaxAmount   float64
}

func rateOrOne(cur string, rate *float64, warns *[]string, ref string) float64 {
	if strings.EqualFold(cur, "PEN") || cur == "" {
		return 1
	}
	if rate == nil || *rate <= 0 {
		*warns = append(*warns, fmt.Sprintf("%s está en %s sin tipo de cambio: se usó 1.00", ref, cur))
		return 1
	}
	return *rate
}

// SalesDocs devuelve los comprobantes fiscales de venta (01/03/07/08) emitidos en [from,to).
// Excluye borradores y los rechazados por SUNAT; las ventas anuladas se incluyen con importes en
// cero (el registro debe mostrar el número usado).
func (s *Service) SalesDocs(from, to time.Time) ([]SaleDoc, []string, error) {
	type row struct {
		database.TenantSale
		CDocType string
		CDocNum  string
		CName    string
	}
	var rows []row
	err := s.db.Table("tenant_sales").
		Select("tenant_sales.*, c.doc_type AS c_doc_type, c.doc_number AS c_doc_num, c.business_name AS c_name").
		Joins("LEFT JOIN tenant_contacts c ON c.id = tenant_sales.contact_id").
		Where("tenant_sales.deleted_at IS NULL AND tenant_sales.issue_date >= ? AND tenant_sales.issue_date < ?", from, to).
		Where("tenant_sales.status <> ? AND tenant_sales.billing_status <> ?", "draft", "rejected").
		Order("tenant_sales.series ASC, tenant_sales.correlative ASC").
		Scan(&rows).Error
	if err != nil {
		return nil, nil, err
	}
	var warns []string
	var ids, origIDs []uint
	for _, r := range rows {
		if saleSunatCode(r.DocType) == "" {
			continue
		}
		ids = append(ids, r.ID)
		if r.OriginalSaleID != nil {
			origIDs = append(origIDs, *r.OriginalSaleID)
		}
	}
	items := map[uint][]lineRow{}
	if len(ids) > 0 {
		var ls []struct {
			SaleID             uint
			IgvAffectationType string
			Subtotal           float64
			TaxAmount          float64
		}
		if err := s.db.Table("tenant_sale_items").Select("sale_id, igv_affectation_type, subtotal, tax_amount").
			Where("sale_id IN ?", ids).Scan(&ls).Error; err != nil {
			return nil, nil, err
		}
		for _, l := range ls {
			items[l.SaleID] = append(items[l.SaleID], lineRow{ParentID: l.SaleID, Affectation: l.IgvAffectationType, Subtotal: l.Subtotal, TaxAmount: l.TaxAmount})
		}
	}
	// Hospedaje a no domiciliados (0202): el adquirente real es el huésped, no el cliente genérico de la venta. El registro lo muestra con su
	// nombre y su pasaporte (o carné de extranjería). La tabla puede no existir en un tenant sin la migración V186.
	guests := map[uint]database.TenantSaleExport{}
	if len(ids) > 0 && s.db.Migrator().HasTable(&database.TenantSaleExport{}) {
		var ex []database.TenantSaleExport
		if err := s.db.Where("sale_id IN ? AND operation_type_code = ?", ids, "0202").Find(&ex).Error; err != nil {
			return nil, nil, err
		}
		for _, e := range ex {
			guests[e.SaleID] = e
		}
	}
	// Una venta «anulada» que tiene una nota de crédito vigente NO está dada de baja: el comprobante
	// original conserva sus importes y es la nota (en negativo) la que lo revierte. Poner en cero el
	// original Y restar la nota duplicaría la reversión.
	reversedByNote := map[uint]bool{}
	if len(ids) > 0 {
		var noted []uint
		if err := s.db.Table("tenant_sales").
			Where("original_sale_id IN ? AND doc_type = ? AND deleted_at IS NULL AND status <> ? AND billing_status <> ?", ids, "NOTA_CREDITO", "draft", "rejected").
			Pluck("original_sale_id", &noted).Error; err != nil {
			return nil, nil, err
		}
		for _, id := range noted {
			reversedByNote[id] = true
		}
	}
	orig := map[uint]database.TenantSale{}
	if len(origIDs) > 0 {
		var os []database.TenantSale
		if err := s.db.Where("id IN ?", origIDs).Find(&os).Error; err != nil {
			return nil, nil, err
		}
		for _, o := range os {
			orig[o.ID] = o
		}
	}

	out := make([]SaleDoc, 0, len(rows))
	for _, r := range rows {
		code := saleSunatCode(r.DocType)
		if code == "" {
			continue
		}
		cl := splitByAffectation(items[r.ID], r.Subtotal, r.TaxAmount)
		b := Breakdown{Taxed: round2(cl.taxed), Exonerated: round2(cl.exo), Unaffected: round2(cl.unaf), Exportation: round2(cl.exp), Igv: round2(cl.tax), OtherCharges: round2(r.ServiceChargeAmount), Total: round2(r.Total)}
		d := SaleDoc{
			ID: r.ID, SunatCode: code, Series: r.Series, Number: fmt.Sprintf("%d", r.Correlative),
			FullNumber: fmt.Sprintf("%s-%d", r.Series, r.Correlative), IssueDate: r.IssueDate, DueDate: r.DueDate,
			ContactDocType: identityCode(r.CDocType, r.CDocNum), ContactDocNumber: strings.TrimSpace(r.CDocNum),
			ContactName: strings.TrimSpace(r.CName), Currency: strings.ToUpper(r.Currency), ExchangeRate: 1,
			// Una nota de crédito aceptada es un documento válido aunque Tukifac la marque como «cancelled» al
			// anular la venta de origen: nunca se trata como dada de baja.
			Voided: r.Status == "cancelled" && !reversedByNote[r.ID] && code != "07", BillingStatus: r.BillingStatus, Observation: strings.TrimSpace(r.Notes),
			PaymentCondition: r.PaymentConditionCode, Orig: b,
		}
		if d.Currency == "" {
			d.Currency = "PEN"
		}
		if g, ok := guests[r.ID]; ok && strings.TrimSpace(g.GuestName) != "" {
			d.ContactName, d.ContactDocType, d.ContactDocNumber = g.GuestName, g.GuestDocType, g.GuestDocNumber
		}
		d.ExchangeRate = rateOrOne(d.Currency, r.ExchangeRate, &warns, d.FullNumber)
		d.PEN = b.scale(d.ExchangeRate)
		if d.Voided {
			d.PEN = Breakdown{}
		}
		if code == "07" || code == "08" {
			d.Affected = s.affectedOf(r.TenantSale, orig)
		}
		out = append(out, d)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Series != out[j].Series {
			return out[i].Series < out[j].Series
		}
		return out[i].ID < out[j].ID
	})
	return out, warns, nil
}

func (s *Service) affectedOf(n database.TenantSale, orig map[uint]database.TenantSale) *DocRef {
	if n.OriginalSaleID != nil {
		if o, ok := orig[*n.OriginalSaleID]; ok {
			d := o.IssueDate
			return &DocRef{SunatCode: saleSunatCode(o.DocType), Series: o.Series, Number: fmt.Sprintf("%d", o.Correlative), Date: &d, Taxable: o.Subtotal, Igv: o.TaxAmount}
		}
	}
	if n.ManualAffectedDocNumber != "" {
		series, num := n.ManualAffectedDocNumber, ""
		if i := strings.Index(series, "-"); i > 0 {
			series, num = series[:i], strings.TrimLeft(series[i+1:], "0")
		}
		return &DocRef{SunatCode: n.ManualAffectedDocType, Series: series, Number: num}
	}
	return nil
}

// PurchaseDocs comprobantes de compra del periodo (excluye anuladas).
func (s *Service) PurchaseDocs(from, to time.Time) ([]PurchaseDoc, []string, error) {
	type row struct {
		database.TenantPurchase
		CDocType string
		CDocNum  string
		CName    string
	}
	var rows []row
	err := s.db.Table("tenant_purchases").
		Select("tenant_purchases.*, c.doc_type AS c_doc_type, c.doc_number AS c_doc_num, c.business_name AS c_name").
		Joins("LEFT JOIN tenant_contacts c ON c.id = tenant_purchases.contact_id").
		Where("tenant_purchases.deleted_at IS NULL AND tenant_purchases.issue_date >= ? AND tenant_purchases.issue_date < ?", from, to).
		Where("tenant_purchases.status <> ?", "cancelled").
		Order("tenant_purchases.issue_date ASC, tenant_purchases.id ASC").
		Scan(&rows).Error
	if err != nil {
		return nil, nil, err
	}
	var ids []uint
	for _, r := range rows {
		ids = append(ids, r.ID)
	}
	items := map[uint][]lineRow{}
	if len(ids) > 0 {
		var ls []struct {
			PurchaseID         uint
			IgvAffectationType string
			Subtotal           float64
			TaxAmount          float64
		}
		if err := s.db.Table("tenant_purchase_items").Select("purchase_id, igv_affectation_type, subtotal, tax_amount").
			Where("purchase_id IN ?", ids).Scan(&ls).Error; err != nil {
			return nil, nil, err
		}
		for _, l := range ls {
			items[l.PurchaseID] = append(items[l.PurchaseID], lineRow{ParentID: l.PurchaseID, Affectation: l.IgvAffectationType, Subtotal: l.Subtotal, TaxAmount: l.TaxAmount})
		}
	}
	var warns []string
	out := make([]PurchaseDoc, 0, len(rows))
	for _, r := range rows {
		code := purchaseSunatCode(r.DocType)
		cl := splitByAffectation(items[r.ID], r.Subtotal, r.TaxAmount)
		b := Breakdown{Taxed: round2(cl.taxed), Exonerated: round2(cl.exo), Unaffected: round2(cl.unaf), Exportation: round2(cl.exp), Igv: round2(cl.tax), Total: round2(r.Total)}
		d := PurchaseDoc{
			ID: r.ID, SunatCode: code, Series: r.Series, Number: strings.TrimLeft(r.Number, "0"),
			IssueDate: r.IssueDate, DueDate: r.DueDate, ContactDocType: identityCode(r.CDocType, r.CDocNum),
			ContactDocNumber: strings.TrimSpace(r.CDocNum), ContactName: strings.TrimSpace(r.CName),
			Currency: strings.ToUpper(r.Currency), Orig: b, Observation: strings.TrimSpace(r.Notes),
		}
		if d.Number == "" {
			d.Number = r.Number
		}
		if d.Currency == "" {
			d.Currency = "PEN"
		}
		d.ExchangeRate = rateOrOne(d.Currency, r.ExchangeRate, &warns, fmt.Sprintf("Compra %s-%s", r.Series, r.Number))
		d.PEN = b.scale(d.ExchangeRate)
		out = append(out, d)
	}
	return out, warns, nil
}
