package service

import (
	"errors"
	"fmt"
	"strings"
	"time"

	salessvc "tukifac/internal/sales/service"
	"tukifac/pkg/database"
	"tukifac/pkg/docseries"
	"tukifac/pkg/money"
	"tukifac/pkg/salecurrency"
	"tukifac/pkg/sunat"
	"tukifac/pkg/tax"

	"gorm.io/gorm"
)

type QuotationService struct {
	db *gorm.DB
}

func NewQuotationService(db *gorm.DB) *QuotationService {
	return &QuotationService{db: db}
}

type QuotationItemInput struct {
	ProductID          *uint   `json:"product_id"`
	Code               string  `json:"code"`
	Description        string  `json:"description"`
	Unit               string  `json:"unit"`
	Quantity           float64 `json:"quantity"`
	UnitPrice          float64 `json:"unit_price"`
	Discount           float64 `json:"discount"`
	IgvAffectationType string  `json:"igv_affectation_type"`
	PriceIncludesIgv   bool    `json:"price_includes_igv"`
	ModifiersJSON      string  `json:"modifiers_json"`
}

type CreateQuotationInput struct {
	BranchID     uint
	ContactID    *uint
	UserID       uint
	SeriesID     uint
	IssueDate    time.Time
	ValidUntil   *time.Time
	Currency     string
	ExchangeRate *float64
	Notes        string
	Items        []QuotationItemInput
	TaxConfig    tax.Config
}

type UpdateQuotationInput struct {
	ContactID    *uint
	SeriesID     uint
	IssueDate    time.Time
	ValidUntil   *time.Time
	Currency     string
	ExchangeRate *float64
	Notes        string
	Items        []QuotationItemInput
	TaxConfig    tax.Config
}

type QuotationListParams struct {
	BranchID uint
	Query    string
	Status   string
	From     time.Time
	To       time.Time
	Limit    int
	Offset   int
}

type ConvertInput struct {
	Target        string // nota_venta | 01 | 03
	SeriesID      uint
	IssueDate     time.Time
	ContactID     *uint
	UserID        uint
	CentralTenant uint
	TaxConfig     tax.Config
}

func productIsCatalogService(p *database.TenantProduct) bool {
	if p == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(p.Type), "service")
}

func (s *QuotationService) buildItems(inputItems []QuotationItemInput, taxCfg tax.Config) ([]database.TenantQuotationItem, float64, float64, float64, error) {
	if len(inputItems) == 0 {
		return nil, 0, 0, 0, errors.New("la cotización debe tener al menos un ítem")
	}
	var subtotal, taxAmount, total float64
	out := make([]database.TenantQuotationItem, 0, len(inputItems))
	for _, item := range inputItems {
		affType := strings.TrimSpace(item.IgvAffectationType)
		if affType == "" {
			affType = "10"
		}
		effectiveRate := taxCfg.EffectiveRate(affType)
		itemSub, itemTax, itemTotal := tax.CalcItem(
			item.UnitPrice, item.Quantity, item.Discount,
			affType, item.PriceIncludesIgv, taxCfg,
		)
		subtotal = money.RoundSunat(subtotal + itemSub)
		taxAmount = money.RoundSunat(taxAmount + itemTax)
		total = money.RoundSunat(total + itemTotal)

		itemType := "product"
		if item.ProductID != nil && *item.ProductID > 0 {
			var prod database.TenantProduct
			if s.db.Select("type").First(&prod, *item.ProductID).Error == nil && productIsCatalogService(&prod) {
				itemType = "service"
			}
		} else if strings.EqualFold(strings.TrimSpace(item.Unit), "ZZ") {
			itemType = "service"
		}

		out = append(out, database.TenantQuotationItem{
			ProductID:          item.ProductID,
			Code:               item.Code,
			Description:        item.Description,
			Unit:               sunat.NormalizeUnit(item.Unit, itemType),
			Quantity:           item.Quantity,
			UnitPrice:          item.UnitPrice,
			Discount:           item.Discount,
			TaxRate:            effectiveRate,
			IgvAffectationType: affType,
			PriceIncludesIgv:   item.PriceIncludesIgv,
			Subtotal:           itemSub,
			TaxAmount:          itemTax,
			Total:              itemTotal,
			ModifiersJSON:      item.ModifiersJSON,
		})
	}
	return out, subtotal, taxAmount, total, nil
}

func (s *QuotationService) validateSeries(seriesID, branchID uint) (database.TenantDocumentSeries, error) {
	series, err := docseries.ValidateForBranch(s.db, seriesID, branchID)
	if err != nil {
		return series, err
	}
	if strings.TrimSpace(strings.ToLower(series.Category)) != "cotizacion" {
		return series, errors.New("la serie debe ser de categoría cotización")
	}
	return series, nil
}

