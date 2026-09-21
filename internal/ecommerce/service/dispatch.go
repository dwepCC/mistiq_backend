package service

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"tukifac/pkg/database"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// CreateDispatchInput datos opcionales del despacho — todos punteros/opcionales porque ningún dato
// de transporte es obligatorio (Contrato v2 Fase 8: RECOJO_TIENDA nunca exige transportista/
// tracking, y ENVIO_DOMICILIO reutiliza la dirección ya persistida en el pedido, nunca pide una
// nueva acá). UserID: quién ejecuta el despacho, para StatusHistory.
type CreateDispatchInput struct {
	CarrierName  *string
	TrackingCode *string
	PackageCount *int
	WeightKg     *float64
	LengthCm     *float64
	WidthCm      *float64
	HeightCm     *float64
	Notes        string
	UserID       uint
}

func validateDispatchMetrics(packageCount *int, weightKg, lengthCm, widthCm, heightCm *float64) error {
	if packageCount != nil && *packageCount <= 0 {
		return errors.New("la cantidad de bultos debe ser mayor a cero")
	}
	if weightKg != nil {
		if math.IsNaN(*weightKg) || math.IsInf(*weightKg, 0) {
			return errors.New("peso inválido")
		}
		if *weightKg < 0 {
			return errors.New("el peso no puede ser negativo")
		}
	}
	if lengthCm != nil {
		if math.IsNaN(*lengthCm) || math.IsInf(*lengthCm, 0) {
			return errors.New("largo inválido")
		}
		if *lengthCm < 0 {
			return errors.New("el largo no puede ser negativo")
		}
	}
	if widthCm != nil {
		if math.IsNaN(*widthCm) || math.IsInf(*widthCm, 0) {
			return errors.New("ancho inválido")
		}
		if *widthCm < 0 {
			return errors.New("el ancho no puede ser negativo")
		}
	}
	if heightCm != nil {
		if math.IsNaN(*heightCm) || math.IsInf(*heightCm, 0) {
			return errors.New("alto inválido")
		}
		if *heightCm < 0 {
			return errors.New("el alto no puede ser negativo")
		}
	}
	return nil
}

// CreateDispatch crea el despacho operativo de un pedido y lo pasa a DESPACHADO — Contrato v2 §5/
// §8, Fase 8. Operación ATÓMICA (una sola transacción): bloquea la fila del pedido con
// SELECT...FOR UPDATE (mismo patrón exacto que el fix de concurrencia de stock de Fase 1.5,
// internal/inventory/service/inventory_service.go RecordMovementTx) para que dos requests
// concurrentes sobre el MISMO pedido nunca puedan crear dos despachos — la segunda transacción
// espera a que la primera confirme, relee el pedido ya en DESPACHADO, y se rechaza limpio por la
// validación de estado (nunca por un error crudo de base de datos). El UNIQUE(order_id) de la
// migración v142 queda como defensa adicional, no como mecanismo primario.
func (s *EcommerceService) CreateDispatch(orderID uint, input CreateDispatchInput) (*database.TenantEcommerceDispatch, error) {
	if err := validateDispatchMetrics(input.PackageCount, input.WeightKg, input.LengthCm, input.WidthCm, input.HeightCm); err != nil {
		return nil, err
	}

	var dispatch database.TenantEcommerceDispatch
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var order database.TenantEcommerceOrder
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&order, orderID).Error; err != nil {
			return errors.New("pedido no encontrado")
		}
		if order.Status != OrderStatusListoParaDespacho {
			return fmt.Errorf("el pedido debe estar LISTO_PARA_DESPACHO para despachar (estado actual: %s)", order.Status)
		}

		var existing database.TenantEcommerceDispatch
		if err := tx.Where("order_id = ?", orderID).First(&existing).Error; err == nil {
			return errors.New("este pedido ya tiene un despacho registrado")
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}

		now := time.Now()
		dispatch = database.TenantEcommerceDispatch{
			OrderID:      orderID,
			Status:       DispatchStatusDespachado,
			CarrierName:  trimmedOrNil(input.CarrierName),
			TrackingCode: trimmedOrNil(input.TrackingCode),
			PackageCount: input.PackageCount,
			WeightKg:     input.WeightKg,
			LengthCm:     input.LengthCm,
			WidthCm:      input.WidthCm,
			HeightCm:     input.HeightCm,
			DispatchedAt: &now,
			Notes:        strings.TrimSpace(input.Notes),
		}
		if input.UserID > 0 {
			dispatch.UserID = &input.UserID
		}
		if err := tx.Create(&dispatch).Error; err != nil {
			// Red de seguridad: si por alguna carrera el chequeo de arriba no alcanzó a verlo (no
			// debería pasar bajo FOR UPDATE, pero el UNIQUE de BD es la garantía final), nunca se
			// filtra un error crudo de SQL al caller.
			return errors.New("no se pudo crear el despacho (es posible que ya exista uno para este pedido)")
		}

		if err := tx.Model(&database.TenantEcommerceOrder{}).Where("id = ?", orderID).
			Update("status", OrderStatusDespachado).Error; err != nil {
			return err
		}
		var userID *uint
		if input.UserID > 0 {
			userID = &input.UserID
		}
		return tx.Create(&database.TenantEcommerceOrderStatusHistory{
			OrderID:    orderID,
			FromStatus: OrderStatusListoParaDespacho,
			ToStatus:   OrderStatusDespachado,
			UserID:     userID,
		}).Error
	})
	if err != nil {
		return nil, err
	}
	return &dispatch, nil
}

