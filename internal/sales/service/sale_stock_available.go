package service

import (
	"fmt"

	"tukifac/pkg/stockreserve"

	"gorm.io/gorm"
)

// checkAvailableStock valida que haya stock DISPONIBLE (en almacén menos lo reservado por pedidos
// web confirmados) para vender qty. excludeOrderID es el pedido web que se está convirtiendo en
// esta venta: su reserva no cuenta contra sí mismo.
func checkAvailableStock(db *gorm.DB, description string, qty, onHand float64, branchID, excludeOrderID, productID uint, presentationID *uint) error {
	// Si no se puede calcular lo reservado (p. ej. tenant sin las tablas de ecommerce), no se
	// bloquea la venta: el stock físico ya se validó y la reserva es una capa adicional.
	reserved, err := stockreserve.ReservedFor(db, branchID, excludeOrderID, productID, presentationID)
	if err != nil {
		reserved = 0
	}
	if onHand-reserved >= qty {
		return nil
	}
	if reserved > 0 {
		return fmt.Errorf("stock insuficiente para %s: requiere %.2f, hay %.2f en almacén pero %.2f están reservados en pedidos web (disponible %.2f)",
			description, qty, onHand, reserved, onHand-reserved)
	}
	return fmt.Errorf("stock insuficiente para %s: requiere %.2f, hay %.2f", description, qty, onHand)
}
