package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	salessvc "tukifac/internal/sales/service"
	"tukifac/pkg/database"
	"tukifac/pkg/salecurrency"
	"tukifac/pkg/tax"

	"gorm.io/gorm"
)

// ConvertInput datos para convertir un pedido web en una venta real (nota de venta/boleta/factura).
type ConvertInput struct {
	Target        string // nota_venta | 01 | 03
	SeriesID      uint
	BranchID      uint
	IssueDate     time.Time
	ContactID     *uint
	UserID        uint
	CentralTenant uint
	TaxConfig     tax.Config
}

const sunatRucLength = 11

func validateContactForFacturaOrder(c *database.TenantContact) error {
	if c == nil {
		return errors.New("la factura electrónica (01) requiere un cliente con RUC de 11 dígitos")
	}
	if c.DocType != "6" {
		return errors.New("la factura solo puede emitirse a clientes con RUC (tipo de documento 6)")
	}
	docNum := strings.TrimSpace(c.DocNumber)
	if len(docNum) != sunatRucLength {
		return fmt.Errorf("el RUC del cliente debe tener exactamente %d dígitos", sunatRucLength)
	}
	for _, r := range docNum {
		if r < '0' || r > '9' {
			return errors.New("el RUC del cliente debe contener solo dígitos")
		}
	}
	return nil
}

func loadContactForConvertOrder(db *gorm.DB, contactID uint) (*database.TenantContact, error) {
	var c database.TenantContact
	if err := db.First(&c, contactID).Error; err != nil {
		return nil, errors.New("cliente no encontrado")
	}
	if !c.Active {
		return nil, errors.New("el cliente seleccionado no está activo")
	}
	ct := strings.ToLower(strings.TrimSpace(c.Type))
	if ct != "customer" && ct != "both" {
		return nil, errors.New("el contacto seleccionado no es un cliente válido")
	}
	return &c, nil
}

