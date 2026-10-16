package service

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"tukifac/pkg/database"
	"tukifac/pkg/tax"

	"gorm.io/gorm"
)

// pickupDueDate fecha límite para cobrar el saldo: la indicada o, si no, hoy + el plazo de recojo de
// la empresa de envío del pedido (15 días si no tiene).
func (s *EcommerceService) pickupDueDate(order *database.TenantEcommerceOrder, in ConvertInput) *time.Time {
	if in.DueDate != nil {
		d := *in.DueDate
		return &d
	}
	days := defaultPickupDeadlineDays
	if order.CarrierID != nil {
		var c database.TenantCarrier
		if s.db.Unscoped().First(&c, *order.CarrierID).Error == nil && c.PickupDeadlineDays > 0 {
			days = c.PickupDeadlineDays
		}
	}
	issue := in.IssueDate
	if issue.IsZero() {
		issue = time.Now()
	}
	d := issue.Add(time.Duration(days) * 24 * time.Hour)
	return &d
}

// ensureOrderContact cliente al que se le cobrará el saldo: el contacto del pedido, uno existente con el
// documento de quien recoge, o uno nuevo creado con esos datos.
func (s *EcommerceService) ensureOrderContact(order *database.TenantEcommerceOrder) (*uint, error) {
	if order.ContactID != nil && *order.ContactID > 0 {
		return order.ContactID, nil
	}
	docNumber := strings.TrimSpace(order.RecipientDocNumber)
	docType := strings.TrimSpace(order.RecipientDocType)
	if docNumber == "" || docType == "" {
		return nil, errors.New("el pedido no tiene documento del cliente")
	}
	var existing database.TenantContact
	if err := s.db.Where("doc_number = ? AND doc_type = ?", docNumber, docType).First(&existing).Error; err == nil {
		id := existing.ID
		return &id, nil
	}
	name := strings.TrimSpace(order.RecipientName)
	if name == "" {
		name = strings.TrimSpace(order.CustomerName)
	}
	if name == "" {
		return nil, errors.New("el pedido no tiene nombre del cliente")
	}
	phone := strings.TrimSpace(order.RecipientPhone)
	if phone == "" {
		phone = strings.TrimSpace(order.CustomerPhone)
	}
	c := database.TenantContact{
		Type: "customer", DocType: docType, DocNumber: docNumber, BusinessName: name, Phone: phone, Active: true,
	}
	if err := s.db.Create(&c).Error; err != nil {
		return nil, err
	}
	s.db.Model(&database.TenantEcommerceOrder{}).Where("id = ?", order.ID).Update("contact_id", c.ID)
	id := c.ID
	return &id, nil
}

// EmitSaleInput datos para emitir la venta de un pedido al despacharlo.
type EmitSaleInput struct {
	UserID        uint
	CentralTenant uint
	TaxConfig     tax.Config
}

// EmitSaleForOrder convierte el pedido en venta con el comprobante por defecto del negocio (ajuste «Emitir
// la venta al despachar»): busca la serie de la sucursal del pedido, y usa los abonos validados como pagos.
func (s *EcommerceService) EmitSaleForOrder(orderID uint, in EmitSaleInput) (*database.TenantSale, error) {
	var order database.TenantEcommerceOrder
	if err := s.db.First(&order, orderID).Error; err != nil {
		return nil, errors.New("pedido no encontrado")
	}
	if order.ConvertedSaleID != nil {
		return nil, errors.New("el pedido ya tiene su venta")
	}
	if order.BranchID == nil {
		return nil, errors.New("el pedido no tiene sucursal asignada")
	}
	settings, err := s.GetSettings()
	if err != nil {
		return nil, err
	}
	target := strings.TrimSpace(settings.EmitSaleTarget)
	if target == "" {
		target = "nota_venta"
	}
	code := target
	if target == "nota_venta" {
		code = "00"
	}
	var series database.TenantDocumentSeries
	if err := s.db.Where("sunat_code = ? AND branch_id = ?", code, *order.BranchID).Order("id ASC").First(&series).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("no hay una serie de %s en la sucursal del pedido", map[string]string{"nota_venta": "nota de venta", "03": "boleta", "01": "factura"}[target])
		}
		return nil, err
	}
	return s.ConvertToSale(orderID, ConvertInput{
		Target: target, SeriesID: series.ID, BranchID: *order.BranchID, IssueDate: time.Now(),
		UserID: in.UserID, CentralTenant: in.CentralTenant, TaxConfig: in.TaxConfig,
	})
}
