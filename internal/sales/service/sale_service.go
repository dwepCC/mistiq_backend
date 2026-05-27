package service

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"tukifac/pkg/billingstate"
	"tukifac/pkg/database"
	"tukifac/pkg/docseries"
	"tukifac/pkg/money"
	"tukifac/pkg/sunat"
	"tukifac/pkg/tax"
	cashbanksvc "tukifac/internal/cashbank/service"

	"gorm.io/gorm"
)

// Monto máximo en soles para venta con cliente doc. tipo 0 (DOC.TRIB.NO.DOM.SIN.RUC) según SUNAT.
const SunatMaxMontoClienteSinRUC = 700

// RUC Perú: 11 dígitos.
const SunatRucLength = 11

type SaleService struct {
	db *gorm.DB
}

func NewSaleService(db *gorm.DB) *SaleService {
	return &SaleService{db: db}
}

// productIsCatalogService: servicios no consumen inventario aunque un registro legacy tenga manage_stock en true.
func productIsCatalogService(p *database.TenantProduct) bool {
	if p == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(p.Type), "service")
}

type SaleItemInput struct {
	ProductID          *uint   `json:"product_id"`
	Code               string  `json:"code"`
	Description        string  `json:"description"`
	Unit               string  `json:"unit"`
	Quantity           float64 `json:"quantity"`
	UnitPrice          float64 `json:"unit_price"`
	Discount           float64 `json:"discount"`
	TaxRate            float64 `json:"tax_rate"`             // ignorado en cálculo; se usa IgvAffectationType + config empresa
	IgvAffectationType string  `json:"igv_affectation_type"` // catálogo SUNAT N°07
	PriceIncludesIgv   bool    `json:"price_includes_igv"`   // si el precio ya incluye IGV
	ModifiersJSON      string  `json:"modifiers_json"`       // detalle de modificadores para ticket
	Serials            []string `json:"serials"`             // números de serie elegidos (productos con ManageSeries)
}

// PaymentInput representa un pago individual (método + monto).
type PaymentInput struct {
	Method string  `json:"method"` // código: cash, yape, plin, etc.
	Amount float64 `json:"amount"`
}

type CreateSaleInput struct {
	BranchID      uint
	ContactID     *uint
	UserID        uint
	CashSessionID *uint
	SeriesID      uint
	DocType       string
	IssueDate     time.Time
	DueDate       *time.Time
	Currency      string
	PaymentMethod string   // legacy: si Payments vacío, se usa para el total
	Payments      []PaymentInput `json:"payments"` // múltiples métodos de pago
	Notes         string
	Items         []SaleItemInput
	TaxConfig     tax.Config // configuración tributaria de la empresa
	// Emisión desde nota de venta (no descontar inventario ni duplicar caja/bancos).
	SkipInventory             bool
	SkipPaymentDistribution   bool
	IssuedFromNotaSaleID      *uint // ID de la NV origen; se guarda en la nueva venta 01/03
}

// NextCorrelative retorna el siguiente correlativo para una serie y lo incrementa (transacción con bloqueo de fila).
func (s *SaleService) NextCorrelative(seriesID uint) (uint, error) {
	return docseries.ReserveNextStandalone(s.db, seriesID)
}

