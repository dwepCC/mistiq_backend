package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	salesvc "tukifac/internal/sales/service"
	"tukifac/pkg/billingstate"
	"tukifac/pkg/database"
	"tukifac/pkg/docseries"
	"tukifac/pkg/money"
	"tukifac/pkg/sunatnote"
	"tukifac/pkg/tax"
)

// IndependentNoteItemInput ítem cargado a mano (Fase 3) — no viene de una venta local, así
// que trae todo lo necesario para calcular su propio subtotal/IGV/total.
type IndependentNoteItemInput struct {
	Code               string  `json:"code"`
	Description        string  `json:"description"`
	Unit               string  `json:"unit"`
	Quantity           float64 `json:"quantity"`
	UnitPrice          float64 `json:"unit_price"`
	IgvAffectationType string  `json:"igv_affectation_type"`
	// PriceIncludesIgv: si el precio unitario ya trae IGV (venta al público típica) o no
	// (precio neto). Default true si no se manda.
	PriceIncludesIgv *bool `json:"price_includes_igv,omitempty"`
}

// IndependentNoteInput datos para emitir una NC/ND sin partir de una venta de Tukifac —
// referenciando un comprobante afectado por serie/número/tipo, local o de otro sistema
// (histórico, migración, otro canal). Mismo rol que el Flujo B del sistema legado
// (documento_afectado con data_affected_document cuando no hay FK local).
type IndependentNoteInput struct {
	BranchID   uint   `json:"branch_id"`
	DocType    string `json:"doc_type"`    // "07" nota de crédito, "08" nota de débito
	ReasonCode string `json:"reason_code"` // catálogo SUNAT 09/10
	Reason     string `json:"reason"`
	// AffectedDocType "01" factura, "03" boleta — el comprobante que la nota modifica.
	AffectedDocType string                     `json:"affected_doc_type"`
	AffectedSeries  string                     `json:"affected_series"`
	AffectedNumber  string                     `json:"affected_number"`
	ContactID       uint                       `json:"contact_id"`
	Currency        string                     `json:"currency"`
	Items           []IndependentNoteItemInput `json:"items"`
}

