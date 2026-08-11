package service

import (
	"strings"

	"tukifac/pkg/database"

	"gorm.io/gorm"
)

// manualItemCode código para líneas escritas a mano, que no salen del catálogo.
//
// SUNAT solo pide que el emisor identifique el ítem con algún código propio; no exige que
// exista en un maestro. Para una línea libre («servicio de instalación», «varios») este valor
// cumple y evita que el comprobante se caiga al emitirse.
const manualItemCode = "VARIOS"

// fillMissingItemCodes completa el código de las líneas que llegan sin él.
//
// Prioridad: el código del producto del catálogo si la línea lo referencia, y si no el
// genérico de línea manual. Antes una línea sin código pasaba la venta sin problema y el
// error aparecía al armar el XML —«el ítem X no tiene código de producto»—, cuando el
// documento ya estaba guardado y el cliente esperando su comprobante.
func fillMissingItemCodes(db *gorm.DB, items []SaleItemInput) {
	for i := range items {
		item := &items[i]
		if strings.TrimSpace(item.Code) != "" {
			item.Code = strings.TrimSpace(item.Code)
			continue
		}
		if item.ProductID != nil && *item.ProductID > 0 && db != nil {
			var product database.TenantProduct
			if db.Select("id", "code").First(&product, *item.ProductID).Error == nil {
				if code := strings.TrimSpace(product.Code); code != "" {
					item.Code = code
					continue
				}
			}
		}
		item.Code = manualItemCode
	}
}
