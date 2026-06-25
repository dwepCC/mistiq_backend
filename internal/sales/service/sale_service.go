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
	"tukifac/pkg/paymentcondition"
	"tukifac/pkg/taxpayment"
	"tukifac/pkg/saas/docusage"
	"tukifac/pkg/salecurrency"
	"tukifac/pkg/salescope"
	"tukifac/pkg/sunat"
	detraccionpkg "tukifac/pkg/sunat/detraccion"
	"tukifac/pkg/tax"
	cashbanksvc "tukifac/internal/cashbank/service"
	detraccionsvc "tukifac/internal/detraccion"
	salecontext "tukifac/internal/fiscal/salecontext"
	"tukifac/internal/sales/nvdisplay"

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
	Currency          string
	OperationTypeCode string
	ExchangeRate      *float64
	PaymentMethod     string   // legacy: si Payments vacío, se usa para el total
	Payments      []PaymentInput `json:"payments"` // múltiples métodos de pago
	Notes         string
	Items         []SaleItemInput
	TaxConfig     tax.Config // configuración tributaria de la empresa
	// Emisión desde nota de venta (no descontar inventario ni duplicar caja/bancos).
	SkipInventory             bool
	SkipPaymentDistribution   bool
	IssuedFromNotaSaleID      *uint // ID de la NV origen; se guarda en la nueva venta 01/03
	IssuedFromQuotationID     *uint // ID de la cotización origen
	CentralTenantID           uint  // tenant SaaS (cupo de documentos electrónicos)
	FiscalContext             *salecontext.FiscalContextInput
	Detraccion                *detraccionsvc.SaleInput
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
	if err := docusage.GuardCountableSunatQuota(input.CentralTenantID, series.SunatCode); err != nil {
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

	currency, err := salecurrency.NormalizeCurrency(input.Currency)
	if err != nil {
		return nil, err
	}
	opCode, err := salecurrency.NormalizeOperationType(input.OperationTypeCode)
	if err != nil {
		return nil, err
	}
	exchangeRate, err := salecurrency.NormalizeExchangeRate(currency, input.ExchangeRate)
	if err != nil {
		return nil, err
	}

	sunatCode := strings.TrimSpace(series.SunatCode)
	if opCode == salecurrency.OpDetraccion {
		if sunatCode != "01" {
			return nil, errors.New("la operación sujeta a detracción (1001) solo aplica a facturas (01)")
		}
		if currency != salecurrency.CurrencyPEN {
			return nil, errors.New("la detracción requiere moneda PEN en la factura")
		}
		if input.Detraccion == nil || strings.TrimSpace(input.Detraccion.GoodCode) == "" {
			return nil, errors.New("seleccione el bien o servicio sujeto a detracción")
		}
		if input.FiscalContext != nil && input.FiscalContext.HasIgvRetention != nil && *input.FiscalContext.HasIgvRetention {
			return nil, errors.New("no se puede combinar detracción con retención IGV en la misma factura")
		}
	}
	if opCode != salecurrency.OpDetraccion && input.Detraccion != nil {
		return nil, errors.New("datos de detracción solo aplican con tipo de operación 1001")
	}

	// Validaciones SUNAT: Factura 01 solo con RUC de 11 dígitos; doc. tipo 0 máximo S/ 700 en boleta/nota de venta
	if sunatCode == "01" || sunatCode == "03" {
		var companyCfg database.TenantCompanyConfig
		if err := s.db.Select("sunat_enabled").First(&companyCfg).Error; err != nil || !companyCfg.SunatEnabled {
			return nil, errors.New("la facturación electrónica no está habilitada para este tenant; solo puede emitir notas de venta (SUNAT 00)")
		}
	}
	var contact *database.TenantContact
	if input.ContactID != nil && *input.ContactID > 0 {
		var c database.TenantContact
		if s.db.First(&c, *input.ContactID).Error == nil {
			contact = &c
		}
	}
	if opCode == salecurrency.OpDetraccion && contact != nil && contact.EsAgenteDePercepcion {
		return nil, errors.New("no se permite detracción con cliente agente de percepción")
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

	// Validar stock y series antes de la transacción (omitir si la NV ya descontó inventario).
	emitFromNV := input.IssuedFromNotaSaleID != nil && *input.IssuedFromNotaSaleID > 0
	skipStockCheck := input.SkipInventory || emitFromNV
	if !skipStockCheck {
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
	}

	// Construir lista de pagos: si Payments está vacío, usar PaymentMethod como pago único
	payments := input.Payments
	if len(payments) == 0 && input.PaymentMethod != "" {
		payments = []PaymentInput{{Method: input.PaymentMethod, Amount: total}}
	}
	if total > 0 && len(payments) == 0 {
		if input.DueDate == nil {
			return nil, errors.New("debe indicar al menos un método de pago para registrar la venta")
		}
	}

	isCreditSale := false
	if emitFromNV && total > 0 && len(payments) > 0 {
		var sumPayments float64
		for _, p := range payments {
			sumPayments += p.Amount
		}
		if money.RoundDisplay(sumPayments) != money.RoundDisplay(total) {
			payments = alignPaymentsToSaleTotal(payments, total)
		}
	}
	if opCode == salecurrency.OpDetraccion && total > 0 {
		eval, err := s.evaluateDetractionForCreate(input, &series, total, saleItems, contact)
		if err != nil {
			return nil, err
		}
		if len(payments) > 0 || input.DueDate != nil {
			var credit bool
			payments, credit, err = PrepareDetractionSalePaymentsAllowCredit(payments, total, eval)
			if err != nil {
				return nil, err
			}
			isCreditSale = credit
		} else {
			return nil, errors.New("debe indicar pagos o fecha de vencimiento para la venta con detracción")
		}
	} else if len(payments) > 0 {
		var sumPayments float64
		for _, p := range payments {
			if paymentcondition.IsCreditCode(p.Method) {
				continue
			}
			sumPayments += p.Amount
		}
		if money.RoundDisplay(sumPayments) > money.RoundDisplay(total)+money.PaymentTolerance {
			return nil, fmt.Errorf("la suma de pagos (%.2f) supera el total (%.2f)", money.RoundDisplay(sumPayments), money.RoundDisplay(total))
		}
		if !money.PaidCoversTotal(sumPayments, total) {
			if input.DueDate == nil {
				return nil, fmt.Errorf("la suma de pagos (%.2f) no coincide con el total (%.2f)", money.RoundDisplay(sumPayments), money.RoundDisplay(total))
			}
			isCreditSale = true
		}
	} else if total > 0 && input.DueDate != nil {
		isCreditSale = true
	}
	primaryMethod := PrimaryDirectPaymentMethod(payments, input.PaymentMethod)
	if isCreditSale && primaryMethod == "" {
		primaryMethod = paymentcondition.CodeCredit
	}

	saleOrigin := salescope.SaleOriginDirect
	if emitFromNV {
		saleOrigin = salescope.SaleOriginConvertedFromNota
	}

	saleStatus := "paid"
	if isCreditSale {
		saleStatus = "credit"
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
		OperationTypeCode:    opCode,
		ExchangeRate:         exchangeRate,
		PaymentMethod:        primaryMethod,
		SaleOrigin:           saleOrigin,
		Notes:                input.Notes,
		Status:               saleStatus,
		BillingStatus:        "pending",
		IssuedFromNotaSaleID:    input.IssuedFromNotaSaleID,
		IssuedFromQuotationID:   input.IssuedFromQuotationID,
	}

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
			resolvedCash, err := cbSvc.ResolveCashSessionForSale(input.BranchID, input.UserID, input.CashSessionID, payLines)
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
			if err := s.persistFiscalContextTx(tx, sale, input, seriesLocked); err != nil {
				return err
			}
			if err := s.persistDetraccionTx(tx, sale, input, seriesLocked, saleItems); err != nil {
				return err
			}
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
		if err := s.persistFiscalContextTx(tx, sale, input, seriesLocked); err != nil {
			return err
		}
		if err := s.persistDetraccionTx(tx, sale, input, seriesLocked, saleItems); err != nil {
			return err
		}
		return nil
	})
}

