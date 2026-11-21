package saleunit

import (
	"errors"
	"fmt"

	"tukifac/pkg/database"

	"gorm.io/gorm"
)

// ResolvePrice resuelve el precio de una TenantProductSaleUnit para una sucursal y nivel de
// precio (1, 2 o 3 — Price1/Price2/Price3) dados. Es el ÚNICO punto que decide ese precio: nadie
// más (handler, servicio de ventas, POS) debe repetir esta cadena de fallback.
//
// Orden de resolución (Fase 4):
//  1. Override de sucursal activo (TenantProductSaleUnitBranchPrice) para ese nivel, si existe y
//     tiene ese nivel configurado (Price2/Price3 son nullable).
//  2. Precio global de la SaleUnit para ese nivel, si lo tiene configurado.
//  3. Price1 de la SaleUnit (siempre existe — es NOT NULL) como último fallback, si el nivel
//     pedido (2 o 3) no está configurado ni globalmente ni en la sucursal.
//
// IMPORTANTE: nunca multiplica por ConversionFactor. El precio de cada SaleUnit (global o de
// sucursal) es un valor independiente, no una fórmula sobre la unidad base.
//
// db ya debe estar scopeado al tenant que llama (una base de datos física por tenant) — no hay
// columna tenant_id que verificar aparte. branchID=0 se trata como "sin contexto de sucursal": se
// salta directo al precio global.
//
// productID (Fase 6.1): defensa adicional — verifica que la SaleUnit pertenezca a este producto
// antes de resolver nada. El flujo real de ventas/compras ya valida esto antes de llegar acá
// (ResolveForLine, desde validateSaleUnits/validatePurchaseSaleUnits), así que esto nunca debería
// disparar en producción hoy; existe para que ResolvePrice no dependa únicamente de que el caller
// haya validado antes — cualquier código futuro que la llame directamente queda protegido igual.
func ResolvePrice(db *gorm.DB, productID uint, saleUnitID uint, branchID uint, level int) (float64, error) {
	var su database.TenantProductSaleUnit
	if err := db.First(&su, saleUnitID).Error; err != nil {
		return 0, errors.New("unidad de venta no encontrada")
	}
	if su.ProductID != productID {
		return 0, errors.New("la unidad de venta no pertenece a este producto")
	}

	if branchID > 0 {
		var override database.TenantProductSaleUnitBranchPrice
		err := db.Where("sale_unit_id = ? AND branch_id = ? AND active = ?", saleUnitID, branchID, true).
			First(&override).Error
		if err == nil {
			if v, ok := priceForLevel(override.Price1, override.Price2, override.Price3, level); ok {
				return v, nil
			}
			// El override existe pero no tiene ese nivel configurado: cae al precio global de la
			// SaleUnit para ese mismo nivel (no se considera "sin override" desde cero).
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, err
		}
	}

	if v, ok := priceForLevel(su.Price1, su.Price2, su.Price3, level); ok {
		return v, nil
	}
	// Nivel 2/3 no configurado ni en la sucursal ni globalmente: Price1 de la SaleUnit es el
	// único valor NOT NULL, así que es el fallback final (nunca salta a Product.SalePrice desde
	// acá — eso lo decide el caller para el caso "producto legacy sin SaleUnit").
	return su.Price1, nil
}

// priceForLevel selecciona price1/2/3 según level (1, 2 o 3; cualquier otro valor se trata como
// 1). Devuelve ok=false si el nivel pedido es 2 o 3 y ese campo es nil.
func priceForLevel(price1 float64, price2, price3 *float64, level int) (float64, bool) {
	switch level {
	case 2:
		if price2 != nil {
			return *price2, true
		}
		return 0, false
	case 3:
		if price3 != nil {
			return *price3, true
		}
		return 0, false
	default:
		return price1, true
	}
}

// ValidateBranchPriceInput valida los campos comunes a crear/actualizar un override de sucursal.
func ValidateBranchPriceInput(price1 float64, price2, price3 *float64) error {
	if price1 <= 0 {
		return fmt.Errorf("price1 debe ser mayor a cero")
	}
	if price2 != nil && *price2 <= 0 {
		return fmt.Errorf("price2 debe ser mayor a cero si se especifica")
	}
	if price3 != nil && *price3 <= 0 {
		return fmt.Errorf("price3 debe ser mayor a cero si se especifica")
	}
	return nil
}
