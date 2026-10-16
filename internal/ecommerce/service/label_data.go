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

	// Envío por agencia: quién recoge, adónde va y qué lleva el paquete.
	RecipientName      string `json:"recipient_name"`
	RecipientDocType   string `json:"recipient_doc_type"`
	RecipientDocNumber string `json:"recipient_doc_number"`
	RecipientPhone     string `json:"recipient_phone"`
	Department         string `json:"department"`
	Province           string `json:"province"`
	District           string `json:"district"`
	AgencyName         string `json:"agency_name"`
	AgencyAddress      string `json:"agency_address"`
	// Items productos del paquete (la etiqueta decide cuántos caben según su formato).
	Items     []EcommerceLabelItem `json:"items"`
	ItemCount int                  `json:"item_count"`
}

// EcommerceLabelItem una línea del contenido del paquete.
type EcommerceLabelItem struct {
	Name     string  `json:"name"`
	Quantity float64 `json:"quantity"`
}

// labelStatuses el rótulo se imprime cuando el pedido está listo para salir (el paquete ya está
// armado) y se puede reimprimir mientras viaja o después de entregado.
var labelStatuses = map[string]bool{
	OrderStatusListoParaDespacho: true, OrderStatusDespachado: true, OrderStatusEntregado: true,
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

	if !labelStatuses[order.Status] {
		return nil, fmt.Errorf("el rótulo se imprime cuando el pedido está listo para despachar")
	}

	svc := &EcommerceService{db: db}
	dispatch, err := svc.GetDispatchByOrderID(orderID)
	if err != nil {
		return nil, err
	}

	label := &EcommerceLabelData{
		OrderID:            order.ID,
		OrderNumber:        "PW-" + strconv.FormatUint(uint64(order.ID), 10),
		CustomerName:       order.CustomerName,
		CustomerPhone:      order.CustomerPhone,
		DeliveryMethod:     order.DeliveryMethod,
		PackageCount:       order.PackageCount,
		WeightKg:           order.WeightKg,
		RecipientName:      order.RecipientName,
		RecipientDocType:   order.RecipientDocType,
		RecipientDocNumber: order.RecipientDocNumber,
		RecipientPhone:     order.RecipientPhone,
		AgencyName:         order.DestinationAgency,
		AgencyAddress:      order.DestinationAddress,
	}
	if order.CarrierID != nil {
		var carrier database.TenantCarrier
		if db.Unscoped().First(&carrier, *order.CarrierID).Error == nil {
			label.CarrierName = carrier.Name
		}
	}
	if dispatch != nil {
		if dispatch.PackageCount != nil {
			label.PackageCount = dispatch.PackageCount
		}
		if dispatch.WeightKg != nil {
			label.WeightKg = dispatch.WeightKg
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
	}
	label.Department, label.Province, label.District = svc.UbigeoNames(order.DestinationUbigeo)

	var lines []database.TenantEcommerceOrderItem
	if err := db.Where("order_id = ?", order.ID).Order("id ASC").Find(&lines).Error; err == nil {
		for _, l := range lines {
			label.Items = append(label.Items, EcommerceLabelItem{Name: l.Name, Quantity: l.Quantity})
		}
		label.ItemCount = len(lines)
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

// LabelResult rótulo de un pedido dentro de una impresión masiva; los que no se pueden imprimir
// vienen con su motivo en lugar de tumbar toda la impresión.
type LabelResult struct {
	OrderID uint                `json:"order_id"`
	Label   *EcommerceLabelData `json:"label,omitempty"`
	Error   string              `json:"error,omitempty"`
}

const maxBulkLabels = 200

// BuildLabelsForOrders datos de rótulo de varios pedidos, en el mismo orden pedido.
func BuildLabelsForOrders(db *gorm.DB, orderIDs []uint) ([]LabelResult, error) {
	if len(orderIDs) == 0 {
		return nil, fmt.Errorf("elige al menos un pedido")
	}
	if len(orderIDs) > maxBulkLabels {
		return nil, fmt.Errorf("máximo %d rótulos por impresión", maxBulkLabels)
	}
	out := make([]LabelResult, 0, len(orderIDs))
	for _, id := range orderIDs {
		res := LabelResult{OrderID: id}
		label, err := BuildLabelDataForOrder(db, id)
		if err != nil {
			res.Error = err.Error()
		} else {
			res.Label = label
		}
		out = append(out, res)
	}
	return out, nil
}