// UpdateDispatchInput solo metadata editable — Status/OrderID quedan deliberadamente fuera
// (decisión aprobada de Fase 8: avanzar Dispatch.Status es Fase 9).
type UpdateDispatchInput struct {
	CarrierName  *string
	TrackingCode *string
	PackageCount *int
	WeightKg     *float64
	LengthCm     *float64
	WidthCm      *float64
	HeightCm     *float64
	Notes        *string
}

// UpdateDispatch corrige/completa datos de un despacho ya creado (p. ej. agregar el tracking una
// vez que el transportista lo entrega). Nunca toca Status, OrderID, ni fechas de auditoría
// controladas por el backend (CreatedAt/UpdatedAt/DispatchedAt) — coherente con la restricción
// aprobada explícitamente para este endpoint.
func (s *EcommerceService) UpdateDispatch(id uint, input UpdateDispatchInput) (*database.TenantEcommerceDispatch, error) {
	if err := validateDispatchMetrics(input.PackageCount, input.WeightKg, input.LengthCm, input.WidthCm, input.HeightCm); err != nil {
		return nil, err
	}
	var dispatch database.TenantEcommerceDispatch
	if err := s.db.First(&dispatch, id).Error; err != nil {
		return nil, errors.New("despacho no encontrado")
	}
	upd := map[string]interface{}{}
	if input.CarrierName != nil {
		upd["carrier_name"] = trimmedOrNil(input.CarrierName)
	}
	if input.TrackingCode != nil {
		upd["tracking_code"] = trimmedOrNil(input.TrackingCode)
	}
	if input.PackageCount != nil {
		upd["package_count"] = *input.PackageCount
	}
	if input.WeightKg != nil {
		upd["weight_kg"] = *input.WeightKg
	}
	if input.LengthCm != nil {
		upd["length_cm"] = *input.LengthCm
	}
	if input.WidthCm != nil {
		upd["width_cm"] = *input.WidthCm
	}
	if input.HeightCm != nil {
		upd["height_cm"] = *input.HeightCm
	}
	if input.Notes != nil {
		upd["notes"] = strings.TrimSpace(*input.Notes)
	}
	if len(upd) > 0 {
		if err := s.db.Model(&database.TenantEcommerceDispatch{}).Where("id = ?", id).Updates(upd).Error; err != nil {
			return nil, err
		}
	}
	if err := s.db.First(&dispatch, id).Error; err != nil {
		return nil, err
	}
	return &dispatch, nil
}

// GetDispatchByOrderID nil (sin error) si el pedido todavía no tiene despacho — GetOrderDetail lo
// usa para exponer "dispatch": null en vez de un 404 cuando simplemente no existe todavía.
func (s *EcommerceService) GetDispatchByOrderID(orderID uint) (*database.TenantEcommerceDispatch, error) {
	var d database.TenantEcommerceDispatch
	err := s.db.Where("order_id = ?", orderID).First(&d).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &d, nil
}

func trimmedOrNil(v *string) *string {
	if v == nil {
		return nil
	}
	t := strings.TrimSpace(*v)
	if t == "" {
		return nil
	}
	return &t
}