// CreateIndependentNote genera y envía una NC/ND que no nace de una venta emitida por
// Tukifac — el comprobante afectado se declara a mano. Habilita casos que antes eran
// imposibles: comprobantes de otro sistema (migración del legado), de otro canal de venta,
// o previos a que el tenant usara Tukifac.
func (s *BillingService) CreateIndependentNote(input IndependentNoteInput) (*database.TenantSale, *database.TenantInvoice, error) {
	if !s.facturadorConfigured() {
		return nil, nil, errors.New("la emisión de notas requiere facturador configurado")
	}
	var cfg database.TenantCompanyConfig
	if err := s.db.First(&cfg).Error; err != nil || !cfg.SunatEnabled {
		return nil, nil, errors.New("la conexión con SUNAT no está activada")
	}
	docType := strings.TrimSpace(input.DocType)
	if docType != "07" && docType != "08" {
		return nil, nil, errors.New("tipo de documento inválido: use 07 (nota de crédito) u 08 (nota de débito)")
	}
	reasonCode := strings.TrimSpace(input.ReasonCode)
	if reasonCode == "" {
		return nil, nil, errors.New("indique el motivo de la nota")
	}
	if sunatnote.ReasonLabel(docType, reasonCode) == "" {
		catalog := "09"
		if docType == "08" {
			catalog = "10"
		}
		return nil, nil, fmt.Errorf("motivo inválido: %q no existe en el catálogo SUNAT %s", reasonCode, catalog)
	}
	reason := strings.TrimSpace(input.Reason)
	if reason == "" {
		reason = sunatnote.ReasonLabel(docType, reasonCode)
	}
	affectedDocType := strings.TrimSpace(input.AffectedDocType)
	if affectedDocType != "01" && affectedDocType != "03" {
		return nil, nil, errors.New("indique si el comprobante afectado es factura (01) o boleta (03)")
	}
	affectedSeries := strings.ToUpper(strings.TrimSpace(input.AffectedSeries))
	affectedNumber := strings.TrimSpace(input.AffectedNumber)
	if affectedSeries == "" || affectedNumber == "" {
		return nil, nil, errors.New("indique serie y número del comprobante afectado")
	}
	if input.BranchID == 0 {
		return nil, nil, errors.New("seleccione la sucursal")
	}
	if input.ContactID == 0 {
		return nil, nil, errors.New("seleccione el cliente")
	}
	var contact database.TenantContact
	if err := s.db.First(&contact, input.ContactID).Error; err != nil {
		return nil, nil, errors.New("cliente no encontrado")
	}
	if len(input.Items) == 0 {
		return nil, nil, errors.New("agregue al menos un ítem")
	}

	series, err := s.resolveIndependentNoteSeries(input.BranchID, docType, affectedDocType)
	if err != nil {
		return nil, nil, err
	}

	taxCfg := tax.LoadFromDB(s.db)

	items := make([]database.TenantSaleItem, 0, len(input.Items))
	var subtotal, taxAmount, total float64
	for _, in := range input.Items {
		desc := strings.TrimSpace(in.Description)
		if desc == "" {
			return nil, nil, errors.New("todo ítem necesita una descripción")
		}
		if in.Quantity <= 0 {
			return nil, nil, fmt.Errorf("cantidad inválida para %q: debe ser mayor a cero", desc)
		}
		if in.UnitPrice < 0 {
			return nil, nil, fmt.Errorf("precio inválido para %q", desc)
		}
		igvType := strings.TrimSpace(in.IgvAffectationType)
		if igvType == "" {
			igvType = "10"
		}
		priceIncludesIgv := true
		if in.PriceIncludesIgv != nil {
			priceIncludesIgv = *in.PriceIncludesIgv
		}
		unit := strings.TrimSpace(in.Unit)
		if unit == "" {
			unit = "NIU"
		}
		itSub, itTax, itTot := tax.CalcItem(in.UnitPrice, in.Quantity, 0, igvType, priceIncludesIgv, taxCfg)
		items = append(items, database.TenantSaleItem{
			Code:               strings.TrimSpace(in.Code),
			Description:        desc,
			Unit:               unit,
			Quantity:           in.Quantity,
			UnitPrice:          in.UnitPrice,
			IgvAffectationType: igvType,
			Subtotal:           itSub,
			TaxAmount:          itTax,
			Total:              itTot,
		})
		subtotal = money.RoundSunat(subtotal + itSub)
		taxAmount = money.RoundSunat(taxAmount + itTax)
		total = money.RoundSunat(total + itTot)
	}

	saleSvc := salesvc.NewSaleService(s.db)
	nextCorr, err := saleSvc.NextCorrelative(series.ID)
	if err != nil {
		return nil, nil, err
	}
	numberStr := fmt.Sprintf("%s-%08d", series.Series, nextCorr)
	now := time.Now()
	currency := strings.TrimSpace(input.Currency)
	if currency == "" {
		currency = "PEN"
	}
	docKind := "NOTA_CREDITO"
	if docType == "08" {
		docKind = "NOTA_DEBITO"
	}
	noteSale := database.TenantSale{
		BranchID:                input.BranchID,
		ContactID:               &input.ContactID,
		SeriesID:                series.ID,
		DocType:                 docKind,
		Series:                  series.Series,
		Correlative:             nextCorr,
		Number:                  numberStr,
		IssueDate:               now,
		Subtotal:                subtotal,
		TaxAmount:               taxAmount,
		Total:                   total,
		Currency:                currency,
		Notes:                   reason,
		NoteReasonCode:          reasonCode,
		Status:                  "paid",
		BillingStatus:           "pending",
		OriginalSaleID:          nil,
		ManualAffectedDocType:   affectedDocType,
		ManualAffectedDocNumber: affectedSeries + "-" + strings.TrimLeft(affectedNumber, "0"),
		SaleOrigin:              "independent_note",
	}
	if noteSale.ManualAffectedDocNumber == affectedSeries+"-" {
		// El número era todo ceros ("0"): TrimLeft lo dejó vacío, no puede quedar sin dígito.
		noteSale.ManualAffectedDocNumber = affectedSeries + "-0"
	}
	if err := s.db.Create(&noteSale).Error; err != nil {
		return nil, nil, fmt.Errorf("crear nota independiente: %w", err)
	}
	reserveKind := "credit_note"
	if docType == "08" {
		reserveKind = "debit_note"
	}
	if err := s.reserveGenericDocument(reserveKind, noteSale.ID, noteSale.Number); err != nil {
		return nil, nil, err
	}
	for i := range items {
		items[i].SaleID = noteSale.ID
		if err := s.db.Create(&items[i]).Error; err != nil {
			return nil, nil, fmt.Errorf("crear ítem de nota: %w", err)
		}
	}

	payload, err := s.buildNotePayload(noteSale.ID)
	if err != nil {
		return nil, nil, err
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return nil, nil, err
	}
	inv := database.TenantInvoice{
		SaleID:          noteSale.ID,
		NotePayloadJSON: string(payloadJSON),
		PipelineStatus:  billingstate.DRAFT,
		SunatStatus:     "pending",
	}
	if err := s.db.Create(&inv).Error; err != nil {
		return nil, nil, fmt.Errorf("crear registro fiscal de la nota: %w", err)
	}

	tenantDB := s.lookupTenantDBName(s.centralTenantID)
	inv2, err := s.EnqueueSendToSUNAT(noteSale.ID, s.centralTenantID, s.tenantSlug, tenantDB, FiscalSourceQueue)
	if err != nil {
		return &noteSale, inv2, err
	}
	if inv2 != nil {
		inv = *inv2
	}
	return &noteSale, &inv, nil
}

