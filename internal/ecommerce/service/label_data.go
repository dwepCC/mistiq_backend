package service

import (
	"fmt"
	"strconv"
	"strings"

	"tukifac/pkg/database"
	"tukifac/pkg/datespe"

	"gorm.io/gorm"
)

// EcommerceLabelData etiqueta LOGÍSTICA interna de un pedido despachado (Fase 10, Contrato v2 §4).
// Deliberadamente SEPARADA de salessvc.PrintData/PrintDespatch — una etiqueta logística no es un
// documento tributario/GRE: sin motivo de traslado, modalidad, QR SUNAT, ni ningún campo fiscal.
// Solo información mínima de identificación que existe REALMENTE en Order+Dispatch, nunca
// inventada (§5: "NO imprimir información que no exista").
type EcommerceLabelData struct {
	OrderID        uint     `json:"order_id"`
	OrderNumber    string   `json:"order_number"`
	CompanyName    string   `json:"company_name"`
	BranchName     string   `json:"branch_name"`
	CustomerName   string   `json:"customer_name"`
	CustomerPhone  string   `json:"customer_phone"`
	DeliveryMethod string   `json:"delivery_method"`
	AddressLine    string   `json:"address_line"`
	Reference      string   `json:"reference"`
	CarrierName    string   `json:"carrier_name"`
	TrackingCode   string   `json:"tracking_code"`
	PackageCount   *int     `json:"package_count"`
	WeightKg       *float64 `json:"weight_kg"`
	DispatchedAt   string   `json:"dispatched_at"`
}

// BuildLabelDataForOrder Fase 10: mismo idioma que BuildPrintDataForOrder (GET devuelve JSON, el
// frontend genera el PDF con jsPDF) pero con un payload propio, mucho más chico — una etiqueta no
// lleva subtotales/impuestos/pagos. Exige que el pedido YA tenga un TenantEcommerceDispatch — sin
// despacho no hay transportista/tracking/bultos que mostrar, nunca se fabrican esos datos.
func BuildLabelDataForOrder(db *gorm.DB, orderID uint) (*EcommerceLabelData, error) {
	var order database.TenantEcommerceOrder
	if err := db.First(&order, orderID).Error; err != nil {
		return nil, fmt.Errorf("pedido no encontrado")
	}

	svc := &EcommerceService{db: db}
	dispatch, err := svc.GetDispatchByOrderID(orderID)
	if err != nil {
		return nil, err
	}
	if dispatch == nil {
		return nil, fmt.Errorf("el pedido todavía no tiene un despacho registrado")
	}

	label := &EcommerceLabelData{
		OrderID:        order.ID,
		OrderNumber:    "PW-" + strconv.FormatUint(uint64(order.ID), 10),
		CustomerName:   order.CustomerName,
		CustomerPhone:  order.CustomerPhone,
		DeliveryMethod: order.DeliveryMethod,
		PackageCount:   dispatch.PackageCount,
		WeightKg:       dispatch.WeightKg,
	}
	if dispatch.CarrierName != nil {
		label.CarrierName = *dispatch.CarrierName
	}
	if dispatch.TrackingCode != nil {
		label.TrackingCode = *dispatch.TrackingCode
	}
	if dispatch.DispatchedAt != nil {
		label.DispatchedAt = dispatch.DispatchedAt.Format("02/01/2006") + " " + datespe.IssueTime(*dispatch.DispatchedAt)
	}

	// Dirección: misma resolución que ya usa el pedido — snapshot de invitado o la dirección real
	// del cliente autenticado (el panel hoy solo mostraba "dirección guardada del cliente" como
	// placeholder sin resolverla; una etiqueta física SÍ necesita el texto real).
	if order.DeliveryMethod == DeliveryMethodShipping {
		if order.DeliveryAddressID != nil {
			var addr database.TenantEcommerceCustomerAddress
			if err := db.First(&addr, *order.DeliveryAddressID).Error; err == nil {
				label.AddressLine = addr.AddressLine
				label.Reference = addr.Reference
			}
		} else {
			label.AddressLine = order.GuestAddressLine
			label.Reference = order.GuestReference
		}
	}

	if order.BranchID != nil {
		var branch database.TenantBranch
		if db.First(&branch, *order.BranchID).Error == nil {
			label.BranchName = branch.Name
		}
	}

	var company database.TenantCompanyConfig
	if db.First(&company).Error == nil {
		name := strings.TrimSpace(company.TradeName)
		if name == "" {
			name = strings.TrimSpace(company.BusinessName)
		}
		label.CompanyName = name
	}

	return label, nil
}