func (s *SaleService) Create(input CreateSaleInput) (*database.TenantSale, error) {
	if len(input.Items) == 0 {
		return nil, errors.New("la venta debe tener al menos un ítem")
	}
	if input.BranchID == 0 || input.UserID == 0 {
		return nil, errors.New("sucursal y usuario son requeridos")
	}

	series, err := docseries.ValidateForBranch(s.db, input.SeriesID, input.BranchID)
	if err != nil {
		return nil, err
	}

	// Usar config tributaria de la empresa; si no se pasó, cargar desde BD
	taxCfg := input.TaxConfig
	if taxCfg.TaxRate == 0 {
		taxCfg = tax.LoadFromDB(s.db)
	}

	// Calcular totales respetando el tipo de afectación SUNAT por ítem
	var subtotal, taxAmount, total float64
	saleItems := make([]database.TenantSaleItem, 0, len(input.Items))

	for _, item := range input.Items {
		affType := item.IgvAffectationType
		if affType == "" {
			affType = "10" // default: gravado
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

		saleItems = append(saleItems, database.TenantSaleItem{
			ProductID:          item.ProductID,
			Code:               item.Code,
			Description:        item.Description,
			Unit:               sunat.NormalizeUnit(item.Unit, itemType),
			Quantity:           item.Quantity,
			UnitPrice:          item.UnitPrice,
			Discount:           item.Discount,
			TaxRate:            effectiveRate,
			IgvAffectationType: affType,
			Subtotal:           itemSub,
			TaxAmount:          itemTax,
			Total:              itemTotal,
			ModifiersJSON:     item.ModifiersJSON,
		})
	}

	currency := input.Currency
	if currency == "" {
		currency = "PEN"
	}

	// Validaciones SUNAT: Factura 01 solo con RUC de 11 dígitos; doc. tipo 0 máximo S/ 700 en boleta/nota de venta
	sunatCode := strings.TrimSpace(series.SunatCode)
	var contact *database.TenantContact
	if input.ContactID != nil && *input.ContactID > 0 {
		var c database.TenantContact
		if s.db.First(&c, *input.ContactID).Error == nil {
			contact = &c
		}
	}
	if sunatCode == "01" {
		if contact == nil {
			return nil, errors.New("la factura electrónica (01) requiere un cliente con RUC de 11 dígitos")
		}
		if contact.DocType != "6" {
			return nil, errors.New("la factura solo puede emitirse a clientes con RUC (tipo de documento 6). El cliente seleccionado no tiene RUC")
		}
		docNum := strings.TrimSpace(contact.DocNumber)
		if len(docNum) != SunatRucLength {
			return nil, fmt.Errorf("el RUC del cliente debe tener exactamente %d dígitos", SunatRucLength)
		}
		for _, r := range docNum {
			if r < '0' || r > '9' {
				return nil, errors.New("el RUC del cliente debe contener solo dígitos")
			}
		}
	}
	if contact != nil && contact.DocType == "0" && (sunatCode == "03" || sunatCode == "00") {
		if total > SunatMaxMontoClienteSinRUC {
			return nil, fmt.Errorf("según SUNAT, con cliente sin RUC (doc. tipo 0) el monto máximo permitido es S/ %d para boleta o nota de venta. Total actual: S/ %.2f", SunatMaxMontoClienteSinRUC, total)
		}
	}

	// Validar stock y series antes de la transacción
	for _, item := range input.Items {
		if item.ProductID == nil {
			continue
		}
		var product database.TenantProduct
		if s.db.First(&product, *item.ProductID).Error != nil {
			continue
		}
		if product.ManageStock && !productIsCatalogService(&product) {
			var stock database.TenantProductStock
			s.db.Where("product_id = ? AND branch_id = ?", *item.ProductID, input.BranchID).First(&stock)
			if stock.Quantity < item.Quantity {
				return nil, fmt.Errorf("stock insuficiente para %s: requiere %.2f, hay %.2f", item.Description, item.Quantity, stock.Quantity)
			}
		}
		if product.ManageSeries && !productIsCatalogService(&product) {
			n := int(item.Quantity)
			if n > 0 {
				if len(item.Serials) >= n {
					for _, serial := range item.Serials[:n] {
						var ps database.TenantProductSerial
						if err := s.db.Where("product_id = ? AND branch_id = ? AND serial = ? AND status = ?",
							*item.ProductID, input.BranchID, serial, "available").First(&ps).Error; err != nil {
							return nil, fmt.Errorf("el serial '%s' no está disponible o no pertenece al producto", serial)
						}
					}
				} else {
					var count int64
					s.db.Model(&database.TenantProductSerial{}).
						Where("product_id = ? AND branch_id = ? AND status = ?", *item.ProductID, input.BranchID, "available").
						Count(&count)
					if count < int64(n) {
						return nil, fmt.Errorf("no hay suficientes seriales disponibles para %s (requiere %d, hay %d)", item.Description, n, count)
					}
				}
			}
		}
	}

	// Construir lista de pagos: si Payments está vacío, usar PaymentMethod como pago único
	payments := input.Payments
	if len(payments) == 0 && input.PaymentMethod != "" {
		payments = []PaymentInput{{Method: input.PaymentMethod, Amount: total}}
	}
	if total > 0 && len(payments) == 0 {
		return nil, errors.New("debe indicar al menos un método de pago para registrar la venta")
	}
	if len(payments) > 0 {
		var sumPayments float64
		for _, p := range payments {
			sumPayments += p.Amount
		}
		if !money.PaidCoversTotal(sumPayments, total) ||
			money.RoundDisplay(sumPayments) > money.RoundDisplay(total)+money.PaymentTolerance {
			return nil, fmt.Errorf("la suma de pagos (%.2f) no coincide con el total (%.2f)", money.RoundDisplay(sumPayments), money.RoundDisplay(total))
		}
	}
	primaryMethod := input.PaymentMethod
	if len(payments) > 0 {
		primaryMethod = payments[0].Method
	}

	sale := &database.TenantSale{
		BranchID:             input.BranchID,
		ContactID:            input.ContactID,
		UserID:               input.UserID,
		CashSessionID:        input.CashSessionID,
		SeriesID:             input.SeriesID,
		DocType:              input.DocType,
		IssueDate:            input.IssueDate,
		DueDate:              input.DueDate,
		Subtotal:             money.RoundSunat(subtotal),
		TaxAmount:            money.RoundSunat(taxAmount),
		Total:                money.RoundSunat(total),
		Currency:             currency,
		PaymentMethod:        primaryMethod,
		Notes:                input.Notes,
		Status:               "paid",
		BillingStatus:        "pending",
		IssuedFromNotaSaleID: input.IssuedFromNotaSaleID,
	}

	emitFromNV := input.IssuedFromNotaSaleID != nil && *input.IssuedFromNotaSaleID > 0
	// Emisión electrónica desde NV: misma operación comercial; nunca repetir stock/seriales ni caja/bancos.
	skipInv := input.SkipInventory || emitFromNV
	skipPay := input.SkipPaymentDistribution || emitFromNV

	return sale, s.db.Transaction(func(tx *gorm.DB) error {
		correlative, seriesLocked, err := docseries.ReserveNext(tx, input.SeriesID)
		if err != nil {
			return err
		}
		sale.Series = seriesLocked.Series
		sale.Correlative = correlative
		sale.Number = fmt.Sprintf("%s-%08d", seriesLocked.Series, correlative)

		if err := tx.Create(sale).Error; err != nil {
			return err
		}
		for i := range saleItems {
			saleItems[i].SaleID = sale.ID
		}
		if err := tx.Create(&saleItems).Error; err != nil {
			return err
		}

		// Crear TenantSalePayment y distribuir cada pago a caja o cuenta bancaria
		cbSvc := cashbanksvc.NewCashBankService(s.db)
		if !skipPay {
			payLines := make([]cashbanksvc.PaymentLineInput, 0, len(payments))
			for _, p := range payments {
				if p.Amount <= 0 || p.Method == "" {
					continue
				}
				payLines = append(payLines, cashbanksvc.PaymentLineInput{Method: p.Method, Amount: p.Amount})
			}
			resolvedCash, err := cbSvc.ResolveCashSessionForPayments(input.BranchID, input.UserID, input.CashSessionID, payLines)
			if err != nil {
				return err
			}
			input.CashSessionID = resolvedCash
			sale.CashSessionID = resolvedCash
		} else if input.CashSessionID != nil && *input.CashSessionID > 0 {
			if _, err := cbSvc.ValidateCashSessionForUser(*input.CashSessionID, input.UserID, input.BranchID); err != nil {
				return err
			}
		}
		for _, p := range payments {
			if p.Amount <= 0 || p.Method == "" {
				continue
			}
			if err := tx.Create(&database.TenantSalePayment{
				SaleID: sale.ID,
				Method: p.Method,
				Amount: p.Amount,
			}).Error; err != nil {
				return err
			}
			if !skipPay {
				desc := "Venta " + sale.Number
				if err := cbSvc.RecordPayment(tx, p.Method, p.Amount, input.CashSessionID, sale.Number, desc, &sale.ID, input.UserID); err != nil {
					return err
				}
			}
		}

		if skipInv {
			return nil
		}

		// Descontar stock y marcar seriales como vendidos (productos con series)
		for i, item := range input.Items {
			if item.ProductID == nil {
				continue
			}
			var product database.TenantProduct
			if tx.First(&product, *item.ProductID).Error != nil {
				continue
			}
			if !product.ManageStock || productIsCatalogService(&product) {
				continue
			}

			// Actualizar stock
			var stock database.TenantProductStock
			tx.Where("product_id = ? AND branch_id = ?", *item.ProductID, input.BranchID).First(&stock)

			newQty := stock.Quantity - item.Quantity
			if stock.ID == 0 {
				tx.Create(&database.TenantProductStock{
					ProductID: *item.ProductID,
					BranchID:  input.BranchID,
					Quantity:  newQty,
				})
			} else {
				tx.Model(&stock).Update("quantity", newQty)
			}

			// Kardex
			tx.Create(&database.TenantStockMovement{
				ProductID: *item.ProductID,
				BranchID:  input.BranchID,
				Type:      "out",
				Quantity:  item.Quantity,
				Balance:   newQty,
				Reference: "VENTA/" + sale.Number,
				UserID:    input.UserID,
				CreatedAt: time.Now(),
			})

			// Productos con series: marcar los N seriales usados como vendidos (no disponibles para otras ventas)
			if product.ManageSeries {
				n := int(item.Quantity)
				if n <= 0 {
					continue
				}
				var serialsToUse []string
				if len(item.Serials) >= n {
					serialsToUse = item.Serials[:n]
				}
				if len(serialsToUse) == 0 {
					// Comportamiento legacy: tomar los primeros N disponibles
					var serials []database.TenantProductSerial
					if err := tx.Where("product_id = ? AND branch_id = ? AND status = ?", *item.ProductID, input.BranchID, "available").
						Order("id ASC").Limit(n).Find(&serials).Error; err != nil || len(serials) < n {
						return fmt.Errorf("no hay suficientes seriales disponibles para %s (requiere %d, hay %d)", item.Description, n, len(serials))
					}
					for _, ps := range serials {
						serialsToUse = append(serialsToUse, ps.Serial)
					}
				} else {
					// Validar que los seriales indicados existen, están disponibles y pertenecen al producto
					for _, serial := range serialsToUse {
						var ps database.TenantProductSerial
						if err := tx.Where("product_id = ? AND branch_id = ? AND serial = ? AND status = ?",
							*item.ProductID, input.BranchID, serial, "available").First(&ps).Error; err != nil {
							return fmt.Errorf("el serial '%s' no está disponible o no pertenece al producto para %s", serial, item.Description)
						}
					}
				}
				saleItemID := saleItems[i].ID
				for _, serial := range serialsToUse {
					if err := tx.Model(&database.TenantProductSerial{}).
						Where("product_id = ? AND branch_id = ? AND serial = ?", *item.ProductID, input.BranchID, serial).
						Updates(map[string]interface{}{
							"status":        "sold",
							"sale_item_id": saleItemID,
							"updated_at":   time.Now(),
						}).Error; err != nil {
						return err
					}
				}
			}
		}
		return nil
	})
}

func (s *SaleService) GetByID(id uint) (*database.TenantSale, error) {
	var sale database.TenantSale
	if err := s.db.First(&sale, id).Error; err != nil {
		return nil, err
	}
	sales := []database.TenantSale{sale}
	billingstate.EnrichSalesBillingStatus(s.db, sales)
	return &sales[0], nil
}

func (s *SaleService) GetItems(saleID uint) ([]database.TenantSaleItem, error) {
	var items []database.TenantSaleItem
	err := s.db.Where("sale_id = ?", saleID).Find(&items).Error
	return items, err
}

type SaleListParams struct {
	BranchID      uint
	ContactID     uint
	DocType       string
	Status        string
	BillingStatus string
	PaymentMethod string
	PaymentMode   string // all | mixed | single
	// CancelledFilter: vacío = todas; exclude = no anuladas (status != cancelled); only = solo anuladas
	CancelledFilter string
	DateFrom        *time.Time
	DateTo          *time.Time
	Query           string
	SunatCodes      []string
	Limit           int // 0 = sin límite
	Offset          int
}

// SaleListSummary totales sobre todas las ventas que cumplen los filtros (no solo la página).
type SaleListSummary struct {
	SumTotal       float64 `json:"sum_total"`
	SumSubtotal    float64 `json:"sum_subtotal"`
	SumTax         float64 `json:"sum_tax"`
	SumCancelled   float64 `json:"sum_cancelled"`
	SumActive      float64 `json:"sum_active"`
	CountCancelled int64   `json:"count_cancelled"`
	CountActive    int64   `json:"count_active"`
	PaymentTotals  []struct {
		Method string  `json:"method"`
		Total  float64 `json:"total"`
	} `json:"payment_totals"`
}

func (s *SaleService) List(params SaleListParams) ([]database.TenantSale, int64, SaleListSummary, error) {
	var sales []database.TenantSale
	q := s.db.Model(&database.TenantSale{})
	useDistinct := false
	if len(params.SunatCodes) > 0 {
		q = q.Joins("JOIN tenant_document_series ON tenant_document_series.id = tenant_sales.series_id").
			Where("tenant_document_series.sunat_code IN ?", params.SunatCodes)
		useDistinct = true
	}
	if params.BranchID > 0 {
		q = q.Where("tenant_sales.branch_id = ?", params.BranchID)
	}
	if params.ContactID > 0 {
		q = q.Where("tenant_sales.contact_id = ?", params.ContactID)
	}
	if params.DocType != "" {
		q = q.Where("tenant_sales.doc_type = ?", params.DocType)
	}
	switch params.CancelledFilter {
	case "exclude":
		q = q.Where("tenant_sales.status != ?", "cancelled")
	case "only":
		q = q.Where("tenant_sales.status = ?", "cancelled")
	}
	if params.Status != "" {
		q = q.Where("tenant_sales.status = ?", params.Status)
	}
	if params.BillingStatus != "" {
		q = q.Where("tenant_sales.billing_status = ?", params.BillingStatus)
	}
	if params.PaymentMethod != "" {
		m := strings.ToLower(strings.TrimSpace(params.PaymentMethod))
		aliases := []string{m}
		switch m {
		case "card":
			aliases = append(aliases, "tarjeta")
		case "tarjeta":
			aliases = append(aliases, "card")
		case "transfer":
			aliases = append(aliases, "transferencia")
		case "transferencia":
			aliases = append(aliases, "transfer")
		case "cash":
			aliases = append(aliases, "efectivo")
		case "efectivo":
			aliases = append(aliases, "cash")
		case "credit":
			aliases = append(aliases, "credito")
		case "credito":
			aliases = append(aliases, "credit")
		}
		q = q.Where(
			"(LOWER(tenant_sales.payment_method) IN ? OR EXISTS (SELECT 1 FROM tenant_sale_payments tsp WHERE tsp.sale_id = tenant_sales.id AND LOWER(tsp.method) IN ?))",
			aliases, aliases,
		)
	}
	if params.PaymentMode == "mixed" {
		q = q.Where("(SELECT COUNT(1) FROM tenant_sale_payments tsp WHERE tsp.sale_id = tenant_sales.id) > 1")
	}
	if params.PaymentMode == "single" {
		q = q.Where("(SELECT COUNT(1) FROM tenant_sale_payments tsp WHERE tsp.sale_id = tenant_sales.id) <= 1")
	}
	if params.Query != "" {
		query := "%" + strings.TrimSpace(params.Query) + "%"
		q = q.Joins("LEFT JOIN tenant_contacts tc_filter ON tc_filter.id = tenant_sales.contact_id").
			Where(
				"tenant_sales.number LIKE ? OR tenant_sales.series LIKE ? OR CONCAT(tenant_sales.series, '-', tenant_sales.number) LIKE ? OR tc_filter.business_name LIKE ? OR tc_filter.doc_number LIKE ?",
				query, query, query, query, query,
			)
	}
	if params.DateFrom != nil {
		q = q.Where("tenant_sales.issue_date >= ?", params.DateFrom)
	}
	if params.DateTo != nil {
		q = q.Where("tenant_sales.issue_date <= ?", params.DateTo)
	}

	summary, sumErr := s.saleListSummary(q.Session(&gorm.Session{}), useDistinct)
	if sumErr != nil {
		return nil, 0, SaleListSummary{}, sumErr
	}

	var total int64
	if params.Limit > 0 {
		countQ := q
		if useDistinct {
			countQ = countQ.Distinct("tenant_sales.id")
		}
		if err := countQ.Count(&total).Error; err != nil {
			return nil, 0, summary, err
		}
		q = q.Offset(params.Offset).Limit(params.Limit)
	}
	if useDistinct {
		q = q.Distinct("tenant_sales.*")
	}
	err := q.Order("tenant_sales.issue_date DESC, tenant_sales.id DESC").Find(&sales).Error
	if err != nil {
		return sales, total, summary, err
	}
	billingstate.EnrichSalesBillingStatus(s.db, sales)
	// Rellenar nombre del cliente (contact_name)
	if len(sales) > 0 {
		ids := make(map[uint]struct{})
		for _, s := range sales {
			if s.ContactID != nil && *s.ContactID > 0 {
				ids[*s.ContactID] = struct{}{}
			}
		}
		if len(ids) > 0 {
			idList := make([]uint, 0, len(ids))
			for id := range ids {
				idList = append(idList, id)
			}
			var contacts []struct {
				ID           uint
				BusinessName string
			}
			if s.db.Table("tenant_contacts").Select("id, business_name").Where("id IN ?", idList).Find(&contacts).Error == nil {
				byID := make(map[uint]string, len(contacts))
				for _, c := range contacts {
					byID[c.ID] = c.BusinessName
				}
				for i := range sales {
					if sales[i].ContactID != nil {
						if name, ok := byID[*sales[i].ContactID]; ok {
							sales[i].ContactName = name
						}
					}
				}
			}
		}
	}
	onlyNV := len(params.SunatCodes) == 1 && strings.TrimSpace(params.SunatCodes[0]) == "00"
	if onlyNV && len(sales) > 0 {
		parentIDs := make([]uint, len(sales))
		for i := range sales {
			parentIDs[i] = sales[i].ID
		}
		var children []database.TenantSale
		if err := s.db.Select("id", "issued_from_nota_sale_id").Where("issued_from_nota_sale_id IN ?", parentIDs).Find(&children).Error; err == nil {
			byParent := make(map[uint]uint, len(children))
			for _, ch := range children {
				if ch.IssuedFromNotaSaleID != nil {
					byParent[*ch.IssuedFromNotaSaleID] = ch.ID
				}
			}
			for i := range sales {
				if id, ok := byParent[sales[i].ID]; ok {
					sales[i].ElectronicIssueSaleID = &id
				}
			}
		}
	}

	// Normalizar issue_date como fecha de negocio Perú al mediodía para evitar corrimientos de día
	// por parsing/serialización (MySQL DATETIME + loc=Local + clientes en UTC).
	if len(sales) > 0 {
		loc, err := time.LoadLocation("America/Lima")
		if err != nil || loc == nil {
			loc = time.Local
		}
		for i := range sales {
			d := sales[i].IssueDate.In(loc)
			sales[i].IssueDate = time.Date(d.Year(), d.Month(), d.Day(), 12, 0, 0, 0, loc)
		}
	}
	return sales, total, summary, nil
}

// saleListSummary agrega montos sobre el mismo conjunto filtrado que List (sin paginar).
func (s *SaleService) saleListSummary(q *gorm.DB, useDistinct bool) (SaleListSummary, error) {
	var out SaleListSummary
	idSub := q.Select("tenant_sales.id")
	if useDistinct {
		idSub = idSub.Distinct("tenant_sales.id")
	}

	type aggRow struct {
		SumTotal       float64 `gorm:"column:sum_total"`
		SumSubtotal    float64 `gorm:"column:sum_subtotal"`
		SumTax         float64 `gorm:"column:sum_tax"`
		SumCancelled   float64 `gorm:"column:sum_cancelled"`
		SumActive      float64 `gorm:"column:sum_active"`
		CountCancelled int64   `gorm:"column:count_cancelled"`
		CountActive    int64   `gorm:"column:count_active"`
	}
	var row aggRow
	err := s.db.Model(&database.TenantSale{}).
		Where("tenant_sales.id IN (?)", idSub).
		Select(`
			COALESCE(SUM(tenant_sales.total), 0) AS sum_total,
			COALESCE(SUM(tenant_sales.subtotal), 0) AS sum_subtotal,
			COALESCE(SUM(tenant_sales.tax_amount), 0) AS sum_tax,
			COALESCE(SUM(CASE WHEN tenant_sales.status = 'cancelled' THEN tenant_sales.total ELSE 0 END), 0) AS sum_cancelled,
			COALESCE(SUM(CASE WHEN tenant_sales.status != 'cancelled' THEN tenant_sales.total ELSE 0 END), 0) AS sum_active,
			COALESCE(SUM(CASE WHEN tenant_sales.status = 'cancelled' THEN 1 ELSE 0 END), 0) AS count_cancelled,
			COALESCE(SUM(CASE WHEN tenant_sales.status != 'cancelled' THEN 1 ELSE 0 END), 0) AS count_active
		`).
		Scan(&row).Error
	if err != nil {
		return out, err
	}
	out.SumTotal = row.SumTotal
	out.SumSubtotal = row.SumSubtotal
	out.SumTax = row.SumTax
	out.SumCancelled = row.SumCancelled
	out.SumActive = row.SumActive
	out.CountCancelled = row.CountCancelled
	out.CountActive = row.CountActive

	// Totales por método: si hay filas en tenant_sale_pagos, usar montos por línea; si no, el campo cabecera payment_method.
	type payRow struct {
		Method string  `gorm:"column:method"`
		Total  float64 `gorm:"column:total"`
	}
	byMethod := make(map[string]float64)

	var fromPayments []payRow
	err = s.db.Table("tenant_sale_payments tsp").
		Select("LOWER(TRIM(tsp.method)) AS method, COALESCE(SUM(tsp.amount), 0) AS total").
		Joins("JOIN tenant_sales ts ON ts.id = tsp.sale_id").
		Where("ts.id IN (?)", idSub).
		Where("ts.status != ?", "cancelled").
		Group("LOWER(TRIM(tsp.method))").
		Scan(&fromPayments).Error
	if err != nil {
		return out, err
	}
	for _, p := range fromPayments {
		m := strings.TrimSpace(p.Method)
		if m == "" {
			m = "sin_definir"
		}
		byMethod[m] += p.Total
	}

	var fromHeader []payRow
	err = s.db.Model(&database.TenantSale{}).
		Select(`LOWER(TRIM(COALESCE(NULLIF(tenant_sales.payment_method, ''), 'sin_definir'))) AS method, COALESCE(SUM(tenant_sales.total), 0) AS total`).
		Where("tenant_sales.id IN (?)", idSub).
		Where("tenant_sales.status != ?", "cancelled").
		Where("NOT EXISTS (SELECT 1 FROM tenant_sale_payments tsp WHERE tsp.sale_id = tenant_sales.id)").
		Group(`LOWER(TRIM(COALESCE(NULLIF(tenant_sales.payment_method, ''), 'sin_definir')))`).
		Scan(&fromHeader).Error
	if err != nil {
		return out, err
	}
	for _, p := range fromHeader {
		m := strings.TrimSpace(p.Method)
		if m == "" {
			m = "sin_definir"
		}
		byMethod[m] += p.Total
	}

	type kv struct {
		method string
		total  float64
	}
	pairs := make([]kv, 0, len(byMethod))
	for m, t := range byMethod {
		pairs = append(pairs, kv{m, t})
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].total > pairs[j].total })
	if len(pairs) > 12 {
		pairs = pairs[:12]
	}
	for _, p := range pairs {
		out.PaymentTotals = append(out.PaymentTotals, struct {
			Method string  `json:"method"`
			Total  float64 `json:"total"`
		}{Method: p.method, Total: p.total})
	}
	return out, nil
}

