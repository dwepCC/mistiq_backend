package service

import (
	"strings"

	"tukifac/pkg/database"
	"tukifac/pkg/money"
	"tukifac/pkg/sunat"
	"tukifac/pkg/tax"

	"gorm.io/gorm"
)

func usesStructuredDiscounts(input CreateSaleInput) bool {
	if strings.TrimSpace(input.GlobalDiscountMode) != "" || input.GlobalDiscountValue > 0 {
		return true
	}
	for _, it := range input.Items {
		if strings.TrimSpace(it.LineDiscountMode) != "" || it.LineDiscountValue > 0 {
			return true
		}
	}
	return false
}

func buildSaleLinesFromEngine(input CreateSaleInput, taxCfg tax.Config, db *gorm.DB) (
	subtotal, taxAmount, total float64,
	saleItems []database.TenantSaleItem,
	globalAmount float64,
	globalMode string,
	globalValue float64,
) {
	lines := make([]tax.SaleLineInput, len(input.Items))
	for i, item := range input.Items {
		lines[i] = tax.SaleLineInput{
			UnitPrice:          item.UnitPrice,
			Quantity:           item.Quantity,
			IgvAffectationType: item.IgvAffectationType,
			PriceIncludesIgv:   item.PriceIncludesIgv,
			LineDiscountMode:   item.LineDiscountMode,
			LineDiscountValue:  item.LineDiscountValue,
		}
	}
	result := tax.CalcSaleCheckout(tax.SaleCheckoutInput{
		Lines:               lines,
		GlobalDiscountMode:  input.GlobalDiscountMode,
		GlobalDiscountValue: input.GlobalDiscountValue,
		TaxCfg:              taxCfg,
	})
	saleItems = make([]database.TenantSaleItem, 0, len(input.Items))
	for i, item := range input.Items {
		lr := result.Lines[i]
		affType := item.IgvAffectationType
		if affType == "" {
			affType = "10"
		}
		itemType := resolveSaleItemType(db, item)
		saleItems = append(saleItems, database.TenantSaleItem{
			ProductID:              item.ProductID,
			PresentationID:         item.PresentationID,
			SaleUnitID:             item.SaleUnitID,
			Code:                   item.Code,
			Description:            item.Description,
			Unit:                   resolveSaleItemUnitCode(db, item, itemType),
			Quantity:               item.Quantity,
			UnitPrice:              item.UnitPrice,
			PurchasePrice:          resolveSaleItemPurchasePrice(db, item),
			Discount:               lr.StoredDiscount,
			LineDiscountSubtotal:   lr.LineDiscountSubtotal,
			GlobalDiscountSubtotal: lr.GlobalDiscountSubtotal,
			TaxRate:                lr.TaxRate,
			IgvAffectationType:     affType,
			Subtotal:               lr.Subtotal,
			TaxAmount:              lr.TaxAmount,
			Total:                  lr.Total,
			ModifiersJSON:          item.ModifiersJSON,
			ItemNote:               item.ItemNote,
			PackLabel:              resolveItemPackLabel(db, item),
		})
	}
	return result.Subtotal, result.TaxAmount, result.Total, saleItems, result.GlobalDiscountAmount,
		strings.TrimSpace(input.GlobalDiscountMode), input.GlobalDiscountValue
}

// resolveSaleItemPurchasePrice snapshotea el costo ACTUAL del producto (TenantProduct.PurchasePrice)
// al momento de vender esta línea — ver v145_sale_item_purchase_price_snapshot.go. nil para líneas
// manuales (sin producto de catálogo) o si el producto ya no existe; el reporte de Utilidades cae
// al costo actual del catálogo como resguardo en ese caso.
func resolveSaleItemPurchasePrice(db *gorm.DB, item SaleItemInput) *float64 {
	if item.ProductID == nil || *item.ProductID == 0 {
		return nil
	}
	var product database.TenantProduct
	if err := db.Select("purchase_price").First(&product, *item.ProductID).Error; err != nil {
		return nil
	}
	price := product.PurchasePrice
	// El costo del catálogo es por unidad BASE (ej. S/ 3 el kg); la línea se vende en su unidad de
	// venta (ej. gramos, a precio por gramo), así que el costo de la línea debe ir en esa misma
	// unidad (3 × 0.001 = S/ 0.003 el gramo). Sin esto la utilidad de una venta fraccionada
	// restaba el costo del kilo por cada gramo vendido.
	if item.SaleUnitID != nil && *item.SaleUnitID != 0 {
		var su database.TenantProductSaleUnit
		if err := db.Select("conversion_factor").
			Where("id = ? AND product_id = ?", *item.SaleUnitID, *item.ProductID).
			First(&su).Error; err == nil && su.ConversionFactor > 0 {
			price = money.RoundSunat(price * su.ConversionFactor)
		}
	}
	return &price
}

