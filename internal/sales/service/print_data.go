package service

import (
	"encoding/json"
	"fmt"
	"strings"

	detraccionsvc "tukifac/internal/detraccion"
	"tukifac/internal/fiscal/salecontext"
	prepaymentsvc "tukifac/internal/prepayment"
	"tukifac/pkg/database"
	"tukifac/pkg/datespe"
	"tukifac/pkg/money"
	"tukifac/pkg/numeroletras"
	"tukifac/pkg/paymentcondition"
	detraccionpkg "tukifac/pkg/sunat/detraccion"
	sunatpre "tukifac/pkg/sunat/prepayment"
	"tukifac/pkg/sunatnote"
	"tukifac/pkg/tax"
	"tukifac/pkg/taxpayment"
	"tukifac/pkg/taxregime"

	"gorm.io/gorm"
)

// PrintData estructura para impresión inmediata del comprobante (web PDF o Tauri impresora POS).
type PrintData struct {
	// Comprobante
	DocType           string   `json:"doc_type"`
	SunatCode         string   `json:"sunat_code"`
	Series            string   `json:"series"`
	Number            string   `json:"number"`
	IssueDate         string   `json:"issue_date"`
	IssueTime         string   `json:"issue_time,omitempty"` // HH:mm:ss
	Currency          string   `json:"currency"`
	ExchangeRate      *float64 `json:"exchange_rate,omitempty"`
	OperationTypeCode string   `json:"operation_type_code,omitempty"`
	SunatHash         string   `json:"sunat_hash,omitempty"` // Hash firma XML (cuando ya enviado a SUNAT)
	QRData            string   `json:"qr_data"`              // String para generar QR según SUNAT

	// Cliente
	Client *PrintClient `json:"client"`

	// Empresa (solo datos básicos para impresión; el tenant ya está en el sistema)
	Company PrintCompany `json:"company"`

	// Sucursal
	Branch PrintBranch `json:"branch"`

	// Detalle
	Items []PrintItem `json:"items"`

	// Totales
	Subtotal             float64 `json:"subtotal"`
	TaxAmount            float64 `json:"tax_amount"`
	Total                float64 `json:"total"`
	GlobalDiscountAmount float64 `json:"global_discount_amount,omitempty"`
	LineDiscountTotal    float64 `json:"line_discount_total,omitempty"`

	// Leyenda en letras (mismo texto que se envía a Lycet/SUNAT en legends code 1000)
	LegendText string `json:"legend_text,omitempty"`

	// Totales por afectación SUNAT (para reportes)
	TotalsByAffectation map[string]PrintAffectTotal `json:"totals_by_affectation,omitempty"`

	// Pagos
	Payments []PrintPayment `json:"payments"`

	// Nota de crédito/débito (07/08): documento afectado según SUNAT (misma info que Lycet).
	AffectedDocSunatCode string `json:"affected_doc_sunat_code,omitempty"` // 01 factura, 03 boleta
	AffectedDocNumber    string `json:"affected_doc_number,omitempty"`     // ej. B001-4
	CreditNoteReason     string `json:"credit_note_reason,omitempty"`      // desMotivo
	// Tipo de nota (catálogo SUNAT 09/10): código y etiqueta «01 - Anulación de la operación».
	// SUNAT lo exige en la representación impresa, aparte del documento que modifica.
	NoteTypeCode  string `json:"note_type_code,omitempty"`
	NoteTypeLabel string `json:"note_type_label,omitempty"`

	// Vuelto cuando el cliente pagó de más (p. ej. efectivo).
	ChangeAmount float64 `json:"change_amount,omitempty"`

	SellerName         string                   `json:"seller_name,omitempty"`
	PaymentCondition   string                   `json:"payment_condition,omitempty"` // Contado, Crédito
	CreditInstallments []PrintCreditInstallment `json:"credit_installments,omitempty"`
	BankAccounts       []PrintBankAccount       `json:"bank_accounts,omitempty"`
	PaymentWallet      *PrintPaymentWallet      `json:"payment_wallet,omitempty"`

	// Información adicional fiscal (retención operativa, O/C, guías — no altera total SUNAT del XML).
	Fiscal *PrintFiscalContext `json:"fiscal,omitempty"`

	// Cotización (pre venta): vigencia y observaciones comerciales.
	ValidUntil string `json:"valid_until,omitempty"`
	Notes      string `json:"notes,omitempty"`
}

