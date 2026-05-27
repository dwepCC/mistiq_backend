package service

import (
	"fmt"
	"strings"

	"tukifac/pkg/database"
	"tukifac/pkg/money"
	"tukifac/pkg/numeroletras"

	"gorm.io/gorm"
)

// PrintData estructura para impresión inmediata del comprobante (web PDF o Tauri impresora POS).
type PrintData struct {
	// Comprobante
	DocType   string `json:"doc_type"`
	SunatCode string `json:"sunat_code"`
	Series    string `json:"series"`
	Number    string `json:"number"`
	IssueDate string `json:"issue_date"`
	IssueTime string `json:"issue_time,omitempty"` // HH:mm:ss
	Currency  string `json:"currency"`
	SunatHash string `json:"sunat_hash,omitempty"` // Hash firma XML (cuando ya enviado a SUNAT)
	QRData    string `json:"qr_data"`              // String para generar QR según SUNAT

	// Cliente
	Client *PrintClient `json:"client"`

	// Empresa (solo datos básicos para impresión; el tenant ya está en el sistema)
	Company PrintCompany `json:"company"`

	// Sucursal
	Branch PrintBranch `json:"branch"`

	// Detalle
	Items []PrintItem `json:"items"`

	// Totales
	Subtotal  float64 `json:"subtotal"`
	TaxAmount float64 `json:"tax_amount"`
	Total     float64 `json:"total"`

	// Leyenda en letras (mismo texto que se envía a Lycet/SUNAT en legends code 1000)
	LegendText string `json:"legend_text,omitempty"`

	// Totales por afectación SUNAT (para reportes)
	TotalsByAffectation map[string]PrintAffectTotal `json:"totals_by_affectation,omitempty"`

	// Pagos
	Payments []PrintPayment `json:"payments"`

	SellerName         string             `json:"seller_name,omitempty"`
	PaymentCondition   string             `json:"payment_condition,omitempty"` // Contado, Crédito
	BankAccounts       []PrintBankAccount `json:"bank_accounts,omitempty"`
}

type PrintClient struct {
	DocType      string `json:"doc_type"`
	DocNumber    string `json:"doc_number"`
	BusinessName string `json:"business_name"`
	Address      string `json:"address,omitempty"`
}

type PrintCompany struct {
	RUC          string `json:"ruc"`
	BusinessName string `json:"business_name"`
	TradeName    string `json:"trade_name,omitempty"`
	Address      string `json:"address,omitempty"`
	Phone        string `json:"phone,omitempty"`
	Email        string `json:"email,omitempty"`
	Website      string `json:"website,omitempty"`
	LogoURL      string `json:"logo_url,omitempty"`
}

type PrintBankAccount struct {
	Name          string `json:"name,omitempty"`
	BankName      string `json:"bank_name"`
	AccountNumber string `json:"account_number"`
	Currency      string `json:"currency"`
}

type PrintBranch struct {
	Name    string `json:"name"`
	Address string `json:"address,omitempty"`
}

type PrintItem struct {
	Code          string  `json:"code"`
	Description   string  `json:"description"`
	Unit          string  `json:"unit"`
	Quantity      float64 `json:"quantity"`
	UnitPrice     float64 `json:"unit_price"`
	Discount      float64 `json:"discount"`
	Subtotal      float64 `json:"subtotal"`
	TaxAmount     float64 `json:"tax_amount"`
	Total         float64 `json:"total"`
	ModifiersJSON string  `json:"modifiers_json,omitempty"`
}

type PrintAffectTotal struct {
	Code        string  `json:"code"`
	Description string  `json:"description"`
	Subtotal    float64 `json:"subtotal"`
	TaxAmount   float64 `json:"tax_amount"`
	Total       float64 `json:"total"`
}

type PrintPayment struct {
	Method    string  `json:"method"`
	Amount    float64 `json:"amount"`
	Reference string  `json:"reference,omitempty"`
}

