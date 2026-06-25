package salecontext

import (
	"strings"

	"tukifac/pkg/facturador"

	"gorm.io/gorm"
)

// InvoiceEnrichment datos fiscales adicionales para Lycet y print_data.
type InvoiceEnrichment struct {
	TipoOperacion       string
	Observacion         string
	Compra              string
	Guias               []InvoiceGuiaRef
	HasIgvRetention     bool
	RetentionAmount     float64
	NetCollectible      float64
	RetentionApplied    bool
	ShowTermsConditions bool
	PurchaseOrderNumber string
	FiscalObservations  string
	SellerUserID        *uint
}

// InvoiceGuiaRef guía relacionada con la venta.
type InvoiceGuiaRef struct {
	Kind    string
	TipoDoc string
	NroDoc  string
}

// LoadInvoiceEnrichment carga enriquecimiento fiscal desde BD (nil si no hay perfil).
func LoadInvoiceEnrichment(db *gorm.DB, saleID uint, saleTotal float64) (*InvoiceEnrichment, error) {
	out, err := NewService(db).Load(saleID, saleTotal)
	if err != nil {
		return nil, err
	}
	return EnrichmentFromOutput(out), nil
}

// EnrichmentFromOutput convierte FiscalContextOutput a InvoiceEnrichment.
func EnrichmentFromOutput(out *FiscalContextOutput) *InvoiceEnrichment {
	if out == nil {
		return nil
	}
	e := &InvoiceEnrichment{
		TipoOperacion:       strings.TrimSpace(out.Profile.OperationTypeCode),
		FiscalObservations:  strings.TrimSpace(out.Profile.FiscalObservations),
		PurchaseOrderNumber: strings.TrimSpace(out.Profile.PurchaseOrderNumber),
		Compra:              strings.TrimSpace(out.Profile.PurchaseOrderNumber),
		Observacion:         strings.TrimSpace(out.Profile.FiscalObservations),
		ShowTermsConditions: out.Profile.ShowTermsConditions,
		HasIgvRetention:     out.Profile.HasIgvRetention,
		RetentionAmount:     out.Summary.RetentionAmount,
		NetCollectible:      out.Summary.NetCollectible,
		RetentionApplied:    out.Summary.RetentionApplied,
		SellerUserID:        out.Profile.SellerUserID,
	}
	if e.TipoOperacion == "" {
		e.TipoOperacion = DefaultOperationType
	}
	for _, ref := range out.References {
		kind := strings.TrimSpace(ref.ReferenceKind)
		nro := strings.TrimSpace(ref.ReferencedFullNumber)
		if nro == "" {
			continue
		}
		tipoDoc := strings.TrimSpace(ref.ReferencedSunatType)
		if tipoDoc == "" {
			switch kind {
			case RefKindGuiaRemitente:
				tipoDoc = "09"
			case RefKindGuiaTransportista:
				tipoDoc = "31"
			}
		}
		e.Guias = append(e.Guias, InvoiceGuiaRef{
			Kind:    kind,
			TipoDoc: tipoDoc,
			NroDoc:  nro,
		})
	}
	return e
}

// ApplyToInvoicePayload aplica campos adicionales al payload Lycet.
// Retención IGV: observacion → XML (cbc:Note); parameters.user.extras → PDF Greenter.
func ApplyToInvoicePayload(payload *facturador.InvoicePayload, e *InvoiceEnrichment) {
	if payload == nil || e == nil {
		return
	}
	if code := strings.TrimSpace(e.TipoOperacion); code != "" {
		payload.TipoOperacion = code
	}
	if obs := strings.TrimSpace(e.Observacion); obs != "" {
		payload.Observacion = obs
	}
	if compra := strings.TrimSpace(e.Compra); compra != "" {
		payload.Compra = compra
	}
	for _, g := range e.Guias {
		nro := strings.TrimSpace(g.NroDoc)
		if nro == "" {
			continue
		}
		tipoDoc := strings.TrimSpace(g.TipoDoc)
		if tipoDoc == "" {
			tipoDoc = "09"
		}
		payload.Guias = append(payload.Guias, facturador.InvoiceRelatedDoc{
			TipoDoc: tipoDoc,
			NroDoc:  nro,
		})
	}
	if e.RetentionApplied && e.RetentionAmount > 0 {
		currency := strings.TrimSpace(payload.TipoMoneda)
		if currency == "" {
			currency = "PEN"
		}
		applyIGVRetentionToInvoicePayload(payload, e.RetentionAmount, e.NetCollectible, currency)
	}
}

