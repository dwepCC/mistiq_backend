package database

import "time"

// Tipos de documento con foto congelada (TenantDocumentSnapshot.DocKind).
const (
	SnapshotKindSale      = "sale"      // ventas: nota de venta, boleta, factura, notas de crédito/débito, guías
	SnapshotKindQuotation = "quotation" // cotizaciones
	SnapshotKindPurchase  = "purchase"  // compras
)

// TenantDocumentSnapshot es la «foto» de los datos que se imprimen en el PDF de un documento y que NO viven en el propio
// registro: el cliente/proveedor, los datos de la empresa y de la sucursal emisora (razón social, RUC, dirección, teléfono…),
// el vendedor, las cuentas/billeteras de cobro y el logo. Se congela la primera vez que se arma el documento (al emitirlo) y,
// desde entonces, editar el cliente, el proveedor, la empresa o la sucursal no cambia lo que muestra el PDF de documentos
// ya emitidos. Los ítems, totales, pagos y datos fiscales ya están guardados en las filas del propio documento.
//
// Vive en una tabla aparte (y no como columna de tenant_sales/purchases) para no engordar las consultas de listados y reportes
// de esas tablas grandes. Una fila por (doc_kind, doc_id).
type TenantDocumentSnapshot struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	DocKind   string    `gorm:"size:20;not null;uniqueIndex:ux_doc_snapshot,priority:1" json:"doc_kind"`
	DocID     uint      `gorm:"not null;uniqueIndex:ux_doc_snapshot,priority:2" json:"doc_id"`
	Version   int       `gorm:"not null;default:1" json:"version"`
	Data      string    `gorm:"type:longtext;not null" json:"-"`
	CreatedAt time.Time `json:"created_at"`
}

// TenantSnapshotLogo guarda cada logo impreso UNA sola vez (por hash de su contenido) para que las fotos de los documentos lo
// referencien sin duplicar los bytes en cada venta. Un logo nuevo es otra fila: el de documentos viejos no cambia.
type TenantSnapshotLogo struct {
	Hash      string    `gorm:"primaryKey;size:64" json:"hash"`
	Data      string    `gorm:"type:longtext;not null" json:"-"`
	CreatedAt time.Time `json:"created_at"`
}