func (s *QuotationService) Create(input CreateQuotationInput) (*database.TenantQuotation, error) {
	if input.BranchID == 0 || input.UserID == 0 {
		return nil, errors.New("sucursal y usuario son requeridos")
	}
	if _, err := s.validateSeries(input.SeriesID, input.BranchID); err != nil {
		return nil, err
	}
	taxCfg := input.TaxConfig
	if taxCfg.TaxRate == 0 {
		taxCfg = tax.LoadFromDB(s.db)
	}
	currency, err := salecurrency.NormalizeCurrency(input.Currency)
	if err != nil {
		return nil, err
	}
	exchangeRate, err := salecurrency.NormalizeExchangeRate(currency, input.ExchangeRate)
	if err != nil {
		return nil, err
	}
	items, subtotal, taxAmount, total, err := s.buildItems(input.Items, taxCfg)
	if err != nil {
		return nil, err
	}

	q := &database.TenantQuotation{
		BranchID:     input.BranchID,
		ContactID:    input.ContactID,
		UserID:       input.UserID,
		SeriesID:     input.SeriesID,
		IssueDate:    input.IssueDate,
		ValidUntil:   input.ValidUntil,
		Subtotal:     money.RoundSunat(subtotal),
		TaxAmount:    money.RoundSunat(taxAmount),
		Total:        money.RoundSunat(total),
		Currency:     currency,
		ExchangeRate: exchangeRate,
		Notes:        input.Notes,
		Status:       "draft",
	}

	err = s.db.Transaction(func(tx *gorm.DB) error {
		correlative, seriesLocked, err := docseries.ReserveNext(tx, input.SeriesID)
		if err != nil {
			return err
		}
		q.Series = seriesLocked.Series
		q.Correlative = correlative
		q.Number = fmt.Sprintf("%s-%08d", seriesLocked.Series, correlative)
		if err := tx.Create(q).Error; err != nil {
			return err
		}
		for i := range items {
			items[i].QuotationID = q.ID
		}
		return tx.Create(&items).Error
	})
	if err != nil {
		return nil, err
	}
	return q, nil
}

func (s *QuotationService) GetByID(id uint) (*database.TenantQuotation, []database.TenantQuotationItem, error) {
	var q database.TenantQuotation
	if err := s.db.First(&q, id).Error; err != nil {
		return nil, nil, errors.New("cotización no encontrada")
	}
	var items []database.TenantQuotationItem
	if err := s.db.Where("quotation_id = ?", id).Order("id").Find(&items).Error; err != nil {
		return nil, nil, err
	}
	if q.ContactID != nil && *q.ContactID > 0 {
		var c database.TenantContact
		if s.db.Select("business_name, trade_name").First(&c, *q.ContactID).Error == nil {
			q.ContactName = strings.TrimSpace(c.TradeName)
			if q.ContactName == "" {
				q.ContactName = strings.TrimSpace(c.BusinessName)
			}
		}
	}
	return &q, items, nil
}

