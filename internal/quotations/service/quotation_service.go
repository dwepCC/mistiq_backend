package service

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	salessvc "tukifac/internal/sales/service"
	"tukifac/pkg/database"
	"tukifac/pkg/docseries"
	"tukifac/pkg/money"
	"tukifac/pkg/salecurrency"
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
	ProductID *uint `json:"product_id"`
	// PresentationID: variante/presentación elegida (ej. color) cuando el producto vende por
	// presentación con stock propio (product.HasVariants). Se conserva como snapshot y se propaga
	// a la venta al convertir la cotización, para descontar la presentación correcta.
	PresentationID     *uint   `json:"presentation_id"`
	Code               string  `json:"code"`
	Description        string  `json:"description"`
	Unit               string  `json:"unit"`
	Quantity           float64 `json:"quantity"`
	UnitPrice          float64 `json:"unit_price"`
	Discount           float64 `json:"discount"`
	IgvAffectationType string  `json:"igv_affectation_type"`
	PriceIncludesIgv   bool    `json:"price_includes_igv"`
	ModifiersJSON      string  `json:"modifiers_json"`
	ItemNote           string  `json:"item_note"`
	// LineDiscountMode/Value: descuento de la línea tal como lo tecleó el usuario (percent|amount
	// sobre la base imponible). Si no vienen y sí `discount` (clientes anteriores), se deriva.
	LineDiscountMode  string  `json:"line_discount_mode"`
	LineDiscountValue float64 `json:"line_discount_value"`
	// SaleUnitID / ComboJSON / Serials: se conservan en la línea y se propagan a la venta al
	// convertir. Antes se ignoraban y la venta descontaba mal el inventario (unidad de venta),
	// no resolvía el combo ni respetaba las series elegidas.
	SaleUnitID *uint    `json:"sale_unit_id"`
	ComboJSON  string   `json:"combo_json"`
	Serials    []string `json:"serials"`
}

type CreateQuotationInput struct {
	BranchID            uint
	ContactID           *uint
	UserID              uint
	SeriesID            uint
	IssueDate           time.Time
	ValidUntil          *time.Time
	Currency            string
	ExchangeRate        *float64
	// PaymentMethods: métodos de pago de referencia (no son un cobro: no tocan caja ni saldos).
	PaymentMethods      []QuotationPaymentRef
	Notes               string
	ShowTermsConditions bool
	Items               []QuotationItemInput
	TaxConfig           tax.Config
	GlobalDiscountMode  string
	GlobalDiscountValue float64
	// UserCanOverridePrice: resuelto por el HANDLER contra los permisos del JWT (sales.override_price),
	// nunca un valor que mande el cliente. false = los precios de catálogo deben coincidir.
	UserCanOverridePrice bool
}

type UpdateQuotationInput struct {
	ContactID           *uint
	SeriesID            uint
	IssueDate           time.Time
	ValidUntil          *time.Time
	Currency            string
	ExchangeRate        *float64
	// PaymentMethods: métodos de pago de referencia (no son un cobro: no tocan caja ni saldos).
	PaymentMethods      []QuotationPaymentRef
	Notes               string
	ShowTermsConditions bool
	Items               []QuotationItemInput
	TaxConfig           tax.Config
	GlobalDiscountMode  string
	GlobalDiscountValue float64
	UserCanOverridePrice bool
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
	// Payments / PaymentConditionCode: opcionales. Sin pagos, la venta se cobra al contado en
	// efectivo por su total REAL (el que calcula la venta, no el guardado en la cotización).
	Payments             []salessvc.PaymentInput
	PaymentConditionCode string
}

