package salecontext

import "strings"

// IsEmptyFiscalInput indica si el bloque fiscal no tiene datos que persistir.
// Ventas POS u otros clientes que omiten fiscal_context no pasan por aquí (nil).
func IsEmptyFiscalInput(in *FiscalContextInput) bool {
	if in == nil {
		return true
	}
	if in.IgvRetentionManualOverride {
		return false
	}
	if in.ShowTermsConditions {
		return false
	}
	if strings.TrimSpace(in.FiscalObservations) != "" {
		return false
	}
	if strings.TrimSpace(in.PurchaseOrderNumber) != "" {
		return false
	}
	if in.SellerUserID != nil && *in.SellerUserID > 0 {
		return false
	}
	if len(in.References) > 0 {
		return false
	}
	if in.HasIgvRetention != nil && *in.HasIgvRetention {
		return false
	}
	// nil => el backend puede auto-sugerir retención; no considerar vacío.
	if in.HasIgvRetention == nil {
		return false
	}
	return true
}
