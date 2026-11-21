package service

import (
	"fmt"
	"strings"

	"tukifac/pkg/database"
	"tukifac/pkg/money"
	"tukifac/pkg/saleunit"

	"gorm.io/gorm"
)

// validatePurchaseItemQuantities exige quantity > 0 en toda línea de compra, sin excepción por
// ManageStock ni por tratarse de una línea manual (sin product_id) — mismo criterio que
// validateSaleItemQuantities en internal/sales/service (Fase 0). Antes de esto, la única barrera
// era la de RecordMovementTx, que solo corre para productos con ManageStock=true.
func validatePurchaseItemQuantities(items []PurchaseItemInput) error {
	for _, it := range items {
		if it.Quantity > 0 {
			continue
		}
		label := strings.TrimSpace(it.Description)
		if label == "" {
			label = strings.TrimSpace(it.Code)
		}
		if label == "" {
			label = "un ítem de la compra"
		}
		return fmt.Errorf("'%s' tiene una cantidad inválida (%.3f); debe ser mayor a cero", label, it.Quantity)
	}
	return nil
}

// validatePurchaseSaleUnits corre para todos los ítems de la compra, antes de la transacción —
// mismo lugar y mismo criterio que validateSaleUnits en internal/sales/service (Fase 2). Verifica
// pertenencia de la SaleUnit al producto de la línea, que esté activa, el factor y AllowFraction,
// todo mediante el resolver compartido (pkg/saleunit) para no repetir esta lógica en dos sitios.
func validatePurchaseSaleUnits(db *gorm.DB, items []PurchaseItemInput) error {
	for _, item := range items {
		if item.SaleUnitID == nil || *item.SaleUnitID == 0 {
			continue
		}
		if item.ProductID == nil || *item.ProductID == 0 {
			label := strings.TrimSpace(item.Description)
			if label == "" {
				label = "un ítem de la compra"
			}
			return fmt.Errorf("'%s' no puede usar una unidad de venta sin un producto de catálogo", label)
		}
		var product database.TenantProduct
		if err := db.First(&product, *item.ProductID).Error; err != nil {
			return fmt.Errorf("producto no encontrado para validar la unidad de venta de '%s'", strings.TrimSpace(item.Description))
		}
		if product.ManageSeries {
			return fmt.Errorf("'%s' controla números de serie: todavía no es compatible con unidades de venta", strings.TrimSpace(item.Description))
		}
		if _, err := saleunit.ResolveForLine(db, product.ID, item.SaleUnitID, item.Quantity); err != nil {
			return err
		}
	}
	return nil
}

// purchaseBaseUnitCost convierte el costo comercial (S/350 por saco) a costo base (S/3.50/KG):
// commercial_unit_cost / conversion_factor. Se redondea con money.RoundSunat (6 decimales, el
// mismo criterio de "precisión interna" que ya usa el proyecto — ver
// pkg/database/tenantmigrations/v047_sale_amounts_sunat_precision.go), no con RoundDisplay (2
// decimales, que perdería precisión en factores como 24 o 1000).
func purchaseBaseUnitCost(commercialUnitCost, conversionFactor float64) float64 {
	if conversionFactor <= 0 {
		return commercialUnitCost
	}
	return money.RoundSunat(commercialUnitCost / conversionFactor)
}
