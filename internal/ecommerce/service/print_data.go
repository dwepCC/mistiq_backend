package service

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"

	salessvc "tukifac/internal/sales/service"
	"tukifac/pkg/branchlogo"
	"tukifac/pkg/database"
	"tukifac/pkg/datespe"
	"tukifac/pkg/money"
	"tukifac/pkg/numeroletras"
	"tukifac/pkg/receiptdesign"
	"tukifac/pkg/tax"
	"tukifac/pkg/taxregime"

	"gorm.io/gorm"
)

func orderAffectDesc(code string) string {
	m := map[string]string{"10": "Gravado", "20": "Exonerado", "30": "Inafecto", "40": "Exportación"}
	if d, ok := m[code]; ok {
		return d
	}
	return code
}

// BuildPrintDataForOrder construye print_data (mismo formato que ventas/cotizaciones) para
// imprimir/descargar un pedido web como PDF. No es un comprobante fiscal (sin serie/SUNAT):
// solo un resumen del pedido armado por el cliente en la tienda pública.
//
// Muestra lo mismo que la cotización (BuildPrintDataForQuotation): logo de la sucursal embebido,
// cuentas bancarias y billetera Yape/Plin configuradas, desglose de IGV por afectación, código y
// unidad de cada producto. El diseño A4 (Clásico/Moderno) lo decide el tenant, igual que allí.
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
	hasCompany := db.First(&company).Error == nil
	if hasCompany {
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
			// Mismo criterio que la cotización/venta: sin esto el PDF omitía Subtotal e IGV.
			ShowIgvBreakdown:           taxregime.For(company.TaxpayerRegime).ShowIgvBreakdown,
			ShowIgvBreakdownOnSaleNote: company.ShowIgvBreakdownOnSaleNote,
			LogoSizeTicket:             salessvc.NormalizePrintLogoSize(company.LogoSizeTicket),
			LogoSizeA4:                 salessvc.NormalizePrintLogoSize(company.LogoSizeA4),
			PdfA4Design:                receiptdesign.NormalizeA4(company.PdfA4Design),
		}
		// Wallet Yape/Plin y cuentas bancarias, como en un comprobante de venta o una cotización.
		pd.PaymentWallet, pd.BankAccounts = salessvc.PopulateCompanyPaymentInfo(db, company)

		// El pedido web no tiene sucursal: se usa la predeterminada de la empresa (o la primera).
		var branch database.TenantBranch
		found := false
		if company.DefaultBranchID != nil && *company.DefaultBranchID > 0 {
			found = db.First(&branch, *company.DefaultBranchID).Error == nil
		}
		if !found {
			found = db.Order("id ASC").First(&branch).Error == nil
		}
		if found {
			pd.Branch = salessvc.PrintBranch{Name: branch.Name, Address: branch.Address}
			if addr := strings.TrimSpace(branch.Address); addr != "" {
				pd.Company.Address = addr
			}
			if ph := strings.TrimSpace(branch.Phone); ph != "" {
				pd.Company.Phone = ph
			}
			pd.Company.LogoURL = branchlogo.ResolveURL(branch.LogoURL, pd.Company.LogoURL)
			// Embebido como data: URL (mismo motivo que en cotizaciones): evita que el navegador
			// tenga que pedir /uploads/* en vivo al armar el PDF.
			branchDataURL := branchlogo.ResolveBranchDataURL(company.RUC, branch.ID, branch.LogoURL)
			companyDataURL := branchlogo.ResolveCompanyDataURL(company.RUC, company.LogoURL)
			if dataURL := branchlogo.ResolvePrintLogo(branch.LogoURL, branchDataURL, company.LogoURL, companyDataURL); dataURL != "" {
				pd.Company.LogoURL = dataURL
			}
		}
	}

	var items []OrderItemInput
	if err := json.Unmarshal([]byte(order.ItemsJSON), &items); err != nil {
		items = nil
	}

	// Datos del catálogo (código, unidad, afectación al IGV): los mismos que usa ConvertToSale.
	productsByID := map[uint]database.TenantProduct{}
	ids := make([]uint, 0, len(items))
	for _, it := range items {
		if it.ProductID > 0 {
			ids = append(ids, it.ProductID)
		}
	}
	if len(ids) > 0 {
		var rows []database.TenantProduct
		if db.Where("id IN ?", ids).Find(&rows).Error == nil {
			for _, p := range rows {
				productsByID[p.ID] = p
			}
		}
	}

	type itemMeta struct {
		code, unit, igvType string
		includesIgv         bool
	}
	metas := make([]itemMeta, len(items))
	lines := make([]tax.SaleLineInput, len(items))
	for i, it := range items {
		m := itemMeta{unit: "NIU", igvType: "10", includesIgv: true}
		if p, ok := productsByID[it.ProductID]; ok {
			m.code = p.Code
			if strings.TrimSpace(p.Unit) != "" {
				m.unit = p.Unit
			}
			if strings.TrimSpace(p.IgvAffectationType) != "" {
				m.igvType = p.IgvAffectationType
			}
			m.includesIgv = p.PriceIncludesIgv
		}
		metas[i] = m
		lines[i] = tax.SaleLineInput{
			UnitPrice: it.UnitPrice, Quantity: it.Quantity,
			IgvAffectationType: m.igvType, PriceIncludesIgv: m.includesIgv,
		}
	}

	res := tax.CalcSaleCheckout(tax.SaleCheckoutInput{Lines: lines, TaxCfg: tax.LoadFromDB(db)})
	// Solo se usa el desglose si cuadra con el total que el cliente vio en la tienda; si el catálogo
	// cambió desde entonces (precios sin IGV, etc.) se conserva el total del pedido sin desglose.
	useEngine := len(items) > 0 && len(res.Lines) == len(items) && math.Abs(res.Total-order.Total) <= 0.05

	pd.Items = make([]salessvc.PrintItem, len(items))
	affMap := make(map[string]*salessvc.PrintAffectTotal)
	for i, it := range items {
		var pid *uint
		if it.ProductID > 0 {
			id := it.ProductID
			pid = &id
		}
		pi := salessvc.PrintItem{
			Code:               metas[i].code,
			Description:        it.Name,
			Unit:               metas[i].unit,
			Quantity:           it.Quantity,
			UnitPrice:          it.UnitPrice,
			IgvAffectationType: metas[i].igvType,
			ModifiersJSON:      it.ModifiersJSON,
			ItemNote:           strings.TrimSpace(it.Detail),
			ProductID:          pid,
			SaleUnitID:         it.SaleUnitID,
		}
		if useEngine {
			l := res.Lines[i]
			pi.Subtotal, pi.TaxAmount, pi.Total = l.Subtotal, l.TaxAmount, l.Total
			if _, ok := affMap[metas[i].igvType]; !ok {
				affMap[metas[i].igvType] = &salessvc.PrintAffectTotal{Code: metas[i].igvType, Description: orderAffectDesc(metas[i].igvType)}
			}
			a := affMap[metas[i].igvType]
			a.Subtotal = money.RoundSunat(a.Subtotal + l.Subtotal)
			a.TaxAmount = money.RoundSunat(a.TaxAmount + l.TaxAmount)
			a.Total = money.RoundSunat(a.Total + l.Total)
		} else {
			sub := it.Quantity * it.UnitPrice
			pi.Subtotal, pi.Total = sub, sub
		}
		pd.Items[i] = pi
	}
	if useEngine {
		pd.Subtotal, pd.TaxAmount, pd.Total = res.Subtotal, res.TaxAmount, res.Total
		pd.TotalsByAffectation = make(map[string]salessvc.PrintAffectTotal, len(affMap))
		for k, v := range affMap {
			pd.TotalsByAffectation[k] = *v
		}
	}

	return pd, nil
}
