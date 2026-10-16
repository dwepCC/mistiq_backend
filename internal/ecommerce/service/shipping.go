package service

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"unicode"

	"tukifac/pkg/database"
	"tukifac/pkg/stockreserve"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Tipos de documento del destinatario (códigos SUNAT, igual que TenantContact.DocType).
const (
	RecipientDocDNI = "1"
	RecipientDocCE  = "4"
	RecipientDocRUC = "6"
)

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

// NormalizeRecipientDoc valida el documento de quien recoge el paquete. Si el tipo viene vacío lo
// deduce por el largo (8 dígitos = DNI, 11 = RUC). Devuelve (tipo, número) normalizados.
func NormalizeRecipientDoc(docType, number string) (string, string, error) {
	number = strings.ToUpper(strings.TrimSpace(number))
	docType = strings.TrimSpace(docType)
	if number == "" {
		return "", "", errors.New("el documento de quien recoge es obligatorio")
	}
	if docType == "" {
		switch {
		case len(number) == 8 && isDigits(number):
			docType = RecipientDocDNI
		case len(number) == 11 && isDigits(number):
			docType = RecipientDocRUC
		default:
			docType = RecipientDocCE
		}
	}
	switch docType {
	case RecipientDocDNI:
		if len(number) != 8 || !isDigits(number) {
			return "", "", errors.New("el DNI debe tener 8 dígitos")
		}
	case RecipientDocRUC:
		if len(number) != 11 || !isDigits(number) {
			return "", "", errors.New("el RUC debe tener 11 dígitos")
		}
	case RecipientDocCE:
		if len(number) < 9 || len(number) > 12 {
			return "", "", errors.New("el carné de extranjería debe tener entre 9 y 12 caracteres")
		}
		for _, r := range number {
			if !unicode.IsDigit(r) && !unicode.IsLetter(r) {
				return "", "", errors.New("el carné de extranjería solo admite letras y números")
			}
		}
	default:
		return "", "", errors.New("tipo de documento inválido (usa DNI, CE o RUC)")
	}
	return docType, number, nil
}

// OrderShippingInput datos de envío editables del pedido. Los punteros distinguen "no tocar" de
// "dejar vacío". CarrierID 0 quita el transportista.
type OrderShippingInput struct {
	CarrierID          *uint
	DestinationUbigeo  *string
	DestinationAgency  *string
	DestinationAddress *string
	RecipientName      *string
	RecipientDocType   *string
	RecipientDocNumber *string
	RecipientPhone     *string
	ShippingCost       *float64
	PackageCount       *int
	WeightKg           *float64
}

// statusesEditableShipping el envío solo se edita mientras el pedido no haya salido.
var statusesEditableShipping = map[string]bool{
	OrderStatusPendiente: true, OrderStatusConfirmado: true, OrderStatusEnPreparacion: true,
	OrderStatusEmpaquetado: true, OrderStatusListoParaDespacho: true,
}

func cleanUbigeo(v string) (string, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return "", nil
	}
	if len(v) != 6 || !isDigits(v) {
		return "", errors.New("el ubigeo debe tener 6 dígitos")
	}
	return v, nil
}

// UpdateOrderShipping actualiza los datos de envío de un pedido que aún no salió.
func (s *EcommerceService) UpdateOrderShipping(orderID uint, in OrderShippingInput) (*database.TenantEcommerceOrder, error) {
	var order database.TenantEcommerceOrder
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&order, orderID).Error; err != nil {
			return errors.New("pedido no encontrado")
		}
		if !statusesEditableShipping[order.Status] {
			return fmt.Errorf("el envío ya no se puede editar (estado %s)", order.Status)
		}
		upd := map[string]interface{}{}
		if in.CarrierID != nil {
			if *in.CarrierID == 0 {
				upd["carrier_id"] = nil
			} else {
				if _, err := s.GetCarrier(*in.CarrierID); err != nil {
					return err
				}
				upd["carrier_id"] = *in.CarrierID
			}
		}
		if in.DestinationUbigeo != nil {
			u, err := cleanUbigeo(*in.DestinationUbigeo)
			if err != nil {
				return err
			}
			upd["destination_ubigeo"] = u
		}
		if in.DestinationAgency != nil {
			upd["destination_agency"] = strings.TrimSpace(*in.DestinationAgency)
		}
		if in.DestinationAddress != nil {
			upd["destination_address"] = strings.TrimSpace(*in.DestinationAddress)
		}
		if in.RecipientName != nil {
			upd["recipient_name"] = strings.TrimSpace(*in.RecipientName)
		}
		if in.RecipientPhone != nil {
			upd["recipient_phone"] = strings.TrimSpace(*in.RecipientPhone)
		}
		if in.RecipientDocNumber != nil || in.RecipientDocType != nil {
			number := order.RecipientDocNumber
			if in.RecipientDocNumber != nil {
				number = *in.RecipientDocNumber
			}
			docType := order.RecipientDocType
			if in.RecipientDocType != nil {
				docType = *in.RecipientDocType
			}
			if strings.TrimSpace(number) == "" {
				upd["recipient_doc_type"] = ""
				upd["recipient_doc_number"] = ""
			} else {
				t, n, err := NormalizeRecipientDoc(docType, number)
				if err != nil {
					return err
				}
				upd["recipient_doc_type"] = t
				upd["recipient_doc_number"] = n
			}
		}
		if in.ShippingCost != nil {
			if math.IsNaN(*in.ShippingCost) || math.IsInf(*in.ShippingCost, 0) || *in.ShippingCost < 0 {
				return errors.New("el costo de envío no puede ser negativo")
			}
			upd["shipping_cost"] = math.Round(*in.ShippingCost*100) / 100
		}
		if in.PackageCount != nil {
			if *in.PackageCount <= 0 {
				return errors.New("la cantidad de bultos debe ser mayor a cero")
			}
			upd["package_count"] = *in.PackageCount
		}
		if in.WeightKg != nil {
			if math.IsNaN(*in.WeightKg) || math.IsInf(*in.WeightKg, 0) || *in.WeightKg < 0 {
				return errors.New("el peso no puede ser negativo")
			}
			upd["weight_kg"] = *in.WeightKg
		}
		if len(upd) == 0 {
			return nil
		}
		if err := tx.Model(&database.TenantEcommerceOrder{}).Where("id = ?", orderID).Updates(upd).Error; err != nil {
			return err
		}
		return tx.First(&order, orderID).Error
	})
	if err != nil {
		return nil, err
	}
	return &order, nil
}

