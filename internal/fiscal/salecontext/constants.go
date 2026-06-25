package salecontext

const (
	SchemaVersion         = 1
	DefaultOperationType  = "0101"
	ObligationIGVRetention = "igv_retention"
	DirectionWithheldFromUs  = "withheld_from_us"
	StatusApplicable         = "applicable"
	StatusNotApplicable      = "not_applicable"
	StatusPending            = "pending"
	SourceAutoRule           = "auto_rule"
	SourceManualOverride     = "manual_override"

	RefKindGuiaRemitente      = "guia_remitente"
	RefKindGuiaTransportista  = "guia_transportista"

	IGVRetentionRate      = 0.03
	IGVRetentionThreshold = 700.0
)
