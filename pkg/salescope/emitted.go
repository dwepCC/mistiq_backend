package salescope

import (
	"fmt"
	"strings"

	"gorm.io/gorm"
)

// Regla de FECHA DE EMISIÓN (reportes de ventas y dashboard).
//
// Para SUNAT lo único que existe es la fecha de emisión del comprobante. Una nota de venta (NV) puede haberse registrado en junio y
// su factura/boleta emitirse en octubre: esa factura pertenece a octubre, no a junio. Por eso, cuando una NV tiene un comprobante hijo
// vigente (factura/boleta emitida desde ella y no anulada), el que cuenta es el COMPROBANTE, con su propia fecha de emisión, su tipo y
// su número; la NV se omite (es un solo hecho comercial, no dos). Si el hijo se anula, la NV vuelve a contar.
//
// Es la regla inversa a CommercialSales (que contaba la NV con su fecha y omitía el hijo). CommercialSales/ScopeCommercial siguen
// vigentes para los reportes de CAJA: ahí manda cuándo y dónde entró el dinero, no la fecha del comprobante.
//
// Se usa un NOT IN con subconsulta NO correlacionada (los hijos se calculan una sola vez, por el índice de issued_from_nota_sale_id):
// una subconsulta correlacionada por fila agotó el pool de un tenant grande (incidente 2026-10-09).
const liveChildParentIDs = "SELECT issued_from_nota_sale_id FROM tenant_sales WHERE issued_from_nota_sale_id > 0 AND status <> 'cancelled' AND deleted_at IS NULL"

// EmittedWhere fragmento SQL (alias ej. "s") para consultas raw: excluye la NV que ya tiene un comprobante hijo vigente.
func EmittedWhere(alias string) string {
	if strings.TrimSpace(alias) == "" {
		alias = defaultTable
	}
	return fmt.Sprintf("%s.id NOT IN (%s)", alias, liveChildParentIDs)
}

// EmittedSales aplica la regla de fecha de emisión a una consulta sobre tenant_sales.
func EmittedSales(db *gorm.DB) *gorm.DB { return db.Where(EmittedWhere(defaultTable)) }

// ScopeEmitted igual que EmittedSales para consultas con alias (JOINs).
func ScopeEmitted(alias string) func(*gorm.DB) *gorm.DB {
	if strings.TrimSpace(alias) == "" {
		alias = defaultTable
	}
	return func(db *gorm.DB) *gorm.DB { return db.Where(EmittedWhere(alias)) }
}

// EmittedSalesNoNotes es EmittedSales + excluye notas de crédito/débito (métricas de venta: la nota es el reverso de un comprobante
// ya contado).
func EmittedSalesNoNotes(db *gorm.DB) *gorm.DB {
	return EmittedSales(db).Where(notDocTypeExpr(defaultTable))
}

// ScopeEmittedNoNotes es ScopeEmitted(alias) + excluye notas de crédito/débito.
func ScopeEmittedNoNotes(alias string) func(*gorm.DB) *gorm.DB {
	emitted := ScopeEmitted(alias)
	return func(db *gorm.DB) *gorm.DB { return emitted(db).Where(notDocTypeExpr(alias)) }
}

// EmittedWhereNoNotes es EmittedWhere(alias) + excluye notas, para consultas raw.
func EmittedWhereNoNotes(alias string) string {
	return EmittedWhere(alias) + " AND " + notDocTypeExpr(alias)
}
