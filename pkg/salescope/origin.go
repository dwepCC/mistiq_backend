// Package salescope centraliza el filtrado comercial de tenant_sales.
//
// REGLA DE ORO
//
// Todo reporte comercial que realice SUM(), COUNT(), GROUP BY o analytics sobre tenant_sales
// debe utilizar SalesScope (CommercialSales, ScopeCommercial, CommercialWhere).
// Está prohibido consultar tenant_sales directamente para obtener métricas comerciales.
package salescope

// Origen comercial del documento de venta (tenant_sales.sale_origin).
const (
	SaleOriginDirect            = "direct"
	SaleOriginConvertedFromNota = "converted_from_nota"
	SaleOriginAPI               = "api"
	SaleOriginMigration         = "migration"
	SaleOriginLegacy            = "legacy"
)

// IsCommercialOrigin indica si un valor sale_origin participa en reportes comerciales agregados.
func IsCommercialOrigin(origin string) bool {
	switch NormalizeOrigin(origin) {
	case SaleOriginConvertedFromNota:
		return false
	default:
		return true
	}
}
