// Package saleunit resuelve la conversión de cantidad comercial → cantidad base a partir de una
// TenantProductSaleUnit (Fase 1). Vive en pkg/ (no en internal/products/service) para poder ser
// importado tanto por internal/sales/service como por internal/purchases/service sin crear un
// ciclo de imports: internal/products/service ya depende de internal/restaurant/service, que a su
// vez depende de internal/sales/service — cualquier resolver puesto en internal/products/service
// sería inalcanzable desde sales sin ciclo.
package saleunit

import (
	"errors"
	"fmt"
	"math"

	"tukifac/pkg/database"

	"gorm.io/gorm"
)

// Resolution resultado de resolver una unidad de venta para una línea (de venta o de compra): la
// cantidad base a mover en stock/Kardex y el factor usado, para dejarlo como snapshot histórico
// en el movimiento correspondiente.
type Resolution struct {
	BaseQuantity     float64
	ConversionFactor float64
}

// ResolveForLine es el ÚNICO punto que traduce cantidad comercial → cantidad base a partir de una
// TenantProductSaleUnit. Lo comparten internal/sales/service e internal/purchases/service —
// ninguno de los dos debe repetir esta multiplicación ni releer el factor por su cuenta. El
// factor SIEMPRE se lee de BD aquí, nunca del cliente.
//
// db ya debe estar scopeado al tenant que llama (una base de datos física por tenant, resuelta
// por middleware antes de construir *gorm.DB) — no hay columna tenant_id que verificar aparte.
//
// Devuelve (nil, nil) si saleUnitID es nil o 0: es la señal de "línea legacy, sin unidad de
// venta", y el caller debe seguir usando la cantidad tal cual (comportamiento de siempre).
func ResolveForLine(db *gorm.DB, productID uint, saleUnitID *uint, commercialQuantity float64) (*Resolution, error) {
	if saleUnitID == nil || *saleUnitID == 0 {
		return nil, nil
	}
	var su database.TenantProductSaleUnit
	if err := db.Where("id = ? AND product_id = ?", *saleUnitID, productID).First(&su).Error; err != nil {
		return nil, errors.New("la unidad de venta indicada no existe o no pertenece a este producto")
	}
	if !su.Active {
		return nil, fmt.Errorf("la unidad de venta '%s' está desactivada", su.Name)
	}
	if su.ConversionFactor <= 0 {
		// No debería ocurrir (Fase 1 ya lo valida al guardar), pero se revalida acá antes de usar
		// el valor para no confiar ciegamente en un dato que pudo cambiar entre medio.
		return nil, fmt.Errorf("la unidad de venta '%s' tiene un factor de conversión inválido", su.Name)
	}
	if !su.AllowFraction && !IsWholeNumber(commercialQuantity) {
		return nil, fmt.Errorf("la unidad de venta '%s' no admite cantidades fraccionadas", su.Name)
	}
	return &Resolution{
		BaseQuantity:     commercialQuantity * su.ConversionFactor,
		ConversionFactor: su.ConversionFactor,
	}, nil
}

// IsWholeNumber con la misma tolerancia de punto flotante que ya usa el proyecto para cantidades
// (ver internal/billing/service/note_partial.go).
func IsWholeNumber(q float64) bool {
	return math.Abs(q-math.Round(q)) <= 0.0001
}