// SalesByProductRow es una fila del reporte de ventas por producto.
type SalesByProductRow struct {
	ProductID     uint    `json:"product_id"`
	ProductCode   string  `json:"product_code"`
	ProductName   string  `json:"product_name"`
	CategoryID    *uint   `json:"category_id,omitempty"`
	CategoryName  string  `json:"category_name"`
	Unit          string  `json:"unit"`
	QuantitySold  float64 `json:"quantity_sold"`
	TotalAmount   float64 `json:"total_amount"`
	LinesCount    int64   `json:"lines_count"`
	SalesCount    int64   `json:"sales_count"`
	AvgLineAmount float64 `json:"avg_line_amount"` // total_amount / lines_count (precio medio por línea del producto)
}

// SalesByProductSummary totales del período (mismos filtros que las filas).
type SalesByProductSummary struct {
	TotalAmount     float64 `json:"total_amount"`
	TotalQuantity   float64 `json:"total_quantity"`
	LineItems       int64   `json:"line_items"`
	DistinctSales   int64   `json:"distinct_sales"`
	ProductsCount   int     `json:"products_count"`
}

// SalesByProductParams filtros para el reporte de ventas por producto.
type SalesByProductParams struct {
	DateFrom   *time.Time
	DateTo     *time.Time
	BranchID   uint
	CategoryID uint
}

