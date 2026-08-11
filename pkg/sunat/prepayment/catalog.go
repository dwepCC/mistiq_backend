package prepayment

// Grupos de afectación IGV para anticipos (equivalente legacy 10/20/30).
const (
	AffectationGravado   = "gravado"
	AffectationExonerado = "exonerado"
	AffectationInafecto  = "inafecto"
)

// Catálogo 12 — documentos relacionados emitidos por anticipos.
const (
	RelatedDocFacturaAnticipo = "02"
	RelatedDocBoletaAnticipo  = "03"
)

// VoucherStatus ciclo de vida emisión (Fase 0; sin aplicaciones).
const (
	StatusPendingAcceptance = "pending_acceptance"
	StatusOpen              = "open"
	StatusVoided            = "voided"
)

// PDF flag enviado a Lycet vía parameters.user.extras (no altera XML SUNAT).
const (
	PDFExtraPrepaymentEmit      = "prepayment_emit"
	PDFExtraPrepaymentEmitValue = "1"
)

// RelatedDocTypeForSunatCode devuelve el código cat. 12 según tipo de comprobante (01/03).
func RelatedDocTypeForSunatCode(sunatDocCode string) string {
	if sunatDocCode == "01" {
		return RelatedDocFacturaAnticipo
	}
	return RelatedDocBoletaAnticipo
}

// IsValidAffectationGroup valida el grupo informado al emitir.
func IsValidAffectationGroup(group string) bool {
	switch group {
	case AffectationGravado, AffectationExonerado, AffectationInafecto:
		return true
	default:
		return false
	}
}

// AffectationGroupLabel etiqueta legible del grupo IGV.
func AffectationGroupLabel(group string) string {
	switch group {
	case AffectationGravado:
		return "Gravado"
	case AffectationExonerado:
		return "Exonerado"
	case AffectationInafecto:
		return "Inafecto"
	default:
		return group
	}
}
