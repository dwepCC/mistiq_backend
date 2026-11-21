package service

import (
	"fmt"
	"strings"

	"tukifac/pkg/database"

	"gorm.io/gorm"
)

// orderStockKey identifica la línea de stock que consume un ítem del pedido: el producto y, si
// vende por presentación, esa presentación (cada una tiene su propio saldo por sucursal).
type orderStockKey struct {
	productID      uint
	presentationID uint
}

// checkOrderItemStock valida, al AGREGAR el pedido, que haya stock para los productos con control
// de stock. Antes solo se validaba al cobrar la mesa: se podía pedir 5 unidades teniendo 4, la
// comanda salía a cocina y recién al cobrar fallaba con un genérico "stock insuficiente", con la
// comida ya preparada.
//
// Cuenta, además de lo pedido ahora, lo ya pedido en esta misma mesa y aún sin cobrar ni anular
// (comandas vivas), y lo comparado es el stock de la presentación (o del producto si no tiene).
// No reserva stock entre mesas distintas: el descuento definitivo sigue siendo al cobrar.
//
// requested acumula lo pedido en este mismo AddOrder para que dos líneas del mismo producto o
// presentación se sumen.
func checkOrderItemStock(
	tx *gorm.DB,
	branchID, sessionID uint,
	product *database.TenantProduct,
	item *NewOrderItem,
	requested map[orderStockKey]float64,
) error {
	if product == nil || item == nil || item.ProductID == nil || *item.ProductID == 0 {
		return nil
	}
	if !product.ManageStock || product.HasCombo || strings.EqualFold(strings.TrimSpace(product.Type), "service") {
		return nil
	}

	key := orderStockKey{productID: product.ID}
	if item.PresentationID != nil {
		key.presentationID = *item.PresentationID
	}
	requested[key] += item.Quantity

	// Ya pedido en esta mesa (comandas vivas) del mismo producto/presentación.
	var already float64
	q := tx.Model(&database.TenantComanda{}).
		Where("session_id = ? AND product_id = ? AND cancelled_at IS NULL AND billed_at IS NULL", sessionID, product.ID)
	if key.presentationID > 0 {
		q = q.Where("presentation_id = ?", key.presentationID)
	} else {
		q = q.Where("presentation_id IS NULL OR presentation_id = 0")
	}
	q.Select("COALESCE(SUM(quantity), 0)").Scan(&already)

	var available float64
	label := "«" + product.Name + "»"
	if key.presentationID > 0 {
		var ps database.TenantProductPresentationStock
		tx.Where("presentation_id = ? AND branch_id = ?", key.presentationID, branchID).First(&ps)
		available = ps.Quantity
		var pres database.TenantProductPresentation
		if tx.Select("id", "name").First(&pres, key.presentationID).Error == nil && pres.Name != "" {
			label += " (" + pres.Name + ")"
		}
	} else {
		var st database.TenantProductStock
		tx.Where("product_id = ? AND branch_id = ?", product.ID, branchID).First(&st)
		available = st.Quantity
	}

	need := requested[key] + already
	if need > available+0.0005 {
		extra := ""
		if already > 0 {
			extra = fmt.Sprintf(" (ya pedidas en la mesa: %.0f)", already)
		}
		return fmt.Errorf("stock insuficiente para %s: pides %.0f%s, disponible %.0f",
			label, requested[key], extra, available)
	}
	return nil
}
