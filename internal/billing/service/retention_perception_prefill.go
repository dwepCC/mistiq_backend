package service

import (
	"fmt"
	"strings"
	"time"

	"tukifac/internal/sales/nvdisplay"
	"tukifac/pkg/database"
	"tukifac/pkg/salecurrency"
)

func normalizeRelatedDocType(docType string) string {
	t := strings.TrimSpace(strings.ToUpper(docType))
	if len(t) == 2 && t[0] >= '0' && t[0] <= '9' {
		return t
	}
	switch {
	case strings.Contains(t, "FACTURA"):
		return "01"
	case strings.Contains(t, "BOLETA"):
		return "03"
	case strings.Contains(t, "CREDITO") || strings.Contains(t, "CRÉDITO") || t == "NC":
		return "07"
	case strings.Contains(t, "DEBITO") || strings.Contains(t, "DÉBITO") || t == "ND":
		return "08"
	case strings.Contains(t, "TICKET"):
		return "12"
	default:
		return "01"
	}
}

func formatRelatedIssueDate(t time.Time) string {
	if t.IsZero() {
		return time.Now().Format(time.RFC3339)
	}
	return t.Format(time.RFC3339)
}

func (s *BillingService) applyRetentionPrefillFromPurchase(input *CreateRetentionInput, purchaseID uint) error {
	var p database.TenantPurchase
	if err := s.db.First(&p, purchaseID).Error; err != nil {
		return fmt.Errorf("compra origen no encontrada: %w", err)
	}
	if strings.EqualFold(strings.TrimSpace(p.Status), "cancelled") {
		return fmt.Errorf("no se puede emitir retención desde una compra anulada")
	}
	if p.ContactID == nil || *p.ContactID == 0 {
		return fmt.Errorf("la compra no tiene proveedor vinculado; asigne un contacto en Compras")
	}
	if input.BranchID == 0 {
		input.BranchID = p.BranchID
	}
	if input.ContactID == 0 {
		input.ContactID = *p.ContactID
	}
	if strings.TrimSpace(input.Regimen) == "" {
		input.Regimen = "01"
	}
	if input.Tasa == 0 {
		input.Tasa = retentionRegimenTasa["01"]
	}
	if strings.TrimSpace(input.FechaEmision) == "" {
		input.FechaEmision = formatRelatedIssueDate(time.Now())
	}
	if len(input.Details) == 0 || (len(input.Details) == 1 && strings.TrimSpace(input.Details[0].NumDoc) == "") {
		issueDate := formatRelatedIssueDate(p.IssueDate)
		moneda := strings.TrimSpace(p.Currency)
		if moneda == "" {
			moneda = salecurrency.CurrencyPEN
		}
		total := p.Total
		detail := RetentionDetailInput{
			TipoDoc:        normalizeRelatedDocType(p.DocType),
			NumDoc:         nvdisplay.FormatDocumentNumber(p.Series, p.Number),
			FechaEmision:   issueDate,
			ImpTotal:       total,
			Moneda:         moneda,
			FechaRetencion: input.FechaEmision,
			Pagos: []retentionPaymentInput{{
				Moneda:  moneda,
				Importe: total,
				Fecha:   p.IssueDate.Format("2006-01-02"),
			}},
		}
		input.Details = []RetentionDetailInput{detail}
	}
	return nil
}

func (s *BillingService) applyPerceptionPrefillFromSale(input *CreatePerceptionInput, sourceSaleID uint) error {
	var src database.TenantSale
	if err := s.db.First(&src, sourceSaleID).Error; err != nil {
		return fmt.Errorf("venta origen no encontrada: %w", err)
	}
	srcCode := strings.TrimSpace(getSeriesSunatCode(s.db, src.SeriesID))
	if srcCode == "" {
		srcCode = normalizeRelatedDocType(src.DocType)
	}
	if srcCode != "01" && srcCode != "03" {
		return fmt.Errorf("solo se puede generar percepción desde factura (01) o boleta (03)")
	}
	if strings.EqualFold(strings.TrimSpace(src.Status), "cancelled") {
		return fmt.Errorf("no se puede emitir percepción desde una venta anulada")
	}
	if src.ContactID == nil || *src.ContactID == 0 {
		return fmt.Errorf("la venta no tiene cliente vinculado; asigne un contacto antes de emitir")
	}
	if input.BranchID == 0 {
		input.BranchID = src.BranchID
	}
	if input.ContactID == 0 {
		input.ContactID = *src.ContactID
	}
	if strings.TrimSpace(input.Regimen) == "" {
		input.Regimen = "01"
	}
	if input.Tasa == 0 {
		input.Tasa = perceptionRegimenTasa["01"]
	}
	if strings.TrimSpace(input.FechaEmision) == "" {
		input.FechaEmision = formatRelatedIssueDate(time.Now())
	}
	if len(input.Details) == 0 || (len(input.Details) == 1 && strings.TrimSpace(input.Details[0].NumDoc) == "") {
		issueDate := formatRelatedIssueDate(src.IssueDate)
		moneda := strings.TrimSpace(src.Currency)
		if moneda == "" {
			moneda = salecurrency.CurrencyPEN
		}
		total := src.Total
		cobroFecha := src.IssueDate.Format("2006-01-02")
		detail := PerceptionDetailInput{
			TipoDoc:         srcCode,
			NumDoc:          nvdisplay.FormatDocumentNumber(src.Series, src.Number),
			FechaEmision:    issueDate,
			ImpTotal:        total,
			Moneda:          moneda,
			FechaPercepcion: input.FechaEmision,
			Cobros: []retentionPaymentInput{{
				Moneda:  moneda,
				Importe: total,
				Fecha:   cobroFecha,
			}},
		}
		if moneda == salecurrency.CurrencyUSD && src.ExchangeRate != nil && *src.ExchangeRate > 0 {
			factor := *src.ExchangeRate
			fechaTC := src.IssueDate.Format("2006-01-02")
			detail.TipoCambio = &retentionExchangeInput{
				MonedaRef: salecurrency.CurrencyUSD,
				MonedaObj: salecurrency.CurrencyPEN,
				Factor:    factor,
				Fecha:     fechaTC,
			}
		}
		input.Details = []PerceptionDetailInput{detail}
	}
	return nil
}
