package service

import (
	"strings"

	"tukifac/internal/catalog/combos"
	"tukifac/pkg/database"

	"gorm.io/gorm"
)

// resolveComboItems resuelve los combos/promociones del carrito antes de emitir la venta.
//
// Un combo es un grupo de productos que se vende a precio fijo (p. ej. polo + pantalón por
// S/ 20 en vez de S/ 40). Se factura como UNA línea con el producto combo, pero el stock
// sale de sus componentes, así que hay que:
//   - validar que la elección del cliente cumple las reglas de cada grupo,
//   - fijar el precio (precio del combo + sobreprecios de las opciones elegidas),
//   - dejar los componentes como detalle de la línea (modifiers_json),
//   - acumular las salidas de almacén de cada componente.
//
// Sin esto, un combo se cobraría al precio base y no descontaría nada del inventario.
func resolveComboItems(
	db *gorm.DB,
	items []SaleItemInput,
) ([]SaleItemInput, []ExtraStockMovement, error) {
	// Salida rápida: la inmensa mayoría de ventas no llevan combos.
	hasCandidate := false
	for _, it := range items {
		if it.ProductID != nil && *it.ProductID > 0 {
			hasCandidate = true
			break
		}
	}
	if !hasCandidate {
		return items, nil, nil
	}

	out := make([]SaleItemInput, len(items))
	copy(out, items)

	stockByProduct := map[uint]float64{}
	stockOrder := make([]uint, 0)

	for i := range out {
		item := &out[i]
		if item.ProductID == nil || *item.ProductID == 0 {
			continue
		}
		var product database.TenantProduct
		if db.First(&product, *item.ProductID).Error != nil {
			continue
		}
		if !product.HasCombo {
			continue
		}

		res, err := combos.Resolve(db, product, item.ComboJSON, item.UnitPrice)
		if err != nil {
			return nil, nil, err
		}
		if res == nil {
			continue
		}

		item.UnitPrice = res.UnitPrice
		if strings.TrimSpace(item.Description) == "" {
			item.Description = res.Name
		}
		if strings.TrimSpace(item.Code) == "" {
			item.Code = res.Code
		}
		if strings.TrimSpace(item.IgvAffectationType) == "" {
			item.IgvAffectationType = res.IgvAffectationType
		}
		item.PriceIncludesIgv = res.PriceIncludesIgv
		// Los componentes se listan bajo la línea del combo, con la misma forma que los
		// modificadores: el visor de comprobantes ya sabe pintarla.
		item.ModifiersJSON = combos.ComponentsSnapshot(res.ComponentsPayload())

		for productID, qty := range res.StockMovements(item.Quantity) {
			if _, seen := stockByProduct[productID]; !seen {
				stockOrder = append(stockOrder, productID)
			}
			stockByProduct[productID] += qty
		}
	}

	extra := make([]ExtraStockMovement, 0, len(stockOrder))
	for _, productID := range stockOrder {
		extra = append(extra, ExtraStockMovement{ProductID: productID, Quantity: stockByProduct[productID]})
	}
	return out, extra, nil
}
