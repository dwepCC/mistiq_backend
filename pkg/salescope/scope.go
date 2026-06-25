package salescope

import (
	"fmt"
	"strings"

	"gorm.io/gorm"
)

const defaultTable = "tenant_sales"

// CommercialSales excluye boletas/facturas emitidas por conversión NV→FE (doble conteo comercial).
// Mantiene NV, boletas/facturas directas y documentos históricos.
func CommercialSales(db *gorm.DB) *gorm.DB {
	return applyCommercialScope(db, defaultTable)
}

// ConvertedSales solo ventas hijo de conversión NV→boleta/factura.
func ConvertedSales(db *gorm.DB) *gorm.DB {
	return applyConvertedScope(db, defaultTable)
}

// DirectSales ventas creadas directamente (no por conversión).
func DirectSales(db *gorm.DB) *gorm.DB {
	origin := normalizedOriginExpr(defaultTable, "sale_origin")
	return db.Where(fmt.Sprintf("%s = ? AND %s", origin, issuedFromNotaNullExpr(defaultTable)), SaleOriginDirect)
}

// FiscalSales comprobantes electrónicos factura/boleta (01/03) — métricas SUNAT/fiscales.
func FiscalSales(db *gorm.DB) *gorm.DB {
	return db.Joins("JOIN tenant_document_series ON tenant_document_series.id = tenant_sales.series_id").
		Where("tenant_document_series.sunat_code IN ?", []string{"01", "03"})
}

// ScopeCommercial retorna un GORM scope para JOINs (alias ej. "s", "tenant_sales").
func ScopeCommercial(alias string) func(*gorm.DB) *gorm.DB {
	if strings.TrimSpace(alias) == "" {
		alias = defaultTable
	}
	return func(db *gorm.DB) *gorm.DB {
		return applyCommercialScope(db, alias)
	}
}

// ScopeConverted retorna scope para ventas convertidas desde NV.
func ScopeConverted(alias string) func(*gorm.DB) *gorm.DB {
	if strings.TrimSpace(alias) == "" {
		alias = defaultTable
	}
	return func(db *gorm.DB) *gorm.DB {
		return applyConvertedScope(db, alias)
	}
}

func applyCommercialScope(db *gorm.DB, alias string) *gorm.DB {
	origin := normalizedOriginExpr(alias, "sale_origin")
	issued := issuedFromNotaNullExpr(alias)
	return db.Where(fmt.Sprintf("%s != ? AND %s", origin, issued), SaleOriginConvertedFromNota)
}

func applyConvertedScope(db *gorm.DB, alias string) *gorm.DB {
	origin := normalizedOriginExpr(alias, "sale_origin")
	issued := issuedFromNotaSetExpr(alias)
	return db.Where(fmt.Sprintf("%s = ? OR %s", origin, issued), SaleOriginConvertedFromNota)
}

func commercialOriginExpr(alias string) string {
	return normalizedOriginExpr(alias, "sale_origin")
}

func normalizedOriginExpr(alias, column string) string {
	prefix := ""
	if alias != "" {
		prefix = alias + "."
	}
	return fmt.Sprintf("COALESCE(NULLIF(TRIM(%s%s), ''), '%s')", prefix, column, SaleOriginDirect)
}

func issuedFromNotaNullExpr(alias string) string {
	prefix := ""
	if alias != "" {
		prefix = alias + "."
	}
	return fmt.Sprintf("(%sissued_from_nota_sale_id IS NULL OR %sissued_from_nota_sale_id = 0)", prefix, prefix)
}

// CommercialWhere fragmento SQL para consultas raw (alias ej. "s").
func CommercialWhere(alias string) string {
	if strings.TrimSpace(alias) == "" {
		alias = defaultTable
	}
	origin := normalizedOriginExpr(alias, "sale_origin")
	issued := issuedFromNotaNullExpr(alias)
	return fmt.Sprintf("(%s != '%s' AND %s)", origin, SaleOriginConvertedFromNota, issued)
}

func issuedFromNotaSetExpr(alias string) string {
	prefix := ""
	if alias != "" {
		prefix = alias + "."
	}
	return fmt.Sprintf("(%sissued_from_nota_sale_id IS NOT NULL AND %sissued_from_nota_sale_id > 0)", prefix, prefix)
}
