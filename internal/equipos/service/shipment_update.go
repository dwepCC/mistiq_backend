package service

import (
	"gorm.io/gorm"

	"tukifac/pkg/database"
)

// UpdateShipment edita los datos del envío vigente (transportista, guía, destino, fecha programada) sin cambiar su estado.
func (s *Service) UpdateShipment(orderID uint, in ShipmentInput) (*OrderResult, error) {
	var warnings []string
	err := s.db.Transaction(func(tx *gorm.DB) error {
		o, err := s.loadOrderForUpdate(tx, orderID)
		if err != nil {
			return err
		}
		if o.Status == "anulado" {
			return invalid("el pedido está anulado")
		}
		if err := s.shipmentUpdateOnOrderTx(tx, orderID, &in); err != nil {
			return err
		}
		var sh database.EquipShipment
		if found, _ := findOne(tx, &sh, "order_id = ? AND is_current = ?", orderID, true); found && sh.Status == "pendiente_envio" {
			if msg := s.dispatchDayWarning(tx, &sh); msg != "" {
				warnings = append(warnings, msg)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	v, err := s.GetOrder(orderID)
	if err != nil {
		return nil, err
	}
	return &OrderResult{Order: v, Warnings: warnings}, nil
}
