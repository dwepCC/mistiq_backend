package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	salecontext "tukifac/internal/fiscal/salecontext"
	"tukifac/pkg/database"
	"tukifac/pkg/facturador"
)

func isGuiaSaleDocType(docType string) bool {
	dt := strings.ToUpper(strings.TrimSpace(docType))
	return dt == "GUIA_REMISION" || dt == "GUIA_TRANSPORTISTA"
}

func isRetentionSaleDocType(docType string) bool {
	return strings.ToUpper(strings.TrimSpace(docType)) == "RETENCION"
}

func isPerceptionSaleDocType(docType string) bool {
	return strings.ToUpper(strings.TrimSpace(docType)) == "PERCEPCION"
}

func (s *BillingService) getRetentionPayloadForSale(saleID uint) (*facturador.RetentionPayload, error) {
	var rec database.TenantRetention
	if err := s.db.Where("sale_id = ?", saleID).First(&rec).Error; err != nil {
		return nil, fmt.Errorf("retención no encontrada: %w", err)
	}
	if strings.TrimSpace(rec.PayloadJSON) == "" {
		return nil, errors.New("payload de retención vacío")
	}
	var payload facturador.RetentionPayload
	if err := json.Unmarshal([]byte(rec.PayloadJSON), &payload); err != nil {
		return nil, fmt.Errorf("payload retención inválido: %w", err)
	}
	return &payload, nil
}

func (s *BillingService) getPerceptionPayloadForSale(saleID uint) (*facturador.PerceptionPayload, error) {
	var rec database.TenantPerception
	if err := s.db.Where("sale_id = ?", saleID).First(&rec).Error; err != nil {
		return nil, fmt.Errorf("percepción no encontrada: %w", err)
	}
	if strings.TrimSpace(rec.PayloadJSON) == "" {
		return nil, errors.New("payload de percepción vacío")
	}
	var payload facturador.PerceptionPayload
	if err := json.Unmarshal([]byte(rec.PayloadJSON), &payload); err != nil {
		return nil, fmt.Errorf("payload percepción inválido: %w", err)
	}
	return &payload, nil
}

func (s *BillingService) getDespatchPayloadForSale(saleID uint) (*facturador.DespatchPayload, error) {
	var despatch database.TenantDespatch
	if err := s.db.Where("sale_id = ?", saleID).First(&despatch).Error; err != nil {
		return nil, fmt.Errorf("guía no encontrada: %w", err)
	}
	if strings.TrimSpace(despatch.PayloadJSON) == "" {
		return nil, errors.New("payload de guía vacío")
	}
	var payload facturador.DespatchPayload
	if err := json.Unmarshal([]byte(despatch.PayloadJSON), &payload); err != nil {
		return nil, fmt.Errorf("payload guía inválido: %w", err)
	}
	return &payload, nil
}

// validateDespatchInput valida reglas de negocio antes de reservar correlativo.
func validateDespatchInput(input CreateDespatchInput, sunatCode string) error {
	if input.BranchID == 0 {
		return errors.New("seleccione la sucursal")
	}
	if input.SeriesID == 0 {
		return errors.New("seleccione la serie de guía")
	}
	dest := input.Destinatario
	if strings.TrimSpace(dest.TipoDoc) == "" {
		return errors.New("tipo de documento del destinatario es obligatorio")
	}
	if strings.TrimSpace(dest.NumDoc) == "" {
		return errors.New("número de documento del destinatario es obligatorio")
	}
	if strings.TrimSpace(dest.RznSocial) == "" {
		return errors.New("razón social del destinatario es obligatoria")
	}
	if strings.TrimSpace(dest.Address) == "" {
		return errors.New("dirección del destinatario es obligatoria")
	}
	if strings.TrimSpace(dest.Ubigeo) == "" {
		return errors.New("ubigeo del destinatario es obligatorio")
	}
	env := input.Envio
	if strings.TrimSpace(env.CodTraslado) == "" {
		return errors.New("motivo de traslado es obligatorio")
	}
	if strings.TrimSpace(env.ModTraslado) == "" {
		return errors.New("modalidad de traslado es obligatoria")
	}
	if strings.TrimSpace(env.FecTraslado) == "" {
		return errors.New("fecha de traslado es obligatoria")
	}
	if strings.TrimSpace(env.PartidaUbigueo) == "" {
		return errors.New("ubigeo de partida es obligatorio")
	}
	if strings.TrimSpace(env.LlegadaUbigueo) == "" {
		return errors.New("ubigeo de llegada es obligatorio")
	}
	if strings.TrimSpace(env.PartidaDireccion) == "" {
		return errors.New("dirección de partida es obligatoria")
	}
	if strings.TrimSpace(env.LlegadaDireccion) == "" {
		return errors.New("dirección de llegada es obligatoria")
	}
	if env.PesoTotal <= 0 {
		return errors.New("el peso total del traslado debe ser mayor a cero")
	}
	modTraslado := resolveDespatchModTraslado(sunatCode, env.ModTraslado)
	if sunatCode == "09" && modTraslado == greModTrasladoPublico {
		if strings.TrimSpace(env.TransportistaRUC) == "" {
			return errors.New("RUC del transportista es obligatorio en transporte público (guía remitente)")
		}
		if strings.TrimSpace(env.TransportistaRazon) == "" {
			return errors.New("razón social del transportista es obligatoria en transporte público")
		}
	}
	if sunatCode == "09" && modTraslado == greModTrasladoPrivado {
		if strings.TrimSpace(env.TransportistaPlaca) == "" {
			return errors.New("placa del vehículo es obligatoria en transporte privado")
		}
		if err := validateDespatchDriverFields(env, true); err != nil {
			return err
		}
	}
	if sunatCode == "31" {
		if strings.TrimSpace(env.TransportistaPlaca) == "" {
			return errors.New("placa del vehículo es obligatoria en guía transportista (31)")
		}
		if err := validateDespatchDriverFields(env, true); err != nil {
			return err
		}
	}
	hasItem := false
	for _, d := range input.Details {
		if strings.TrimSpace(d.Descripcion) != "" && d.Cantidad > 0 {
			hasItem = true
			break
		}
	}
	if !hasItem {
		return errors.New("agregue al menos un producto con cantidad mayor a cero")
	}
	return nil
}

