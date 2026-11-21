package service

import (
	"encoding/json"
	"fmt"
	"strings"

	"tukifac/pkg/database"
	"tukifac/pkg/facturador"
)

// isNoteSaleDocType indica venta NC (07) o ND (08).
func isNoteSaleDocType(docType string) bool {
	switch strings.ToUpper(strings.TrimSpace(docType)) {
	case "NOTA_CREDITO", "NOTA_DEBITO":
		return true
	default:
		return false
	}
}

// shouldRegenerateNotePayload true cuando no hay payload o el envío previo falló (error) o SUNAT lo rechazó (rejected): el payload
// guardado es el que se rechazó, así que reenviarlo tal cual lo rechazaría igual aunque el armado ya esté corregido.
func shouldRegenerateNotePayload(inv *database.TenantInvoice) bool {
	if inv == nil {
		return true
	}
	if strings.TrimSpace(inv.NotePayloadJSON) == "" {
		return true
	}
	st := strings.TrimSpace(inv.SunatStatus)
	return strings.EqualFold(st, "error") || strings.EqualFold(st, "rejected")
}

func parseNotePayloadJSON(raw string) (*facturador.NotePayload, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("payload de nota vacío")
	}
	var payload facturador.NotePayload
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return nil, fmt.Errorf("payload nota inválido: %w", err)
	}
	return &payload, nil
}

func (s *BillingService) noteFacturadorClient() *facturador.Client {
	if s != nil && s.facturadorConfigured() {
		return facturador.NewClient(s.baseURL, s.token)
	}
	return facturador.Shared()
}

func (s *BillingService) getNoteDocumentPDF(invoice *database.TenantInvoice) ([]byte, error) {
	if invoice == nil || !s.facturadorConfigured() {
		return nil, nil
	}
	payload, err := parseNotePayloadJSON(invoice.NotePayloadJSON)
	if err != nil {
		return nil, err
	}
	return s.noteFacturadorClient().GetNotePDF(payload)
}

func (s *BillingService) getNoteDocumentXMLGenerated(invoice *database.TenantInvoice) ([]byte, error) {
	if invoice == nil || !s.facturadorConfigured() {
		return nil, nil
	}
	payload, err := parseNotePayloadJSON(invoice.NotePayloadJSON)
	if err != nil {
		return nil, err
	}
	return s.noteFacturadorClient().GetNoteXML(payload)
}
