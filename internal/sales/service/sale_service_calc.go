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
			Code:                   item.Code,
			Description:            item.Description,
			Unit:                   sunat.NormalizeUnit(item.Unit, itemType),
			Quantity:               item.Quantity,
			UnitPrice:              item.UnitPrice,
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
		})
	}
	return result.Subtotal, result.TaxAmount, result.Total, saleItems, result.GlobalDiscountAmount,
		strings.TrimSpace(input.GlobalDiscountMode), input.GlobalDiscountValue
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
		saleItems = append(saleItems, database.TenantSaleItem{
			ProductID:          item.ProductID,
			PresentationID:     item.PresentationID,
			Code:               item.Code,
			Description:        item.Description,
			Unit:               sunat.NormalizeUnit(item.Unit, resolveSaleItemType(db, item)),
			Quantity:           item.Quantity,
			UnitPrice:          item.UnitPrice,
			Discount:           item.Discount,
			TaxRate:            effectiveRate,
			IgvAffectationType: affType,
			Subtotal:           itemSub,
			TaxAmount:          itemTax,
			Total:              chargeableTotal,
			ModifiersJSON:      item.ModifiersJSON,
			ItemNote:           item.ItemNote,
		})
	}
	return subtotal, taxAmount, total, saleItems
}