// BuildPrintData construye la estructura print_data para una venta.
func BuildPrintData(db *gorm.DB, sale *database.TenantSale, items []database.TenantSaleItem, payments []PrintPaymentInput, sunatHash string) (*PrintData, error) {
	pd := &PrintData{
		DocType:   sale.DocType,
		Series:    sale.Series,
		Number:    sale.Number,
		IssueDate: sale.IssueDate.Format("02/01/2006"),
		IssueTime: sale.IssueDate.Format("15:04:05"),
		Currency:  sale.Currency,
		Subtotal:  sale.Subtotal,
		TaxAmount: sale.TaxAmount,
		Total:     sale.Total,
		SunatHash: sunatHash,
	}

	// Leyenda en letras construida igual que para Lycet (monto total e ISO moneda)
	currency := strings.TrimSpace(sale.Currency)
	if currency == "" {
		currency = "PEN"
	}
	pd.LegendText = numeroletras.MontoEnLetras(sale.Total, currency)
	pd.PaymentCondition = "Contado"

	// Serie → sunat_code
	var series database.TenantDocumentSeries
	if err := db.First(&series, sale.SeriesID).Error; err == nil {
		pd.SunatCode = series.SunatCode
	}

	// Cliente
	if sale.ContactID != nil && *sale.ContactID > 0 {
		var contact database.TenantContact
		if db.First(&contact, *sale.ContactID).Error == nil {
			addr, _ := database.NormalizeTenantContactAddressUbigeo(contact.Address, contact.Ubigeo)
			pd.Client = &PrintClient{
				DocType:      contact.DocType,
				DocNumber:    contact.DocNumber,
				BusinessName: contact.BusinessName,
				Address:      addr,
			}
		}
	}
	if pd.Client == nil {
		pd.Client = &PrintClient{DocType: "0", DocNumber: "99999999", BusinessName: "Cliente genérico"}
	}

	// Empresa
	var company database.TenantCompanyConfig
	if db.First(&company).Error == nil {
		pd.Company = PrintCompany{
			RUC:          company.RUC,
			BusinessName: company.BusinessName,
			TradeName:    company.TradeName,
			Address:      company.Address,
			Phone:        strings.TrimSpace(company.Phone),
			Email:        strings.TrimSpace(company.Email),
			Website:      strings.TrimSpace(company.Website),
			LogoURL:      company.LogoURL,
		}
	}

	var bankAccounts []database.TenantBankAccount
	if db.Where("active = ?", true).Order("id ASC").Find(&bankAccounts).Error == nil {
		for _, ba := range bankAccounts {
			if strings.TrimSpace(ba.AccountNumber) == "" && strings.TrimSpace(ba.BankName) == "" {
				continue
			}
			pd.BankAccounts = append(pd.BankAccounts, PrintBankAccount{
				Name:          ba.Name,
				BankName:      ba.BankName,
				AccountNumber: ba.AccountNumber,
				Currency:      ba.Currency,
			})
		}
	}

	if sale.UserID > 0 {
		var user database.TenantUser
		if db.Select("name").First(&user, sale.UserID).Error == nil {
			pd.SellerName = strings.TrimSpace(user.Name)
		}
	}

	// Sucursal: en comprobantes impresos/PDF la dirección es la de la sucursal de la venta.
	var branch database.TenantBranch
	if db.First(&branch, sale.BranchID).Error == nil {
		pd.Branch = PrintBranch{Name: branch.Name, Address: branch.Address}
		if addr := strings.TrimSpace(branch.Address); addr != "" {
			pd.Company.Address = addr
		}
	}

	// Items
	pd.Items = make([]PrintItem, len(items))
	for i, it := range items {
		pd.Items[i] = PrintItem{
			Code:          it.Code,
			Description:   it.Description,
			Unit:          it.Unit,
			Quantity:      it.Quantity,
			UnitPrice:     it.UnitPrice,
			Discount:      it.Discount,
			Subtotal:      it.Subtotal,
			TaxAmount:     it.TaxAmount,
			Total:         it.Total,
			ModifiersJSON: it.ModifiersJSON,
		}
	}

	// Totales por afectación
	affMap := make(map[string]*PrintAffectTotal)
	for _, it := range items {
		code := it.IgvAffectationType
		if code == "" {
			code = "10"
		}
		desc := affectDesc(code)
		if _, ok := affMap[code]; !ok {
			affMap[code] = &PrintAffectTotal{Code: code, Description: desc}
		}
		affMap[code].Subtotal = money.RoundSunat(affMap[code].Subtotal + it.Subtotal)
		affMap[code].TaxAmount = money.RoundSunat(affMap[code].TaxAmount + it.TaxAmount)
		affMap[code].Total = money.RoundSunat(affMap[code].Total + it.Total)
	}
	if len(affMap) > 0 {
		pd.TotalsByAffectation = make(map[string]PrintAffectTotal)
		for k, v := range affMap {
			row := *v
			row.Subtotal = money.RoundSunat(row.Subtotal)
			row.TaxAmount = money.RoundSunat(row.TaxAmount)
			row.Total = money.RoundSunat(row.Total)
			pd.TotalsByAffectation[k] = row
		}
	}

	// Pagos
	pd.Payments = make([]PrintPayment, len(payments))
	for i, p := range payments {
		pd.Payments[i] = PrintPayment{Method: p.Method, Amount: p.Amount, Reference: p.Reference}
	}

	pd.QRData = pd.buildQRData()
	return pd, nil
}

