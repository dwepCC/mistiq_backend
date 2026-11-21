package service

import (
	"errors"
	"sort"
	"strings"
	"time"

	"tukifac/pkg/database"
	"tukifac/pkg/paymentcondition"
	"tukifac/pkg/salescope"

	"gorm.io/gorm"
)

// Tipos de comprobante que lista el detalle de caja.
const (
	SessionDocKindNotaVenta = "nota_venta"
	SessionDocKindBoleta    = "boleta"
	SessionDocKindFactura   = "factura"
)

// SessionDocumentRow comprobante (vigente o anulado) registrado en una sesión de caja.
type SessionDocumentRow struct {
	SaleID        uint          `json:"sale_id"`
	Kind          string        `json:"kind"` // nota_venta | boleta | factura
	DocNumber     string        `json:"doc_number"`
	CreatedAt     time.Time     `json:"created_at"`
	ContactName   string        `json:"contact_name"`
	Total         float64       `json:"total"`
	Currency      string        `json:"currency"`
	Status        string        `json:"status"`
	Cancelled     bool          `json:"cancelled"`
	CancelReason  string        `json:"cancel_reason"`
	PaymentMethod string        `json:"payment_method"` // método principal (o "mixto")
	Payments      []MethodTotal `json:"payments"`
}

// SessionDocumentKindSummary subtotal por tipo de comprobante (solo vigentes) y anulados del tipo.
type SessionDocumentKindSummary struct {
	Kind           string  `json:"kind"`
	Count          int     `json:"count"`
	Total          float64 `json:"total"`
	CancelledCount int     `json:"cancelled_count"`
	CancelledTotal float64 `json:"cancelled_total"`
}

// SessionDocumentsReport listado de comprobantes de la sesión con subtotales.
type SessionDocumentsReport struct {
	Documents      []SessionDocumentRow         `json:"documents"`
	ByKind         []SessionDocumentKindSummary `json:"by_kind"`
	ActiveCount    int                          `json:"active_count"`
	ActiveTotal    float64                      `json:"active_total"`
	CancelledCount int                          `json:"cancelled_count"`
	CancelledTotal float64                      `json:"cancelled_total"`
}

// classifySessionDocKind decide el tipo de comprobante: el código SUNAT de la serie manda (01 factura,
// 03 boleta, 00 nota de venta); sin serie conocida se infiere del texto de doc_type.
func classifySessionDocKind(sunatCode, docType string) string {
	switch strings.TrimSpace(sunatCode) {
	case "01":
		return SessionDocKindFactura
	case "03":
		return SessionDocKindBoleta
	case "00":
		return SessionDocKindNotaVenta
	}
	dt := strings.ToLower(strings.TrimSpace(docType))
	switch {
	case strings.Contains(dt, "factura"):
		return SessionDocKindFactura
	case strings.Contains(dt, "boleta"):
		return SessionDocKindBoleta
	}
	return SessionDocKindNotaVenta
}

// summarizeSessionDocuments arma los subtotales por tipo y los totales vigente/anulado.
func summarizeSessionDocuments(rows []SessionDocumentRow) SessionDocumentsReport {
	out := SessionDocumentsReport{Documents: rows}
	byKind := map[string]*SessionDocumentKindSummary{}
	for _, r := range rows {
		k := byKind[r.Kind]
		if k == nil {
			k = &SessionDocumentKindSummary{Kind: r.Kind}
			byKind[r.Kind] = k
		}
		if r.Cancelled {
			k.CancelledCount++
			k.CancelledTotal += r.Total
			out.CancelledCount++
			out.CancelledTotal += r.Total
			continue
		}
		k.Count++
		k.Total += r.Total
		out.ActiveCount++
		out.ActiveTotal += r.Total
	}
	order := map[string]int{SessionDocKindNotaVenta: 0, SessionDocKindBoleta: 1, SessionDocKindFactura: 2}
	for _, k := range byKind {
		out.ByKind = append(out.ByKind, *k)
	}
	sort.Slice(out.ByKind, func(i, j int) bool { return order[out.ByKind[i].Kind] < order[out.ByKind[j].Kind] })
	if out.Documents == nil {
		out.Documents = []SessionDocumentRow{}
	}
	if out.ByKind == nil {
		out.ByKind = []SessionDocumentKindSummary{}
	}
	return out
}

// cancelReasonFromSaleNotes extrae el motivo tras "ANULADA:" de las notas internas de la venta.
func cancelReasonFromSaleNotes(notes string) string {
	i := strings.Index(notes, "ANULADA:")
	if i < 0 {
		return ""
	}
	return strings.TrimSpace(notes[i+len("ANULADA:"):])
}

