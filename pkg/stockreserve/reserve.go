// Package stockreserve calcula el stock RESERVADO por pedidos web.
//
// No hay tabla de reservas: la reserva se deriva del propio pedido, así que no puede quedar
// desincronizada. Un pedido reserva su mercadería desde que se confirma (tiene sucursal) hasta que
// se convierte en venta (ahí la venta descuenta el stock de verdad) o se cancela/rechaza/devuelve
// (ahí la reserva simplemente deja de contar).
//
//	disponible = stock en almacén − reservado
package stockreserve

import (
	"encoding/json"

	"gorm.io/gorm"
)

// Estados de pedido que reservan stock. PENDIENTE no reserva (un pedido sin confirmar no debe
// bloquear mercadería); ENTREGADO sigue reservando mientras no se haya emitido la venta, porque el
// stock contable no ha salido todavía.
var reservingStatuses = []string{
	"CONFIRMADO", "EN_PREPARACION", "EMPAQUETADO", "LISTO_PARA_DESPACHO", "DESPACHADO", "ENTREGADO",
}

// Key identifica una línea de stock: producto simple (PresentationID 0) o una de sus variantes.
type Key struct {
	ProductID      uint
	PresentationID uint
}

type reservedRow struct {
	ProductID      uint
	PresentationID uint
	Qty            float64
}

// Reserved cantidades reservadas por producto/presentación; branchID 0 = todas las sucursales
// (suma de todas). excludeOrderID > 0
// deja fuera la reserva de ese pedido (para cuando el propio pedido se convierte en venta o se
// valida su disponibilidad). productIDs vacío = todos los productos con reserva.
func Reserved(db *gorm.DB, branchID, excludeOrderID uint, productIDs []uint) (map[Key]float64, error) {
	out := map[Key]float64{}
	base := func() *gorm.DB {
		q := db.Table("tenant_ecommerce_order_items AS i").
			Joins("JOIN tenant_ecommerce_orders AS o ON o.id = i.order_id").
			Where("o.status IN ? AND o.converted_sale_id IS NULL AND o.branch_id IS NOT NULL", reservingStatuses)
		if branchID > 0 {
			q = q.Where("o.branch_id = ?", branchID)
		}
		if excludeOrderID > 0 {
			q = q.Where("o.id <> ?", excludeOrderID)
		}
		return q
	}

	// Líneas normales (productos y variantes).
	q := base().
		Select("i.product_id AS product_id, COALESCE(i.presentation_id, 0) AS presentation_id, SUM(i.quantity) AS qty").
		Where("(i.combo_components_json IS NULL OR i.combo_components_json = '')")
	if len(productIDs) > 0 {
		q = q.Where("i.product_id IN ?", productIDs)
	}
	var rows []reservedRow
	if err := q.Group("i.product_id, i.presentation_id").Scan(&rows).Error; err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[Key{ProductID: r.ProductID, PresentationID: r.PresentationID}] = r.Qty
	}

	// Combos: no tienen stock propio, reservan lo que llevan dentro (sus componentes).
	var comboRows []struct {
		Quantity   float64
		Components string `gorm:"column:components"`
	}
	if err := base().
		Select("i.quantity AS quantity, i.combo_components_json AS components").
		Where("i.combo_components_json IS NOT NULL AND i.combo_components_json <> ''").
		Scan(&comboRows).Error; err != nil {
		return nil, err
	}
	want := map[uint]bool{}
	for _, id := range productIDs {
		want[id] = true
	}
	for _, cr := range comboRows {
		for _, c := range ParseComponents(cr.Components) {
			if len(want) > 0 && !want[c.ProductID] {
				continue
			}
			out[Key{ProductID: c.ProductID}] += cr.Quantity * c.Quantity
		}
	}
	return out, nil
}

// Component un componente de combo (el subconjunto del snapshot que importa para el stock).
type Component struct {
	ProductID   uint    `json:"product_id"`
	ProductName string  `json:"product_name"`
	Quantity    float64 `json:"quantity"`
}

// ParseComponents lee el snapshot de componentes de una línea de combo (vacío si no hay o está dañado).
func ParseComponents(raw string) []Component {
	if raw == "" {
		return nil
	}
	var out []Component
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	return out
}

// ReservedFor reservado de UNA línea (producto simple o variante) en la sucursal.
func ReservedFor(db *gorm.DB, branchID, excludeOrderID, productID uint, presentationID *uint) (float64, error) {
	m, err := Reserved(db, branchID, excludeOrderID, []uint{productID})
	if err != nil {
		return 0, err
	}
	var pid uint
	if presentationID != nil {
		pid = *presentationID
	}
	if pid > 0 {
		return m[Key{ProductID: productID, PresentationID: pid}], nil
	}
	return m[Key{ProductID: productID}], nil
}

// ReservedByProduct reservado total por producto (suma de sus variantes) — para listados que
// muestran el stock del producto completo.
func ReservedByProduct(db *gorm.DB, branchID uint, productIDs []uint) (map[uint]float64, error) {
	m, err := Reserved(db, branchID, 0, productIDs)
	if err != nil {
		return nil, err
	}
	out := make(map[uint]float64, len(m))
	for k, v := range m {
		out[k.ProductID] += v
	}
	return out, nil
}