func affectDesc(code string) string {
	m := map[string]string{"10": "Gravado", "20": "Exonerado", "30": "Inafecto", "40": "Exportación"}
	if d, ok := m[code]; ok {
		return d
	}
	return code
}

// PrintPaymentInput entrada para pagos en print_data.
type PrintPaymentInput struct {
	Method    string
	Amount    float64
	Reference string
}

// BuildPrintDataForSale construye print_data desde una venta existente (carga items, payments, invoice).
func BuildPrintDataForSale(db *gorm.DB, saleID uint) (*PrintData, error) {
	saleSvc := NewSaleService(db)
	sale, err := saleSvc.GetByID(saleID)
	if err != nil {
		return nil, err
	}
	items, err := saleSvc.GetItems(sale.ID)
	if err != nil {
		return nil, err
	}

	var payments []PrintPaymentInput
	var dbPayments []database.TenantSalePayment
	if db.Where("sale_id = ?", sale.ID).Find(&dbPayments).Error == nil {
		for _, p := range dbPayments {
			payments = append(payments, PrintPaymentInput{Method: p.Method, Amount: p.Amount, Reference: p.Reference})
		}
	}
	if len(payments) == 0 && sale.PaymentMethod != "" && sale.Total > 0 {
		payments = []PrintPaymentInput{{Method: sale.PaymentMethod, Amount: sale.Total}}
	}

	sunatHash := ""
	var inv database.TenantInvoice
	if db.Where("sale_id = ?", sale.ID).First(&inv).Error == nil && inv.SunatHash != "" {
		sunatHash = inv.SunatHash
	}

	return BuildPrintData(db, sale, items, payments, sunatHash)
}

// buildQRData genera el string para el código QR según SUNAT (llamado internamente).
// Las notas de venta (SUNAT 00) no llevan QR; solo comprobantes electrónicos (p. ej. 01, 03, 07).
func (p *PrintData) buildQRData() string {
	if strings.TrimSpace(p.SunatCode) == "00" {
		return ""
	}
	clienteTipo := "0"
	clienteNumero := "99999999"
	if p.Client != nil {
		clienteTipo = p.Client.DocType
		clienteNumero = p.Client.DocNumber
	}
	ruc := p.Company.RUC
	if ruc == "" {
		ruc = "0"
	}
	numero := p.Number
	if idx := strings.LastIndex(p.Number, "-"); idx >= 0 && idx+1 < len(p.Number) {
		numero = p.Number[idx+1:]
	}
	hash := p.SunatHash
	if hash == "" {
		hash = "0"
	}
	return fmt.Sprintf("%s|%s|%s|%s|%.2f|%.2f|%s|%s|%s|%s",
		ruc, p.SunatCode, p.Series, numero,
		p.TaxAmount, p.Total, p.IssueDate, clienteTipo, clienteNumero, hash)
}