// GetSessionDocumentsReport lista los comprobantes de una sesión de caja (notas de venta, boletas y
// facturas; vigentes y anulados). Excluye borradores, notas de crédito/débito y los comprobantes
// emitidos por conversión desde una nota de venta (la nota ya cuenta la misma venta). Incluye también
// las ventas vigentes sin sesión propia de la ventana de la sesión, igual que GetSessionReport.
func (s *CashBankService) GetSessionDocumentsReport(sessionID uint) (*SessionDocumentsReport, error) {
	var session database.TenantCashSession
	if err := s.db.First(&session, sessionID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("sesión de caja no encontrada")
		}
		return nil, err
	}

	var sales []database.TenantSale
	if err := salescope.CommercialSales(s.db.Model(&database.TenantSale{})).
		Where("cash_session_id = ? AND status <> ?", sessionID, "draft").
		Where("doc_type NOT IN ?", salescope.NoteDocTypes).
		Order("created_at ASC").Find(&sales).Error; err != nil {
		return nil, err
	}
	seen := make(map[uint]struct{}, len(sales))
	for _, sale := range sales {
		seen[sale.ID] = struct{}{}
	}
	orphans, _ := s.listOrphanSalesForSession(&session)
	for _, sale := range orphans {
		if _, ok := seen[sale.ID]; !ok {
			sales = append(sales, sale)
		}
	}
	sort.SliceStable(sales, func(i, j int) bool { return sales[i].CreatedAt.Before(sales[j].CreatedAt) })

	saleIDs := make([]uint, 0, len(sales))
	seriesIDs := make(map[uint]struct{})
	contactIDs := make(map[uint]struct{})
	salesMap := make(map[uint]database.TenantSale, len(sales))
	for _, sale := range sales {
		saleIDs = append(saleIDs, sale.ID)
		salesMap[sale.ID] = sale
		seriesIDs[sale.SeriesID] = struct{}{}
		if sale.ContactID != nil {
			contactIDs[*sale.ContactID] = struct{}{}
		}
	}

	sunatBySeries := make(map[uint]string)
	if len(seriesIDs) > 0 && s.db.Migrator().HasTable(&database.TenantDocumentSeries{}) {
		var series []database.TenantDocumentSeries
		s.db.Select("id", "sunat_code").Where("id IN ?", keysUint(seriesIDs)).Find(&series)
		for _, sr := range series {
			sunatBySeries[sr.ID] = sr.SunatCode
		}
	}
	contactNames := map[uint]string{}
	if len(contactIDs) > 0 {
		contactNames = loadContactNamesMap(s.db, contactIDs)
	}

	paymentsBySale := make(map[uint][]database.TenantSalePayment)
	var amounts map[uint]float64
	if len(saleIDs) > 0 {
		var payments []database.TenantSalePayment
		s.db.Where("sale_id IN ?", saleIDs).Order("created_at ASC").Find(&payments)
		for _, p := range payments {
			paymentsBySale[p.SaleID] = append(paymentsBySale[p.SaleID], p)
		}
		amounts = buildSalePaymentReportAmountsFromPayments(salesMap, payments)
	}

	rows := make([]SessionDocumentRow, 0, len(sales))
	for _, sale := range sales {
		row := SessionDocumentRow{
			SaleID:      sale.ID,
			Kind:        classifySessionDocKind(sunatBySeries[sale.SeriesID], sale.DocType),
			DocNumber:   sale.Number,
			CreatedAt:   sale.CreatedAt,
			Total:       sale.Total,
			Currency:    sale.Currency,
			Status:      sale.Status,
			Cancelled:   strings.EqualFold(sale.Status, "cancelled"),
			Payments:    []MethodTotal{},
		}
		if sale.ContactID != nil {
			row.ContactName = contactNames[*sale.ContactID]
		}
		if row.Cancelled {
			row.CancelReason = cancelReasonFromSaleNotes(sale.Notes)
		}

		byMethod := map[string]float64{}
		for _, p := range paymentsBySale[sale.ID] {
			meth := normalizeReportMethod(p.Method)
			if paymentcondition.IsCreditCode(meth) || paymentcondition.IsCreditCode(p.Method) {
				continue
			}
			byMethod[meth] += paymentReportAmount(p.Amount, p.ID, amounts)
		}
		if len(byMethod) == 0 {
			isCredit := paymentcondition.IsCreditCode(sale.PaymentConditionCode) || paymentcondition.IsCreditCode(sale.PaymentMethod)
			switch {
			case isCredit:
				byMethod["credito"] = sale.Total
			case strings.TrimSpace(sale.PaymentMethod) != "":
				byMethod[normalizeReportMethod(sale.PaymentMethod)] = sale.Total
			}
		}
		row.Payments = mapToMethodTotals(byMethod)
		sort.Slice(row.Payments, func(i, j int) bool { return row.Payments[i].Method < row.Payments[j].Method })
		switch len(row.Payments) {
		case 0:
		case 1:
			row.PaymentMethod = row.Payments[0].Method
		default:
			row.PaymentMethod = "mixto"
		}
		rows = append(rows, row)
	}

	report := summarizeSessionDocuments(rows)
	return &report, nil
}

