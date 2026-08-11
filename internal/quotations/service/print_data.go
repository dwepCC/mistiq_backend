package service

import (
	"strings"

	salessvc "tukifac/internal/sales/service"
	"tukifac/pkg/database"
	"tukifac/pkg/datespe"
	"tukifac/pkg/money"
	"tukifac/pkg/numeroletras"

	"gorm.io/gorm"
)

func quotationAffectDesc(code string) string {
	m := map[string]string{"10": "Gravado", "20": "Exonerado", "30": "Inafecto", "40": "Exportación"}
	if d, ok := m[code]; ok {
		return d
	}
	return code
}

// BuildPrintDataForQuotation construye print_data para PDF A4/ticket de cotización.
func BuildPrintDataForQuotation(db *gorm.DB, quotationID uint) (*salessvc.PrintData, error) {
	qSvc := NewQuotationService(db)
	q, items, err := qSvc.GetByID(quotationID)
	if err != nil {
		return nil, err
	}

	pd := &salessvc.PrintData{
		DocType:   "Cotización",
		SunatCode: "QT",
		Series:    strings.TrimSpace(q.Series),
		Number:    strings.TrimSpace(q.Number),
		IssueDate: q.IssueDate.Format("02/01/2006"),
		// La hora sale de CreatedAt, no de IssueDate: esa fecha se fija a las 12:00 a
		// propósito y hacía que toda cotización se imprimiera con «12:00:00».
		IssueTime:    datespe.IssueTime(q.CreatedAt),
		Currency:     q.Currency,
		ExchangeRate: q.ExchangeRate,
		Subtotal:     q.Subtotal,
		TaxAmount:    q.TaxAmount,
		Total:        q.Total,
		Notes:        strings.TrimSpace(q.Notes),
		Payments:     []salessvc.PrintPayment{},
		QRData:       "",
	}

	if q.ValidUntil != nil {
		pd.ValidUntil = q.ValidUntil.Format("02/01/2006")
	}

	currency := strings.TrimSpace(q.Currency)
	if currency == "" {
		currency = "PEN"
	}
	pd.LegendText = numeroletras.MontoEnLetras(q.Total, currency)

	if q.ContactID != nil && *q.ContactID > 0 {
		var contact database.TenantContact
		if db.First(&contact, *q.ContactID).Error == nil {
			addr, _ := database.NormalizeTenantContactAddressUbigeo(contact.Address, contact.Ubigeo)
			pd.Client = &salessvc.PrintClient{
				DocType:      contact.DocType,
				DocNumber:    contact.DocNumber,
				BusinessName: contact.BusinessName,
				Address:      addr,
				Email:        strings.TrimSpace(contact.Email),
			}
		}
	}
	if pd.Client == nil {
		pd.Client = &salessvc.PrintClient{DocType: "0", DocNumber: "—", BusinessName: "Sin cliente"}
	}

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

	if q.UserID > 0 {
		var user database.TenantUser
		if db.Select("name").First(&user, q.UserID).Error == nil {
			pd.SellerName = strings.TrimSpace(user.Name)
		}
	}

	var branch database.TenantBranch
	if db.First(&branch, q.BranchID).Error == nil {
		pd.Branch = salessvc.PrintBranch{Name: branch.Name, Address: branch.Address}
		if addr := strings.TrimSpace(branch.Address); addr != "" {
			pd.Company.Address = addr
		}
	}

	pd.Items = make([]salessvc.PrintItem, len(items))
	affMap := make(map[string]*salessvc.PrintAffectTotal)
	for i, it := range items {
		pd.Items[i] = salessvc.PrintItem{
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
			ItemNote:      it.ItemNote,
		}
		code := strings.TrimSpace(it.IgvAffectationType)
		if code == "" {
			code = "10"
		}
		if _, ok := affMap[code]; !ok {
			affMap[code] = &salessvc.PrintAffectTotal{Code: code, Description: quotationAffectDesc(code)}
		}
		affMap[code].Subtotal = money.RoundSunat(affMap[code].Subtotal + it.Subtotal)
		affMap[code].TaxAmount = money.RoundSunat(affMap[code].TaxAmount + it.TaxAmount)
		affMap[code].Total = money.RoundSunat(affMap[code].Total + it.Total)
	}
	if len(affMap) > 0 {
		pd.TotalsByAffectation = make(map[string]salessvc.PrintAffectTotal)
		for k, v := range affMap {
			row := *v
			row.Subtotal = money.RoundSunat(row.Subtotal)
			row.TaxAmount = money.RoundSunat(row.TaxAmount)
			row.Total = money.RoundSunat(row.Total)
			pd.TotalsByAffectation[k] = row
		}
	}

	if q.ShowTermsConditions {
		terms := strings.TrimSpace(company.TermsAndConditions)
		if terms != "" {
			pd.Fiscal = &salessvc.PrintFiscalContext{
				ShowTermsConditions: true,
				TermsText:           terms,
			}
		}
	}

	return pd, nil
}

// EmailQuotationInput datos para enviar cotización por correo.
type EmailQuotationInput struct {
	Email     string
	PdfBase64 string
	Format    string // a4 | ticket
}

func (s *QuotationService) EmailQuotation(quotationID uint, in EmailQuotationInput) error {
	q, _, err := s.GetByID(quotationID)
	if err != nil {
		return err
	}
	return salessvc.SendDocumentPdfEmail(salessvc.DocumentPdfEmailInput{
		To:         in.Email,
		PdfBase64:  in.PdfBase64,
		DocLabel:   "Cotización",
		DocNumber:  strings.TrimSpace(q.Number),
		FilePrefix: "cotizacion",
	})
}