// resolveIndependentNoteSeries elige la serie NC/ND según el tipo de comprobante afectado
// declarado a mano — mismo criterio de prefijo (FC/BC, FD/BD) que resolveCreditNoteSeries y
// resolveDebitNoteSeries, sin depender de una venta local.
func (s *BillingService) resolveIndependentNoteSeries(branchID uint, tipoDoc, affectedDocType string) (database.TenantDocumentSeries, error) {
	affectedKind := "BOLETA"
	if affectedDocType == "01" {
		affectedKind = "FACTURA"
	}
	category := "nota_credito"
	noteLabel := "nota de crédito"
	var prefix string
	matches := docseries.SeriesMatchesCreditNotePrefix
	if tipoDoc == "08" {
		category = "nota_debito"
		noteLabel = "nota de débito"
		prefix = docseries.DebitNoteSeriesPrefixForAffected(affectedKind, affectedDocType)
		matches = docseries.SeriesMatchesDebitNotePrefix
	} else {
		prefix = docseries.CreditNoteSeriesPrefixForAffected(affectedKind, affectedDocType)
	}
	var rows []database.TenantDocumentSeries
	if err := s.db.Where("branch_id = ? AND category = ? AND active = ? AND TRIM(sunat_code) = ?",
		branchID, category, true, tipoDoc).Order("id ASC").Find(&rows).Error; err != nil {
		return database.TenantDocumentSeries{}, err
	}
	for _, row := range rows {
		if matches(row.Series, prefix) {
			return row, nil
		}
	}
	docLabel := docseries.AffectedDocLabel(affectedKind, affectedDocType)
	if len(rows) == 0 {
		return database.TenantDocumentSeries{}, fmt.Errorf(
			"no hay serie de %s en esta sucursal — cree una serie %s## activa para %ss",
			noteLabel, prefix, docLabel,
		)
	}
	return database.TenantDocumentSeries{}, fmt.Errorf(
		"ninguna serie de %s coincide con %s: configure serie %s## (ej. %s01)",
		noteLabel, docLabel, prefix, prefix,
	)
}
