package saas

import "tukifac/pkg/saas/pricing"

// Alias del cálculo de importes, que vive en su propio paquete para que también lo use
// docusage (no puede importar saas: crearía un ciclo).
type (
	Discount     = pricing.Discount
	CycleAmounts = pricing.CycleAmounts
)

const (
	DiscountNone    = pricing.DiscountNone
	DiscountPercent = pricing.DiscountPercent
	DiscountFixed   = pricing.DiscountFixed
)

var (
	ComputeCycleAmounts = pricing.ComputeCycleAmounts
	NormalizeDiscount   = pricing.NormalizeDiscount
	ErrInvalidDiscount  = pricing.ErrInvalidDiscount
)
