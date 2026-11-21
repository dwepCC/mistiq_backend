package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	salecontext "tukifac/internal/fiscal/salecontext"
	"tukifac/pkg/database"
	"tukifac/pkg/facturador"

	"gorm.io/gorm"
)

func isGuiaSaleDocType(docType string) bool {
	dt := strings.ToUpper(strings.TrimSpace(docType))
	return dt == "GUIA_REMISION" || dt == "GUIA_TRANSPORTISTA"
}

// isSalesNoteDocType nota de venta (comprobante interno, sin código SUNAT).
func isSalesNoteDocType(docType string) bool {
	dt := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(docType), "_", " "))
	return dt == "NOTA DE VENTA" || dt == "NOTA VENTA"
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
	// La dirección y el ubigeo del destinatario no los exige SUNAT (el XML lleva el punto de
	// llegada aparte): son datos de ayuda que el formulario autocompleta y deja editar.
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
	if sunatCode == "09" && modTraslado == greModTrasladoPrivado && !env.IndM1L {
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
	if srcCode != "01" && srcCode != "03" && srcCode != "00" && !isSalesNoteDocType(src.DocType) {
		return fmt.Errorf("solo se puede generar guía desde factura, boleta o nota de venta")
	}
	if input.BranchID == 0 {
		input.BranchID = src.BranchID
	}
	// Antes, Address/Ubigeo solo se rellenaban desde el contacto cuando NumDoc también venía
	// vacío (el `if` exterior estaba condicionado únicamente a NumDoc): el frontend arma el
	// destinatario "desde factura/boleta" con NumDoc ya resuelto (buildDespatchPrefillFromSaleDetail
	// en DespatchFormModal.tsx), así que ese bloque nunca corría y el ubigeo quedaba vacío aunque
	// el contacto lo tuviera bien guardado — el error "ubigeo del destinatario es obligatorio"
	// salía sin que el usuario pudiera entender por qué, si su cliente sí tenía ubigeo configurado.
	// Ahora cada campo se rellena por separado, solo si vino vacío, sin depender de NumDoc.
	if src.ContactID != nil {
		var contact database.TenantContact
		if err := s.db.First(&contact, *src.ContactID).Error; err == nil {
			if strings.TrimSpace(input.Destinatario.NumDoc) == "" {
				tipoDoc, numDoc, rzn, _, err := s.resolveInvoiceClient(&contact)
				if err == nil {
					input.Destinatario.TipoDoc = tipoDoc
					input.Destinatario.NumDoc = numDoc
					input.Destinatario.RznSocial = rzn
				}
			}
			if strings.TrimSpace(input.Destinatario.Address) == "" {
				input.Destinatario.Address = strings.TrimSpace(contact.Address)
			}
			if strings.TrimSpace(input.Destinatario.Ubigeo) == "" {
				input.Destinatario.Ubigeo = strings.TrimSpace(contact.Ubigeo)
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
	// Solo factura y boleta son documentos relacionados válidos (catálogo 61); la nota de venta no.
	if tipo != "01" && tipo != "03" {
		return nil
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

// resolveOrCreateDespatchContact busca (o crea) el TenantContact del destinatario de la guía por
// número de documento, para que la venta-guía quede con ContactID poblado igual que cualquier
// otra venta — sin esto, sale.ContactID quedaba siempre NULL (se creó o no desde una venta
// origen, el destinatario real solo se guardaba en tenant_despatches, nunca en tenant_sales), y
// print_data.go (PDF del panel) solo mira sale.ContactID: al no encontrarlo, caía siempre al
// "Cliente genérico" (doc 99999999) sin importar quién fuera el destinatario real de la guía.
func (s *BillingService) resolveOrCreateDespatchContact(dest DespatchDestinatarioInput, contactType, emisorRUC string) (*uint, error) {
	numDoc := strings.TrimSpace(dest.NumDoc)
	if numDoc == "" || (emisorRUC != "" && numDoc == strings.TrimSpace(emisorRUC)) {
		// La propia empresa (traslado entre locales, compra) no es un contacto del directorio.
		return nil, nil
	}
	if contactType == "" {
		contactType = "customer"
	}
	tipoDoc := strings.TrimSpace(dest.TipoDoc)
	if tipoDoc == "" {
		tipoDoc = "6"
	}
	var contact database.TenantContact
	err := s.db.Where("doc_number = ? AND active = ?", numDoc, true).First(&contact).Error
	if err == nil {
		// Guarda lo que el usuario completó en la guía solo si el contacto no lo tenía.
		upd := map[string]interface{}{}
		if strings.TrimSpace(contact.Address) == "" && strings.TrimSpace(dest.Address) != "" {
			upd["address"] = strings.TrimSpace(dest.Address)
		}
		if strings.TrimSpace(contact.Ubigeo) == "" && strings.TrimSpace(dest.Ubigeo) != "" {
			upd["ubigeo"] = strings.TrimSpace(dest.Ubigeo)
		}
		if len(upd) > 0 {
			_ = s.db.Model(&contact).Updates(upd).Error
		}
		return &contact.ID, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	addr, ubi := database.NormalizeTenantContactAddressUbigeo(dest.Address, dest.Ubigeo)
	razon := strings.TrimSpace(dest.RznSocial)
	if razon == "" {
		razon = numDoc
	}
	contact = database.TenantContact{
		Type:         contactType,
		DocType:      tipoDoc,
		DocNumber:    numDoc,
		BusinessName: razon,
		Address:      addr,
		Ubigeo:       ubi,
		Active:       true,
	}
	if err := s.db.Create(&contact).Error; err != nil {
		return nil, err
	}
	return &contact.ID, nil
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
