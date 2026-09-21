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

func (s *EcommerceService) GetDispatch(id uint) (*database.TenantEcommerceDispatch, error) {
	var d database.TenantEcommerceDispatch
	if err := s.db.First(&d, id).Error; err != nil {
		return nil, errors.New("despacho no encontrado")
	}
	return &d, nil
}

// DispatchTransitionInput UserID: quién ejecuta la transición, para DispatchStatusHistory (y
// OrderStatusHistory cuando corresponda). Notes: opcional, mismo patrón que
// UpdateOrderStatusInput.Notes.
type DispatchTransitionInput struct {
	UserID uint
	Notes  string
}

// MarkDispatchInTransit DESPACHADO->EN_TRANSITO — Contrato v2 §9, Fase 9. SOLO cambia
// Dispatch.Status; Order.Status se queda en DESPACHADO (decisión aprobada explícitamente: son
// ciclos de vida independientes, ver comentario de TenantEcommerceDispatch). Transacción con
// SELECT...FOR UPDATE sobre el Dispatch (mismo patrón de concurrencia de Fase 8/1.5) para que dos
// requests concurrentes sobre el MISMO despacho nunca produzcan dos transiciones ni dos filas de
// historial.
func (s *EcommerceService) MarkDispatchInTransit(dispatchID uint, input DispatchTransitionInput) (*database.TenantEcommerceDispatch, error) {
	var dispatch database.TenantEcommerceDispatch
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&dispatch, dispatchID).Error; err != nil {
			return errors.New("despacho no encontrado")
		}
		if dispatch.Status != DispatchStatusDespachado {
			return fmt.Errorf("el despacho debe estar DESPACHADO para pasar a EN_TRANSITO (estado actual: %s)", dispatch.Status)
		}
		if err := tx.Model(&database.TenantEcommerceDispatch{}).Where("id = ?", dispatchID).
			Update("status", DispatchStatusEnTransito).Error; err != nil {
			return err
		}
		var userID *uint
		if input.UserID > 0 {
			userID = &input.UserID
		}
		if err := tx.Create(&database.TenantEcommerceDispatchStatusHistory{
			DispatchID: dispatchID, FromStatus: DispatchStatusDespachado, ToStatus: DispatchStatusEnTransito,
			UserID: userID, Notes: strings.TrimSpace(input.Notes),
		}).Error; err != nil {
			return err
		}
		dispatch.Status = DispatchStatusEnTransito
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &dispatch, nil
}

// MarkDispatchDelivered EN_TRANSITO->ENTREGADO (Dispatch) + DESPACHADO->ENTREGADO (Order) —
// Contrato v2 §9, Fase 9. A diferencia de MarkDispatchInTransit, ACÁ sí cambian ambos ciclos de
// vida — atómico en una sola transacción: bloquea Dispatch y Order (FOR UPDATE en ambos, mismo
// criterio que CreateDispatch), valida los dos estados actuales, fija DeliveredAt con el reloj del
// servidor (nunca confía en un valor enviado por el cliente — CreateDispatchInput/
// UpdateDispatchInput ni siquiera tienen ese campo, es estructuralmente imposible que el caller lo
// mande), y escribe AMBOS historiales (Dispatch y Order) antes de confirmar. Si cualquier paso
// falla, la transacción entera revierte — nunca queda Dispatch=ENTREGADO con Order=DESPACHADO ni
// viceversa.
func (s *EcommerceService) MarkDispatchDelivered(dispatchID uint, input DispatchTransitionInput) (*database.TenantEcommerceDispatch, error) {
	var dispatch database.TenantEcommerceDispatch
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&dispatch, dispatchID).Error; err != nil {
			return errors.New("despacho no encontrado")
		}
		if dispatch.Status != DispatchStatusEnTransito {
			return fmt.Errorf("el despacho debe estar EN_TRANSITO para marcarse ENTREGADO (estado actual: %s)", dispatch.Status)
		}
		var order database.TenantEcommerceOrder
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&order, dispatch.OrderID).Error; err != nil {
			return errors.New("pedido no encontrado")
		}
		if order.Status != OrderStatusDespachado {
			// Defensa en profundidad: por construcción esto nunca debería divergir de
			// Dispatch.Status==EN_TRANSITO, pero nunca se asume — siempre se revalida.
			return fmt.Errorf("el pedido debe estar DESPACHADO para marcarse ENTREGADO (estado actual: %s)", order.Status)
		}

		now := time.Now()
		if err := tx.Model(&database.TenantEcommerceDispatch{}).Where("id = ?", dispatchID).
			Updates(map[string]interface{}{"status": DispatchStatusEntregado, "delivered_at": &now}).Error; err != nil {
			return err
		}
		if err := tx.Model(&database.TenantEcommerceOrder{}).Where("id = ?", order.ID).
			Update("status", OrderStatusEntregado).Error; err != nil {
			return err
		}
		var userID *uint
		if input.UserID > 0 {
			userID = &input.UserID
		}
		if err := tx.Create(&database.TenantEcommerceDispatchStatusHistory{
			DispatchID: dispatchID, FromStatus: DispatchStatusEnTransito, ToStatus: DispatchStatusEntregado,
			UserID: userID, Notes: strings.TrimSpace(input.Notes),
		}).Error; err != nil {
			return err
		}
		if err := tx.Create(&database.TenantEcommerceOrderStatusHistory{
			OrderID: order.ID, FromStatus: OrderStatusDespachado, ToStatus: OrderStatusEntregado, UserID: userID,
		}).Error; err != nil {
			return err
		}
		dispatch.Status = DispatchStatusEntregado
		dispatch.DeliveredAt = &now
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &dispatch, nil
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
