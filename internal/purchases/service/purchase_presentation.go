package service

import (
	"fmt"
	"strings"

	"tukifac/pkg/database"

	"gorm.io/gorm"
)

// purchasePresentationID devuelve la presentación de la línea solo si es válida (>0).
func purchasePresentationID(item PurchaseItemInput) *uint {
	if item.PresentationID == nil || *item.PresentationID == 0 {
		return nil
	}
	return item.PresentationID
}

// validatePurchasePresentations exige elegir presentación cuando el producto maneja stock por
// presentación, y rechaza una presentación ajena, inactiva o combinada con una unidad de venta.
// Se valida antes de escribir nada: el kardex también lo exige, pero ahí el error llegaba tarde.
func validatePurchasePresentations(db *gorm.DB, items []PurchaseItemInput) error {
	for _, item := range items {
		if item.ProductID == nil || *item.ProductID == 0 {
			continue
		}
		label := strings.TrimSpace(item.Description)
		if label == "" {
			label = "un ítem de la compra"
		}
		var product database.TenantProduct
		if err := db.Select("id", "name", "has_variants", "manage_stock").First(&product, *item.ProductID).Error; err != nil {
			continue // validatePurchaseProducts ya reporta el producto inexistente
		}
		chosen := purchasePresentationID(item)
		if chosen != nil && item.SaleUnitID != nil && *item.SaleUnitID > 0 {
			return fmt.Errorf("'%s' no puede combinar presentación y unidad de venta en la misma línea", label)
		}
		var active int64
		if product.HasVariants {
			db.Model(&database.TenantProductPresentation{}).
				Where("product_id = ? AND active = ?", product.ID, true).Count(&active)
		}
		if !product.HasVariants || active == 0 {
			if chosen != nil {
				return fmt.Errorf("«%s» no maneja presentaciones", product.Name)
			}
			continue
		}
		if chosen == nil && !product.ManageStock {
			continue // sin control de stock la presentación no cambia nada: es opcional
		}
		if chosen == nil {
			return fmt.Errorf("«%s» maneja stock por presentación: seleccione una presentación", product.Name)
		}
		var pres database.TenantProductPresentation
		if err := db.First(&pres, *chosen).Error; err != nil || pres.ProductID != product.ID {
			return fmt.Errorf("la presentación indicada no pertenece a «%s»", product.Name)
		}
		if !pres.Active {
			return fmt.Errorf("la presentación «%s» de «%s» está inactiva", pres.Name, product.Name)
		}
	}
	return nil
}
