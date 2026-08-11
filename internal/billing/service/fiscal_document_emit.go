package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"tukifac/pkg/database"
	"tukifac/pkg/docseries"
	"tukifac/pkg/facturador"
)

// emitFiscalDocumentBySale enruta emisión según tipo SUNAT de la venta.
func (s *BillingService) emitFiscalDocumentBySale(saleID uint, companyCfg *database.TenantCompanyConfig) (*database.TenantInvoice, error) {
	sunatCode := s.saleSunatCodeByID(saleID)
	switch sunatCode {
	case "07", "08":
		return s.emitNoteDocument(saleID, companyCfg)
	case "09", "31":
		return s.emitDespatchDocument(saleID, companyCfg)
	case "20":
		return s.emitRetentionDocument(saleID, companyCfg)
	case "40":
		return s.emitPerceptionDocument(saleID, companyCfg)
	default:
		return s.emitInvoiceDocument(saleID, companyCfg)
	}
}

func (s *BillingService) saleSunatCodeByID(saleID uint) string {
	var sale database.TenantSale
	if err := s.db.First(&sale, saleID).Error; err != nil {
		return ""
	}
	sunatCode := strings.TrimSpace(getSeriesSunatCode(s.db, sale.SeriesID))
	if sunatCode != "" {
		return sunatCode
	}
	switch strings.ToUpper(strings.TrimSpace(sale.DocType)) {
	case "NOTA_CREDITO":
		return "07"
	case "NOTA_DEBITO":
		return "08"
	case "GUIA_REMISION", "GUIA_TRANSPORTISTA":
		if strings.Contains(strings.ToUpper(sale.DocType), "TRANSPORT") {
			return "31"
		}
		return "09"
	case "RETENCION":
		return "20"
	case "PERCEPCION":
		return "40"
	case "FACTURA":
		return "01"
	default:
		return "03"
	}
}

func (s *BillingService) emitNoteDocument(saleID uint, companyCfg *database.TenantCompanyConfig) (*database.TenantInvoice, error) {
	var inv database.TenantInvoice
	_ = s.db.Where("sale_id = ?", saleID).First(&inv).Error
	if strings.TrimSpace(inv.NotePayloadJSON) != "" && !shouldRegenerateNotePayload(&inv) {
		tipo := s.saleSunatCodeByID(saleID)
		if tipo == "" {
			tipo = "07"
		}
		payload := enrichFiscalPayloadJSON(inv.NotePayloadJSON, tipo, "note")
		return s.enqueueFiscalMicroservice(saleID, companyCfg, nil, payload)
	}
	return s.buildAndPersistNotePayload(saleID, companyCfg)
}

