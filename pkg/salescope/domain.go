package salescope

import (
	"strings"

	"tukifac/pkg/database"
)

// NormalizeOrigin devuelve el origen efectivo (vacío → direct).
func NormalizeOrigin(origin string) string {
	o := strings.TrimSpace(origin)
	if o == "" {
		return SaleOriginDirect
	}
	return o
}

func saleOriginOf(sale *database.TenantSale) string {
	if sale == nil {
		return SaleOriginDirect
	}
	return NormalizeOrigin(sale.SaleOrigin)
}

func hasIssuedFromNotaParent(sale *database.TenantSale) bool {
	return sale != nil && sale.IssuedFromNotaSaleID != nil && *sale.IssuedFromNotaSaleID > 0
}

// IsCommercial indica si la venta cuenta en métricas comerciales agregadas.
func IsCommercial(sale *database.TenantSale) bool {
	if sale == nil {
		return false
	}
	if saleOriginOf(sale) == SaleOriginConvertedFromNota {
		return false
	}
	return !hasIssuedFromNotaParent(sale)
}

// IsConverted indica venta hijo de conversión NV→boleta/factura.
func IsConverted(sale *database.TenantSale) bool {
	if sale == nil {
		return false
	}
	if saleOriginOf(sale) == SaleOriginConvertedFromNota {
		return true
	}
	return hasIssuedFromNotaParent(sale)
}

// IsDirect indica venta creada directamente (no por conversión).
func IsDirect(sale *database.TenantSale) bool {
	if sale == nil {
		return false
	}
	return saleOriginOf(sale) == SaleOriginDirect && !hasIssuedFromNotaParent(sale)
}

// IsFiscal indica comprobante electrónico factura/boleta (01/03).
func IsFiscal(sale *database.TenantSale) bool {
	if sale == nil {
		return false
	}
	switch strings.ToUpper(strings.TrimSpace(sale.DocType)) {
	case "FACTURA", "BOLETA", "01", "03":
		return true
	default:
		return false
	}
}

// IsLegacy indica ventas históricas sin origen explícito backfill.
func IsLegacy(sale *database.TenantSale) bool {
	if sale == nil {
		return false
	}
	return saleOriginOf(sale) == SaleOriginLegacy
}