// ConvertToSale crea una venta real a partir de un pedido web. A diferencia de una cotización,
// el pedido no guarda datos SUNAT por línea (código, unidad, afectación): se recalculan aquí
// leyendo el producto vigente por ID. Si un producto fue eliminado o editado, se usan valores
// por defecto razonables (gravado 10%, precio incluye IGV) para no bloquear la conversión.
func (s *EcommerceService) ConvertToSale(orderID uint, input ConvertInput) (*database.TenantSale, error) {
	var order database.TenantEcommerceOrder
	if err := s.db.First(&order, orderID).Error; err != nil {
		return nil, errors.New("pedido no encontrado")
	}
	if order.ConvertedSaleID != nil {
		return nil, errors.New("este pedido ya fue convertido a una venta")
	}

	target := strings.TrimSpace(strings.ToLower(input.Target))
	if target == "" {
		return nil, errors.New("target es obligatorio (nota_venta, 01 o 03)")
	}
	if input.BranchID == 0 {
		return nil, errors.New("sucursal es obligatoria")
	}

	var targetSeries database.TenantDocumentSeries
	if err := s.db.First(&targetSeries, input.SeriesID).Error; err != nil {
		return nil, errors.New("serie destino no encontrada")
	}
	sunatCode := strings.TrimSpace(targetSeries.SunatCode)
	switch target {
	case "nota_venta":
		if sunatCode != "00" {
			return nil, errors.New("la serie destino debe ser nota de venta (SUNAT 00)")
		}
	case "01", "03":
		if sunatCode != target {
			return nil, errors.New("la serie destino no coincide con el tipo de comprobante solicitado")
		}
		var companyCfg database.TenantCompanyConfig
		if err := s.db.Select("sunat_enabled").First(&companyCfg).Error; err != nil || !companyCfg.SunatEnabled {
			return nil, errors.New("la facturación electrónica no está habilitada")
		}
	default:
		return nil, errors.New("target inválido: use nota_venta, 01 o 03")
	}

	var items []OrderItemInput
	if err := json.Unmarshal([]byte(order.ItemsJSON), &items); err != nil || len(items) == 0 {
		return nil, errors.New("el pedido no tiene productos válidos")
	}

	productIDs := make([]uint, 0, len(items))
	for _, it := range items {
		if it.ProductID > 0 {
			productIDs = append(productIDs, it.ProductID)
		}
	}
	productsByID := map[uint]database.TenantProduct{}
	if len(productIDs) > 0 {
		var rows []database.TenantProduct
		if err := s.db.Where("id IN ?", productIDs).Find(&rows).Error; err == nil {
			for _, p := range rows {
				productsByID[p.ID] = p
			}
		}
	}

	saleItems := make([]salessvc.SaleItemInput, 0, len(items))
	for _, it := range items {
		code := ""
		unit := "NIU"
		igvType := "10"
		priceIncludesIgv := true
		var productID *uint
		if it.ProductID > 0 {
			pid := it.ProductID
			productID = &pid
			if p, ok := productsByID[it.ProductID]; ok {
				code = p.Code
				unit = p.Unit
				igvType = p.IgvAffectationType
				priceIncludesIgv = p.PriceIncludesIgv
			}
		}
		saleItems = append(saleItems, salessvc.SaleItemInput{
			ProductID:          productID,
			Code:               code,
			Description:        it.Name,
			Unit:               unit,
			Quantity:           it.Quantity,
			UnitPrice:          it.UnitPrice,
			IgvAffectationType: igvType,
			PriceIncludesIgv:   priceIncludesIgv,
		})
	}

	contactID := input.ContactID
	if target == "01" {
		var c *database.TenantContact
		if contactID != nil && *contactID > 0 {
			loaded, err := loadContactForConvertOrder(s.db, *contactID)
			if err != nil {
				return nil, err
			}
			c = loaded
		}
		if err := validateContactForFacturaOrder(c); err != nil {
			return nil, err
		}
	} else if contactID != nil && *contactID > 0 {
		if _, err := loadContactForConvertOrder(s.db, *contactID); err != nil {
			return nil, err
		}
	}

	notesParts := []string{fmt.Sprintf("Pedido web #%d.", order.ID)}
	if name := strings.TrimSpace(order.CustomerName); name != "" {
		notesParts = append(notesParts, "Cliente: "+name+".")
	}
	if phone := strings.TrimSpace(order.CustomerPhone); phone != "" {
		notesParts = append(notesParts, "Tel: "+phone+".")
	}
	if notes := strings.TrimSpace(order.Notes); notes != "" {
		notesParts = append(notesParts, notes)
	}

	payments := []salessvc.PaymentInput{}
	if order.Total > 0 {
		payments = []salessvc.PaymentInput{{Method: "cash", Amount: order.Total}}
	}

	taxCfg := input.TaxConfig
	if taxCfg.TaxRate == 0 {
		taxCfg = tax.LoadFromDB(s.db)
	}

	saleSvc := salessvc.NewSaleService(s.db)
	sale, err := saleSvc.Create(salessvc.CreateSaleInput{
		BranchID:          input.BranchID,
		ContactID:         contactID,
		UserID:            input.UserID,
		SeriesID:          input.SeriesID,
		DocType:           strings.TrimSpace(targetSeries.DocType),
		IssueDate:         input.IssueDate,
		Currency:          "PEN",
		OperationTypeCode: salecurrency.OpVentaInterna,
		Payments:          payments,
		Notes:             strings.Join(notesParts, " "),
		Items:             saleItems,
		TaxConfig:         taxCfg,
		CentralTenantID:   input.CentralTenant,
	})
	if err != nil {
		return nil, err
	}

	now := time.Now()
	if err := s.db.Model(&order).Updates(map[string]interface{}{
		"converted_sale_id": sale.ID,
		"converted_at":      now,
		"status":            "cerrado",
	}).Error; err != nil {
		return sale, err
	}

	return sale, nil
}