// PrintFiscalContext datos adicionales para impresión/PDF.
type PrintFiscalContext struct {
	PurchaseOrderNumber         string                     `json:"purchase_order_number,omitempty"`
	FiscalObservations          string                     `json:"fiscal_observations,omitempty"`
	Guias                       []PrintGuiaRef             `json:"guias,omitempty"`
	HasIgvRetention             bool                       `json:"has_igv_retention,omitempty"`
	IgvRetentionAmount          float64                    `json:"igv_retention_amount,omitempty"`
	NetCollectible              float64                    `json:"net_collectible,omitempty"`
	RetentionApplied            bool                       `json:"retention_applied,omitempty"`
	HasDetraccion               bool                       `json:"has_detraccion,omitempty"`
	DetraccionGoodCode          string                     `json:"detraccion_good_code,omitempty"`
	DetraccionGoodLabel         string                     `json:"detraccion_good_label,omitempty"`
	DetraccionRatePercent       float64                    `json:"detraccion_rate_percent,omitempty"`
	DetraccionAmount            float64                    `json:"detraccion_amount,omitempty"`
	DetraccionBankAccount       string                     `json:"detraccion_bank_account,omitempty"`
	DetraccionPaymentMethodCode string                     `json:"detraccion_payment_method_code,omitempty"`
	DetraccionNetPayable        float64                    `json:"detraccion_net_payable,omitempty"`
	HasPrepaymentEmit           bool                       `json:"has_prepayment_emit,omitempty"`
	PrepaymentLabel             string                     `json:"prepayment_label,omitempty"`
	PrepaymentAffectationGroup  string                     `json:"prepayment_affectation_group,omitempty"`
	PrepaymentRelatedDocType    string                     `json:"prepayment_related_doc_type,omitempty"`
	HasPrepaymentDeduction      bool                       `json:"has_prepayment_deduction,omitempty"`
	PrepaymentDeductionTotal    float64                    `json:"prepayment_deduction_total,omitempty"`
	PrepaymentDeductions        []PrintPrepaymentDeduction `json:"prepayment_deductions,omitempty"`
	ShowTermsConditions         bool                       `json:"show_terms_conditions,omitempty"`
	TermsText                   string                     `json:"terms_text,omitempty"`
}

type PrintGuiaRef struct {
	Kind   string `json:"kind,omitempty"`
	Number string `json:"number"`
}

// PrintPrepaymentDeduction anticipo deducido en venta final (legacy document.prepayments).
type PrintPrepaymentDeduction struct {
	DocumentNumber string  `json:"document_number"`
	RelatedDocType string  `json:"related_doc_type"`
	Amount         float64 `json:"amount"`
	Total          float64 `json:"total"`
}

type PrintPaymentWallet struct {
	Provider     string `json:"provider"` // yape | plin
	Phone        string `json:"phone"`
	QrURL        string `json:"qr_url"`
	ShowOnA4     bool   `json:"show_on_a4"`
	ShowOnTicket bool   `json:"show_on_ticket"`
}

type PrintClient struct {
	DocType      string `json:"doc_type"`
	DocNumber    string `json:"doc_number"`
	BusinessName string `json:"business_name"`
	Address      string `json:"address,omitempty"`
	Email        string `json:"email,omitempty"`
}

