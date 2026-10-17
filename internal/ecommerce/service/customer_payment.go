package service

import (
	"errors"
	"strings"

	"tukifac/pkg/database"
)

// TrackPaymentMethod medio de pago que el cliente puede elegir al enviar su voucher.
type TrackPaymentMethod struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

// findTrackedOrder el pedido de quien dio el número y el celular correctos. Mismo error para «no existe» y
// «el celular no coincide».
func (s *EcommerceService) findTrackedOrder(orderRef, phone string) (database.TenantEcommerceOrder, error) {
	var order database.TenantEcommerceOrder
	id, ok := parseOrderRef(orderRef)
	want := digitsTail(phone, 9)
	if !ok || len(want) < 7 {
		return order, ErrTrackNotFound
	}
	if err := s.db.First(&order, id).Error; err != nil {
		return order, ErrTrackNotFound
	}
	if want != digitsTail(order.CustomerPhone, 9) && want != digitsTail(order.RecipientPhone, 9) {
		return database.TenantEcommerceOrder{}, ErrTrackNotFound
	}
	return order, nil
}

// AuthorizeTrack devuelve el id del pedido si el número y el celular corresponden a él.
func (s *EcommerceService) AuthorizeTrack(orderRef, phone string) (uint, error) {
	order, err := s.findTrackedOrder(orderRef, phone)
	if err != nil {
		return 0, err
	}
	return order.ID, nil
}

// customerPaymentMethods medios directos y activos del negocio (sin crédito ni detracción).
func (s *EcommerceService) customerPaymentMethods() []TrackPaymentMethod {
	if !s.db.Migrator().HasTable(&database.TenantPaymentMethod{}) {
		return nil
	}
	var rows []database.TenantPaymentMethod
	if err := s.db.Where("active = ?", true).Order("sort_order ASC, id ASC").Find(&rows).Error; err != nil {
		return nil
	}
	out := make([]TrackPaymentMethod, 0, len(rows))
	for _, m := range rows {
		lower := strings.ToLower(m.Code)
		if lower == "credit" || lower == "credito" || strings.Contains(lower, "detrac") {
			continue
		}
		out = append(out, TrackPaymentMethod{Code: m.Code, Name: m.Name})
	}
	return out
}

// maxCustomerPendingPayments tope de comprobantes del cliente sin revisar por pedido (evita llenarlo de envíos).
const maxCustomerPendingPayments = 3

var errTooManyPendingPayments = errors.New("ya enviaste varios comprobantes de este pedido: espera a que los revisemos")
