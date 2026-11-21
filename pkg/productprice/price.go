// Package productprice resuelve el precio base de un producto "normal" (sin unidad de venta) para
// una sucursal puntual — mismo rol que pkg/saleunit para SaleUnits, separado a propósito: un
// producto sin SaleUnits no debe activar el selector de "elegir unidad" del POS solo por tener un
// precio de sucursal configurado (ver comentario de database.TenantProductBranchPrice).
package productprice

import (
	"errors"

	"tukifac/pkg/database"

	"gorm.io/gorm"
)

// ResolveSalePrice resuelve el precio base de un producto para una sucursal: override activo
// (TenantProductBranchPrice) si existe, si no defaultPrice (normalmente product.SalePrice).
// branchID=0 se trata como "sin contexto de sucursal": devuelve defaultPrice directo, sin
// consultar la tabla. Único punto que decide esto — nadie más (handler, servicio de ventas, POS)
// debe repetir esta resolución.
func ResolveSalePrice(db *gorm.DB, productID, branchID uint, defaultPrice float64) (float64, error) {
	if branchID == 0 {
		return defaultPrice, nil
	}
	var override database.TenantProductBranchPrice
	err := db.Where("product_id = ? AND branch_id = ? AND active = ?", productID, branchID, true).
		First(&override).Error
	if err == nil {
		return override.SalePrice, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, err
	}
	return defaultPrice, nil
}