func (s *SaleService) salesByProductBaseQuery(params SalesByProductParams) *gorm.DB {
	q := s.db.Table("tenant_sale_items").
		Joins("INNER JOIN tenant_sales ON tenant_sales.id = tenant_sale_items.sale_id AND tenant_sales.status != 'cancelled'").
		Joins("LEFT JOIN tenant_products p ON p.id = tenant_sale_items.product_id").
		Joins("LEFT JOIN tenant_categories c ON c.id = p.category_id")
	if params.DateFrom != nil {
		q = q.Where("tenant_sales.issue_date >= ?", params.DateFrom)
	}
	if params.DateTo != nil {
		q = q.Where("tenant_sales.issue_date <= ?", params.DateTo)
	}
	if params.BranchID > 0 {
		q = q.Where("tenant_sales.branch_id = ?", params.BranchID)
	}
	if params.CategoryID > 0 {
		q = q.Where("p.category_id = ?", params.CategoryID)
	}
	return q
}

// SalesByProduct agrupa ítems de ventas por producto (solo ventas no anuladas).
func (s *SaleService) SalesByProduct(params SalesByProductParams) ([]SalesByProductRow, SalesByProductSummary, error) {
	type row struct {
		ProductID    uint
		ProductCode  string
		ProductName  string
		CategoryID   *uint
		CategoryName string
		Unit         string
		QuantitySold float64
		TotalAmount  float64
		LinesCount   int64
		SalesCount   int64
	}

	var meta struct {
		DistinctSales int64
		LineItems     int64
	}
	qMeta := s.salesByProductBaseQuery(params).
		Select("COUNT(DISTINCT tenant_sale_items.sale_id) as distinct_sales, COUNT(*) as line_items")
	if err := qMeta.Scan(&meta).Error; err != nil {
		return nil, SalesByProductSummary{}, err
	}

	q := s.salesByProductBaseQuery(params).
		Select(`COALESCE(tenant_sale_items.product_id, 0) as product_id,
			COALESCE(MAX(p.code), '') as product_code,
			COALESCE(MAX(NULLIF(TRIM(p.name), '')), MAX(tenant_sale_items.description)) as product_name,
			MAX(p.category_id) as category_id,
			COALESCE(MAX(c.name), '') as category_name,
			COALESCE(MAX(NULLIF(TRIM(p.unit), '')), MAX(tenant_sale_items.unit)) as unit,
			SUM(tenant_sale_items.quantity) as quantity_sold,
			SUM(tenant_sale_items.total) as total_amount,
			COUNT(*) as lines_count,
			COUNT(DISTINCT tenant_sale_items.sale_id) as sales_count`).
		Group("tenant_sale_items.product_id")

	var raw []row
	if err := q.Scan(&raw).Error; err != nil {
		return nil, SalesByProductSummary{}, err
	}

	out := make([]SalesByProductRow, len(raw))
	var sumAmt, sumQty float64
	for i, r := range raw {
		catName := strings.TrimSpace(r.CategoryName)
		if catName == "" {
			catName = "Sin categoría"
		}
		avgLine := float64(0)
		if r.LinesCount > 0 {
			avgLine = r.TotalAmount / float64(r.LinesCount)
		}
		out[i] = SalesByProductRow{
			ProductID:     r.ProductID,
			ProductCode:   r.ProductCode,
			ProductName:   r.ProductName,
			CategoryID:    r.CategoryID,
			CategoryName:  catName,
			Unit:          strings.TrimSpace(r.Unit),
			QuantitySold:  r.QuantitySold,
			TotalAmount:   r.TotalAmount,
			LinesCount:    r.LinesCount,
			SalesCount:    r.SalesCount,
			AvgLineAmount: avgLine,
		}
		sumAmt += r.TotalAmount
		sumQty += r.QuantitySold
	}

	sort.Slice(out, func(i, j int) bool {
		if out[i].CategoryName != out[j].CategoryName {
			return out[i].CategoryName < out[j].CategoryName
		}
		if out[i].TotalAmount != out[j].TotalAmount {
			return out[i].TotalAmount > out[j].TotalAmount
		}
		return out[i].ProductName < out[j].ProductName
	})

	summary := SalesByProductSummary{
		TotalAmount:    sumAmt,
		TotalQuantity:  sumQty,
		LineItems:      meta.LineItems,
		DistinctSales:  meta.DistinctSales,
		ProductsCount:  len(out),
	}
	return out, summary, nil
}

