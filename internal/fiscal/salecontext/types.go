package salecontext

// ContactSnapshot datos mínimos del cliente para reglas fiscales.
type ContactSnapshot struct {
	DocType                 string
	DocNumber               string
	EsAgenteDeRetencion     bool
	EsAgenteDePercepcion    bool
}

// FiscalContextInput entrada desde API al crear venta.
type FiscalContextInput struct {
	HasIgvRetention            *bool                   `json:"has_igv_retention"`
	IgvRetentionManualOverride bool                    `json:"igv_retention_manual_override"`
	ShowTermsConditions        bool                    `json:"show_terms_conditions"`
	FiscalObservations         string                  `json:"fiscal_observations"`
	PurchaseOrderNumber        string                  `json:"purchase_order_number"`
	SellerUserID               *uint                   `json:"seller_user_id"`
	References                 []FiscalReferenceInput  `json:"references"`
}

// FiscalReferenceInput referencia a otro documento (guía, etc.).
type FiscalReferenceInput struct {
	ReferenceKind        string `json:"reference_kind"`
	ReferencedSunatType  string `json:"referenced_sunat_type"`
	ReferencedSeries     string `json:"referenced_series"`
	ReferencedNumber     string `json:"referenced_number"`
	ReferencedFullNumber string `json:"referenced_full_number"`
	SortOrder            int    `json:"sort_order"`
}

// RetentionEvalInput parámetros para evaluar retención IGV.
type RetentionEvalInput struct {
	RequestedRetention     bool
	ManualOverride         bool
	SunatDocCode           string
	Contact                *ContactSnapshot
	SaleTotal              float64
	Currency               string
	ExchangeRate           *float64
}

// RetentionEvalResult resultado del motor de reglas de retención.
type RetentionEvalResult struct {
	HasIgvRetention         bool
	IgvRetentionManualOverride bool
	AutoSuggested           bool
	Applicable              bool
	Reason                  string
	RatePercent             float64
	BaseAmount              float64
	ObligationAmount        float64
	NetCollectible          float64
	Source                  string
}

// FiscalContextOutput respuesta enriquecida para API.
type FiscalContextOutput struct {
	Profile     FiscalProfileOutput      `json:"profile"`
	References  []FiscalReferenceOutput  `json:"references,omitempty"`
	Obligations []FiscalObligationOutput `json:"obligations,omitempty"`
	Summary     FiscalSummaryOutput      `json:"summary"`
}

type FiscalProfileOutput struct {
	SaleID                     uint   `json:"sale_id"`
	SchemaVersion              int    `json:"schema_version"`
	OperationTypeCode          string `json:"operation_type_code"`
	HasIgvRetention            bool   `json:"has_igv_retention"`
	IgvRetentionManualOverride bool   `json:"igv_retention_manual_override"`
	ShowTermsConditions        bool   `json:"show_terms_conditions"`
	FiscalObservations         string `json:"fiscal_observations"`
	PurchaseOrderNumber        string `json:"purchase_order_number"`
	SellerUserID               *uint  `json:"seller_user_id,omitempty"`
}

type FiscalReferenceOutput struct {
	ID                   uint   `json:"id"`
	ReferenceKind        string `json:"reference_kind"`
	ReferencedSunatType  string `json:"referenced_sunat_type"`
	ReferencedSeries     string `json:"referenced_series"`
	ReferencedNumber     string `json:"referenced_number"`
	ReferencedFullNumber string `json:"referenced_full_number"`
	SortOrder            int    `json:"sort_order"`
}

type FiscalObligationOutput struct {
	ID                  uint    `json:"id"`
	ObligationKind      string  `json:"obligation_kind"`
	Direction           string  `json:"direction"`
	RatePercent         float64 `json:"rate_percent"`
	BaseAmount          float64 `json:"base_amount"`
	ObligationAmount    float64 `json:"obligation_amount"`
	Currency            string  `json:"currency"`
	ApplicabilityStatus string  `json:"applicability_status"`
	ApplicabilityReason string  `json:"applicability_reason"`
	Source              string  `json:"source"`
	Status              string  `json:"status"`
}

type FiscalSummaryOutput struct {
	SaleTotal        float64 `json:"sale_total"`
	RetentionAmount  float64 `json:"retention_amount"`
	NetCollectible   float64 `json:"net_collectible"`
	RetentionApplied bool    `json:"retention_applied"`
}

// PersistInput datos para guardar contexto fiscal post-venta.
type PersistInput struct {
	SaleID        uint
	UserID        uint
	SunatDocCode  string
	SaleTotal     float64
	Currency      string
	ExchangeRate  *float64
	Contact       *ContactSnapshot
	FiscalContext *FiscalContextInput
}