func resolveSaleItemType(db *gorm.DB, item SaleItemInput) string {
	itemType := "product"
	if item.ProductID != nil && *item.ProductID > 0 {
		var prod database.TenantProduct
		if db.Select("type").First(&prod, *item.ProductID).Error == nil && productIsCatalogService(&prod) {
			itemType = "service"
		}
	} else if strings.EqualFold(strings.TrimSpace(item.Unit), "ZZ") {
		itemType = "service"
	}
	return itemType
}

// resolveSaleItemUnitCode resuelve el código SUNAT (Catálogo N°03) de la unidad COMERCIAL de una
// línea con producto de catálogo — nunca confía en item.Unit (lo que mande el cliente) en ese
// caso, exactamente igual que ya rige el precio (validateAuthorizedPrices) y la cantidad base
// (saleunit.ResolveForLine): el cliente no debe poder provocar sale_unit_id=<Caja>, unit=NIU y
// conseguir que el backend persista NIU.
//
//   - sin SaleUnitID → código de la unidad base del producto (Product.Unit).
//   - con SaleUnitID → código propio de esa SaleUnit (TenantProductSaleUnit.Unit) si está
//     configurado; si la SaleUnit fue creada antes de que existiera este campo (Unit=""), cae a la
//     unidad base del producto como resguardo de compatibilidad — nunca se infiere un código a
//     partir del nombre de la SaleUnit.
//
// Líneas manuales (sin producto de catálogo, ProductID nil) no pasan por acá: ahí el cliente sigue
// siendo la única fuente posible, porque no hay catálogo contra el cual resolver nada.
func resolveSaleItemUnitCode(db *gorm.DB, item SaleItemInput, itemType string) string {
	if item.ProductID == nil || *item.ProductID == 0 {
		return sunat.NormalizeUnit(item.Unit, itemType)
	}
	var product database.TenantProduct
	if err := db.Select("unit").First(&product, *item.ProductID).Error; err != nil {
		// No debería ocurrir (el producto ya se validó antes en el flujo) — mismo resguardo
		// defensivo que el comportamiento previo a esta corrección.
		return sunat.NormalizeUnit(item.Unit, itemType)
	}
	baseUnit := sunat.NormalizeUnit(product.Unit, itemType)
	if item.SaleUnitID == nil || *item.SaleUnitID == 0 {
		return baseUnit
	}
	var su database.TenantProductSaleUnit
	if err := db.Where("id = ? AND product_id = ?", *item.SaleUnitID, *item.ProductID).First(&su).Error; err != nil {
		// La SaleUnit no existe/no pertenece al producto: validateSaleUnits ya rechazó la venta
		// con un error claro antes de llegar acá — este resguardo nunca debería activarse.
		return baseUnit
	}
	if code := strings.TrimSpace(su.Unit); code != "" {
		return code
	}
	return baseUnit
}

func buildSaleLinesLegacy(input CreateSaleInput, taxCfg tax.Config, db *gorm.DB) (
	subtotal, taxAmount, total float64,
	saleItems []database.TenantSaleItem,
) {
	saleItems = make([]database.TenantSaleItem, 0, len(input.Items))
	for _, item := range input.Items {
		affType := item.IgvAffectationType
		if affType == "" {
			affType = "10"
		}
		effectiveRate := taxCfg.EffectiveRate(affType)
		itemSub, itemTax, itemTotal := tax.CalcItem(
			item.UnitPrice, item.Quantity, item.Discount,
			affType, item.PriceIncludesIgv, taxCfg,
		)
		chargeableTotal := itemTotal
		if tax.IsBonificacionGravada(affType) {
			chargeableTotal = 0
		} else {
			subtotal = money.RoundSunat(subtotal + itemSub)
			taxAmount = money.RoundSunat(taxAmount + itemTax)
			total = money.RoundSunat(total + chargeableTotal)
		}
		itemType := resolveSaleItemType(db, item)
		saleItems = append(saleItems, database.TenantSaleItem{
			ProductID:          item.ProductID,
			PresentationID:     item.PresentationID,
			SaleUnitID:         item.SaleUnitID,
			Code:               item.Code,
			Description:        item.Description,
			Unit:               resolveSaleItemUnitCode(db, item, itemType),
			Quantity:           item.Quantity,
			UnitPrice:          item.UnitPrice,
			PurchasePrice:      resolveSaleItemPurchasePrice(db, item),
			Discount:           item.Discount,
			TaxRate:            effectiveRate,
			IgvAffectationType: affType,
			Subtotal:           itemSub,
			TaxAmount:          itemTax,
			Total:              chargeableTotal,
			ModifiersJSON:      item.ModifiersJSON,
			ItemNote:           item.ItemNote,
			PackLabel:          resolveItemPackLabel(db, item),
		})
	}
	return subtotal, taxAmount, total, saleItems
}