func (s *SaleService) persistFiscalContextTx(tx *gorm.DB, sale *database.TenantSale, input CreateSaleInput, series database.TenantDocumentSeries) error {
	if input.FiscalContext == nil {
		return nil
	}
	var contactSnap *salecontext.ContactSnapshot
	if input.ContactID != nil {
		var c database.TenantContact
		if tx.First(&c, *input.ContactID).Error == nil {
			contactSnap = salecontext.ContactFromModel(&c)
		}
	}
	currency := strings.TrimSpace(input.Currency)
	if currency == "" {
		currency = "PEN"
	}
	_, err := salecontext.NewService(tx).Persist(salecontext.PersistInput{
		SaleID:        sale.ID,
		UserID:        input.UserID,
		SunatDocCode:  salecontext.SunatCodeFromSeries(&series, input.DocType),
		SaleTotal:     sale.Total,
		Currency:      currency,
		ExchangeRate:  sale.ExchangeRate,
		Contact:       contactSnap,
		FiscalContext: input.FiscalContext,
	})
	return err
}

func (s *SaleService) persistDetraccionTx(
	tx *gorm.DB,
	sale *database.TenantSale,
	input CreateSaleInput,
	series database.TenantDocumentSeries,
	saleItems []database.TenantSaleItem,
) error {
	if strings.TrimSpace(input.OperationTypeCode) != salecurrency.OpDetraccion {
		return nil
	}
	var companyCfg database.TenantCompanyConfig
	if err := tx.First(&companyCfg).Error; err != nil {
		return errors.New("configure los datos de la empresa antes de emitir con detracción")
	}
	paymentMethod := strings.TrimSpace(companyCfg.DetractionDefaultPaymentMethod)
	if paymentMethod == "" {
		paymentMethod = "001"
	}
	var contactEsPercepcion bool
	if input.ContactID != nil {
		var c database.TenantContact
		if tx.First(&c, *input.ContactID).Error == nil {
			contactEsPercepcion = c.EsAgenteDePercepcion
		}
	}
	affItems := make([]detraccionpkg.ItemAffectation, 0, len(saleItems))
	for _, it := range saleItems {
		affItems = append(affItems, detraccionpkg.ItemAffectation{
			IgvAffectationType: it.IgvAffectationType,
			Total:              it.Total,
		})
	}
	gravadoTotal := detraccionpkg.GravadoTotalFromItems(affItems)
	_, err := detraccionsvc.NewService(tx).Persist(detraccionsvc.PersistInput{
		SaleID:              sale.ID,
		OperationTypeCode:   input.OperationTypeCode,
		SunatDocCode:        salecontext.SunatCodeFromSeries(&series, input.DocType),
		Currency:            sale.Currency,
		ExchangeRate:        sale.ExchangeRate,
		SaleTotal:           sale.Total,
		GravadoTotal:        gravadoTotal,
		BankAccount:         companyCfg.DetractionBNAccount,
		PaymentMethodCode:   paymentMethod,
		Detraccion:          input.Detraccion,
		ContactEsPercepcion: contactEsPercepcion,
	})
	return err
}

