package service

import (
	"encoding/json"
	"fmt"
	"strings"

	"tukifac/pkg/database"
	"tukifac/pkg/saleunit"

	"gorm.io/gorm"
)

// resolveSaleUnitForLine delega en el resolver compartido (pkg/saleunit) — el mismo que usa
// internal/purchases/service para compras (Fase 3). Se mantiene como wrapper local en vez de
// llamar saleunit.ResolveForLine directo en cada sitio de este archivo/paquete para no tener que
// cambiar las llamadas existentes si el resolver compartido cambia de firma.
func resolveSaleUnitForLine(db *gorm.DB, productID uint, saleUnitID *uint, commercialQuantity float64) (*saleunit.Resolution, error) {
	return saleunit.ResolveForLine(db, productID, saleUnitID, commercialQuantity)
}

// validateSaleUnits corre para TODOS los ítems de la venta, antes de la transacción — igual que
// validateSaleItemPrices/validateSaleItemQuantities (Fase 0). No revalida ítems con
// PriceAuthorized=true: ese flag ya significa "código de confianza resolvió/vetó esta línea antes
// de llegar acá" (combo, reemisión de una nota de venta ya validada) — ninguno de esos casos usa
// SaleUnitID hoy, así que en la práctica esto solo salta líneas que de todos modos no lo traen.
// ValidateSaleUnits expone la validación de unidades de venta (pertenencia, activa, factor y
// fracción) a otros módulos que cotizan lo mismo que luego se vende (cotizaciones).
func ValidateSaleUnits(db *gorm.DB, items []SaleItemInput) error {
	return validateSaleUnits(db, items)
}

func validateSaleUnits(db *gorm.DB, items []SaleItemInput) error {
	for _, item := range items {
		if item.PriceAuthorized {
			continue
		}
		if item.SaleUnitID == nil || *item.SaleUnitID == 0 {
			continue
		}
		if item.ProductID == nil || *item.ProductID == 0 {
			return fmt.Errorf("'%s' no puede usar una unidad de venta sin un producto de catálogo", saleItemLabel(item))
		}
		if item.PresentationID != nil && *item.PresentationID > 0 {
			return fmt.Errorf("'%s' no puede combinar presentación y unidad de venta en la misma línea", saleItemLabel(item))
		}
		var product database.TenantProduct
		if err := db.First(&product, *item.ProductID).Error; err != nil {
			return fmt.Errorf("producto no encontrado para validar la unidad de venta de '%s'", saleItemLabel(item))
		}
		if product.HasCombo {
			return fmt.Errorf("'%s' es un combo: no admite unidad de venta", saleItemLabel(item))
		}
		if product.ManageSeries {
			return fmt.Errorf("'%s' controla números de serie: todavía no es compatible con unidades de venta", saleItemLabel(item))
		}
		hasExtra, err := modifiersJSONHasModifierEntry(item.ModifiersJSON)
		if err != nil {
			return err
		}
		if hasExtra {
			return fmt.Errorf("'%s' no puede combinar unidad de venta con extras/modificadores todavía", saleItemLabel(item))
		}
		if _, err := resolveSaleUnitForLine(db, product.ID, item.SaleUnitID, item.Quantity); err != nil {
			return err
		}
	}
	return nil
}

// modifiersJSONHasModifierEntry indica si modifiers_json trae al menos una entrada tipo "modifier"
// (extra que suma precio — ver modifierkind.Extra). Se usa solo para RECHAZAR explícitamente la
// combinación SaleUnit+extra con un mensaje claro, en vez de dejar que validateAuthorizedPrices la
// ignore en silencio (ver nota en sale_price_authorization.go: cuando hay SaleUnit, el precio
// autorizado es SaleUnit.Price1 y los extras no se suman todavía). No revisa entradas "variant":
// esas ya están cubiertas por el rechazo de PresentationID+SaleUnitID en validateSaleUnits.
func modifiersJSONHasModifierEntry(modifiersJSON string) (bool, error) {
	raw := strings.TrimSpace(modifiersJSON)
	if raw == "" {
		return false, nil
	}
	var entries []saleModifierEntry
	if err := json.Unmarshal([]byte(raw), &entries); err != nil {
		return false, fmt.Errorf("modifiers_json inválido")
	}
	for _, e := range entries {
		if e.Type == "modifier" {
			return true, nil
		}
	}
	return false, nil
}