func productIsCatalogService(p *database.TenantProduct) bool {
	if p == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(p.Type), "service")
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
	currency, exchangeRate, err := s.validateHeader(input.ContactID, input.Currency, input.ExchangeRate, input.IssueDate, input.ValidUntil)
	if err != nil {
		return nil, err
	}
	paymentRefs, err := normalizePaymentRefs(input.PaymentMethods)
	if err != nil {
		return nil, err
	}
	calc, err := s.buildQuotation(input.Items, input.GlobalDiscountMode, input.GlobalDiscountValue, taxCfg)
	if err != nil {
		return nil, err
	}
	if err := s.checkAuthorizedPrices(input.BranchID, input.Items, input.UserCanOverridePrice, nil, currency, exchangeRate); err != nil {
		return nil, err
	}
	items := calc.items

	q := &database.TenantQuotation{
		BranchID:            input.BranchID,
		ContactID:           input.ContactID,
		UserID:              input.UserID,
		SeriesID:            input.SeriesID,
		IssueDate:           input.IssueDate,
		ValidUntil:          input.ValidUntil,
		Subtotal:             money.RoundSunat(calc.subtotal),
		TaxAmount:            money.RoundSunat(calc.taxAmount),
		Total:                money.RoundSunat(calc.total),
		GlobalDiscountMode:   calc.globalMode,
		GlobalDiscountValue:  calc.globalValue,
		GlobalDiscountAmount: calc.globalAmount,
		Currency:             currency,
		ExchangeRate:         exchangeRate,
		PaymentMethodsJSON:   paymentRefs,
		Notes:                input.Notes,
		ShowTermsConditions:  input.ShowTermsConditions,
		Status:               "draft",
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

// BranchOf devuelve la sucursal de una cotización (para comprobar acceso antes de operar sobre ella).
func (s *QuotationService) BranchOf(id uint) (uint, error) {
	var q database.TenantQuotation
	if err := s.db.Select("id", "branch_id").First(&q, id).Error; err != nil {
		return 0, errors.New("cotización no encontrada")
	}
	return q.BranchID, nil
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
	// La serie ya numeró esta cotización (series/correlative/number): cambiarla aquí dejaba
	// series_id apuntando a una serie y el número a otra.
	if input.SeriesID != 0 && input.SeriesID != q.SeriesID {
		return nil, errors.New("no se puede cambiar la serie de una cotización ya numerada")
	}
	input.SeriesID = q.SeriesID
	if _, err := s.validateSeries(input.SeriesID, q.BranchID); err != nil {
		return nil, err
	}
	taxCfg := input.TaxConfig
	if taxCfg.TaxRate == 0 {
		taxCfg = tax.LoadFromDB(s.db)
	}
	currency, exchangeRate, err := s.validateHeader(input.ContactID, input.Currency, input.ExchangeRate, input.IssueDate, input.ValidUntil)
	if err != nil {
		return nil, err
	}
	paymentRefs, err := normalizePaymentRefs(input.PaymentMethods)
	if err != nil {
		return nil, err
	}
	calc, err := s.buildQuotation(input.Items, input.GlobalDiscountMode, input.GlobalDiscountValue, taxCfg)
	if err != nil {
		return nil, err
	}
	var storedItems []database.TenantQuotationItem
	if err := s.db.Where("quotation_id = ?", id).Find(&storedItems).Error; err != nil {
		return nil, err
	}
	// Los precios guardados solo cuentan como "sin cambios" si la cotización sigue en la misma moneda y TC; si cambió (p. ej.
	// de soles a dólares con el mismo número), cada línea se vuelve a validar contra el catálogo.
	if !strings.EqualFold(strings.TrimSpace(q.Currency), currency) || (q.ExchangeRate == nil) != (exchangeRate == nil) ||
		(q.ExchangeRate != nil && exchangeRate != nil && math.Abs(*q.ExchangeRate-*exchangeRate) > 1e-9) {
		storedItems = nil
	}
	if err := s.checkAuthorizedPrices(q.BranchID, input.Items, input.UserCanOverridePrice, storedItems, currency, exchangeRate); err != nil {
		return nil, err
	}
	items := calc.items

	err = s.db.Transaction(func(tx *gorm.DB) error {
		// Reclamo condicional: si otra petición convirtió la cotización entre la lectura de arriba
		// y este punto, no se edita una cotización que ya dio origen a una venta.
		res := tx.Model(&database.TenantQuotation{}).
			Where("id = ? AND status <> ?", id, "converted").
			Updates(map[string]interface{}{
				"contact_id":             input.ContactID,
				"series_id":              input.SeriesID,
				"issue_date":             input.IssueDate,
				"valid_until":            input.ValidUntil,
				"currency":               currency,
				"exchange_rate":          exchangeRate,
				"payment_methods_json":   paymentRefs,
				"notes":                  input.Notes,
				"show_terms_conditions":  input.ShowTermsConditions,
				"subtotal":               money.RoundSunat(calc.subtotal),
				"tax_amount":             money.RoundSunat(calc.taxAmount),
				"total":                  money.RoundSunat(calc.total),
				"global_discount_mode":   calc.globalMode,
				"global_discount_value":  calc.globalValue,
				"global_discount_amount": calc.globalAmount,
			})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			// MySQL cuenta solo filas que cambiaron: guardar sin tocar nada también da 0. Se
			// distingue releyendo el estado.
			var status string
			if err := tx.Model(&database.TenantQuotation{}).Where("id = ?", id).Select("status").Scan(&status).Error; err != nil {
				return err
			}
			if strings.EqualFold(strings.TrimSpace(status), "converted") {
				return errors.New("no se puede editar una cotización ya convertida")
			}
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
	// La cotización se editó a propósito: su foto de impresión se vuelve a congelar en la próxima impresión.
	_ = s.db.Where("doc_kind = ? AND doc_id = ?", database.SnapshotKindQuotation, id).Delete(&database.TenantDocumentSnapshot{}).Error
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
		if err := tx.Where("doc_kind = ? AND doc_id = ?", database.SnapshotKindQuotation, id).Delete(&database.TenantDocumentSnapshot{}).Error; err != nil {
			return err
		}
		return tx.Unscoped().Delete(&database.TenantQuotation{}, id).Error
	})
}

// ErrQuotationAlreadyConverted: la cotización ya dio origen a una venta.
var ErrQuotationAlreadyConverted = errors.New("esta cotización ya fue convertida a una venta")

// ClaimForConversionTx marca la cotización como convertida con un UPDATE CONDICIONAL
// (status <> 'converted') dentro de la transacción de la venta. Es lo que hace la conversión
// atómica: dos conversiones simultáneas crean cada una su venta, pero solo una consigue el UPDATE;
// la otra recibe ErrQuotationAlreadyConverted y su transacción entera (venta, correlativo, caja,
// stock) hace rollback. Antes la venta se creaba y la marca iba después, en otra transacción, y
// cada petición que ganaba la carrera dejaba una venta duplicada.
func ClaimForConversionTx(tx *gorm.DB, quotationID, saleID uint, target string) error {
	res := tx.Model(&database.TenantQuotation{}).
		Where("id = ? AND status <> ?", quotationID, "converted").
		Updates(map[string]interface{}{
			"status":            "converted",
			"converted_sale_id": saleID,
			"converted_at":      time.Now(),
			"converted_target":  strings.TrimSpace(target),
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		var n int64
		if err := tx.Model(&database.TenantQuotation{}).Where("id = ?", quotationID).Count(&n).Error; err != nil {
			return err
		}
		if n == 0 {
			return errors.New("cotización no encontrada")
		}
		return ErrQuotationAlreadyConverted
	}
	return nil
}

// MarkConverted se conserva por compatibilidad; usa el mismo UPDATE condicional.
func (s *QuotationService) MarkConverted(quotationID, saleID uint, target string) error {
	return ClaimForConversionTx(s.db, quotationID, saleID, target)
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
			PresentationID:     it.PresentationID,
			Code:               it.Code,
			Description:        it.Description,
			Unit:               it.Unit,
			Quantity:           it.Quantity,
			UnitPrice:          it.UnitPrice,
			Discount:           it.Discount,
			LineDiscountMode:   it.LineDiscountMode,
			LineDiscountValue:  it.LineDiscountValue,
			SaleUnitID:         it.SaleUnitID,
			ComboJSON:          it.ComboJSON,
			Serials:            parseSerialsJSON(it.SerialsJSON),
			IgvAffectationType: it.IgvAffectationType,
			PriceIncludesIgv:   it.PriceIncludesIgv,
			ModifiersJSON:      it.ModifiersJSON,
			ItemNote:           it.ItemNote,
			// El precio ya fue pactado/autorizado al crear la cotización (puede ser distinto del
			// catálogo vigente a propósito — precio negociado); convertir no debe reevaluarlo
			// contra validateAuthorizedPrices.
			PriceAuthorized: true,
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

	// Pagos: los que indique quien convierte; sin ellos, contado en efectivo por el total REAL de
	// la venta (PaymentMethod sin Payments hace que SaleService use su propio total). Antes se
	// registraba q.Total, y cualquier diferencia entre cotización y venta (bonificación, IGV no
	// incluido) dejaba un pago de más o de menos en caja.
	payments := input.Payments
	paymentMethod := ""
	if len(payments) == 0 {
		paymentMethod = "cash"
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
		PaymentMethod:         paymentMethod,
		Payments:              payments,
		PaymentConditionCode:  input.PaymentConditionCode,
		Notes:                 notes,
		Items:                 saleItems,
		GlobalDiscountMode:    q.GlobalDiscountMode,
		GlobalDiscountValue:   q.GlobalDiscountValue,
		TaxConfig:             taxCfg,
		CentralTenantID:       input.CentralTenant,
		IssuedFromQuotationID: &qID,
		// Atómico con la venta: o se crea la venta Y la cotización queda convertida, o ninguna.
		OnCreatedTx: func(tx *gorm.DB, sale *database.TenantSale) error {
			return ClaimForConversionTx(tx, quotationID, sale.ID, target)
		},
	})
	if err != nil {
		return nil, err
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