func (s *SaleService) evaluateDetractionForCreate(
	input CreateSaleInput,
	series *database.TenantDocumentSeries,
	total float64,
	saleItems []database.TenantSaleItem,
	contact *database.TenantContact,
) (detraccionpkg.CalcResult, error) {
	var companyCfg database.TenantCompanyConfig
	if err := s.db.First(&companyCfg).Error; err != nil {
		return detraccionpkg.CalcResult{}, errors.New("configure los datos de la empresa antes de emitir con detracción")
	}
	paymentMethod := strings.TrimSpace(companyCfg.DetractionDefaultPaymentMethod)
	if paymentMethod == "" {
		paymentMethod = "001"
	}
	var contactEsPercepcion bool
	if contact != nil {
		contactEsPercepcion = contact.EsAgenteDePercepcion
	}
	affItems := make([]detraccionpkg.ItemAffectation, 0, len(saleItems))
	for _, it := range saleItems {
		affItems = append(affItems, detraccionpkg.ItemAffectation{
			IgvAffectationType: it.IgvAffectationType,
			Total:              it.Total,
		})
	}
	gravadoTotal := detraccionpkg.GravadoTotalFromItems(affItems)
	currency := strings.TrimSpace(input.Currency)
	if currency == "" {
		currency = salecurrency.CurrencyPEN
	}
	return detraccionsvc.NewService(s.db).Evaluate(detraccionsvc.PersistInput{
		OperationTypeCode:   input.OperationTypeCode,
		SunatDocCode:        salecontext.SunatCodeFromSeries(series, input.DocType),
		Currency:            currency,
		ExchangeRate:        input.ExchangeRate,
		SaleTotal:           total,
		GravadoTotal:        gravadoTotal,
		BankAccount:         companyCfg.DetractionBNAccount,
		PaymentMethodCode:   paymentMethod,
		Detraccion:          input.Detraccion,
		ContactEsPercepcion: contactEsPercepcion,
	})
}