func (s *SaleService) Cancel(id uint) error {
	var sale database.TenantSale
	if err := s.db.First(&sale, id).Error; err != nil {
		return err
	}
	if sale.Status == "cancelled" {
		return errors.New("la venta ya está cancelada")
	}

	items, err := s.GetItems(id)
	if err != nil {
		return err
	}

	ref := "ANULACION VENTA/" + sale.Number

	return s.db.Transaction(func(tx *gorm.DB) error {
		for _, item := range items {
			if item.ProductID == nil {
				continue
			}
			var product database.TenantProduct
			if tx.First(&product, *item.ProductID).Error != nil {
				continue
			}
			if !product.ManageStock || productIsCatalogService(&product) {
				continue
			}

			// Restaurar stock (sumar la cantidad vendida)
			var stock database.TenantProductStock
			tx.Where("product_id = ? AND branch_id = ?", *item.ProductID, sale.BranchID).First(&stock)
			newQty := stock.Quantity + item.Quantity
			if stock.ID == 0 {
				if err := tx.Create(&database.TenantProductStock{
					ProductID: *item.ProductID,
					BranchID:  sale.BranchID,
					Quantity:  newQty,
				}).Error; err != nil {
					return err
				}
			} else {
				if err := tx.Model(&stock).Updates(map[string]interface{}{
					"quantity":   newQty,
					"updated_at": time.Now(),
				}).Error; err != nil {
					return err
				}
			}

			// Kardex: entrada por anulación
			if err := tx.Create(&database.TenantStockMovement{
				ProductID: *item.ProductID,
				BranchID:  sale.BranchID,
				Type:      "in",
				Quantity:  item.Quantity,
				Balance:   newQty,
				Reference: ref,
				UserID:    sale.UserID,
				CreatedAt: time.Now(),
			}).Error; err != nil {
				return err
			}

			// Productos con series: marcar seriales de este ítem como disponibles nuevamente
			if product.ManageSeries && !productIsCatalogService(&product) {
				if err := tx.Model(&database.TenantProductSerial{}).
					Where("sale_item_id = ?", item.ID).
					Updates(map[string]interface{}{
						"status":      "available",
						"sale_item_id": nil,
						"updated_at":  time.Now(),
					}).Error; err != nil {
					return err
				}
			}
		}

		return tx.Model(&sale).Update("status", "cancelled").Error
	})
}

