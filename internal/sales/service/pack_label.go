package service

import (
	"strconv"
	"strings"

	"tukifac/pkg/database"

	"gorm.io/gorm"
)

// FormatPackLabel arma el contenido de una unidad de venta: «12 UND» (factor + símbolo de la unidad base). El factor se muestra sin
// ceros sobrantes (12, 2.5). Sin símbolo no hay etiqueta útil → vacío.
func FormatPackLabel(factor float64, baseSymbol string) string {
	baseSymbol = strings.ToUpper(strings.TrimSpace(baseSymbol))
	if factor <= 1 || baseSymbol == "" {
		return ""
	}
	return strconv.FormatFloat(factor, 'f', -1, 64) + " " + baseSymbol
}

// ResolvePackLabel calcula la etiqueta de contenido («12 UND») de una línea vendida por unidad de venta, o "" si no aplica: sin
// producto ni unidad de venta, unidad base, o factor ≤ 1 (las fraccionarias como gramo/mililitro tienen factor < 1 y no llevan
// paréntesis). El símbolo sale de la unidad base del producto (NIU → UND, KGM → KG). Se llama UNA vez, al crear la línea: el valor
// queda congelado en el documento aunque la unidad de venta se edite después.
func ResolvePackLabel(db *gorm.DB, productID, saleUnitID *uint) string {
	if db == nil || productID == nil || *productID == 0 || saleUnitID == nil || *saleUnitID == 0 {
		return ""
	}
	var su database.TenantProductSaleUnit
	if err := db.Where("id = ? AND product_id = ?", *saleUnitID, *productID).First(&su).Error; err != nil {
		return ""
	}
	if su.IsBase || su.ConversionFactor <= 1 {
		return ""
	}
	var prod database.TenantProduct
	if err := db.Select("unit").First(&prod, *productID).Error; err != nil {
		return ""
	}
	code := strings.ToUpper(strings.TrimSpace(prod.Unit))
	if code == "" {
		return ""
	}
	symbol := code
	var unit database.TenantUnit
	if err := db.Select("symbol").Where("code = ?", code).First(&unit).Error; err == nil && strings.TrimSpace(unit.Symbol) != "" {
		symbol = unit.Symbol
	}
	return FormatPackLabel(su.ConversionFactor, symbol)
}

// resolveItemPackLabel respeta la etiqueta que ya trae la línea (comprobante emitido desde una nota de venta) y, si no, la calcula.
func resolveItemPackLabel(db *gorm.DB, item SaleItemInput) string {
	if l := strings.TrimSpace(item.PackLabel); l != "" {
		return l
	}
	return ResolvePackLabel(db, item.ProductID, item.SaleUnitID)
}
