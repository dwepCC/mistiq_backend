package service

import (
	"encoding/json"
	"strconv"
	"strings"

	salessvc "tukifac/internal/sales/service"
	"tukifac/pkg/database"
	"tukifac/pkg/datespe"
	"tukifac/pkg/numeroletras"

	"gorm.io/gorm"
)

// BuildPrintDataForOrder construye print_data (mismo formato que ventas/cotizaciones) para
// imprimir/descargar un pedido web como PDF. No es un comprobante fiscal (sin serie/SUNAT):
// solo un resumen del pedido armado por el cliente en la tienda pública.
func BuildPrintDataForOrder(db *gorm.DB, orderID uint) (*salessvc.PrintData, error) {
	var order database.TenantEcommerceOrder
	if err := db.First(&order, orderID).Error; err != nil {
		return nil, err
	}

	pd := &salessvc.PrintData{
		DocType:   "Pedido web",
		SunatCode: "QT", // no fiscal: reusa el mismo código que cotizaciones para omitir el QR
		Series:    "WEB",
		Number:    "PW-" + strconv.FormatUint(uint64(order.ID), 10),
		IssueDate: order.CreatedAt.Format("02/01/2006"),
		IssueTime: datespe.IssueTime(order.CreatedAt),
		Currency:  "PEN",
		Subtotal:  order.Total,
		Total:     order.Total,
		Payments:  []salessvc.PrintPayment{},
	}
	pd.LegendText = numeroletras.MontoEnLetras(order.Total, "PEN")

	name := strings.TrimSpace(order.CustomerName)
	if name == "" {
		name = "Cliente de tienda virtual"
	}
	pd.Client = &salessvc.PrintClient{DocType: "0", DocNumber: "—", BusinessName: name}

	notes := strings.TrimSpace(order.Notes)
	if phone := strings.TrimSpace(order.CustomerPhone); phone != "" {
		if notes != "" {
			notes += "\n"
		}
		notes += "Teléfono: " + phone
	}
	pd.Notes = notes

	var company database.TenantCompanyConfig
	if db.First(&company).Error == nil {
		pd.Company = salessvc.PrintCompany{
			RUC:             company.RUC,
			BusinessName:    company.BusinessName,
			TradeName:       company.TradeName,
			Address:         company.Address,
			Phone:           strings.TrimSpace(company.Phone),
			Email:           strings.TrimSpace(company.Email),
			Website:         strings.TrimSpace(company.Website),
			LogoURL:         company.LogoURL,
			AdditionalNotes: strings.TrimSpace(company.AdditionalNotes),
		}
	}

	var items []OrderItemInput
	if err := json.Unmarshal([]byte(order.ItemsJSON), &items); err != nil {
		items = nil
	}
	pd.Items = make([]salessvc.PrintItem, len(items))
	for i, it := range items {
		subtotal := it.Quantity * it.UnitPrice
		pd.Items[i] = salessvc.PrintItem{
			Description: it.Name,
			Unit:        "UND",
			Quantity:    it.Quantity,
			UnitPrice:   it.UnitPrice,
			Subtotal:    subtotal,
			Total:       subtotal,
		}
	}

	return pd, nil
}
