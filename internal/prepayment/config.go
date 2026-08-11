package prepayment

import sunatpre "tukifac/pkg/sunat/prepayment"

// ModuleConfig configuración pública del módulo para frontend y otros consumidores.
type ModuleConfig struct {
	EmitOperationType      string                   `json:"emit_operation_type"`
	EmitOperationLabel     string                   `json:"emit_operation_label"`
	EmitOperationFullLabel string                   `json:"emit_operation_full_label"`
	PDFLabel               string                   `json:"pdf_label"`
	AffectationGroups      []AffectationGroupOption `json:"affectation_groups"`
	AllowedDocTypes        []AllowedDocTypeOption   `json:"allowed_doc_types"`
}

// AffectationGroupOption grupo IGV para emisión de anticipo.
type AffectationGroupOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// AllowedDocTypeOption tipos de comprobante habilitados para anticipo.
type AllowedDocTypeOption struct {
	Code  string `json:"code"`
	Label string `json:"label"`
}

// GetModuleConfig expone la configuración del módulo (incluye tipoOperacion activo).
func GetModuleConfig() ModuleConfig {
	return ModuleConfig{
		EmitOperationType:      sunatpre.EmitOperationTypeCode(),
		EmitOperationLabel:     sunatpre.EmitOperationLabel(),
		EmitOperationFullLabel: sunatpre.EmitOperationFullLabel(),
		PDFLabel:               EmitPDFLabel(),
		AffectationGroups: []AffectationGroupOption{
			{Value: sunatpre.AffectationGravado, Label: sunatpre.AffectationGroupLabel(sunatpre.AffectationGravado)},
			{Value: sunatpre.AffectationExonerado, Label: sunatpre.AffectationGroupLabel(sunatpre.AffectationExonerado)},
			{Value: sunatpre.AffectationInafecto, Label: sunatpre.AffectationGroupLabel(sunatpre.AffectationInafecto)},
		},
		AllowedDocTypes: []AllowedDocTypeOption{
			{Code: "01", Label: "Factura"},
			{Code: "03", Label: "Boleta"},
		},
	}
}