func (s *BillingService) applyDespatchPrefillFromSale(input *CreateDespatchInput, sourceSaleID uint) error {
	var src database.TenantSale
	if err := s.db.First(&src, sourceSaleID).Error; err != nil {
		return fmt.Errorf("venta origen no encontrada: %w", err)
	}
	srcCode := strings.TrimSpace(getSeriesSunatCode(s.db, src.SeriesID))
	if srcCode != "01" && srcCode != "03" {
		return fmt.Errorf("solo se puede generar guía desde factura (01) o boleta (03)")
	}
	if input.BranchID == 0 {
		input.BranchID = src.BranchID
	}
	if strings.TrimSpace(input.Destinatario.NumDoc) == "" && src.ContactID != nil {
		var contact database.TenantContact
		if err := s.db.First(&contact, *src.ContactID).Error; err == nil {
			tipoDoc, numDoc, rzn, _, err := s.resolveInvoiceClient(&contact)
			if err == nil {
				input.Destinatario.TipoDoc = tipoDoc
				input.Destinatario.NumDoc = numDoc
				input.Destinatario.RznSocial = rzn
				if strings.TrimSpace(input.Destinatario.Address) == "" {
					input.Destinatario.Address = strings.TrimSpace(contact.Address)
				}
				if strings.TrimSpace(input.Destinatario.Ubigeo) == "" {
					input.Destinatario.Ubigeo = strings.TrimSpace(contact.Ubigeo)
				}
			}
		}
	}
	if len(input.Details) == 0 || (len(input.Details) == 1 && input.Details[0].Descripcion == "" && input.Details[0].Codigo == "") {
		var items []database.TenantSaleItem
		s.db.Where("sale_id = ?", sourceSaleID).Find(&items)
		if len(items) > 0 {
			input.Details = make([]DespatchDetailInput, len(items))
			for i, it := range items {
				unit := strings.TrimSpace(it.Unit)
				if unit == "" {
					unit = "NIU"
				}
				input.Details[i] = DespatchDetailInput{
					Codigo:      strings.TrimSpace(it.Code),
					Descripcion: strings.TrimSpace(it.Description),
					Unidad:      unit,
					Cantidad:    it.Quantity,
				}
			}
		}
	}
	if strings.TrimSpace(input.Envio.CodTraslado) == "" {
		input.Envio.CodTraslado = "01"
	}
	if strings.TrimSpace(input.Envio.DesTraslado) == "" {
		input.Envio.DesTraslado = "Venta"
	}
	if strings.TrimSpace(input.Envio.ModTraslado) == "" {
		input.Envio.ModTraslado = greModTrasladoPrivado
	}
	if strings.TrimSpace(input.Envio.LlegadaDireccion) == "" {
		input.Envio.LlegadaDireccion = strings.TrimSpace(input.Destinatario.Address)
	}
	if strings.TrimSpace(input.Envio.LlegadaUbigueo) == "" {
		input.Envio.LlegadaUbigueo = strings.TrimSpace(input.Destinatario.Ubigeo)
	}
	return nil
}

func (s *BillingService) despatchAddDocFromSale(sourceSaleID uint, emisorRUC string) *facturador.DespatchAdditionalDoc {
	var src database.TenantSale
	if err := s.db.First(&src, sourceSaleID).Error; err != nil {
		return nil
	}
	tipo := strings.TrimSpace(getSeriesSunatCode(s.db, src.SeriesID))
	if tipo == "" {
		switch strings.ToUpper(src.DocType) {
		case "FACTURA":
			tipo = "01"
		case "BOLETA":
			tipo = "03"
		}
	}
	nro := formatAffectedDocumentNumber(&src)
	if tipo == "" || nro == "" {
		return nil
	}
	tipoDesc := "Documento relacionado"
	switch tipo {
	case "01":
		tipoDesc = "Factura"
	case "03":
		tipoDesc = "Boleta de Venta"
	}
	return &facturador.DespatchAdditionalDoc{
		Tipo:     tipo,
		Nro:      nro,
		TipoDesc: tipoDesc,
		Emisor:   strings.TrimSpace(emisorRUC),
	}
}

func (s *BillingService) linkDespatchFiscalReference(sourceSaleID uint, sunatCode, fullNumber string, guiaSaleID uint) {
	if sourceSaleID == 0 || strings.TrimSpace(fullNumber) == "" {
		return
	}
	kind := salecontext.RefKindGuiaRemitente
	refTipo := "09"
	if sunatCode == "31" {
		kind = salecontext.RefKindGuiaTransportista
		refTipo = "31"
	}
	parts := strings.SplitN(fullNumber, "-", 2)
	series := parts[0]
	number := ""
	if len(parts) > 1 {
		number = parts[1]
	}
	rec := database.TenantSaleFiscalReference{
		SaleID:               sourceSaleID,
		ReferenceKind:        kind,
		ReferencedSunatType:  refTipo,
		ReferencedSeries:     series,
		ReferencedNumber:     number,
		ReferencedFullNumber: fullNumber,
		ReferencedSaleID:     &guiaSaleID,
	}
	_ = s.db.Create(&rec).Error
}
