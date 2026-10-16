package stockreserve

import (
	"fmt"
	"strings"

	"tukifac/pkg/database"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Line detalle de disponibilidad de una línea del pedido, para mostrarlo en el panel.
type Line struct {
	ProductID      uint    `json:"product_id"`
	PresentationID uint    `json:"presentation_id"`
	Name           string  `json:"name"`
	Quantity       float64 `json:"quantity"`
	// Managed false = producto sin control de stock (o servicio): no reserva ni se valida.
	Managed bool `json:"managed"`
	// OnHand stock en almacén en la sucursal.
	OnHand float64 `json:"on_hand"`
	// ReservedOthers reservado por OTROS pedidos (la reserva del propio pedido no se cuenta).
	ReservedOthers float64 `json:"reserved_others"`
	// Available = OnHand − ReservedOthers: lo que este pedido puede tomar.
	Available float64 `json:"available"`
	Enough    bool    `json:"enough"`
}

// OrderLines disponibilidad línea por línea del pedido en la sucursal dada. lock=true bloquea las
// filas de stock (SELECT … FOR UPDATE) para serializar confirmaciones concurrentes del mismo producto.
func OrderLines(db *gorm.DB, orderID, branchID uint, lock bool) ([]Line, error) {
	var items []database.TenantEcommerceOrderItem
	if err := db.Where("order_id = ?", orderID).Order("id ASC").Find(&items).Error; err != nil {
		return nil, err
	}
	// Una misma línea de stock puede aparecer repetida en el pedido: se agrupa.
	type agg struct {
		line  Line
		order int
	}
	groups := map[Key]*agg{}
	var order []Key
	for _, it := range items {
		k := Key{ProductID: it.ProductID}
		if it.PresentationID != nil {
			k.PresentationID = *it.PresentationID
		}
		g, ok := groups[k]
		if !ok {
			g = &agg{line: Line{ProductID: k.ProductID, PresentationID: k.PresentationID, Name: it.Name}}
			groups[k] = g
			order = append(order, k)
		}
		g.line.Quantity += it.Quantity
	}

	productIDs := make([]uint, 0, len(order))
	for _, k := range order {
		productIDs = append(productIDs, k.ProductID)
	}
	reserved, err := Reserved(db, branchID, orderID, productIDs)
	if err != nil {
		return nil, err
	}

	out := make([]Line, 0, len(order))
	for _, k := range order {
		l := groups[k].line
		var product database.TenantProduct
		if err := db.First(&product, k.ProductID).Error; err != nil {
			l.Managed = false
			l.Enough = true
			out = append(out, l)
			continue
		}
		if !product.ManageStock || strings.EqualFold(strings.TrimSpace(product.Type), "service") {
			l.Managed = false
			l.Enough = true
			out = append(out, l)
			continue
		}
		l.Managed = true
		q := db
		if lock {
			q = db.Clauses(clause.Locking{Strength: "UPDATE"})
		}
		if k.PresentationID > 0 {
			var ps database.TenantProductPresentationStock
			q.Where("presentation_id = ? AND branch_id = ?", k.PresentationID, branchID).First(&ps)
			l.OnHand = ps.Quantity
		} else {
			var st database.TenantProductStock
			q.Where("product_id = ? AND branch_id = ?", k.ProductID, branchID).First(&st)
			l.OnHand = st.Quantity
		}
		l.ReservedOthers = reserved[k]
		l.Available = l.OnHand - l.ReservedOthers
		l.Enough = l.Available >= l.Quantity
		out = append(out, l)
	}
	return out, nil
}

// CheckOrder devuelve un error legible si alguna línea del pedido no alcanza con el stock
// disponible en la sucursal (almacén menos lo reservado por otros pedidos).
func CheckOrder(db *gorm.DB, orderID, branchID uint) error {
	lines, err := OrderLines(db, orderID, branchID, true)
	if err != nil {
		return err
	}
	var short []string
	for _, l := range lines {
		if l.Managed && !l.Enough {
			short = append(short, fmt.Sprintf("%s: pides %.2f y hay %.2f disponibles (%.2f en almacén, %.2f reservados en otros pedidos)",
				l.Name, l.Quantity, l.Available, l.OnHand, l.ReservedOthers))
		}
	}
	if len(short) > 0 {
		return fmt.Errorf("stock insuficiente para confirmar el pedido — %s", strings.Join(short, "; "))
	}
	return nil
}