type PrintCompany struct {
	RUC             string `json:"ruc"`
	BusinessName    string `json:"business_name"`
	TradeName       string `json:"trade_name,omitempty"`
	Address         string `json:"address,omitempty"`
	Phone           string `json:"phone,omitempty"`
	Email           string `json:"email,omitempty"`
	Website         string `json:"website,omitempty"`
	LogoURL         string `json:"logo_url,omitempty"`
	AdditionalNotes string `json:"additional_notes,omitempty"`
	// Discriminar IGV/valor de venta en el impreso. Nuevo RUS = false (solo total).
	ShowIgvBreakdown bool `json:"show_igv_breakdown"`
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
	Code                   string  `json:"code"`
	Description            string  `json:"description"`
	Unit                   string  `json:"unit"`
	Quantity               float64 `json:"quantity"`
	UnitPrice              float64 `json:"unit_price"`
	Discount               float64 `json:"discount"`
	LineDiscountSubtotal   float64 `json:"line_discount_subtotal,omitempty"`
	GlobalDiscountSubtotal float64 `json:"global_discount_subtotal,omitempty"`
	Subtotal               float64 `json:"subtotal"`
	TaxAmount              float64 `json:"tax_amount"`
	Total                  float64 `json:"total"`
	IgvAffectationType     string  `json:"igv_affectation_type,omitempty"`
	ModifiersJSON          string  `json:"modifiers_json,omitempty"`
	ItemNote               string  `json:"item_note,omitempty"`
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

type PrintCreditInstallment struct {
	InstallmentNo int     `json:"installment_no"`
	DueDate       string  `json:"due_date"`
	Amount        float64 `json:"amount"`
	Currency      string  `json:"currency,omitempty"`
	Status        string  `json:"status,omitempty"`
}

// BuildPrintData construye la estructura print_data para una venta.
func BuildPrintData(db *gorm.DB, sale *database.TenantSale, items []database.TenantSaleItem, payments []PrintPaymentInput, sunatHash string) (*PrintData, error) {
	pd := &PrintData{
		DocType:              sale.DocType,
		Series:               sale.Series,
		Number:               sale.Number,
		IssueDate:            sale.IssueDate.Format("02/01/2006"),
		IssueTime:            datespe.IssueTime(sale.CreatedAt),
		Currency:             sale.Currency,
		ExchangeRate:         sale.ExchangeRate,
		OperationTypeCode:    sale.OperationTypeCode,
		Subtotal:             sale.Subtotal,
		TaxAmount:            sale.TaxAmount,
		Total:                sale.Total,
		GlobalDiscountAmount: sale.GlobalDiscountAmount,
		SunatHash:            sunatHash,
	}

	// Leyenda en letras construida igual que para Lycet (monto total e ISO moneda)
	currency := strings.TrimSpace(sale.Currency)
	if currency == "" {
		currency = "PEN"
	}
	pd.LegendText = numeroletras.MontoEnLetras(sale.Total, currency)
	pd.PaymentCondition = paymentcondition.NameCash
	if paymentcondition.IsCreditCode(sale.PaymentConditionCode) || sale.Status == "credit" {
		pd.PaymentCondition = paymentcondition.NameCredit
	} else if paymentcondition.IsCashCode(sale.PaymentConditionCode) {
		pd.PaymentCondition = paymentcondition.NameCash
	}

	var creditRows []database.TenantSaleCreditInstallment
	if db.Where("sale_id = ?", sale.ID).Order("installment_no ASC").Find(&creditRows).Error == nil && len(creditRows) > 0 {
		pd.CreditInstallments = make([]PrintCreditInstallment, len(creditRows))
		for i, row := range creditRows {
			pd.CreditInstallments[i] = PrintCreditInstallment{
				InstallmentNo: row.InstallmentNo,
				DueDate:       row.DueDate.Format("02/01/2006"),
				Amount:        row.Amount,
				Currency:      row.Currency,
				Status:        row.Status,
			}
		}
	}

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
				Email:        strings.TrimSpace(contact.Email),
			}
		}
	}
	if pd.Client == nil {
		pd.Client = &PrintClient{DocType: "0", DocNumber: "99999999", BusinessName: "Cliente genérico"}
	}

	// Empresa
	var company database.TenantCompanyConfig
	companyOK := db.First(&company).Error == nil
	var receiptBankIDs []uint
	var receiptBanksConfigured bool
	if companyOK {
		receiptBankIDs, receiptBanksConfigured = decodeReceiptBankAccountIDs(company.ReceiptBankAccountIDs)
		pd.Company = PrintCompany{
			RUC:             company.RUC,
			BusinessName:    company.BusinessName,
			TradeName:       company.TradeName,
			Address:         company.Address,
			Phone:           strings.TrimSpace(company.Phone),
			Email:           strings.TrimSpace(company.Email),
			Website:         strings.TrimSpace(company.Website),
			LogoURL:         company.LogoURL,
			AdditionalNotes: strings.TrimSpace(company.AdditionalNotes),
			// Nuevo RUS: la boleta no discrimina IGV en el impreso (Reglamento CP Art. 8).
			ShowIgvBreakdown: taxregime.For(company.TaxpayerRegime).ShowIgvBreakdown,
		}
		provider := strings.TrimSpace(strings.ToLower(company.WalletProvider))
		phone := strings.TrimSpace(company.WalletPhone)
		qrURL := strings.TrimSpace(company.WalletQrURL)
		if provider != "" && phone != "" && qrURL != "" {
			pd.PaymentWallet = &PrintPaymentWallet{
				Provider:     provider,
				Phone:        phone,
				QrURL:        qrURL,
				ShowOnA4:     company.WalletShowOnA4,
				ShowOnTicket: company.WalletShowOnTicket,
			}
		}
	}

	var bankAccounts []database.TenantBankAccount
	if db.Where("active = ?", true).Order("id ASC").Find(&bankAccounts).Error == nil {
		for _, ba := range bankAccounts {
			// Cuentas tipo caja no van en el comprobante.
			if strings.EqualFold(strings.TrimSpace(ba.Type), "cash") {
				continue
			}
			name := strings.TrimSpace(ba.Name)
			bankName := strings.TrimSpace(ba.BankName)
			acct := strings.TrimSpace(ba.AccountNumber)
			// Muchas cuentas seed solo tienen Name (sin bank_name ni número); igual deben imprimirse.
			if name == "" && bankName == "" && acct == "" {
				continue
			}
			if receiptBanksConfigured && !receiptBankAccountAllowed(ba.ID, receiptBankIDs) {
				continue
			}
			if bankName == "" {
				bankName = name
			}
			pd.BankAccounts = append(pd.BankAccounts, PrintBankAccount{
				Name:          name,
				BankName:      bankName,
				AccountNumber: acct,
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

	enrichFiscalPrintData(db, sale.ID, sale.Total, pd)

	// Términos: preferencia global de empresa (toggle en registro). Asegura impresión aunque
	// el perfil fiscal de la venta no se haya persistido (p. ej. binding JSON incompleto).
	if companyOK && company.ShowTermsConditions {
		if pd.Fiscal == nil {
			pd.Fiscal = &PrintFiscalContext{}
		}
		pd.Fiscal.ShowTermsConditions = true
		if strings.TrimSpace(pd.Fiscal.TermsText) == "" {
			pd.Fiscal.TermsText = strings.TrimSpace(company.TermsAndConditions)
		}
	}

	// Sucursal (nombre/dirección propios). La dirección del emisor en el PDF es la de la empresa
	// (editable en registro de ventas); no se pisa con la de la sucursal.
	var branch database.TenantBranch
	if db.First(&branch, sale.BranchID).Error == nil {
		pd.Branch = PrintBranch{Name: branch.Name, Address: branch.Address}
		if strings.TrimSpace(pd.Company.Address) == "" {
			if addr := strings.TrimSpace(branch.Address); addr != "" {
				pd.Company.Address = addr
			}
		}
	}

	// Items
	var lineDiscSum float64
	pd.Items = make([]PrintItem, len(items))
	for i, it := range items {
		lineDiscSum = money.RoundSunat(lineDiscSum + it.LineDiscountSubtotal)
		pd.Items[i] = PrintItem{
			Code:                   it.Code,
			Description:            it.Description,
			Unit:                   it.Unit,
			Quantity:               it.Quantity,
			UnitPrice:              it.UnitPrice,
			Discount:               it.Discount,
			LineDiscountSubtotal:   it.LineDiscountSubtotal,
			GlobalDiscountSubtotal: it.GlobalDiscountSubtotal,
			Subtotal:               it.Subtotal,
			TaxAmount:              it.TaxAmount,
			Total:                  it.Total,
			IgvAffectationType:     it.IgvAffectationType,
			ModifiersJSON:          it.ModifiersJSON,
			ItemNote:               it.ItemNote,
		}
	}
	pd.LineDiscountTotal = money.RoundSunat(lineDiscSum)

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
	var directPaidSum float64
	for i, p := range payments {
		pd.Payments[i] = PrintPayment{Method: p.Method, Amount: p.Amount, Reference: p.Reference}
		if taxpayment.IsDetractionCode(p.Method) || paymentcondition.IsCreditCode(p.Method) {
			continue
		}
		directPaidSum += p.Amount
	}
	payable := sale.Total
	if pd.Fiscal != nil {
		if pd.Fiscal.HasDetraccion && pd.Fiscal.DetraccionNetPayable > 0 {
			payable = pd.Fiscal.DetraccionNetPayable
		} else if pd.Fiscal.RetentionApplied && pd.Fiscal.NetCollectible > 0 {
			payable = pd.Fiscal.NetCollectible
		}
	}
	if directPaidSum > payable+0.001 {
		pd.ChangeAmount = money.CalcPaymentChange(directPaidSum, payable)
	}

	pd.QRData = pd.buildQRData()
	enrichCreditNotePrintData(db, sale, pd)
	return pd, nil
}

func affectDesc(code string) string {
	if label := tax.SunatIgvTypeLabel(code); label != "" {
		return label
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
	code := strings.TrimSpace(p.SunatCode)
	if code == "00" || code == "QT" {
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

func decodeReceiptBankAccountIDs(raw string) (ids []uint, configured bool) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return nil, false
	}
	var out []uint
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return nil, false
	}
	return out, true
}

func receiptBankAccountAllowed(id uint, selected []uint) bool {
	for _, sid := range selected {
		if sid == id {
			return true
		}
	}
	return false
}

func isCreditOrDebitNotePrint(sunatCode, docType string) bool {
	sc := strings.TrimSpace(sunatCode)
	if sc == "07" || sc == "08" {
		return true
	}
	dt := strings.ToUpper(strings.TrimSpace(docType))
	return dt == "NOTA_CREDITO" || dt == "NOTA_DEBITO"
}

func printAffectedDocSunatType(orig *database.TenantSale, seriesSunatCode string) string {
	sc := strings.TrimSpace(seriesSunatCode)
	switch sc {
	case "01":
		return "01"
	case "03":
		return "03"
	}
	dt := strings.ToUpper(strings.TrimSpace(orig.DocType))
	if dt == "FACTURA" || strings.Contains(dt, "FACTURA") {
		return "01"
	}
	return "03"
}

func printAffectedDocNumber(orig *database.TenantSale) string {
	nro := strings.TrimSpace(orig.Number)
	if nro == "" {
		return fmt.Sprintf("%s-%d", strings.TrimSpace(orig.Series), orig.Correlative)
	}
	if i := strings.LastIndex(nro, "-"); i > 0 {
		suf := strings.TrimLeft(nro[i+1:], "0")
		if suf == "" {
			suf = "0"
		}
		return nro[:i+1] + suf
	}
	return nro
}

func enrichFiscalPrintData(db *gorm.DB, saleID uint, saleTotal float64, pd *PrintData) {
	if pd == nil {
		return
	}
	var fc *PrintFiscalContext
	enrich, err := salecontext.LoadInvoiceEnrichment(db, saleID, saleTotal)
	if err == nil && enrich != nil {
		fc = &PrintFiscalContext{
			PurchaseOrderNumber: enrich.PurchaseOrderNumber,
			FiscalObservations:  enrich.FiscalObservations,
			HasIgvRetention:     enrich.HasIgvRetention,
			IgvRetentionAmount:  money.RoundSunat(enrich.RetentionAmount),
			NetCollectible:      money.RoundSunat(enrich.NetCollectible),
			RetentionApplied:    enrich.RetentionApplied,
			ShowTermsConditions: enrich.ShowTermsConditions,
		}
		for _, g := range enrich.Guias {
			fc.Guias = append(fc.Guias, PrintGuiaRef{Kind: g.Kind, Number: g.NroDoc})
		}
		if enrich.ShowTermsConditions {
			var company database.TenantCompanyConfig
			if db.First(&company).Error == nil {
				fc.TermsText = strings.TrimSpace(company.TermsAndConditions)
			}
		}
		if enrich.SellerUserID != nil && *enrich.SellerUserID > 0 {
			var seller database.TenantUser
			if db.Select("name").First(&seller, *enrich.SellerUserID).Error == nil {
				if name := strings.TrimSpace(seller.Name); name != "" {
					pd.SellerName = name
				}
			}
		}
	}

	if det, err := detraccionsvc.NewService(db).LoadBySaleID(saleID); err == nil && det != nil {
		if fc == nil {
			fc = &PrintFiscalContext{}
		}
		cat, _ := detraccionpkg.DefaultCatalog()
		label := det.GoodCode
		if cat != nil {
			if g, ok := cat.GoodByCode(det.GoodCode); ok {
				label = g.Description
			}
		}
		fc.HasDetraccion = true
		fc.DetraccionGoodCode = det.GoodCode
		fc.DetraccionGoodLabel = label
		fc.DetraccionRatePercent = det.RatePercent
		fc.DetraccionAmount = money.RoundSunat(det.DetractionAmountPen)
		fc.DetraccionBankAccount = det.BankAccount
		fc.DetraccionPaymentMethodCode = det.PaymentMethodCode
		fc.DetraccionNetPayable = money.RoundSunat(det.NetPayablePen)
	}

	if voucher, err := prepaymentsvc.NewService(db).LoadBySaleID(saleID); err == nil && prepaymentsvc.IsEmitVoucher(voucher) {
		if fc == nil {
			fc = &PrintFiscalContext{}
		}
		fc.HasPrepaymentEmit = true
		fc.PrepaymentLabel = prepaymentsvc.EmitPDFLabel()
		fc.PrepaymentAffectationGroup = voucher.AffectationGroup
		fc.PrepaymentRelatedDocType = voucher.RelatedDocType
		if pd.OperationTypeCode == "" {
			if voucher.OperationTypeCode != "" {
				pd.OperationTypeCode = voucher.OperationTypeCode
			} else {
				pd.OperationTypeCode = sunatpre.EmitOperationTypeCode()
			}
		}
	}

	if apps, err := prepaymentsvc.NewService(db).LoadApplicationsByConsumerSale(saleID); err == nil && len(apps) > 0 {
		if fc == nil {
			fc = &PrintFiscalContext{}
		}
		fc.HasPrepaymentDeduction = true
		fc.PrepaymentDeductions = make([]PrintPrepaymentDeduction, 0, len(apps))
		var sumTotal float64
		for _, app := range apps {
			fc.PrepaymentDeductions = append(fc.PrepaymentDeductions, PrintPrepaymentDeduction{
				DocumentNumber: strings.TrimSpace(app.DocumentNumber),
				RelatedDocType: strings.TrimSpace(app.RelatedDocType),
				Amount:         money.RoundSunat(app.Amount),
				Total:          money.RoundSunat(app.Total),
			})
			sumTotal += app.Total
		}
		fc.PrepaymentDeductionTotal = money.RoundSunat(sumTotal)
	}

	if fc != nil && (fc.PurchaseOrderNumber != "" || fc.FiscalObservations != "" || len(fc.Guias) > 0 ||
		fc.RetentionApplied || fc.HasIgvRetention || fc.ShowTermsConditions || fc.HasDetraccion ||
		fc.HasPrepaymentEmit || fc.HasPrepaymentDeduction) {
		pd.Fiscal = fc
	}
}

func enrichCreditNotePrintData(db *gorm.DB, sale *database.TenantSale, pd *PrintData) {
	if pd == nil || sale == nil || !isCreditOrDebitNotePrint(pd.SunatCode, sale.DocType) {
		return
	}
	if reason := strings.TrimSpace(sale.Notes); reason != "" {
		pd.CreditNoteReason = reason
	}
	if sale.OriginalSaleID != nil && *sale.OriginalSaleID > 0 {
		var orig database.TenantSale
		if err := db.First(&orig, *sale.OriginalSaleID).Error; err == nil {
			origSunat := ""
			var origSeries database.TenantDocumentSeries
			if err := db.First(&origSeries, orig.SeriesID).Error; err == nil {
				origSunat = origSeries.SunatCode
			}
			pd.AffectedDocSunatCode = printAffectedDocSunatType(&orig, origSunat)
			pd.AffectedDocNumber = printAffectedDocNumber(&orig)
		}
	}
	// El payload se lee siempre, no solo cuando falta el documento afectado: el tipo de nota
	// (codMotivo) vive únicamente ahí y SUNAT lo exige en la representación impresa.
	var inv database.TenantInvoice
	if err := db.Where("sale_id = ?", sale.ID).First(&inv).Error; err == nil &&
		strings.TrimSpace(inv.NotePayloadJSON) != "" {
		var note notePayloadRef
		if json.Unmarshal([]byte(inv.NotePayloadJSON), &note) == nil {
			if pd.AffectedDocNumber == "" {
				if v := strings.TrimSpace(note.TipDocAfectado); v != "" {
					pd.AffectedDocSunatCode = v
				}
				if v := strings.TrimSpace(note.NumDocfectado); v != "" {
					pd.AffectedDocNumber = v
				}
			}
			if pd.CreditNoteReason == "" {
				pd.CreditNoteReason = strings.TrimSpace(note.DesMotivo)
			}
			if pd.NoteTypeCode == "" {
				pd.NoteTypeCode = strings.TrimSpace(note.CodMotivo)
			}
		}
	}

	tipoDoc := noteSunatTipoDoc(sale.DocType)
	// Sin codMotivo declarado (notas antiguas), se asume el del catálogo que emite el sistema.
	if pd.NoteTypeCode == "" {
		pd.NoteTypeCode = defaultNoteReasonCode(tipoDoc)
	}
	pd.NoteTypeLabel = sunatnote.TypeLabel(tipoDoc, pd.NoteTypeCode)
	if pd.CreditNoteReason == "" {
		pd.CreditNoteReason = sunatnote.ReasonLabel(tipoDoc, pd.NoteTypeCode)
	}
}

// defaultNoteReasonCode el mismo que usa la emisión cuando no se declara otro.
func defaultNoteReasonCode(tipoDoc string) string {
	if tipoDoc == "08" {
		return "02"
	}
	return "01"
}