func (s *QuotationService) List(params QuotationListParams) ([]database.TenantQuotation, int64, error) {
	q := s.db.Model(&database.TenantQuotation{})
	if params.BranchID > 0 {
		q = q.Where("branch_id = ?", params.BranchID)
	}
	if params.Status != "" {
		q = q.Where("status = ?", params.Status)
	}
	if !params.From.IsZero() {
		q = q.Where("issue_date >= ?", params.From)
	}
	if !params.To.IsZero() {
		q = q.Where("issue_date <= ?", params.To)
	}
	if params.Query != "" {
		like := "%" + strings.TrimSpace(params.Query) + "%"
		q = q.Where("number LIKE ? OR notes LIKE ?", like, like)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	limit := params.Limit
	if limit <= 0 {
		limit = 25
	}
	var rows []database.TenantQuotation
	if err := q.Order("issue_date DESC, id DESC").Limit(limit).Offset(params.Offset).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	if len(rows) == 0 {
		return rows, total, nil
	}
	contactIDs := make([]uint, 0)
	for _, r := range rows {
		if r.ContactID != nil && *r.ContactID > 0 {
			contactIDs = append(contactIDs, *r.ContactID)
		}
	}
	if len(contactIDs) > 0 {
		var contacts []database.TenantContact
		s.db.Select("id, business_name, trade_name").Where("id IN ?", contactIDs).Find(&contacts)
		byID := make(map[uint]database.TenantContact, len(contacts))
		for _, c := range contacts {
			byID[c.ID] = c
		}
		for i := range rows {
			if rows[i].ContactID == nil {
				continue
			}
			if c, ok := byID[*rows[i].ContactID]; ok {
				rows[i].ContactName = strings.TrimSpace(c.TradeName)
				if rows[i].ContactName == "" {
					rows[i].ContactName = strings.TrimSpace(c.BusinessName)
				}
			}
		}
	}
	return rows, total, nil
}

func (s *QuotationService) Update(id uint, input UpdateQuotationInput) (*database.TenantQuotation, error) {
	var q database.TenantQuotation
	if err := s.db.First(&q, id).Error; err != nil {
		return nil, errors.New("cotización no encontrada")
	}
	if strings.EqualFold(strings.TrimSpace(q.Status), "converted") {
		return nil, errors.New("no se puede editar una cotización ya convertida")
	}
	if _, err := s.validateSeries(input.SeriesID, q.BranchID); err != nil {
		return nil, err
	}
	taxCfg := input.TaxConfig
	if taxCfg.TaxRate == 0 {
		taxCfg = tax.LoadFromDB(s.db)
	}
	currency, err := salecurrency.NormalizeCurrency(input.Currency)
	if err != nil {
		return nil, err
	}
	exchangeRate, err := salecurrency.NormalizeExchangeRate(currency, input.ExchangeRate)
	if err != nil {
		return nil, err
	}
	items, subtotal, taxAmount, total, err := s.buildItems(input.Items, taxCfg)
	if err != nil {
		return nil, err
	}

	err = s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&q).Updates(map[string]interface{}{
			"contact_id":    input.ContactID,
			"series_id":     input.SeriesID,
			"issue_date":    input.IssueDate,
			"valid_until":   input.ValidUntil,
			"currency":      currency,
			"exchange_rate": exchangeRate,
			"notes":         input.Notes,
			"subtotal":      money.RoundSunat(subtotal),
			"tax_amount":    money.RoundSunat(taxAmount),
			"total":         money.RoundSunat(total),
		}).Error; err != nil {
			return err
		}
		if err := tx.Where("quotation_id = ?", id).Delete(&database.TenantQuotationItem{}).Error; err != nil {
			return err
		}
		for i := range items {
			items[i].QuotationID = id
		}
		return tx.Create(&items).Error
	})
	if err != nil {
		return nil, err
	}
	return s.reloadHeader(id)
}

func (s *QuotationService) reloadHeader(id uint) (*database.TenantQuotation, error) {
	var q database.TenantQuotation
	if err := s.db.First(&q, id).Error; err != nil {
		return nil, err
	}
	return &q, nil
}

func (s *QuotationService) Delete(id uint) error {
	var q database.TenantQuotation
	if err := s.db.First(&q, id).Error; err != nil {
		return errors.New("cotización no encontrada")
	}
	if strings.EqualFold(strings.TrimSpace(q.Status), "converted") {
		return errors.New("no se puede eliminar una cotización ya convertida")
	}
	return s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("quotation_id = ?", id).Delete(&database.TenantQuotationItem{}).Error; err != nil {
			return err
		}
		return tx.Unscoped().Delete(&database.TenantQuotation{}, id).Error
	})
}

func (s *QuotationService) MarkConverted(quotationID, saleID uint, target string) error {
	var q database.TenantQuotation
	if err := s.db.First(&q, quotationID).Error; err != nil {
		return errors.New("cotización no encontrada")
	}
	if strings.EqualFold(strings.TrimSpace(q.Status), "converted") {
		if q.ConvertedSaleID != nil && *q.ConvertedSaleID == saleID {
			return nil
		}
		return errors.New("esta cotización ya fue convertida a una venta")
	}
	now := time.Now()
	return s.db.Model(&q).Updates(map[string]interface{}{
		"status":            "converted",
		"converted_sale_id": saleID,
		"converted_at":      now,
		"converted_target":  strings.TrimSpace(target),
	}).Error
}

func (s *QuotationService) EnsureCanLinkToSale(quotationID uint) (*database.TenantQuotation, error) {
	var q database.TenantQuotation
	if err := s.db.First(&q, quotationID).Error; err != nil {
		return nil, errors.New("cotización no encontrada")
	}
	if strings.EqualFold(strings.TrimSpace(q.Status), "converted") {
		return nil, errors.New("esta cotización ya fue convertida a una venta")
	}
	return &q, nil
}