func parseIssueDateForSale(issueYMD string, fallback time.Time) time.Time {
	s := strings.TrimSpace(issueYMD)
	if s == "" {
		return fallback
	}
	loc, err := time.LoadLocation("America/Lima")
	if err != nil || loc == nil {
		loc = time.Local
	}
	if t, err := time.ParseInLocation("2006-01-02", s, loc); err == nil {
		return time.Date(t.Year(), t.Month(), t.Day(), 12, 0, 0, 0, loc)
	}
	return fallback
}

// GetPayments devuelve los pagos registrados de una venta.
func (s *SaleService) GetPayments(saleID uint) ([]database.TenantSalePayment, error) {
	var rows []database.TenantSalePayment
	err := s.db.Where("sale_id = ?", saleID).Order("id ASC").Find(&rows).Error
	return rows, err
}

// IssueElectronicFromNota crea el registro de factura/boleta (01/03) para SUNAT copiando líneas y pagos de la NV (00).
// No es una “segunda venta” en contabilidad de inventario: IssuedFromNotaSaleID fuerza omitir stock, seriales y caja/bancos.
func (s *SaleService) IssueElectronicFromNota(notaSaleID uint, targetSeriesID uint, userID uint, issueYMD string) (*database.TenantSale, error) {
	var nota database.TenantSale
	if err := s.db.First(&nota, notaSaleID).Error; err != nil {
		return nil, errors.New("nota de venta no encontrada")
	}
	if strings.EqualFold(strings.TrimSpace(nota.Status), "cancelled") {
		return nil, errors.New("la nota de venta está anulada")
	}
	var nvSeries database.TenantDocumentSeries
	if err := s.db.First(&nvSeries, nota.SeriesID).Error; err != nil {
		return nil, errors.New("serie de la nota no encontrada")
	}
	if strings.TrimSpace(nvSeries.SunatCode) != "00" {
		return nil, errors.New("solo se puede emitir comprobante electrónico a partir de una nota de venta (SUNAT 00)")
	}
	var dup int64
	if err := s.db.Model(&database.TenantSale{}).Where("issued_from_nota_sale_id = ?", notaSaleID).Count(&dup).Error; err != nil {
		return nil, err
	}
	if dup > 0 {
		return nil, errors.New("esta nota de venta ya tiene un comprobante electrónico (factura o boleta) emitido")
	}
	var target database.TenantDocumentSeries
	if err := s.db.First(&target, targetSeriesID).Error; err != nil {
		return nil, errors.New("serie destino no encontrada")
	}
	code := strings.TrimSpace(target.SunatCode)
	if code != "01" && code != "03" {
		return nil, errors.New("la serie destino debe ser factura (01) o boleta (03)")
	}
	if target.BranchID != nota.BranchID {
		return nil, errors.New("la serie debe pertenecer a la misma sucursal que la nota de venta")
	}
	if !target.Active || !strings.EqualFold(strings.TrimSpace(target.Category), "venta") {
		return nil, errors.New("la serie destino debe estar activa y ser de categoría venta")
	}
	items, err := s.GetItems(nota.ID)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, errors.New("la nota de venta no tiene líneas")
	}
	var inputs []SaleItemInput
	for _, it := range items {
		inputs = append(inputs, SaleItemInput{
			ProductID:          it.ProductID,
			Code:               it.Code,
			Description:        it.Description,
			Unit:               it.Unit,
			Quantity:           it.Quantity,
			UnitPrice:          it.UnitPrice,
			Discount:           it.Discount,
			IgvAffectationType: it.IgvAffectationType,
			PriceIncludesIgv:   false,
			ModifiersJSON:      it.ModifiersJSON,
		})
	}
	paymentsDB, err := s.GetPayments(nota.ID)
	if err != nil {
		return nil, err
	}
	var pays []PaymentInput
	for _, p := range paymentsDB {
		if p.Amount > 0 && strings.TrimSpace(p.Method) != "" {
			pays = append(pays, PaymentInput{Method: p.Method, Amount: p.Amount})
		}
	}
	taxCfg := tax.LoadFromDB(s.db)
	issueAt := parseIssueDateForSale(issueYMD, nota.IssueDate)
	nvRef := strings.TrimSpace(nota.Series) + "-" + strings.TrimSpace(nota.Number)
	notes := strings.TrimSpace(nota.Notes)
	if notes != "" {
		notes = "Referencia NV " + nvRef + ". " + notes
	} else {
		notes = "Referencia NV " + nvRef + "."
	}
	nvID := notaSaleID
	return s.Create(CreateSaleInput{
		BranchID:                nota.BranchID,
		ContactID:               nota.ContactID,
		UserID:                  userID,
		CashSessionID:           nil,
		SeriesID:                targetSeriesID,
		DocType:                 strings.TrimSpace(target.DocType),
		IssueDate:               issueAt,
		DueDate:                 nota.DueDate,
		Currency:                nota.Currency,
		PaymentMethod:           "",
		Payments:                pays,
		Notes:                   notes,
		Items:                   inputs,
		TaxConfig:               taxCfg,
		SkipInventory:           true,
		SkipPaymentDistribution: true,
		IssuedFromNotaSaleID:    &nvID,
	})
}

// SummaryStats retorna estadísticas resumidas de ventas.
func (s *SaleService) SummaryStats(branchID uint, from, to time.Time) map[string]interface{} {
	q := s.db.Model(&database.TenantSale{}).
		Where("issue_date >= ? AND issue_date <= ? AND status != ?", from, to, "cancelled")
	if branchID > 0 {
		q = q.Where("branch_id = ?", branchID)
	}

	var count int64
	var totalAmount float64
	q.Count(&count)
	q.Select("COALESCE(SUM(total), 0)").Scan(&totalAmount)

	return map[string]interface{}{
		"count":  count,
		"total":  totalAmount,
	}
}