// GetFiscalContext carga información adicional fiscal de una venta.
func (s *SaleService) GetFiscalContext(saleID uint) (*salecontext.FiscalContextOutput, error) {
	sale, err := s.GetByID(saleID)
	if err != nil {
		return nil, err
	}
	out, err := salecontext.NewService(s.db).Load(saleID, sale.Total)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return out, err
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
	SumDetraccion  float64 `json:"sum_detraccion"`
	SumNetPayable  float64 `json:"sum_net_payable"`
	CountDetraccion int64  `json:"count_detraccion"`
	SpotTotal      float64 `json:"spot_total"`
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
		bs := strings.TrimSpace(params.BillingStatus)
		if strings.Contains(bs, ",") {
			parts := make([]string, 0, 4)
			for _, p := range strings.Split(bs, ",") {
				p = strings.TrimSpace(strings.ToLower(p))
				if p != "" {
					parts = append(parts, p)
				}
			}
			if len(parts) > 0 {
				q = q.Where("tenant_sales.billing_status IN ?", parts)
			}
		} else {
			q = q.Where("tenant_sales.billing_status = ?", bs)
		}
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
	s.enrichSalesWithDetraccion(sales)
	nvdisplay.EnrichSales(s.db, sales)

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
	err := salescope.CommercialSales(s.db.Model(&database.TenantSale{})).
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

	type detAggRow struct {
		SumDetraccion   float64 `gorm:"column:sum_detraccion"`
		SumNetPayable   float64 `gorm:"column:sum_net_payable"`
		CountDetraccion int64   `gorm:"column:count_detraccion"`
	}
	var detRow detAggRow
	if err := s.db.Table("tenant_sale_detraccion d").
		Select(`
			COALESCE(SUM(d.detraction_amount_pen), 0) AS sum_detraccion,
			COALESCE(SUM(d.net_payable_pen), 0) AS sum_net_payable,
			COUNT(*) AS count_detraccion
		`).
		Joins("JOIN tenant_sales ts ON ts.id = d.sale_id").
		Scopes(salescope.ScopeCommercial("ts")).
		Where("ts.id IN (?)", idSub).
		Where("ts.status != ?", "cancelled").
		Scan(&detRow).Error; err != nil {
		return out, err
	}
	out.SumDetraccion = detRow.SumDetraccion
	out.SumNetPayable = detRow.SumNetPayable
	out.CountDetraccion = detRow.CountDetraccion

	// Totales por método: si hay filas en tenant_sale_pagos, usar montos por línea; si no, el campo cabecera payment_method.
	type payRow struct {
		Method string  `gorm:"column:method"`
		Total  float64 `gorm:"column:total"`
	}
	byMethod := make(map[string]float64)
	var spotTotal float64

	var fromPayments []payRow
	err = s.db.Table("tenant_sale_payments tsp").
		Select("LOWER(TRIM(tsp.method)) AS method, COALESCE(SUM(tsp.amount), 0) AS total").
		Joins("JOIN tenant_sales ts ON ts.id = tsp.sale_id").
		Scopes(salescope.ScopeCommercial("ts")).
		Where("ts.id IN (?)", idSub).
		Where("ts.status != ?", "cancelled").
		Group("LOWER(TRIM(tsp.method))").
		Scan(&fromPayments).Error
	if err != nil {
		return out, err
	}
	for _, p := range fromPayments {
		if taxpayment.IsDetractionCode(p.Method) {
			spotTotal += p.Total
			continue
		}
		m := strings.TrimSpace(p.Method)
		if m == "" {
			m = "sin_definir"
		}
		byMethod[m] += p.Total
	}

	var fromHeader []payRow
	err = salescope.CommercialSales(s.db.Model(&database.TenantSale{})).
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
	if spotTotal > 0 {
		out.SpotTotal = spotTotal
		out.PaymentTotals = append(out.PaymentTotals, struct {
			Method string  `json:"method"`
			Total  float64 `json:"total"`
		}{Method: taxpayment.CodeDetraccionBN, Total: spotTotal})
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
		Joins("LEFT JOIN tenant_categories c ON c.id = p.category_id").
		Scopes(salescope.ScopeCommercial("tenant_sales"))
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

func (s *SaleService) CancelNotaVenta(id uint, userID uint, reason string) error {
	var sale database.TenantSale
	if err := s.db.First(&sale, id).Error; err != nil {
		return err
	}
	if sale.Status == "cancelled" {
		return errors.New("la venta ya está cancelada")
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return errors.New("indique el motivo de anulación")
	}

	var saleSeries database.TenantDocumentSeries
	if err := s.db.First(&saleSeries, sale.SeriesID).Error; err != nil {
		return errors.New("serie del comprobante no encontrada")
	}
	sunatCode := strings.TrimSpace(saleSeries.SunatCode)
	docType := strings.ToUpper(strings.TrimSpace(sale.DocType))
	if sunatCode != "00" && docType != "NOTA_VENTA" && !strings.Contains(docType, "NOTA") {
		return errors.New("solo se pueden anular notas de venta desde esta operación; para facturas o boletas use nota de crédito")
	}
	if sale.DocType == "FACTURA" || sale.DocType == "BOLETA" || sunatCode == "01" || sunatCode == "03" {
		return errors.New("para anular facturas o boletas debe usar nota de crédito electrónica")
	}
	var electronicChild int64
	if err := s.db.Model(&database.TenantSale{}).Where("issued_from_nota_sale_id = ?", id).Count(&electronicChild).Error; err != nil {
		return err
	}
	if electronicChild > 0 {
		return errors.New("no se puede anular: esta nota ya tiene factura o boleta electrónica emitida")
	}

	items, err := s.GetItems(id)
	if err != nil {
		return err
	}

	ref := "ANULACION VENTA/" + sale.Number
	cashSvc := cashbanksvc.NewCashBankService(s.db)

	return s.db.Transaction(func(tx *gorm.DB) error {
		var incomeMovements []database.TenantCashMovement
		if err := tx.Where("sale_id = ? AND type = ?", id, "income").Find(&incomeMovements).Error; err != nil {
			return err
		}
		for _, m := range incomeMovements {
			uid := userID
			if uid == 0 {
				uid = m.UserID
			}
			if err := tx.Create(&database.TenantCashMovement{
				CashSessionID: m.CashSessionID,
				Type:          "expense",
				Amount:        m.Amount,
				PaymentMethod: m.PaymentMethod,
				Category:      "Anulación venta",
				Reference:     ref,
				SaleID:        &id,
				Notes:         reason,
				UserID:        uid,
				CreatedAt:     time.Now(),
			}).Error; err != nil {
				return err
			}
		}
		if len(incomeMovements) == 0 && sale.Total > 0 {
			// Ventas sin movimiento de caja indexado: revertir pagos registrados en sesión original.
			var payments []database.TenantSalePayment
			tx.Where("sale_id = ?", id).Find(&payments)
			sessionID := sale.CashSessionID
			for _, p := range payments {
				if p.Amount <= 0 {
					continue
				}
				pm, _ := cashSvc.GetPaymentMethodByCode(p.Method)
				if pm != nil && pm.DestinationType == "cash" && sessionID != nil && *sessionID > 0 {
					uid := userID
					if uid == 0 {
						uid = sale.UserID
					}
					if err := tx.Create(&database.TenantCashMovement{
						CashSessionID: *sessionID,
						Type:          "expense",
						Amount:        p.Amount,
						PaymentMethod: p.Method,
						Category:      "Anulación venta",
						Reference:     ref,
						SaleID:        &id,
						Notes:         reason,
						UserID:        uid,
						CreatedAt:     time.Now(),
					}).Error; err != nil {
						return err
					}
				}
			}
		}
		var bankCredits []database.TenantBankMovement
		if err := tx.Where("reference = ? AND type = ?", sale.Number, "credit").Find(&bankCredits).Error; err != nil {
			return err
		}
		for _, bm := range bankCredits {
			uid := userID
			if uid == 0 {
				uid = bm.UserID
			}
			desc := "Anulación venta " + sale.Number + ": " + reason
			if err := tx.Create(&database.TenantBankMovement{
				BankAccountID: bm.BankAccountID,
				Type:          "debit",
				Amount:        bm.Amount,
				Description:   desc,
				Reference:     ref,
				Date:          time.Now(),
				UserID:        uid,
				CreatedAt:     time.Now(),
			}).Error; err != nil {
				return err
			}
			if err := tx.Model(&database.TenantBankAccount{}).
				Where("id = ?", bm.BankAccountID).
				Update("balance", gorm.Expr("balance - ?", bm.Amount)).Error; err != nil {
				return err
			}
		}

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

		cancelNotes := strings.TrimSpace(sale.Notes)
		if cancelNotes != "" {
			cancelNotes = cancelNotes + " | ANULADA: " + reason
		} else {
			cancelNotes = "ANULADA: " + reason
		}
		return tx.Model(&sale).Updates(map[string]interface{}{
			"status": "cancelled",
			"notes":  cancelNotes,
		}).Error
	})
}

// Cancel marca la venta como anulada y revierte stock (uso interno: NC aceptada, panel legacy).
// No revierte caja ni valida tipo de comprobante.
func (s *SaleService) Cancel(id uint, userID uint, reason string) error {
	var sale database.TenantSale
	if err := s.db.First(&sale, id).Error; err != nil {
		return err
	}
	if sale.Status == "cancelled" {
		return errors.New("la venta ya está cancelada")
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "Anulación de venta"
	}
	items, err := s.GetItems(id)
	if err != nil {
		return err
	}
	ref := "ANULACION VENTA/" + sale.Number
	_ = userID
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
			if product.ManageSeries && !productIsCatalogService(&product) {
				if err := tx.Model(&database.TenantProductSerial{}).
					Where("sale_item_id = ?", item.ID).
					Updates(map[string]interface{}{
						"status":       "available",
						"sale_item_id": nil,
						"updated_at":   time.Now(),
					}).Error; err != nil {
					return err
				}
			}
		}
		cancelNotes := strings.TrimSpace(sale.Notes)
		if cancelNotes != "" {
			cancelNotes = cancelNotes + " | ANULADA: " + reason
		} else {
			cancelNotes = "ANULADA: " + reason
		}
		return tx.Model(&sale).Updates(map[string]interface{}{
			"status": "cancelled",
			"notes":  cancelNotes,
		}).Error
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

func alignPaymentsToSaleTotal(pays []PaymentInput, total float64) []PaymentInput {
	roundedTotal := money.RoundSunat(total)
	if len(pays) == 0 {
		return []PaymentInput{{Method: "cash", Amount: roundedTotal}}
	}
	if len(pays) == 1 {
		return []PaymentInput{{Method: pays[0].Method, Amount: roundedTotal}}
	}
	var sum float64
	for _, p := range pays {
		sum += p.Amount
	}
	if sum <= 0 {
		return []PaymentInput{{Method: pays[0].Method, Amount: roundedTotal}}
	}
	out := make([]PaymentInput, 0, len(pays))
	var allocated float64
	for i, p := range pays {
		amt := money.RoundSunat(p.Amount * roundedTotal / sum)
		if i == len(pays)-1 {
			amt = money.RoundSunat(roundedTotal - allocated)
		}
		out = append(out, PaymentInput{Method: p.Method, Amount: amt})
		allocated += amt
	}
	return out
}

// inferPriceIncludesIgvFromSaleItem deduce si el precio unitario de la línea ya incluye IGV,
// comparando el total almacenado con el recálculo tributario (evita desfase al emitir boleta/factura desde NV).
func inferPriceIncludesIgvFromSaleItem(db *gorm.DB, it database.TenantSaleItem, taxCfg tax.Config) bool {
	affType := strings.TrimSpace(it.IgvAffectationType)
	if affType == "" {
		affType = "10"
	}
	storedTotal := money.RoundSunat(it.Total)
	_, _, withTrue := tax.CalcItem(it.UnitPrice, it.Quantity, it.Discount, affType, true, taxCfg)
	if money.RoundSunat(withTrue) == storedTotal {
		return true
	}
	_, _, withFalse := tax.CalcItem(it.UnitPrice, it.Quantity, it.Discount, affType, false, taxCfg)
	if money.RoundSunat(withFalse) == storedTotal {
		return false
	}
	if it.ProductID != nil && *it.ProductID > 0 {
		var p database.TenantProduct
		if db.Select("price_includes_igv").First(&p, *it.ProductID).Error == nil {
			return p.PriceIncludesIgv
		}
	}
	return true
}

// IssueElectronicFromNota crea el registro de factura/boleta (01/03) para SUNAT copiando líneas y pagos de la NV (00).
// No es una “segunda venta” en contabilidad de inventario: IssuedFromNotaSaleID fuerza omitir stock, seriales y caja/bancos.
func (s *SaleService) IssueElectronicFromNota(notaSaleID uint, targetSeriesID uint, userID uint, issueYMD string, centralTenantID uint, overrideContactID *uint) (*database.TenantSale, error) {
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
	var companyCfg database.TenantCompanyConfig
	if err := s.db.Select("sunat_enabled").First(&companyCfg).Error; err != nil || !companyCfg.SunatEnabled {
		return nil, errors.New("la facturación electrónica no está habilitada para este tenant")
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
	taxCfg := tax.LoadFromDB(s.db)
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
			PriceIncludesIgv:   inferPriceIncludesIgvFromSaleItem(s.db, it, taxCfg),
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
	if len(pays) == 0 && nota.Total > 0 {
		method := strings.TrimSpace(nota.PaymentMethod)
		if method == "" {
			method = "cash"
		}
		pays = []PaymentInput{{Method: method, Amount: nota.Total}}
	}
	issueAt := parseIssueDateForSale(issueYMD, nota.IssueDate)
	nvRef := strings.TrimSpace(nota.Series) + "-" + strings.TrimSpace(nota.Number)
	notes := strings.TrimSpace(nota.Notes)
	if notes != "" {
		notes = "Referencia NV " + nvRef + ". " + notes
	} else {
		notes = "Referencia NV " + nvRef + "."
	}
	contactID := nota.ContactID
	if overrideContactID != nil && *overrideContactID > 0 {
		var c database.TenantContact
		if err := s.db.First(&c, *overrideContactID).Error; err != nil {
			return nil, errors.New("cliente no encontrado")
		}
		if !c.Active {
			return nil, errors.New("el cliente seleccionado no está activo")
		}
		ct := strings.ToLower(strings.TrimSpace(c.Type))
		if ct != "customer" && ct != "both" {
			return nil, errors.New("el contacto seleccionado no es un cliente válido")
		}
		cid := *overrideContactID
		contactID = &cid
	}
	nvID := notaSaleID
	return s.Create(CreateSaleInput{
		BranchID:                nota.BranchID,
		ContactID:               contactID,
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
		CentralTenantID:         centralTenantID,
	})
}

// SummaryStats retorna estadísticas resumidas de ventas.
func (s *SaleService) SummaryStats(branchID uint, from, to time.Time) map[string]interface{} {
	q := salescope.CommercialSales(s.db.Model(&database.TenantSale{})).
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