// validateAgencyOrderForDispatch un envío por agencia no sale sin transportista, destino, agencia
// y documento de quien recoge: la agencia no entrega el paquete sin ellos.
func validateAgencyOrderForDispatch(order *database.TenantEcommerceOrder, carrierID *uint) error {
	if order.DeliveryMethod != DeliveryMethodAgency {
		return nil
	}
	var missing []string
	if carrierID == nil && order.CarrierID == nil {
		missing = append(missing, "transportista")
	}
	if strings.TrimSpace(order.DestinationAgency) == "" {
		missing = append(missing, "agencia de destino")
	}
	if strings.TrimSpace(order.DestinationUbigeo) == "" {
		missing = append(missing, "ciudad de destino")
	}
	if strings.TrimSpace(order.RecipientName) == "" {
		missing = append(missing, "nombre de quien recoge")
	}
	if strings.TrimSpace(order.RecipientDocNumber) == "" {
		missing = append(missing, "documento (DNI) de quien recoge")
	}
	if len(missing) > 0 {
		return fmt.Errorf("faltan datos del envío: %s", strings.Join(missing, ", "))
	}
	return nil
}

// agencyOrderData datos de envío por agencia ya validados, listos para copiarse al pedido.
type agencyOrderData struct {
	carrierID *uint
	ubigeo    string
	agency    string
	address   string
	name      string
	docType   string
	docNumber string
	phone     string
}

// validateAgencyCheckout valida el checkout de un envío por agencia: el cliente elige
// transportista, ciudad y agencia, y deja el documento de quien recogerá el paquete.
func (s *EcommerceService) validateAgencyCheckout(in CreateOrderInput) (agencyOrderData, error) {
	var out agencyOrderData
	if in.CarrierID == nil || *in.CarrierID == 0 {
		return out, errors.New("elige la empresa de envío")
	}
	var carrier database.TenantCarrier
	if err := s.db.Where("id = ? AND active = ? AND show_in_store = ?", *in.CarrierID, true, true).First(&carrier).Error; err != nil {
		return out, errors.New("la empresa de envío elegida ya no está disponible")
	}
	out.carrierID = &carrier.ID

	ubigeo, err := cleanUbigeo(in.DestinationUbigeo)
	if err != nil {
		return out, err
	}
	if ubigeo == "" {
		return out, errors.New("elige el departamento y la ciudad de destino")
	}
	out.ubigeo = ubigeo

	out.agency = strings.TrimSpace(in.DestinationAgency)
	if out.agency == "" {
		return out, errors.New("indica la agencia donde recogerás el paquete")
	}
	if len(out.agency) > 150 {
		return out, errors.New("el nombre de la agencia es demasiado largo")
	}
	out.address = strings.TrimSpace(in.DestinationAddress)

	out.name = strings.TrimSpace(in.RecipientName)
	if out.name == "" {
		out.name = strings.TrimSpace(in.CustomerName)
	}
	if out.name == "" {
		return out, errors.New("el nombre de quien recoge es obligatorio")
	}
	out.docType, out.docNumber, err = NormalizeRecipientDoc(in.RecipientDocType, in.RecipientDocNumber)
	if err != nil {
		return out, err
	}
	out.phone = strings.TrimSpace(in.RecipientPhone)
	if out.phone == "" {
		out.phone = strings.TrimSpace(in.CustomerPhone)
	}
	return out, nil
}

// OrderStockLines disponibilidad de cada línea del pedido en la sucursal (almacén, reservado por
// otros pedidos y disponible), para mostrarla al preparar o confirmar.
func (s *EcommerceService) OrderStockLines(orderID, branchID uint) ([]stockreserve.Line, error) {
	return stockreserve.OrderLines(s.db, orderID, branchID, false)
}