func (s *QuotationService) ConvertToSale(quotationID uint, input ConvertInput) (*database.TenantSale, error) {
	q, items, err := s.GetByID(quotationID)
	if err != nil {
		return nil, err
	}
	if strings.EqualFold(strings.TrimSpace(q.Status), "converted") {
		return nil, errors.New("esta cotización ya fue convertida")
	}
	target := strings.TrimSpace(strings.ToLower(input.Target))
	if target == "" {
		return nil, errors.New("target es obligatorio (nota_venta, 01 o 03)")
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
	if targetSeries.BranchID != q.BranchID {
		return nil, errors.New("la serie debe pertenecer a la misma sucursal que la cotización")
	}
	if !targetSeries.Active {
		return nil, errors.New("la serie destino debe estar activa")
	}

	saleItems := make([]salessvc.SaleItemInput, 0, len(items))
	for _, it := range items {
		saleItems = append(saleItems, salessvc.SaleItemInput{
			ProductID:          it.ProductID,
			Code:               it.Code,
			Description:        it.Description,
			Unit:               it.Unit,
			Quantity:           it.Quantity,
			UnitPrice:          it.UnitPrice,
			Discount:           it.Discount,
			IgvAffectationType: it.IgvAffectationType,
			PriceIncludesIgv:   it.PriceIncludesIgv,
			ModifiersJSON:      it.ModifiersJSON,
		})
	}

	contactID := q.ContactID
	if input.ContactID != nil && *input.ContactID > 0 {
		c, err := loadContactForConvert(s.db, *input.ContactID)
		if err != nil {
			return nil, err
		}
		cid := c.ID
		contactID = &cid
	}
	if target == "01" {
		var c *database.TenantContact
		if contactID != nil && *contactID > 0 {
			var loaded database.TenantContact
			if err := s.db.First(&loaded, *contactID).Error; err != nil {
				return nil, errors.New("cliente no encontrado")
			}
			c = &loaded
		}
		if err := validateContactForFactura(c); err != nil {
			return nil, err
		}
	}

	qRef := strings.TrimSpace(q.Number)
	notes := strings.TrimSpace(q.Notes)
	if notes != "" {
		notes = "Referencia cotización " + qRef + ". " + notes
	} else {
		notes = "Referencia cotización " + qRef + "."
	}

	payments := []salessvc.PaymentInput{}
	if q.Total > 0 {
		payments = []salessvc.PaymentInput{{Method: "cash", Amount: q.Total}}
	}

	taxCfg := input.TaxConfig
	if taxCfg.TaxRate == 0 {
		taxCfg = tax.LoadFromDB(s.db)
	}

	qID := quotationID
	saleSvc := salessvc.NewSaleService(s.db)
	sale, err := saleSvc.Create(salessvc.CreateSaleInput{
		BranchID:              q.BranchID,
		ContactID:             contactID,
		UserID:                input.UserID,
		SeriesID:              input.SeriesID,
		DocType:               strings.TrimSpace(targetSeries.DocType),
		IssueDate:             input.IssueDate,
		DueDate:               q.ValidUntil,
		Currency:              q.Currency,
		OperationTypeCode:     salecurrency.OpVentaInterna,
		ExchangeRate:          q.ExchangeRate,
		Payments:              payments,
		Notes:                 notes,
		Items:                 saleItems,
		TaxConfig:             taxCfg,
		CentralTenantID:       input.CentralTenant,
		IssuedFromQuotationID: &qID,
	})
	if err != nil {
		return nil, err
	}

	convertedTarget := target
	if target == "nota_venta" {
		convertedTarget = "nota_venta"
	}
	if err := s.MarkConverted(quotationID, sale.ID, convertedTarget); err != nil {
		return sale, err
	}
	return sale, nil
}

const SunatRucLength = 11

func validateContactForFactura(c *database.TenantContact) error {
	if c == nil {
		return errors.New("la factura electrónica (01) requiere un cliente con RUC de 11 dígitos")
	}
	if c.DocType != "6" {
		return errors.New("la factura solo puede emitirse a clientes con RUC (tipo de documento 6)")
	}
	docNum := strings.TrimSpace(c.DocNumber)
	if len(docNum) != SunatRucLength {
		return fmt.Errorf("el RUC del cliente debe tener exactamente %d dígitos", SunatRucLength)
	}
	for _, r := range docNum {
		if r < '0' || r > '9' {
			return errors.New("el RUC del cliente debe contener solo dígitos")
		}
	}
	return nil
}

func loadContactForConvert(db *gorm.DB, contactID uint) (*database.TenantContact, error) {
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

func ParseQuotationIssueDate(issueYMD string, fallback time.Time) time.Time {
	issueYMD = strings.TrimSpace(issueYMD)
	if issueYMD == "" {
		return fallback
	}
	loc, err := time.LoadLocation("America/Lima")
	if err != nil || loc == nil {
		loc = time.Local
	}
	t, err := time.ParseInLocation("2006-01-02", issueYMD, loc)
	if err != nil {
		return fallback
	}
	return time.Date(t.Year(), t.Month(), t.Day(), 12, 0, 0, 0, loc)
}

func ParseOptionalDateYMD(ymd string) *time.Time {
	ymd = strings.TrimSpace(ymd)
	if ymd == "" {
		return nil
	}
	loc, err := time.LoadLocation("America/Lima")
	if err != nil || loc == nil {
		loc = time.Local
	}
	t, err := time.ParseInLocation("2006-01-02", ymd, loc)
	if err != nil {
		return nil
	}
	tt := time.Date(t.Year(), t.Month(), t.Day(), 12, 0, 0, 0, loc)
	return &tt
}
