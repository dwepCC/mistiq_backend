package service

import (
	invsvc "tukifac/internal/inventory/service"
	"tukifac/pkg/database"

	"gorm.io/gorm"

	"time"
)

// saleStockReference referencia con la que la venta escribió sus salidas de kardex.
func saleStockReference(number string) string {
	return "VENTA/" + number
}

// restoreStockFromKardexTx devuelve al inventario TODO lo que salió por esta venta.
//
// Se revierte leyendo el kardex, no recorriendo las líneas de la venta. Un combo se factura
// como una sola línea que no tiene stock propio: lo que se descuenta es el de sus componentes,
// que no aparecen como ítems. Al recorrer líneas, ese stock nunca volvía —el combo hacía
// `continue` por no manejar stock— y el inventario quedaba corto tras cada anulación. Lo mismo
// pasaba con los componentes que descuenta el flujo de comandas del restaurante.
//
// El kardex sí tiene una fila por cada salida real, venga de una línea, de un componente de
// combo o de una comanda, así que revertir desde ahí cubre los tres casos con una sola regla.
func restoreStockFromKardexTx(tx *gorm.DB, sale *database.TenantSale, ref string, userID uint) error {
	var outs []database.TenantStockMovement
	if err := tx.Where("reference = ? AND type = ?", saleStockReference(sale.Number), "out").
		Find(&outs).Error; err != nil {
		return err
	}
	if len(outs) == 0 {
		return nil
	}

	uid := userID
	if uid == 0 {
		uid = sale.UserID
	}
	inv := invsvc.NewInventoryService(tx)
	for i := range outs {
		mv := &outs[i]
		if mv.Quantity <= 0 {
			continue
		}
		if err := inv.RecordMovementTx(tx, invsvc.MovementInput{
			ProductID:      mv.ProductID,
			PresentationID: mv.PresentationID,
			BranchID:       mv.BranchID,
			Type:           "in",
			Quantity:       mv.Quantity,
			Reference:      ref,
			UserID:         uid,
			OperationCode:  "SALE",
		}); err != nil {
			return err
		}
	}
	return nil
}

// RestorePartialStockFromKardexTx repone al inventario SOLO lo que corresponde a las líneas
// de una nota de crédito parcial (Fase 2) — cada línea de la nota (con OriginalSaleItemID)
// referencia la línea de la venta original de la que nace. Se busca el kardex de ESA línea
// específica (tenant_stock_movements.sale_item_id) y se revierte proporcional a lo que la
// nota devuelve: si la nota devolvió menos cantidad que la vendida en esa línea, solo se
// repone esa fracción, no el 100% de lo que salió por ella.
//
// Exportada (mayúscula) porque el caller vive en el paquete de billing, no en este.
func RestorePartialStockFromKardexTx(tx *gorm.DB, noteSale *database.TenantSale, ref string, userID uint) error {
	var noteItems []database.TenantSaleItem
	if err := tx.Where("sale_id = ? AND original_sale_item_id IS NOT NULL", noteSale.ID).
		Find(&noteItems).Error; err != nil {
		return err
	}
	if len(noteItems) == 0 {
		return nil
	}
	uid := userID
	if uid == 0 {
		uid = noteSale.UserID
	}
	inv := invsvc.NewInventoryService(tx)
	for _, ni := range noteItems {
		if ni.OriginalSaleItemID == nil || ni.Quantity <= 0 {
			continue
		}
		var outs []database.TenantStockMovement
		if err := tx.Where("sale_item_id = ? AND type = ?", *ni.OriginalSaleItemID, "out").
			Find(&outs).Error; err != nil {
			return err
		}
		if len(outs) == 0 {
			continue
		}
		var totalOut float64
		for _, mv := range outs {
			totalOut += mv.Quantity
		}
		if totalOut <= 0 {
			continue
		}
		ratio := ni.Quantity / totalOut
		if ratio > 1 {
			ratio = 1
		}
		for i := range outs {
			mv := &outs[i]
			qty := mv.Quantity * ratio
			if qty <= 0 {
				continue
			}
			if err := inv.RecordMovementTx(tx, invsvc.MovementInput{
				ProductID:      mv.ProductID,
				PresentationID: mv.PresentationID,
				BranchID:       mv.BranchID,
				Type:           "in",
				Quantity:       qty,
				Reference:      ref,
				UserID:         uid,
				OperationCode:  "SALE",
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

// releaseSerialsForSaleTx devuelve a «disponible» los números de serie de la venta.
//
// Va por línea y no por kardex porque la serie se asocia al ítem vendido, no al movimiento.
func releaseSerialsForSaleTx(tx *gorm.DB, items []database.TenantSaleItem) error {
	for _, item := range items {
		if item.ProductID == nil {
			continue
		}
		var product database.TenantProduct
		if tx.First(&product, *item.ProductID).Error != nil {
			continue
		}
		if !product.ManageSeries || productIsCatalogService(&product) {
			continue
		}
		if err := tx.Model(&database.TenantProductSerial{}).
			Where("sale_item_id = ?", item.ID).
			Updates(map[string]interface{}{
				"status":       "available",
				"sale_item_id": nil,
				"updated_at":   time.Now(),
			}).Error; err != nil {
			return err
		}
	}
	return nil
}
