package prepayment

// SaleInput datos enviados al crear una venta con anticipos (emisión Fase 0 o deducción Fase 1).
type SaleInput struct {
	Emit             bool             `json:"emit"`
	AffectationGroup string           `json:"affectation_group"` // gravado | exonerado | inafecto
	Deductions       []DeductionInput `json:"deductions,omitempty"`
}

// DeductionInput anticipo abierto a deducir en la venta final.
type DeductionInput struct {
	SourceSaleID uint    `json:"source_sale_id"`
	Amount       float64 `json:"amount"` // base imponible a deducir (legacy PHP amount)
}

// OpenVoucherOption anticipo disponible para deducción (GET /prepayment/vouchers).
type OpenVoucherOption struct {
	SourceSaleID     uint    `json:"source_sale_id"`
	Description      string  `json:"description"`
	DocumentNumber   string  `json:"document_number"`
	RelatedDocType   string  `json:"related_doc_type"`
	SunatDocCode     string  `json:"sunat_doc_code"`
	AffectationGroup string  `json:"affectation_group"`
	ContactID        *uint   `json:"contact_id,omitempty"`
	ContactName      string  `json:"contact_name,omitempty"`
	Amount           float64 `json:"amount"`
	Total            float64 `json:"total"`
	BalanceAmount    float64 `json:"balance_amount"`
	Currency         string  `json:"currency"`
}