// buildAndPersistNotePayload reconstruye NC/ND desde la venta y persiste note_payload_json.
func (s *BillingService) buildAndPersistNotePayload(saleID uint, companyCfg *database.TenantCompanyConfig) (*database.TenantInvoice, error) {
	payload, err := s.buildNotePayload(saleID)
	if err != nil {
		return nil, err
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	payloadJSON := enrichFiscalPayloadJSON(string(b), payload.TipoDoc, "note")
	_ = s.db.Model(&database.TenantInvoice{}).Where("sale_id = ?", saleID).
		Update("note_payload_json", payloadJSON).Error
	return s.enqueueFiscalMicroservice(saleID, companyCfg, nil, payloadJSON)
}

func (s *BillingService) emitDespatchDocument(saleID uint, companyCfg *database.TenantCompanyConfig) (*database.TenantInvoice, error) {
	var despatch database.TenantDespatch
	if err := s.db.Where("sale_id = ?", saleID).First(&despatch).Error; err != nil {
		return nil, fmt.Errorf("guía no encontrada para venta %d: %w", saleID, err)
	}
	if strings.TrimSpace(despatch.PayloadJSON) == "" {
		return nil, errors.New("payload de guía vacío")
	}
	tipo := s.saleSunatCodeByID(saleID)
	if tipo == "" {
		tipo = "09"
	}
	kind := "guia_remision"
	if tipo == "31" {
		kind = "guia_transportista"
	}
	payload := enrichDespatchFiscalPayloadJSON(despatch.PayloadJSON, tipo, kind)
	return s.enqueueFiscalMicroservice(saleID, companyCfg, nil, payload)
}

func (s *BillingService) emitRetentionDocument(saleID uint, companyCfg *database.TenantCompanyConfig) (*database.TenantInvoice, error) {
	var rec database.TenantRetention
	if err := s.db.Where("sale_id = ?", saleID).First(&rec).Error; err != nil {
		return nil, fmt.Errorf("retención no encontrada para venta %d: %w", saleID, err)
	}
	if strings.TrimSpace(rec.PayloadJSON) == "" {
		return nil, errors.New("payload de retención vacío")
	}
	payload := enrichFiscalPayloadJSON(rec.PayloadJSON, "20", "retention")
	return s.enqueueFiscalMicroservice(saleID, companyCfg, nil, payload)
}

func (s *BillingService) emitPerceptionDocument(saleID uint, companyCfg *database.TenantCompanyConfig) (*database.TenantInvoice, error) {
	var rec database.TenantPerception
	if err := s.db.Where("sale_id = ?", saleID).First(&rec).Error; err != nil {
		return nil, fmt.Errorf("percepción no encontrada para venta %d: %w", saleID, err)
	}
	if strings.TrimSpace(rec.PayloadJSON) == "" {
		return nil, errors.New("payload de percepción vacío")
	}
	payload := enrichFiscalPayloadJSON(rec.PayloadJSON, "40", "perception")
	return s.enqueueFiscalMicroservice(saleID, companyCfg, nil, payload)
}

// buildNotePayload construye NC (07) o ND (08) desde tenant_sales + ítems.
func (s *BillingService) buildNotePayload(noteSaleID uint) (*facturador.NotePayload, error) {
	var noteSale database.TenantSale
	if err := s.db.First(&noteSale, noteSaleID).Error; err != nil {
		return nil, errors.New("nota no encontrada")
	}
	tipoDoc := strings.TrimSpace(getSeriesSunatCode(s.db, noteSale.SeriesID))
	if tipoDoc == "" {
		switch strings.ToUpper(noteSale.DocType) {
		case "NOTA_DEBITO":
			tipoDoc = "08"
		default:
			tipoDoc = "07"
		}
	}
	if tipoDoc != "07" && tipoDoc != "08" {
		tipoDoc = "07"
	}

	var orig database.TenantSale
	hasOrig := false
	if noteSale.OriginalSaleID != nil {
		if err := s.db.First(&orig, *noteSale.OriginalSaleID).Error; err == nil {
			hasOrig = true
		}
	}
	companyCfg, companyAddr, err := s.getCompanyConfigAndAddress()
	if err != nil {
		return nil, err
	}
	var contact database.TenantContact
	if noteSale.ContactID != nil {
		s.db.First(&contact, *noteSale.ContactID)
	} else if hasOrig && orig.ContactID != nil {
		s.db.First(&contact, *orig.ContactID)
	}
	clientTipoDoc, clientNumDoc, clientRazon, clientAddr, err := s.resolveInvoiceClient(&contact)
	if err != nil {
		return nil, err
	}

	var tipDocAfectado, numDocAfectado string
	if hasOrig {
		tipDocAfectado = affectedDocumentSunatType(&orig, getSeriesSunatCode(s.db, orig.SeriesID))
		numDocAfectado = formatAffectedDocumentNumber(&orig)
	}
	if tipoDoc == "07" && (!hasOrig || tipDocAfectado == "" || numDocAfectado == "") {
		return nil, errors.New("la nota de crédito debe referenciar la factura o boleta que anula (tipDocAfectado y numDocfectado)")
	}
	if tipoDoc == "07" && hasOrig {
		wantPrefix := docseries.CreditNoteSeriesPrefixForAffected(orig.DocType, getSeriesSunatCode(s.db, orig.SeriesID))
		if !docseries.SeriesMatchesCreditNotePrefix(noteSale.Series, wantPrefix) {
			return nil, fmt.Errorf(
				"la serie %s no anula %ss: use %s## (ej. %s01) según SUNAT",
				noteSale.Series,
				docseries.AffectedDocLabel(orig.DocType, getSeriesSunatCode(s.db, orig.SeriesID)),
				wantPrefix,
				wantPrefix,
			)
		}
	}

	var items []database.TenantSaleItem
	s.db.Where("sale_id = ?", noteSaleID).Find(&items)
	if len(items) == 0 && hasOrig {
		s.db.Where("sale_id = ?", orig.ID).Find(&items)
	}
	if len(items) == 0 {
		return nil, errors.New("la nota no tiene ítems")
	}
	companyTaxRate, err := s.resolveCompanyTaxRate()
	if err != nil {
		return nil, err
	}
	details, err := BuildInvoiceDetailsFromSaleItems(items, companyTaxRate, normUnit)
	if err != nil {
		return nil, err
	}
	sunatTotals := ComputeInvoiceSunatTotals(items, noteSale.Total)
	nombreComercial := companyCfg.TradeName
	if nombreComercial == "" {
		nombreComercial = companyCfg.BusinessName
	}
	tipoMoneda := noteSale.Currency
	if tipoMoneda == "" {
		tipoMoneda = "PEN"
	}
	codMotivo := "01"
	if tipoDoc == "08" {
		codMotivo = "02"
	}
	desMotivo := strings.TrimSpace(noteSale.Notes)
	if desMotivo == "" {
		if tipoDoc == "08" {
			desMotivo = "Aumento en el valor"
		} else {
			desMotivo = "Anulación de la operación"
		}
	}
	var legends []facturador.InvoiceLegend
	facturador.SetSUNATLegend1000(&legends, sunatTotals.MtoImpVenta, tipoMoneda)
	return &facturador.NotePayload{
		UBLVersion:        "2.1",
		TipoDoc:           tipoDoc,
		Serie:             noteSale.Series,
		Correlativo:       fmt.Sprintf("%d", noteSale.Correlative),
		FechaEmision: facturador.FormatFiscalDateTime(noteSale.IssueDate),
		// Sin formaPago: SUNAT 3246 rechaza PaymentTerms/PaymentMeansID "Contado" en NC/ND (07/08).
		Company:      facturador.InvoiceCompany{RUC: companyCfg.RUC, RazonSocial: companyCfg.BusinessName, NombreComercial: nombreComercial, Address: companyAddr},
		Client:            facturador.InvoiceClient{TipoDoc: clientTipoDoc, NumDoc: clientNumDoc, RznSocial: clientRazon, Address: clientAddr},
		TipoMoneda:        tipoMoneda,
		CodMotivo:         codMotivo,
		DesMotivo:         desMotivo,
		TipDocAfectado:  tipDocAfectado,
		NumDocfectado:   numDocAfectado,
		// Sin relDocs duplicando el comprobante afectado: va a AdditionalDocumentReference (cat. 12)
		// y SUNAT observa 4009 si se repite 01/03 del cat. 01 ya presente en BillingReference.
		MtoOperGravadas:   sunatTotals.MtoOperGravadas,
		MtoOperExoneradas: sunatTotals.MtoOperExoneradas,
		MtoOperInafectas:  sunatTotals.MtoOperInafectas,
		MtoOperGratuitas:  sunatTotals.MtoOperGratuitas,
		MtoIGVGratuitas:   sunatTotals.MtoIGVGratuitas,
		MtoIGV:            sunatTotals.MtoIGV,
		TotalImpuestos:    sunatTotals.TotalImpuestos,
		ValorVenta:        sunatTotals.ValorVenta,
		SubTotal:          sunatTotals.MtoImpVenta,
		MtoImpVenta:       sunatTotals.MtoImpVenta,
		Details:           details,
		Legends:           legends,
	}, nil
}

// buildCreditNotePayload compatibilidad interna.
func (s *BillingService) buildCreditNotePayload(ncSaleID uint) (*facturador.NotePayload, error) {
	return s.buildNotePayload(ncSaleID)
}

// affectedDocumentSunatType tipo SUNAT del comprobante que corrige la nota: 01 factura, 03 boleta.
func affectedDocumentSunatType(orig *database.TenantSale, seriesSunatCode string) string {
	sc := strings.TrimSpace(seriesSunatCode)
	switch sc {
	case "01":
		return "01"
	case "03":
		return "03"
	}
	dt := strings.ToUpper(strings.TrimSpace(orig.DocType))
	if dt == "FACTURA" || strings.Contains(dt, "FACTURA") {
		return "01"
	}
	return "03"
}

// formatAffectedDocumentNumber serie-correlativo para Greenter (B001-4, no B001-00000004).
func formatAffectedDocumentNumber(orig *database.TenantSale) string {
	nro := strings.TrimSpace(orig.Number)
	if nro == "" {
		return fmt.Sprintf("%s-%d", strings.TrimSpace(orig.Series), orig.Correlative)
	}
	if i := strings.LastIndex(nro, "-"); i > 0 {
		suf := strings.TrimLeft(nro[i+1:], "0")
		if suf == "" {
			suf = "0"
		}
		return nro[:i+1] + suf
	}
	return nro
}

func (s *BillingService) resolveInvoiceClient(contact *database.TenantContact) (tipoDoc, numDoc, rzn string, addr facturador.InvoiceAddress, err error) {
	tipoDoc = "6"
	numDoc = "00000000000"
	rzn = "CLIENTE VARIOS"
	clientDir := ""
	clientUbigeo := ""
	if contact != nil && contact.ID > 0 {
		rzn = contact.BusinessName
		numDoc = contact.DocNumber
		clientDir = strings.TrimSpace(contact.Address)
		clientUbigeo = strings.TrimSpace(contact.Ubigeo)
		clientDir, clientUbigeo = database.NormalizeTenantContactAddressUbigeo(clientDir, clientUbigeo)
		switch strings.ToUpper(contact.DocType) {
		case "DNI", "1":
			tipoDoc = "1"
		case "RUC", "6":
			tipoDoc = "6"
		case "CE", "4", "CARNET":
			tipoDoc = "4"
		case "PASAPORTE", "7":
			tipoDoc = "7"
		default:
			if len(contact.DocNumber) == 8 {
				tipoDoc = "1"
			} else if len(contact.DocNumber) == 11 {
				tipoDoc = "6"
			}
		}
	}
	if numDoc == "" {
		numDoc = "00000000000"
	}
	if numDoc == "00000000000" || numDoc == "00000000" || numDoc == "99999999" ||
		(contact != nil && contact.ID > 0 && (strings.ToUpper(strings.TrimSpace(contact.DocType)) == "SIN DOCUMENTO" || strings.TrimSpace(contact.DocType) == "0")) {
		tipoDoc = "0"
		numDoc = "99999999999"
	}
	if contact == nil || contact.ID == 0 {
		return "", "", "", addr, errors.New("cliente con dirección y ubigeo requerido")
	}
	depC, provC, distC, errC := s.resolveUbigeoToAddress(clientUbigeo)
	if errC != nil {
		return "", "", "", addr, fmt.Errorf("cliente: %w", errC)
	}
	addr = facturador.InvoiceAddress{
		Ubigueo: clientUbigeo, CodigoPais: "PE",
		Departamento: depC, Provincia: provC, Distrito: distC,
		Direccion: clientDir,
	}
	return tipoDoc, numDoc, rzn, addr, nil
}
