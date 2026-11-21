package service

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"tukifac/config"
	cashbanksvc "tukifac/internal/cashbank/service"
	detraccionsvc "tukifac/internal/detraccion"
	salecontext "tukifac/internal/fiscal/salecontext"
	invsvc "tukifac/internal/inventory/service"
	prepaymentsvc "tukifac/internal/prepayment"
	"tukifac/internal/exportacion"
	"tukifac/internal/sales/nvdisplay"
	"tukifac/pkg/billingstate"
	"tukifac/pkg/database"
	"tukifac/pkg/docseries"
	"tukifac/pkg/money"
	"tukifac/pkg/paymentcondition"
	"tukifac/pkg/productbranch"
	"tukifac/pkg/saas/docusage"
	"tukifac/pkg/salecurrency"
	"tukifac/pkg/salescope"
	detraccionpkg "tukifac/pkg/sunat/detraccion"
	sunatpre "tukifac/pkg/sunat/prepayment"
	"tukifac/pkg/tax"
	"tukifac/pkg/taxpayment"
	"tukifac/pkg/taxregime"

	"gorm.io/gorm"
)

// Monto máximo en soles para venta con cliente doc. tipo 0 (DOC.TRIB.NO.DOM.SIN.RUC) según SUNAT.
const SunatMaxMontoClienteSinRUC = 700

// RUC Perú: 11 dígitos.
const SunatRucLength = 11

// Detecta una búsqueda tipo "B001-358": serie + correlativo tal como lo escribe el usuario, sin
// los ceros a la izquierda con los que se guarda tenant_sales.number (p. ej. "B001-00000358").
// El LIKE por substring de List() no matchea ese caso porque no hay match literal.
var saleSeriesCorrelativeQueryRe = regexp.MustCompile(`^([A-Za-z0-9]+)-(\d{1,8})$`)

type SaleService struct {
	db *gorm.DB
}

func NewSaleService(db *gorm.DB) *SaleService {
	return &SaleService{db: db}
}

// validateSaleItemPrices exige un precio unitario real (>0) en cada línea de venta. No distingue
// por tipo de afectación IGV: en bonificación (código 15) lo único que se fuerza a 0 es el total
// cobrado (ver CalcItemPayableTotal), el precio de referencia siempre debe ser real.
func validateSaleItemPrices(items []SaleItemInput) error {
	for _, it := range items {
		if it.UnitPrice > 0 {
			continue
		}
		label := strings.TrimSpace(it.Description)
		if label == "" {
			label = strings.TrimSpace(it.Code)
		}
		if label == "" {
			label = "un ítem de la venta"
		}
		return fmt.Errorf("'%s' no tiene un precio de venta válido (S/ 0.00)", label)
	}
	return nil
}

// validateSaleItemQuantities exige quantity > 0 en toda línea. Antes de este chequeo, una
// cantidad <= 0 solo se rechazaba dentro de RecordMovementTx, y solo cuando el producto tenía
// ManageStock=true — para servicios, productos catálogo o líneas manuales, una cantidad negativa
// o cero pasaba sin control y llegaba a construir totales con tax.CalcItem.
func validateSaleItemQuantities(items []SaleItemInput) error {
	for _, it := range items {
		if it.Quantity > 0 {
			continue
		}
		return fmt.Errorf("'%s' tiene una cantidad inválida (%.3f); debe ser mayor a cero", saleItemLabel(it), it.Quantity)
	}
	return nil
}

// saleItemLabel arma una etiqueta legible para mensajes de error, igual que validateSaleItemPrices.
func saleItemLabel(it SaleItemInput) string {
	label := strings.TrimSpace(it.Description)
	if label == "" {
		label = strings.TrimSpace(it.Code)
	}
	if label == "" {
		label = "un ítem de la venta"
	}
	return label
}

// productIsCatalogService: servicios no consumen inventario aunque un registro legacy tenga manage_stock en true.
func productIsCatalogService(p *database.TenantProduct) bool {
	if p == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(p.Type), "service")
}

type SaleItemInput struct {
	ProductID *uint `json:"product_id"`
	// PresentationID: variante/presentación elegida (ej. color) cuando el producto vende por
	// presentación con stock propio (product.HasVariants). nil = producto sin variantes o venta
	// que no distingue cuál se descuenta (comportamiento previo).
	PresentationID *uint `json:"presentation_id"`
	// SaleUnitID: unidad de venta con conversión elegida (ej. "Saco 100 KG"), cuando el producto
	// vende en una unidad distinta de la base. Quantity sigue siendo la cantidad COMERCIAL (1.5),
	// nunca la convertida — el backend calcula la cantidad base a partir de esto, siempre server-
	// side (ver sale_unit_resolver.go). nil = línea legacy sin conversión (comportamiento previo,
	// intacto). Mutuamente excluyente con PresentationID.
	SaleUnitID         *uint   `json:"sale_unit_id"`
	Code               string  `json:"code"`
	Description        string  `json:"description"`
	Unit               string  `json:"unit"`
	Quantity           float64 `json:"quantity"`
	UnitPrice          float64 `json:"unit_price"`
	Discount           float64 `json:"discount"`
	LineDiscountMode   string  `json:"line_discount_mode"`
	LineDiscountValue  float64 `json:"line_discount_value"`
	TaxRate            float64 `json:"tax_rate"`             // ignorado en cálculo; se usa IgvAffectationType + config empresa
	IgvAffectationType string  `json:"igv_affectation_type"` // catálogo SUNAT N°07
	PriceIncludesIgv   bool    `json:"price_includes_igv"`   // si el precio ya incluye IGV
	ModifiersJSON      string  `json:"modifiers_json"`       // detalle de modificadores para ticket
	ItemNote           string  `json:"item_note"`            // nota libre de la línea; no toca el catálogo
	// PackLabel: contenido de la unidad de venta ("12 UND"). Vacío = se calcula de la unidad de venta de la línea; al emitir un
	// comprobante desde una nota de venta se copia el de la nota para que ambos digan lo mismo.
	PackLabel string `json:"pack_label,omitempty"`
	// ComboJSON: elección del cliente cuando el producto es un combo/promoción.
	// [{ group_id, items: [{ product_id, quantity }] }]. El servicio resuelve el precio,
	// valida la selección y descuenta el stock de los componentes.
	ComboJSON string   `json:"combo_json"`
	Serials   []string `json:"serials"` // números de serie elegidos (productos con ManageSeries)
	// PriceAuthorized: el UnitPrice de esta línea ya fue resuelto/autorizado por código de
	// confianza antes de llegar aquí (combo, POS de Tukichef con "precio acordado en caja/mesa",
	// reemisión de una nota de venta ya validada). Sin json tag a propósito: el body JSON del
	// endpoint de ventas jamás puede poblarlo, así que ningún cliente puede autoexcluirse de
	// validateAuthorizedPrices. Ver sale_price_authorization.go.
	PriceAuthorized bool `json:"-"`
}

// ExtraStockMovement salida de kardex de un producto que no es línea de la venta
// (componentes de un combo). La cantidad ya viene multiplicada por lo vendido.
type ExtraStockMovement struct {
	ProductID uint
	Quantity  float64
}

// PaymentInput representa un pago individual (método + monto + referencia opcional).
type PaymentInput struct {
	Method    string  `json:"method"` // código: cash, yape, plin, etc.
	Amount    float64 `json:"amount"`
	Reference string  `json:"reference,omitempty"` // nro. transferencia, depósito, voucher, etc.
	// DestinationAccountID: cuenta bancaria que recibe ESTE pago (nil = la del método de pago). No aplica al efectivo.
	DestinationAccountID *uint `json:"destination_account_id,omitempty"`
}

type CreateSaleInput struct {
	BranchID             uint
	ContactID            *uint
	UserID               uint
	CashSessionID        *uint
	SeriesID             uint
	DocType              string
	IssueDate            time.Time
	DueDate              *time.Time
	Currency             string
	// ApplyServiceCharge cobra el recargo al consumo de la empresa (el % sale de la configuración, no de la petición). Exige que el tenant lo
	// tenga activado; no se combina con anticipos ni detracción.
	ApplyServiceCharge bool
	// PresetServiceChargeRate: solo para uso interno (emisión electrónica desde una nota de venta que ya cobró recargo): usa ese % en vez del de la
	// configuración, aunque el tenant lo haya apagado desde entonces, para que el comprobante coincida con lo que el cliente ya pagó. Nunca viene de la petición.
	PresetServiceChargeRate float64
	OperationTypeCode    string
	ExchangeRate         *float64
	PaymentMethod        string                   // legacy: si Payments vacío, se usa para el total
	Payments             []PaymentInput           `json:"payments"`               // múltiples métodos de pago
	PaymentConditionCode string                   `json:"payment_condition_code"` // cash | credit
	CreditInstallments   []CreditInstallmentInput `json:"credit_installments"`
	Notes                string
	Items                []SaleItemInput
	GlobalDiscountMode   string     `json:"global_discount_mode"`
	GlobalDiscountValue  float64    `json:"global_discount_value"`
	TaxConfig            tax.Config // configuración tributaria de la empresa
	// ExtraStockMovements: salidas de kardex que no corresponden a una línea de la venta.
	// Caso de uso: un combo se factura como una línea, pero el stock sale de sus componentes.
	// Se registran dentro de la misma transacción que la venta.
	ExtraStockMovements []ExtraStockMovement
	// Emisión desde nota de venta (no descontar inventario ni duplicar caja/bancos).
	SkipInventory           bool
	SkipPaymentDistribution bool
	IssuedFromNotaSaleID    *uint // ID de la NV origen; se guarda en la nueva venta 01/03
	// ReservedOrderID: pedido web que se está convirtiendo en esta venta. Su propia reserva no
	// cuenta contra el stock disponible (es justo la mercadería que se va a descontar).
	ReservedOrderID uint
	IssuedFromQuotationID   *uint // ID de la cotización origen
	CentralTenantID         uint  // tenant SaaS (cupo de documentos electrónicos)
	FiscalContext           *salecontext.FiscalContextInput
	Detraccion              *detraccionsvc.SaleInput
	Prepayment              *prepaymentsvc.SaleInput
	// Export: datos de la exportación de servicios (operaciones 0201/0202/0208); nil en cualquier otra operación.
	Export *exportacion.SaleInput
	// UserCanOverridePrice: resuelto por el HANDLER contra los permisos del JWT del usuario
	// autenticado (middleware.HasPermission(claims, "sales.override_price")) — nunca un valor que
	// pueda mandar el cliente en el body. true exime a TODA la venta de validateAuthorizedPrices,
	// igual que ya hacían las líneas con item.PriceAuthorized=true (combos/Tukichef/reconstrucción
	// desde nota) antes de este permiso. Ver docs/INCIDENT-2026-09-17-PRICE-AUTHORIZATION.md.
	UserCanOverridePrice bool
	// IdempotencyKey: clave por intento de cobro generada por el cliente (ver sale_idempotency.go).
	// Vacía = sin protección (flujos internos: ecommerce, membresías, cotizaciones, reemisión).
	IdempotencyKey string
	// OnCreatedTx se ejecuta DENTRO de la transacción de la venta, cuando ya está completamente
	// persistida (cabecera, ítems, pagos, caja, kardex). Si devuelve error, la transacción entera
	// hace rollback —venta, correlativo, caja y stock incluidos—. Sirve para atar a la venta una
	// operación que debe ocurrir "o ambas o ninguna" (p. ej. marcar una cotización como
	// convertida con un UPDATE condicional, para que dos conversiones simultáneas no creen dos
	// ventas). nil = sin hook.
	OnCreatedTx func(tx *gorm.DB, sale *database.TenantSale) error
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

	// Reintento de un cobro que ya se guardó (respuesta perdida, timeout, pantalla congelada):
	// devolver la venta original ANTES de validar o reservar correlativo. Ver sale_idempotency.go.
	input.IdempotencyKey = NormalizeIdempotencyKey(input.IdempotencyKey)
	if existing, err := FindSaleByIdempotencyKey(s.db, input.UserID, input.IdempotencyKey); err != nil {
		return nil, err
	} else if existing != nil {
		return existing, ErrIdempotentReplay
	}

	// Productos asignados a sucursales (tenant_product_branches): no se venden fuera de ellas.
	// Sin asignación = todas, así que lo existente no cambia. Al emitir desde una NV ya se validó
	// al registrar la NV.
	if input.IssuedFromNotaSaleID == nil || *input.IssuedFromNotaSaleID == 0 {
		for _, item := range input.Items {
			if item.ProductID == nil {
				continue
			}
			if err := productbranch.EnsureAvailable(s.db, *item.ProductID, input.BranchID, item.Description); err != nil {
				return nil, err
			}
		}
	}

	// Combos/promociones: fija el precio del grupo y añade las salidas de almacén de sus
	// componentes. Debe ir antes de calcular totales y stock. Si no hay combos, no toca nada.
	resolvedItems, comboStock, err := resolveComboItems(s.db, input.Items)
	if err != nil {
		return nil, err
	}
	input.Items = resolvedItems
	input.ExtraStockMovements = append(input.ExtraStockMovements, comboStock...)
	// SUNAT exige código en cada línea: sin él la venta se guardaba y reventaba recién al
	// emitir, con el cliente esperando. Se completa antes de calcular nada.
	fillMissingItemCodes(s.db, input.Items)
	// Precio real obligatorio en toda línea (ya con combos resueltos): en bonificación (código
	// IGV 15) lo que se zeroa es el total cobrado, no el precio de referencia — así que esto no
	// bloquea bonificaciones legítimas, solo precios en 0 por error de catálogo/carrito. Es la
	// última barrera si alguien llama a la API directo sin pasar por el frontend.
	if err := validateSaleItemPrices(input.Items); err != nil {
		return nil, err
	}
	// Cantidad estrictamente positiva en toda línea, sin excepción por ManageStock ni por
	// tratarse de una línea manual — no depende de si más adelante se registra kardex.
	if err := validateSaleItemQuantities(input.Items); err != nil {
		return nil, err
	}
	// Unidad de venta: valida pertenencia al producto, que esté activa, el factor y AllowFraction
	// ANTES de calcular ningún precio/stock con ella. Ver sale_unit_resolver.go.
	if err := validateSaleUnits(s.db, input.Items); err != nil {
		return nil, err
	}
	// Tipo de cambio: toda venta en USD queda registrada con el TC de su fecha de emisión; si el cliente no lo envía se
	// completa con el TC guardado del día (y sin ninguno, se rechaza). Antes un USD sin TC quedaba con exchange_rate NULL.
	if cur, rate, _, rerr := salecurrency.ResolveDocumentRate(input.Currency, input.ExchangeRate, input.IssueDate); rerr != nil {
		return nil, rerr
	} else {
		input.Currency, input.ExchangeRate = cur, rate
	}
	// Precio autorizado: el unit_price de cada línea con producto real debe coincidir con el
	// catálogo (o su presentación + extras, o el Price1 de su unidad de venta), SALVO que el
	// usuario autenticado tenga el permiso sales.override_price (UserCanOverridePrice, resuelto
	// server-side por el handler contra el JWT — nunca un flag que mande el cliente). Reactivada
	// el 2026-09-17 tras el incidente que la desactivó por completo (ver
	// docs/INCIDENT-2026-09-17-PRICE-AUTHORIZATION.md): el hueco real no era la validación en sí,
	// sino la falta de una excepción para el precio pactado/corrección manual que el POS y
	// "Registrar venta" ya permiten editar — sales.override_price es esa excepción. Las líneas ya
	// vetadas por código de confianza (item.PriceAuthorized=true) no se reevalúan de todos modos.
	// Ver sale_price_authorization.go.
	if !input.UserCanOverridePrice {
		if err := salecurrency.EnsureRateNearOfficial(input.Currency, input.ExchangeRate, input.IssueDate); err != nil {
			return nil, err
		}
		// Documento en USD: el precio autorizado es el de catálogo (soles) convertido con el TC del documento.
		if err := validateAuthorizedPricesFX(s.db, input.BranchID, input.Items, input.Currency, input.ExchangeRate); err != nil {
			return nil, err
		}
	}

	series, err := docseries.ValidateForBranch(s.db, input.SeriesID, input.BranchID)
	if err != nil {
		return nil, err
	}
	if err := docusage.GuardCountableSunatQuota(input.CentralTenantID, series.SunatCode); err != nil {
		return nil, err
	}

	// Exportación de servicios (0201/0202/0208): valida, y fuerza IGV 0 (afectación 40) antes de calcular los totales.
	exportRow, err := s.prepareExport(&input, strings.TrimSpace(series.SunatCode))
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
	var saleItems []database.TenantSaleItem
	var globalDiscAmt float64
	var globalDiscMode string
	var globalDiscValue float64

	if usesStructuredDiscounts(input) {
		subtotal, taxAmount, total, saleItems, globalDiscAmt, globalDiscMode, globalDiscValue =
			buildSaleLinesFromEngine(input, taxCfg, s.db)
	} else {
		subtotal, taxAmount, total, saleItems = buildSaleLinesLegacy(input, taxCfg, s.db)
	}

	currency, err := salecurrency.NormalizeCurrency(input.Currency)
	if err != nil {
		return nil, err
	}
	if input.Prepayment != nil && input.Prepayment.Emit {
		if input.Detraccion != nil {
			return nil, errors.New("no se puede combinar emisión de anticipo con detracción")
		}
		if len(input.Prepayment.Deductions) > 0 {
			return nil, errors.New("no se puede emitir y deducir anticipo en la misma venta")
		}
		// PHP legacy: operation_type_id permanece 0101; has_prepayment marca el anticipo.
		if strings.TrimSpace(input.OperationTypeCode) == "" {
			input.OperationTypeCode = salecurrency.OpVentaInterna
		}
	}

	var prepaymentDeductionPlan *prepaymentsvc.DeductionPlan
	if input.Prepayment != nil && len(input.Prepayment.Deductions) > 0 {
		if input.Detraccion != nil {
			return nil, errors.New("no se puede combinar deducción de anticipos con detracción")
		}
		if input.Prepayment.Emit {
			return nil, errors.New("no se puede emitir y deducir anticipo en la misma venta")
		}
		if sunatCode := strings.TrimSpace(series.SunatCode); sunatCode != "01" && sunatCode != "03" {
			return nil, errors.New("la deducción de anticipos solo aplica a factura (01) o boleta (03)")
		}
		// Un anticipo se deduce en su misma moneda: restar un saldo en soles de un total en dólares (o al revés) descuadra el documento.
		for _, d := range input.Prepayment.Deductions {
			var srcCur string
			if err := s.db.Model(&database.TenantSale{}).Where("id = ?", d.SourceSaleID).Select("currency").Scan(&srcCur).Error; err != nil {
				return nil, err
			}
			if !strings.EqualFold(strings.TrimSpace(srcCur), strings.TrimSpace(input.Currency)) && !(strings.TrimSpace(srcCur) == "" && input.Currency == salecurrency.CurrencyPEN) {
				return nil, errors.New("el anticipo está en una moneda distinta a la de esta venta: solo se puede deducir en la misma moneda")
			}
		}
		plan, err := prepaymentsvc.NewService(s.db).PlanDeductions(
			input.ContactID,
			input.Prepayment.AffectationGroup,
			saleItems,
			input.Prepayment.Deductions,
			taxCfg.TaxRate,
		)
		if err != nil {
			return nil, err
		}
		prepaymentDeductionPlan = &plan
		subtotal = plan.AdjustedSubtotal
		taxAmount = plan.AdjustedTax
		total = plan.AdjustedTotal
	}

	// Recargo al consumo: cargo global fuera de la base del IGV que suma al total a pagar (DL 25988, 5.ª disp. compl.).
	serviceChargeRate, err := tax.ResolveServiceChargeRate(s.db, input.ApplyServiceCharge && input.PresetServiceChargeRate <= 0)
	if err != nil {
		return nil, err
	}
	if input.PresetServiceChargeRate > 0 {
		if !tax.ValidServiceChargeRate(input.PresetServiceChargeRate) {
			return nil, errors.New("el recargo al consumo de la nota de venta no es válido")
		}
		serviceChargeRate = input.PresetServiceChargeRate
	}
	var serviceChargeAmount float64
	if serviceChargeRate > 0 {
		if input.Prepayment != nil || input.Detraccion != nil || salecurrency.IsExportService(input.OperationTypeCode) {
			return nil, errors.New("el recargo al consumo no se puede combinar con anticipos, detracción ni exportación de servicios")
		}
		serviceChargeAmount, total = tax.AddServiceCharge(subtotal, total, serviceChargeRate)
	}

	// Un total negativo (descuentos mayores al subtotal, p. ej.) nunca es una venta válida: antes se
	// guardaba y restaba de todos los totales del dashboard y de los reportes.
	if total < 0 {
		return nil, errors.New("el total de la venta no puede ser negativo: revise los descuentos y los precios")
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
	if opCode == salecurrency.OpVentasNoDomiciliados && sunatCode != "01" {
		return nil, errors.New("la operación ventas no domiciliados (0401) solo aplica a facturas (01)")
	}
	if salecurrency.IsDetraccion(opCode) {
		if sunatCode != "01" {
			return nil, fmt.Errorf("la operación sujeta a detracción (%s) solo aplica a facturas (01)", opCode)
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
	if !salecurrency.IsDetraccion(opCode) && input.Detraccion != nil {
		return nil, fmt.Errorf("datos de detracción solo aplican con tipo de operación %s o %s", salecurrency.OpDetraccion, salecurrency.OpDetraccionTransporte)
	}
	if input.Prepayment != nil && input.Prepayment.Emit {
		if sunatCode != "01" && sunatCode != "03" {
			return nil, errors.New("el comprobante de anticipo solo aplica a factura (01) o boleta (03)")
		}
		if !sunatpre.IsValidAffectationGroup(input.Prepayment.AffectationGroup) {
			return nil, errors.New("indique el grupo de afectación del anticipo: gravado, exonerado o inafecto")
		}
		if opCode != salecurrency.OpVentaInterna {
			return nil, errors.New("el comprobante de anticipo requiere tipo de operación venta interna (0101)")
		}
	}

	// Validaciones SUNAT: Factura 01 solo con RUC de 11 dígitos; doc. tipo 0 máximo S/ 700 en boleta/nota de venta
	if sunatCode == "01" || sunatCode == "03" {
		var companyCfg database.TenantCompanyConfig
		if err := s.db.Select("sunat_enabled", "taxpayer_regime").First(&companyCfg).Error; err != nil || !companyCfg.SunatEnabled {
			return nil, errors.New("la facturación electrónica no está habilitada para este tenant; solo puede emitir notas de venta (SUNAT 00)")
		}
		// Gate por régimen tributario: p. ej. el Nuevo RUS no puede emitir factura (01).
		if !taxregime.For(companyCfg.TaxpayerRegime).CanEmit(sunatCode) {
			return nil, errors.New("el régimen tributario de la empresa no permite emitir este tipo de comprobante (el Nuevo RUS no emite facturas)")
		}
	}
	var contact *database.TenantContact
	if input.ContactID != nil && *input.ContactID > 0 {
		var c database.TenantContact
		if s.db.First(&c, *input.ContactID).Error == nil {
			contact = &c
		}
	}
	if salecurrency.IsDetraccion(opCode) && contact != nil && contact.EsAgenteDePercepcion {
		return nil, errors.New("no se permite detracción con cliente agente de percepción")
	}
	if sunatCode == "01" {
		if contact == nil {
			return nil, errors.New("la factura electrónica (01) requiere un cliente")
		}
		// 0401 (ventas no domiciliados): el cliente no tiene RUC peruano —extranjero sin RUC,
		// pasaporte, carné de extranjería—, así que no se exige RUC. Cualquier otro tipo de
		// operación en factura sigue exigiéndolo (mismo comportamiento de siempre).
		if opCode != salecurrency.OpVentasNoDomiciliados && !salecurrency.IsExportService(opCode) {
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
	}
	// Tope SUNAT de S/ 700 con cliente sin RUC: aplica solo a la boleta electrónica (03),
	// que sí se declara. La nota de venta (00) es un documento interno que nunca llega a
	// SUNAT, así que no tiene por qué heredar su límite.
	if contact != nil && contact.DocType == "0" && sunatCode == "03" {
		totalPEN := salecurrency.TotalInPEN(currency, total, exchangeRate)
		if totalPEN > SunatMaxMontoClienteSinRUC {
			return nil, fmt.Errorf("según SUNAT, con cliente sin RUC (doc. tipo 0) el monto máximo permitido es S/ %d para la boleta. Total equivalente: S/ %.2f", SunatMaxMontoClienteSinRUC, totalPEN)
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
				// Producto con presentaciones: exige una presentación válida (del producto y activa)
				// antes de mirar stock; sin esto la venta sin presentación (o con una ajena) caía en el
				// stock "agregado" y fallaba con un engañoso "stock insuficiente… hay 0.00".
				_, _, presErr := invsvc.CheckProductPresentation(s.db, *item.ProductID, item.PresentationID, true)
				if presErr != nil {
					return nil, presErr
				}
				switch {
				case product.HasVariants && item.PresentationID != nil && *item.PresentationID > 0:
					var pstock database.TenantProductPresentationStock
					s.db.Where("presentation_id = ? AND branch_id = ?", *item.PresentationID, input.BranchID).First(&pstock)
					if err := checkAvailableStock(s.db, item.Description, item.Quantity, pstock.Quantity, input.BranchID, input.ReservedOrderID, *item.ProductID, item.PresentationID); err != nil {
						return nil, err
					}
				case item.SaleUnitID != nil && *item.SaleUnitID > 0:
					// validateSaleUnits ya corrió antes y garantiza que esto no falle salvo carrera
					// real entre esa validación y este chequeo (misma tolerancia que el resto del
					// método: el guard definitivo contra sobreventa vive en RecordMovementTx).
					res, err := resolveSaleUnitForLine(s.db, *item.ProductID, item.SaleUnitID, item.Quantity)
					if err != nil {
						return nil, err
					}
					var stock database.TenantProductStock
					s.db.Where("product_id = ? AND branch_id = ?", *item.ProductID, input.BranchID).First(&stock)
					if stock.Quantity < res.BaseQuantity {
						return nil, fmt.Errorf("stock insuficiente para %s: requiere %.3f (equivalente a %.3f en unidad base), hay %.3f",
							item.Description, item.Quantity, res.BaseQuantity, stock.Quantity)
					}
				default:
					var stock database.TenantProductStock
					s.db.Where("product_id = ? AND branch_id = ?", *item.ProductID, input.BranchID).First(&stock)
					if err := checkAvailableStock(s.db, item.Description, item.Quantity, stock.Quantity, input.BranchID, input.ReservedOrderID, *item.ProductID, nil); err != nil {
						return nil, err
					}
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
		// Componentes de combo: el stock sale de ellos, así que también deben alcanzar (descontando lo que
		// otros pedidos web tienen reservado; el pedido que se está convirtiendo no cuenta contra sí mismo).
		for _, mv := range input.ExtraStockMovements {
			if mv.ProductID == 0 || mv.Quantity <= 0 {
				continue
			}
			var comp database.TenantProduct
			if s.db.First(&comp, mv.ProductID).Error != nil || !comp.ManageStock || productIsCatalogService(&comp) {
				continue
			}
			var stock database.TenantProductStock
			s.db.Where("product_id = ? AND branch_id = ?", mv.ProductID, input.BranchID).First(&stock)
			if err := checkAvailableStock(s.db, comp.Name, mv.Quantity, stock.Quantity, input.BranchID, input.ReservedOrderID, mv.ProductID, nil); err != nil {
				return nil, err
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

	var retentionAmount float64
	if !salecurrency.IsDetraccion(opCode) && total > 0 {
		var contactSnap *salecontext.ContactSnapshot
		if contact != nil {
			contactSnap = salecontext.ContactFromModel(contact)
		}
		retentionAmount = igvRetentionAmountForPayments(input.FiscalContext, sunatCode, contactSnap, total, currency, exchangeRate)
	}

	isCreditSale := false
	if emitFromNV && total > 0 && len(payments) > 0 && normalizePaymentConditionCode(input.PaymentConditionCode) != paymentcondition.CodeCredit {
		var sumPayments float64
		for _, p := range payments {
			sumPayments += p.Amount
		}
		// Solo realinear cuando el pago original NO alcanza el total recalculado (redondeo SUNAT
		// al emitir boleta/factura desde la NV). Si sumPayments >= total, puede ser un pago exacto
		// o uno de más (vuelto real, en efectivo o cualquier otro medio) — en ese caso se conserva
		// tal cual para que print_data.ChangeAmount lo calcule (ver comentario "Vuelto" más abajo);
		// antes esto se realineaba siempre, borrando el vuelto de cualquier método al convertir.
		if money.RoundDisplay(sumPayments) < money.RoundDisplay(total) {
			payments = alignPaymentsToSaleTotal(payments, total)
		}
	}
	if salecurrency.IsDetraccion(opCode) && total > 0 {
		eval, err := s.evaluateDetractionForCreate(input, &series, total, saleItems, contact)
		if err != nil {
			return nil, err
		}
		if len(payments) > 0 || input.DueDate != nil {
			var credit bool
			if input.Detraccion != nil && input.Detraccion.SelfDetraction {
				payments, credit, err = PrepareSelfDetractionSalePayments(payments, total, eval)
			} else {
				payments, credit, err = PrepareDetractionSalePaymentsAllowCredit(payments, total, eval)
			}
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
		// Con retención IGV el cliente paga solo el neto (total − retención).
		payTarget := total - retentionAmount
		if !money.PaidCoversTotal(sumPayments, payTarget) {
			if input.DueDate == nil {
				return nil, fmt.Errorf(
					"la suma de pagos (%.2f) no cubre el total (%.2f)",
					money.RoundDisplay(sumPayments),
					money.RoundDisplay(payTarget),
				)
			}
			isCreditSale = true
		}
		// Vuelto: sumPayments puede superar total (p. ej. efectivo). Se calcula en print_data.change_amount.
	} else if total > 0 && input.DueDate != nil {
		isCreditSale = true
	}
	primaryMethod := PrimaryDirectPaymentMethod(payments, input.PaymentMethod)
	if isCreditSale && primaryMethod == "" {
		primaryMethod = paymentcondition.CodeCredit
	}

	payCond := normalizePaymentConditionCode(input.PaymentConditionCode)
	if payCond == paymentcondition.CodeCredit {
		isCreditSale = true
	} else if payCond == paymentcondition.CodeCash {
		isCreditSale = false
	}

	salePayable := total - retentionAmount
	if salecurrency.IsDetraccion(opCode) && total > 0 {
		// Autodetracción: el cliente paga el TOTAL (neto + detracción); si no, solo el neto.
		selfDetraction := input.Detraccion != nil && input.Detraccion.SelfDetraction
		if eval, derr := s.evaluateDetractionForCreate(input, &series, total, saleItems, contact); derr == nil && eval.Applicable && !selfDetraction {
			salePayable = eval.NetPayablePEN
		}
	}

	var creditInstallmentRows []database.TenantSaleCreditInstallment
	if isCreditSale {
		loc, locErr := time.LoadLocation("America/Lima")
		if locErr != nil || loc == nil {
			loc = time.Local
		}
		directPaid := sumDirectPaymentsExclSpecial(payments)
		creditTarget := money.RoundDisplay(salePayable - directPaid)
		if creditTarget <= 0.009 {
			return nil, errors.New("el saldo a crédito debe ser mayor a cero; reduzca el pago directo o elija contado")
		}
		installments := input.CreditInstallments
		if len(installments) == 0 && input.DueDate != nil {
			installments = []CreditInstallmentInput{{
				DueDate: input.DueDate.Format("2006-01-02"),
				Amount:  creditTarget,
			}}
		}
		// Conversión NV→comprobante: el total se recalcula (redondeo SUNAT) y puede diferir unos
		// centavos del de la NV; se absorbe en la última cuota copiada.
		if emitFromNV && len(installments) > 0 {
			var instSum float64
			for _, in := range installments {
				instSum += in.Amount
			}
			if diff := money.RoundDisplay(creditTarget - instSum); diff != 0 && diff > -0.10 && diff < 0.10 {
				installments = append([]CreditInstallmentInput(nil), installments...)
				installments[len(installments)-1].Amount = money.RoundDisplay(installments[len(installments)-1].Amount + diff)
			}
		}
		var lastDue *time.Time
		var instErr error
		creditInstallmentRows, lastDue, instErr = validateCreditInstallments(installments, creditTarget, currency, loc, input.IssueDate)
		if instErr != nil {
			return nil, instErr
		}
		if lastDue != nil {
			input.DueDate = lastDue
		}
	} else if payCond == paymentcondition.CodeCash && total > 0 {
		if len(payments) == 0 {
			return nil, errors.New("debe indicar al menos un método de pago para venta al contado")
		}
		if !money.PaidCoversTotal(sumDirectPaymentsExclSpecial(payments), salePayable) {
			return nil, fmt.Errorf(
				"venta al contado: los pagos (%.2f) deben cubrir el total (%.2f)",
				sumDirectPaymentsExclSpecial(payments),
				money.RoundDisplay(salePayable),
			)
		}
		// El vuelto solo puede salir de efectivo (ver pkg/money/payment_report.go): ningún método
		// no-efectivo puede por sí solo superar el total a pagar, porque no hay forma real de
		// devolver cambio por Yape/Plin/tarjeta/transferencia.
		if nonCash := sumNonCashDirectPayments(payments); nonCash > salePayable+money.PaymentTolerance {
			return nil, fmt.Errorf(
				"el vuelto solo aplica a efectivo: los métodos no efectivo (%.2f) superan el total a pagar (%.2f)",
				nonCash,
				money.RoundDisplay(salePayable),
			)
		}
	}

	if payCond == "" {
		payCond = paymentcondition.CodeCash
		if isCreditSale {
			payCond = paymentcondition.CodeCredit
		}
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
		BranchID:              input.BranchID,
		ContactID:             input.ContactID,
		UserID:                input.UserID,
		CashSessionID:         input.CashSessionID,
		SeriesID:              input.SeriesID,
		DocType:               input.DocType,
		IssueDate:             input.IssueDate,
		DueDate:               input.DueDate,
		Subtotal:              money.RoundSunat(subtotal),
		TaxAmount:             money.RoundSunat(taxAmount),
		Total:                 money.RoundSunat(total),
		GlobalDiscountAmount:  money.RoundSunat(globalDiscAmt),
		GlobalDiscountMode:    globalDiscMode,
		GlobalDiscountValue:   globalDiscValue,
		ServiceChargeRate:     serviceChargeRate,
		ServiceChargeAmount:   serviceChargeAmount,
		Currency:              currency,
		OperationTypeCode:     opCode,
		ExchangeRate:          exchangeRate,
		PaymentMethod:         primaryMethod,
		PaymentConditionCode:  payCond,
		SaleOrigin:            saleOrigin,
		Notes:                 input.Notes,
		Status:                saleStatus,
		BillingStatus:         "pending",
		IssuedFromNotaSaleID:  input.IssuedFromNotaSaleID,
		IssuedFromQuotationID: input.IssuedFromQuotationID,
		IdempotencyKey:        idempotencyKeyPtr(input.IdempotencyKey),
	}

	// Emisión electrónica desde NV: misma operación comercial; nunca repetir stock/seriales ni caja/bancos.
	skipInv := input.SkipInventory || emitFromNV
	skipPay := input.SkipPaymentDistribution || emitFromNV

	txErr := s.db.Transaction(func(tx *gorm.DB) error {
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
		if err := s.persistCreditInstallmentsTx(tx, sale.ID, creditInstallmentRows); err != nil {
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
			if resolvedCash == nil || *resolvedCash == 0 {
				return errors.New("debe abrir una sesión de caja antes de registrar ventas")
			}
			input.CashSessionID = resolvedCash
			sale.CashSessionID = resolvedCash
		} else if input.CashSessionID != nil && *input.CashSessionID > 0 {
			if _, err := cbSvc.ValidateCashSessionForUser(*input.CashSessionID, input.UserID, input.BranchID); err != nil {
				return err
			}
		}
		refLines := make([]cashbanksvc.PaymentRefLine, 0, len(payments))
		for _, p := range payments {
			if p.Amount > 0 && p.Method != "" {
				refLines = append(refLines, cashbanksvc.PaymentRefLine{Method: p.Method, Reference: p.Reference})
			}
		}
		if err := cbSvc.ValidatePaymentReferences(refLines); err != nil {
			return err
		}
		var recordLines []money.SalePaymentLine
		for _, p := range payments {
			if p.Amount <= 0 || p.Method == "" {
				continue
			}
			recordLines = append(recordLines, money.SalePaymentLine{Amount: p.Amount, IsCash: money.IsCashMethod(p.Method)})
		}
		netRecordAmounts := money.AllocateSalePaymentNetAmounts(sale.Total, recordLines)
		recordIdx := 0
		for _, p := range payments {
			if p.Amount <= 0 || p.Method == "" {
				continue
			}
			if err := tx.Create(&database.TenantSalePayment{
				SaleID:        sale.ID,
				Method:        p.Method,
				Amount:        p.Amount,
				Reference:     strings.TrimSpace(p.Reference),
				CashSessionID: input.CashSessionID,
			}).Error; err != nil {
				return err
			}
			if !skipPay {
				desc := "Venta " + sale.Number
				recordAmt := netRecordAmounts[recordIdx]
				recordIdx++
				if err := cbSvc.RecordPaymentToDestination(tx, p.Method, recordAmt, input.CashSessionID, sale.Number, desc, &sale.ID, input.UserID, p.DestinationAccountID); err != nil {
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
			if err := s.persistPrepaymentTx(tx, sale, input, seriesLocked, saleItems); err != nil {
				return err
			}
			if err := s.persistPrepaymentDeductionTx(tx, sale.ID, prepaymentDeductionPlan); err != nil {
				return err
			}
			if err := exportacion.Persist(tx, sale.ID, exportRow); err != nil {
				return err
			}
			return nil
		}

		// Descontar stock y marcar seriales como vendidos (productos con series)
		inv := invsvc.NewInventoryService(tx)
		for i, item := range input.Items {
			if item.ProductID == nil {
				continue
			}
			var product database.TenantProduct
			if tx.First(&product, *item.ProductID).Error != nil {
				continue
			}
			// Un combo no tiene stock propio: sale el de sus componentes (ExtraStockMovements).
			if product.HasCombo {
				continue
			}
			if !product.ManageStock || productIsCatalogService(&product) {
				continue
			}

			var itemPresentationID *uint
			movementQuantity := item.Quantity
			var movementSaleUnitID *uint
			var movementSaleUnitQuantity *float64
			var movementConversionFactor *float64
			switch {
			case product.HasVariants && item.PresentationID != nil && *item.PresentationID > 0:
				itemPresentationID = item.PresentationID
			case item.SaleUnitID != nil && *item.SaleUnitID > 0:
				res, err := resolveSaleUnitForLine(tx, product.ID, item.SaleUnitID, item.Quantity)
				if err != nil {
					return err
				}
				movementQuantity = res.BaseQuantity
				movementSaleUnitID = item.SaleUnitID
				commercialQty := item.Quantity
				movementSaleUnitQuantity = &commercialQty
				factor := res.ConversionFactor
				movementConversionFactor = &factor
			}
			currentSaleItemID := saleItems[i].ID
			if err := inv.RecordMovementTx(tx, invsvc.MovementInput{
				ProductID:        *item.ProductID,
				PresentationID:   itemPresentationID,
				BranchID:         input.BranchID,
				Type:             "out",
				Quantity:         movementQuantity,
				Reference:        "VENTA/" + sale.Number,
				UserID:           input.UserID,
				OperationCode:    "SALE",
				SaleItemID:       &currentSaleItemID,
				SaleUnitID:       movementSaleUnitID,
				SaleUnitQuantity: movementSaleUnitQuantity,
				ConversionFactor: movementConversionFactor,
			}); err != nil {
				return err
			}

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
							"status":       "sold",
							"sale_item_id": saleItemID,
							"updated_at":   time.Now(),
						}).Error; err != nil {
						return err
					}
				}
			}

		}

		// Componentes de combo: el stock sale de ellos, no de la línea facturada.
		// Va FUERA del bucle de ítems a propósito: un combo se factura como una sola línea con
		// product.HasCombo, que hace `continue` arriba. Si este descuento viviera dentro del
		// bucle, no correría nunca cuando se vende un combo solo (todos los ítems saltan) y se
		// duplicaría cuando hay varios ítems con stock. Debe correr una vez por venta.
		// Misma transacción que la venta: o sale todo, o no sale nada.
		for _, mv := range input.ExtraStockMovements {
			if mv.ProductID == 0 || mv.Quantity <= 0 {
				continue
			}
			var comp database.TenantProduct
			if tx.First(&comp, mv.ProductID).Error != nil {
				continue
			}
			if !comp.ManageStock || productIsCatalogService(&comp) {
				continue
			}
			if err := inv.RecordMovementTx(tx, invsvc.MovementInput{
				ProductID:     mv.ProductID,
				BranchID:      input.BranchID,
				Type:          "out",
				Quantity:      mv.Quantity,
				Reference:     "VENTA/" + sale.Number,
				UserID:        input.UserID,
				OperationCode: "SALE",
			}); err != nil {
				return err
			}
		}
		if err := s.persistFiscalContextTx(tx, sale, input, seriesLocked); err != nil {
			return err
		}
		if err := s.persistDetraccionTx(tx, sale, input, seriesLocked, saleItems); err != nil {
			return err
		}
		if err := s.persistPrepaymentTx(tx, sale, input, seriesLocked, saleItems); err != nil {
			return err
		}
		if err := s.persistPrepaymentDeductionTx(tx, sale.ID, prepaymentDeductionPlan); err != nil {
			return err
		}
		if err := exportacion.Persist(tx, sale.ID, exportRow); err != nil {
			return err
		}
		if input.OnCreatedTx != nil {
			if err := input.OnCreatedTx(tx, sale); err != nil {
				return err
			}
		}
		return nil
	})
	if txErr != nil {
		// Carrera: dos peticiones con la misma clave llegaron a la vez y la otra ganó el insert.
		// La transacción ya hizo rollback (correlativo incluido); devolvemos la ganadora.
		if winner, replayErr := ReplayIfIdempotentConflict(s.db, input.UserID, input.IdempotencyKey, txErr); winner != nil {
			return winner, replayErr
		}
	}
	return sale, txErr
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
	if !salecurrency.IsDetraccion(strings.TrimSpace(input.OperationTypeCode)) {
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

func (s *SaleService) persistPrepaymentTx(
	tx *gorm.DB,
	sale *database.TenantSale,
	input CreateSaleInput,
	series database.TenantDocumentSeries,
	saleItems []database.TenantSaleItem,
) error {
	if input.Prepayment == nil || !input.Prepayment.Emit {
		return nil
	}
	itemAffs := make([]string, 0, len(saleItems))
	for _, it := range saleItems {
		itemAffs = append(itemAffs, it.IgvAffectationType)
	}
	_, err := prepaymentsvc.NewService(tx).Persist(prepaymentsvc.PersistInput{
		SaleID:            sale.ID,
		ContactID:         saleContactID(input.ContactID, sale.ContactID),
		SunatDocCode:      salecontext.SunatCodeFromSeries(&series, input.DocType),
		DocumentNumber:    sale.Number,
		OperationTypeCode: sale.OperationTypeCode,
		Currency:          sale.Currency,
		SaleTotal:         sale.Total,
		AffectationGroup:  input.Prepayment.AffectationGroup,
		ItemAffs:          itemAffs,
	})
	return err
}

func saleContactID(input *uint, sale *uint) *uint {
	if input != nil && *input > 0 {
		return input
	}
	if sale != nil && *sale > 0 {
		return sale
	}
	return input
}

func (s *SaleService) persistPrepaymentDeductionTx(
	tx *gorm.DB,
	consumerSaleID uint,
	plan *prepaymentsvc.DeductionPlan,
) error {
	if plan == nil || len(plan.Resolved) == 0 {
		return nil
	}
	return prepaymentsvc.NewService(tx).PersistApplicationsTx(tx, consumerSaleID, plan.Resolved)
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
	// UserID: usuario que registró la venta (tenant_sales.user_id).
	UserID uint
	// SellerUserID: vendedor atribuido a la venta (perfil fiscal); si la venta no tiene vendedor
	// explícito, el vendedor es quien la registró (mismo criterio que el PDF y el formulario).
	SellerUserID uint
	// CancelledFilter: vacío = todas; exclude = no anuladas (status != cancelled); only = solo anuladas
	CancelledFilter string
	DateFrom        *time.Time
	DateTo          *time.Time
	Query           string
	SunatCodes      []string
	// CommercialReport: el listado se calcula igual que el dashboard, para que las filas, las
	// tarjetas de totales y los gráficos cuenten lo mismo. Excluye los comprobantes emitidos por
	// conversión NV→FE (ya están representados por su nota de venta) y filtra SunatCodes por el
	// tipo de comprobante EFECTIVO: una nota de venta convertida cuenta como la boleta o factura
	// que originó, no como nota de venta. Sin este flag (pantallas de ventas y facturación) el
	// listado conserva todas las filas, incluidos los comprobantes hijo, porque ahí se necesita ver
	// el estado SUNAT de cada uno.
	CommercialReport bool
	Limit            int // 0 = sin límite
	Offset           int
	// SkipSummary no calcula los totales (summary queda en cero). Las pantallas que solo muestran
	// filas (p. ej. Ventas) no los usan; calcularlos cuesta ~8 sentencias sobre todo el filtro.
	SkipSummary bool
}

// ErrSaleListTimeout el listado/totales excedieron el tiempo máximo permitido para un reporte.
var ErrSaleListTimeout = errors.New("el reporte tardó demasiado: acota el rango de fechas o los filtros e intenta de nuevo")

// saleListTimeout tope de una llamada a List. Las consultas llevan este contexto para que un
// reporte pesado no retenga indefinidamente una de las pocas conexiones del tenant (login y el
// resto de requests esperan la misma conexión). La exportación completa (sin paginar) tiene más margen.
func saleListTimeout(export bool) time.Duration {
	if config.AppConfig == nil {
		if export {
			return 45 * time.Second
		}
		return 15 * time.Second
	}
	if export {
		return config.AppConfig.DBBillingTimeout
	}
	return config.AppConfig.DBReportTimeout
}

// SaleListSummary totales sobre todas las ventas que cumplen los filtros (no solo la página).
type SaleListSummary struct {
	SumTotal        float64 `json:"sum_total"`
	SumSubtotal     float64 `json:"sum_subtotal"`
	SumTax          float64 `json:"sum_tax"`
	SumCancelled    float64 `json:"sum_cancelled"`
	SumActive       float64 `json:"sum_active"`
	CountCancelled  int64   `json:"count_cancelled"`
	CountActive     int64   `json:"count_active"`
	SumDetraccion   float64 `json:"sum_detraccion"`
	SumNetPayable   float64 `json:"sum_net_payable"`
	CountDetraccion int64   `json:"count_detraccion"`
	SpotTotal       float64 `json:"spot_total"`
	// Vuelto total entregado en las ventas del filtro (ver enrichSalesWithChangeAmount): suma de
	// pagos directos por encima del importe cobrable de cada venta, nunca negativo por venta.
	SumChangeAmount float64 `json:"sum_change_amount"`
	// SumActivePEN: ventas vigentes de TODAS las monedas convertidas a soles (USD × TC de cada venta). RateEstimatedCount /
	// RateMissingCount: ventas en USD sin TC guardado (estimadas con el TC del día / sin poder convertirse).
	SumActivePEN       float64 `json:"sum_active_pen"`
	RateEstimatedCount int64   `json:"rate_estimated_count"`
	RateMissingCount   int64   `json:"rate_missing_count"`
	// PaymentTotals: monto por método y MONEDA (una entrada por método+moneda; los montos de
	// monedas distintas nunca se suman entre sí). La detracción SPOT va siempre en PEN.
	PaymentTotals []SaleListPaymentTotal `json:"payment_totals"`
	// ByCurrency: los mismos totales de cabecera (sum_*, count_*, vuelto) separados por moneda.
	// Los campos sum_* planos de arriba suman todas las monedas tal cual (comportamiento legado,
	// correcto solo si todo el filtro es una sola moneda): quien muestre importes debe usar este.
	ByCurrency []SaleListCurrencySummary `json:"by_currency"`
}

type SaleListPaymentTotal struct {
	Method   string  `json:"method"`
	Currency string  `json:"currency"`
	Total    float64 `json:"total"`
}

type SaleListCurrencySummary struct {
	Currency        string  `json:"currency"`
	SumTotal        float64 `json:"sum_total"`
	SumSubtotal     float64 `json:"sum_subtotal"`
	SumTax          float64 `json:"sum_tax"`
	SumCancelled    float64 `json:"sum_cancelled"`
	SumActive       float64 `json:"sum_active"`
	CountCancelled  int64   `json:"count_cancelled"`
	CountActive     int64   `json:"count_active"`
	SumChangeAmount float64 `json:"sum_change_amount"`
	// SumActivePEN: ventas vigentes de esta moneda convertidas a SOLES con el TC de cada venta (en PEN = SumActive).
	// RateEstimatedCount: ventas en USD sin TC guardado cuyo equivalente usa el TC del día de emisión.
	// RateMissingCount: ventas en USD sin TC guardado ni TC del día (no entran en SumActivePEN).
	SumActivePEN       float64 `json:"sum_active_pen"`
	RateEstimatedCount int64   `json:"rate_estimated_count"`
	RateMissingCount   int64   `json:"rate_missing_count"`
}

// saleCurrencySQL normaliza la moneda de cabecera: vacío = PEN (ventas antiguas sin moneda).
const saleCurrencySQL = "UPPER(COALESCE(NULLIF(TRIM(tenant_sales.currency), ''), 'PEN'))"

func (s *SaleService) List(params SaleListParams) (sales []database.TenantSale, total int64, summary SaleListSummary, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), saleListTimeout(params.Limit == 0))
	defer cancel()
	s = &SaleService{db: s.db.WithContext(ctx)}
	defer func() {
		if err != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
			err = ErrSaleListTimeout
		}
	}()

	q := s.db.Model(&database.TenantSale{})
	useDistinct := false
	if params.CommercialReport {
		// Fecha de emisión: cuenta el comprobante (con su fecha, tipo y número); la NV de la que salió se omite mientras su hijo esté vigente.
		q = salescope.EmittedSales(q)
	}
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
	if params.UserID > 0 {
		q = q.Where("tenant_sales.user_id = ?", params.UserID)
	}
	if params.SellerUserID > 0 {
		q = q.Where("COALESCE((SELECT tsfp.seller_user_id FROM tenant_sale_fiscal_profiles tsfp WHERE tsfp.sale_id = tenant_sales.id), tenant_sales.user_id) = ?", params.SellerUserID)
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
		trimmed := strings.TrimSpace(params.Query)
		like := "%" + trimmed + "%"
		conds := []string{
			"tenant_sales.number LIKE ?",
			"tenant_sales.series LIKE ?",
			"CONCAT(tenant_sales.series, '-', tenant_sales.number) LIKE ?",
			"tc_filter.business_name LIKE ?",
			"tc_filter.doc_number LIKE ?",
		}
		args := []interface{}{like, like, like, like, like}
		// "B001-358" debe encontrar "B001-00000358": comparar serie + correlativo numérico
		// aparte del LIKE, que solo matchea substring literal y no ignora los ceros a la izquierda.
		if m := saleSeriesCorrelativeQueryRe.FindStringSubmatch(trimmed); m != nil {
			if correlative, err := strconv.ParseUint(m[2], 10, 64); err == nil {
				conds = append(conds, "(tenant_sales.series LIKE ? AND tenant_sales.correlative = ?)")
				args = append(args, "%"+m[1]+"%", correlative)
			}
		}
		q = q.Joins("LEFT JOIN tenant_contacts tc_filter ON tc_filter.id = tenant_sales.contact_id").
			Where(strings.Join(conds, " OR "), args...)
	}
	if params.DateFrom != nil {
		q = q.Where("tenant_sales.issue_date >= ?", params.DateFrom)
	}
	if params.DateTo != nil {
		q = q.Where("tenant_sales.issue_date <= ?", params.DateTo)
	}

	if !params.SkipSummary {
		var sumErr error
		summary, sumErr = s.saleListSummary(q.Session(&gorm.Session{}), useDistinct, len(params.SunatCodes) > 0 && !params.CommercialReport)
		if sumErr != nil {
			return nil, 0, SaleListSummary{}, sumErr
		}
	}

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
	err = q.Order("tenant_sales.issue_date DESC, tenant_sales.id DESC").Find(&sales).Error
	if err != nil {
		return sales, total, summary, err
	}
	billingstate.EnrichSalesBillingStatus(s.db, sales)
	fillSalesPEN(sales)
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
				DocNumber    string
			}
			if s.db.Table("tenant_contacts").Select("id, business_name, doc_number").Where("id IN ?", idList).Find(&contacts).Error == nil {
				nameByID := make(map[uint]string, len(contacts))
				docByID := make(map[uint]string, len(contacts))
				for _, c := range contacts {
					nameByID[c.ID] = c.BusinessName
					docByID[c.ID] = c.DocNumber
				}
				for i := range sales {
					if sales[i].ContactID != nil {
						if name, ok := nameByID[*sales[i].ContactID]; ok {
							sales[i].ContactName = name
						}
						if doc, ok := docByID[*sales[i].ContactID]; ok {
							sales[i].ContactDocNumber = doc
						}
					}
				}
			}
		}
		// Rellenar nombre del usuario que registró la venta
		userIDs := make(map[uint]struct{})
		for _, sale := range sales {
			if sale.UserID > 0 {
				userIDs[sale.UserID] = struct{}{}
			}
		}
		if len(userIDs) > 0 {
			idList := make([]uint, 0, len(userIDs))
			for id := range userIDs {
				idList = append(idList, id)
			}
			var users []struct {
				ID    uint
				Name  string
				Email string
			}
			if s.db.Table("tenant_users").Select("id, name, email").Where("id IN ?", idList).Find(&users).Error == nil {
				byID := make(map[uint]string, len(users))
				for _, u := range users {
					label := strings.TrimSpace(u.Name)
					if label == "" {
						label = strings.TrimSpace(u.Email)
					}
					if label == "" {
						label = fmt.Sprintf("Usuario #%d", u.ID)
					}
					byID[u.ID] = label
				}
				for i := range sales {
					if sales[i].UserID > 0 {
						if name, ok := byID[sales[i].UserID]; ok {
							sales[i].UserName = name
						}
					}
				}
			}
		}
	}
	s.enrichSalesWithDetraccion(sales)
	s.enrichSalesWithChangeAmount(sales)
	nvdisplay.EnrichSales(s.db, sales)
	// Notas de crédito/débito: documento afectado y tipo de nota para el listado.
	enrichNotesAffectedDoc(s.db, sales)

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

// effectiveSunatCodeExpr código SUNAT efectivo de una venta: el de su comprobante electrónico hijo
// si es una nota de venta convertida, y si no el de su propia serie. Equivale al
// COALESCE(fe.doc_type, tenant_sales.doc_type) del dashboard. Requiere joinEffectiveSunatCode.
const effectiveSunatCodeExpr = "COALESCE(eff_fds.sunat_code, eff_ds.sunat_code)"

// joinEffectiveSunatCode agrega los JOIN que resuelven effectiveSunatCodeExpr.
//
// Antes esto era una subconsulta correlacionada sobre tenant_sales evaluada por cada venta. El
// índice de issued_from_nota_sale_id tiene cardinalidad 1 en los tenants sin conversiones (todo
// NULL), así que MySQL la resolvía con un recorrido completo de la tabla por fila: O(n²). Un tenant
// con ~30 mil ventas tardaba más de 10 minutos por sentencia y agotaba su pool de conexiones
// (incidente 2026-10-09, gylconcesioneseirl). Aquí los hijos se agrupan UNA vez (índice en rango
// issued_from_nota_sale_id > 0) y se unen por hash: costo lineal. El hijo elegido es el de menor
// id, igual que el ORDER BY fe.id LIMIT 1 anterior.
func joinEffectiveSunatCode(q *gorm.DB) *gorm.DB {
	return q.
		Joins("LEFT JOIN (SELECT issued_from_nota_sale_id AS parent_id, MIN(id) AS child_id FROM tenant_sales " +
			"WHERE issued_from_nota_sale_id > 0 AND deleted_at IS NULL GROUP BY issued_from_nota_sale_id) eff_ch " +
			"ON eff_ch.parent_id = tenant_sales.id").
		Joins("LEFT JOIN tenant_sales eff_fe ON eff_fe.id = eff_ch.child_id").
		Joins("LEFT JOIN tenant_document_series eff_fds ON eff_fds.id = eff_fe.series_id").
		Joins("LEFT JOIN tenant_document_series eff_ds ON eff_ds.id = tenant_sales.series_id")
}

// saleListSummary agrega montos sobre el mismo conjunto filtrado que List (sin paginar).
func (s *SaleService) saleListSummary(q *gorm.DB, useDistinct, rowsAreDocuments bool) (SaleListSummary, error) {
	var out SaleListSummary
	// Un listado crudo filtrado por tipo de comprobante (Facturación de Tukichef: 01,03) muestra los
	// comprobantes hijo de una conversión NV→FE como filas propias, y su nota de venta padre queda
	// fuera por el filtro de tipo. Excluir los hijos del total dejaba esas ventas sin contar en
	// ninguna parte: las filas y las tarjetas dejaban de coincidir (S/ 501 vs S/ 112, 2026-10).
	type scopeFns struct {
		sales func(*gorm.DB) *gorm.DB
		alias func(string) func(*gorm.DB) *gorm.DB
	}
	sc := scopeFns{sales: salescope.EmittedSales, alias: salescope.ScopeEmitted}
	if rowsAreDocuments {
		sc = scopeFns{
			sales: func(db *gorm.DB) *gorm.DB { return db },
			alias: func(string) func(*gorm.DB) *gorm.DB { return func(db *gorm.DB) *gorm.DB { return db } },
		}
	}
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
	// Una nota de crédito es la reversión de la venta anulada, no una venta nueva ni una resta
	// aparte: su propia venta original ya queda excluida de sum_active (status != 'cancelled') y
	// contabilizada en sum_cancelled a su valor real. Si la NC también restara su total, la
	// reversión se contaría dos veces (una por excluir la original, otra por restar la NC) y el
	// neto quedaría negativo de más. netTotal simplemente la excluye (aporta 0): su efecto ya
	// está reflejado en que la original no se cuenta como venta vigente.
	//
	// La nota de débito se trata igual (aporta 0): el dashboard excluye TODAS las notas
	// (salescope.CommercialSalesNoNotes) porque una nota es el ajuste de un comprobante ya contado,
	// y sumarla aquí hacía que este reporte y el dashboard dejaran de coincidir en cuanto se emitía
	// una. Tampoco cuentan en count_*: son documentos, no operaciones de venta.
	const isNote = "tenant_sales.doc_type IN ('NOTA_CREDITO', 'NOTA_DEBITO')"
	const netTotal = "(CASE WHEN " + isNote + " THEN 0 ELSE tenant_sales.total END)"
	const netSubtotal = "(CASE WHEN " + isNote + " THEN 0 ELSE tenant_sales.subtotal END)"
	const netTax = "(CASE WHEN " + isNote + " THEN 0 ELSE tenant_sales.tax_amount END)"
	var row aggRow
	err := sc.sales(s.db.Model(&database.TenantSale{})).
		Where("tenant_sales.id IN (?)", idSub).
		Select(`
			COALESCE(SUM(` + netTotal + `), 0) AS sum_total,
			COALESCE(SUM(` + netSubtotal + `), 0) AS sum_subtotal,
			COALESCE(SUM(` + netTax + `), 0) AS sum_tax,
			COALESCE(SUM(CASE WHEN tenant_sales.status = 'cancelled' THEN tenant_sales.total ELSE 0 END), 0) AS sum_cancelled,
			COALESCE(SUM(CASE WHEN tenant_sales.status != 'cancelled' THEN ` + netTotal + ` ELSE 0 END), 0) AS sum_active,
			COALESCE(SUM(CASE WHEN tenant_sales.status = 'cancelled' AND NOT ` + isNote + ` THEN 1 ELSE 0 END), 0) AS count_cancelled,
			COALESCE(SUM(CASE WHEN tenant_sales.status != 'cancelled' AND NOT ` + isNote + ` THEN 1 ELSE 0 END), 0) AS count_active
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

	type curAggRow struct {
		Currency       string  `gorm:"column:cur_code"`
		SumTotal       float64 `gorm:"column:sum_total"`
		SumSubtotal    float64 `gorm:"column:sum_subtotal"`
		SumTax         float64 `gorm:"column:sum_tax"`
		SumCancelled   float64 `gorm:"column:sum_cancelled"`
		SumActive      float64 `gorm:"column:sum_active"`
		CountCancelled int64   `gorm:"column:count_cancelled"`
		CountActive    int64   `gorm:"column:count_active"`
		// Ventas vigentes con TC guardado ya en soles (USD × TC) y cuántas vigentes no tienen TC guardado.
		SumActivePENKnown float64 `gorm:"column:sum_active_pen_known"`
		CountMissingRate  int64   `gorm:"column:count_missing_rate"`
	}
	var curRows []curAggRow
	if err := sc.sales(s.db.Model(&database.TenantSale{})).
		Where("tenant_sales.id IN (?)", idSub).
		Select(saleCurrencySQL + ` AS cur_code,
			COALESCE(SUM(` + netTotal + `), 0) AS sum_total,
			COALESCE(SUM(` + netSubtotal + `), 0) AS sum_subtotal,
			COALESCE(SUM(` + netTax + `), 0) AS sum_tax,
			COALESCE(SUM(CASE WHEN tenant_sales.status = 'cancelled' THEN tenant_sales.total ELSE 0 END), 0) AS sum_cancelled,
			COALESCE(SUM(CASE WHEN tenant_sales.status != 'cancelled' THEN ` + netTotal + ` ELSE 0 END), 0) AS sum_active,
			COALESCE(SUM(CASE WHEN tenant_sales.status = 'cancelled' AND NOT ` + isNote + ` THEN 1 ELSE 0 END), 0) AS count_cancelled,
			COALESCE(SUM(CASE WHEN tenant_sales.status != 'cancelled' AND NOT ` + isNote + ` THEN 1 ELSE 0 END), 0) AS count_active,
			COALESCE(SUM(CASE WHEN tenant_sales.status != 'cancelled' AND tenant_sales.exchange_rate > 0 THEN ` + netTotal + ` * tenant_sales.exchange_rate ELSE 0 END), 0) AS sum_active_pen_known,
			COALESCE(SUM(CASE WHEN tenant_sales.status != 'cancelled' AND NOT ` + isNote + ` AND (tenant_sales.exchange_rate IS NULL OR tenant_sales.exchange_rate <= 0) THEN 1 ELSE 0 END), 0) AS count_missing_rate`).
		Group("cur_code").
		Order("cur_code ASC").
		Scan(&curRows).Error; err != nil {
		return out, err
	}
	// Equivalente en SOLES por moneda: PEN = su propio total; USD = suma de (total × TC de cada venta) + las ventas sin TC
	// guardado convertidas con el TC del día de emisión (si existe).
	penByCurrency := map[string]struct {
		pen                  float64
		estimated, missing   int64
	}{}
	for _, c := range curRows {
		if c.Currency != salecurrency.CurrencyUSD {
			penByCurrency[c.Currency] = struct {
				pen                float64
				estimated, missing int64
			}{pen: c.SumActive}
			continue
		}
		pen := c.SumActivePENKnown
		var est, miss int64
		if c.CountMissingRate > 0 {
			var pending []struct {
				IssueDate time.Time
				Total     float64
			}
			if err := sc.sales(s.db.Model(&database.TenantSale{})).
				Where("tenant_sales.id IN (?)", idSub).
				Where(saleCurrencySQL+" = ?", salecurrency.CurrencyUSD).
				Where("tenant_sales.status != 'cancelled' AND NOT "+isNote+" AND (tenant_sales.exchange_rate IS NULL OR tenant_sales.exchange_rate <= 0)").
				Select("tenant_sales.issue_date AS issue_date, tenant_sales.total AS total").
				Limit(5000).Scan(&pending).Error; err != nil {
				return out, err
			}
			add, e, m := estimateMissingRatePEN(pending)
			pen += add
			est, miss = e, m
		}
		penByCurrency[c.Currency] = struct {
			pen                float64
			estimated, missing int64
		}{pen: money.RoundDisplay(pen), estimated: est, missing: miss}
	}
	changeByCurrency := map[string]float64{}
	currencyOfSale := map[uint]string{}

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
		Scopes(sc.alias("ts")).
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
		Method   string  `gorm:"column:method"`
		Currency string  `gorm:"column:cur_code"`
		Total    float64 `gorm:"column:total"`
	}
	type methodCur struct{ method, currency string }
	byMethod := make(map[methodCur]float64)
	var spotTotal float64

	// Los montos por línea se acotan al total de la venta con AllocateSalePaymentReportAmounts —
	// igual que ya hace el reporte de caja (buildSalePaymentReportAmountsFromPayments) — en vez de
	// sumar tsp.amount tal cual. Un pago en efectivo guarda el monto ENTREGADO por el cliente, no
	// el neto (el vuelto se calcula después, en print_data.change_amount): sumar esos montos brutos
	// inflaba el total de "Efectivo" del reporte por encima de lo realmente vendido cada vez que
	// alguien pagaba con un billete mayor al total (incidente reportado 2026-09-15, SEDE KEIKO:
	// "Efectivo" S/139.20 vs. "Total no anuladas" S/122.00 del mismo filtro).
	type saleTotalRow struct {
		ID       uint    `gorm:"column:id"`
		Total    float64 `gorm:"column:total"`
		Currency string  `gorm:"column:currency"`
	}
	var saleTotals []saleTotalRow
	if err := sc.sales(s.db.Model(&database.TenantSale{})).
		Select(`tenant_sales.id AS id, `+netTotal+` AS total, `+saleCurrencySQL+` AS currency`).
		Where("tenant_sales.id IN (?)", idSub).
		Where("tenant_sales.status != ?", "cancelled").
		Scan(&saleTotals).Error; err != nil {
		return out, err
	}
	saleTotalByID := make(map[uint]float64, len(saleTotals))
	for _, st := range saleTotals {
		saleTotalByID[st.ID] = st.Total
		currencyOfSale[st.ID] = st.Currency
	}

	type paymentLineRow struct {
		ID     uint    `gorm:"column:id"`
		SaleID uint    `gorm:"column:sale_id"`
		Method string  `gorm:"column:method"`
		Amount float64 `gorm:"column:amount"`
	}
	var paymentRows []paymentLineRow
	if err := s.db.Table("tenant_sale_payments tsp").
		Select("tsp.id AS id, tsp.sale_id AS sale_id, LOWER(TRIM(tsp.method)) AS method, tsp.amount AS amount").
		Joins("JOIN tenant_sales ts ON ts.id = tsp.sale_id").
		Scopes(sc.alias("ts")).
		Where("ts.id IN (?)", idSub).
		Where("ts.status != ?", "cancelled").
		Scan(&paymentRows).Error; err != nil {
		return out, err
	}
	paymentsBySale := make(map[uint][]paymentLineRow, len(paymentRows))
	methodByPaymentID := make(map[uint]string, len(paymentRows))
	for _, p := range paymentRows {
		paymentsBySale[p.SaleID] = append(paymentsBySale[p.SaleID], p)
		methodByPaymentID[p.ID] = p.Method
	}
	// currencyOf: moneda de la venta de un pago (sin dato = PEN).
	currencyOf := func(saleID uint) string {
		if c := currencyOfSale[saleID]; c != "" {
			return c
		}
		return "PEN"
	}
	addToMethod := func(saleID uint, method string, amount float64) {
		if taxpayment.IsDetractionCode(method) {
			spotTotal += amount
			return
		}
		m := strings.TrimSpace(method)
		if m == "" {
			m = "sin_definir"
		}
		byMethod[methodCur{m, currencyOf(saleID)}] += amount
	}
	for saleID, rows := range paymentsBySale {
		saleTotal, ok := saleTotalByID[saleID]
		if !ok {
			// No debería pasar (idSub ya excluye cancelled/otras sucursales), pero si ocurre no
			// hay total contra el cual acotar: se conserva el monto tal cual, como antes.
			for _, r := range rows {
				addToMethod(saleID, r.Method, r.Amount)
			}
			continue
		}
		lines := make([]money.SalePaymentLine, 0, len(rows))
		for _, r := range rows {
			// El marcador "credito" no es dinero recibido — fuera de la base del prorrateo, mismo
			// criterio que en el reporte de caja.
			if paymentcondition.IsCreditCode(r.Method) {
				continue
			}
			lines = append(lines, money.SalePaymentLine{ID: r.ID, Amount: r.Amount, IsCash: money.IsCashMethod(r.Method)})
		}
		for id, amt := range money.AllocateSalePaymentReportAmounts(saleTotal, lines) {
			addToMethod(saleID, methodByPaymentID[id], amt)
		}
	}

	// Vuelto total del filtro: compara la suma de pagos directos de cada venta contra su importe
	// cobrable (net_payable si tiene detracción, si no el total) — mismo criterio que
	// print_data.ChangeAmount/enrichSalesWithChangeAmount, agregado sobre todo el filtro en vez de
	// por venta individual.
	type netPayableRow struct {
		SaleID     uint    `gorm:"column:sale_id"`
		NetPayable float64 `gorm:"column:net_payable_pen"`
	}
	var netPayableRows []netPayableRow
	if err := s.db.Table("tenant_sale_detraccion d").
		Select("d.sale_id AS sale_id, d.net_payable_pen AS net_payable_pen").
		Joins("JOIN tenant_sales ts ON ts.id = d.sale_id").
		Scopes(sc.alias("ts")).
		Where("ts.id IN (?)", idSub).
		Where("ts.status != ?", "cancelled").
		Scan(&netPayableRows).Error; err != nil {
		return out, err
	}
	payableOverrideBySale := make(map[uint]float64, len(netPayableRows))
	for _, r := range netPayableRows {
		if r.NetPayable > 0 {
			payableOverrideBySale[r.SaleID] = r.NetPayable
		}
	}
	for saleID, rows := range paymentsBySale {
		var directSum float64
		for _, r := range rows {
			if taxpayment.IsDetractionCode(r.Method) || paymentcondition.IsCreditCode(r.Method) {
				continue
			}
			directSum += r.Amount
		}
		payable, ok := payableOverrideBySale[saleID]
		if !ok {
			payable = saleTotalByID[saleID]
		}
		chg := money.CalcPaymentChange(directSum, payable)
		out.SumChangeAmount += chg
		changeByCurrency[currencyOf(saleID)] += chg
	}

	var fromHeader []payRow
	err = sc.sales(s.db.Model(&database.TenantSale{})).
		Select(`LOWER(TRIM(COALESCE(NULLIF(tenant_sales.payment_method, ''), 'sin_definir'))) AS method, `+saleCurrencySQL+` AS cur_code, COALESCE(SUM(`+netTotal+`), 0) AS total`).
		Where("tenant_sales.id IN (?)", idSub).
		Where("tenant_sales.status != ?", "cancelled").
		Where("NOT EXISTS (SELECT 1 FROM tenant_sale_payments tsp WHERE tsp.sale_id = tenant_sales.id)").
		Group("method, cur_code").
		Scan(&fromHeader).Error
	if err != nil {
		return out, err
	}
	for _, p := range fromHeader {
		m := strings.TrimSpace(p.Method)
		if m == "" {
			m = "sin_definir"
		}
		byMethod[methodCur{m, p.Currency}] += p.Total
	}

	pairs := make([]SaleListPaymentTotal, 0, len(byMethod))
	for k, t := range byMethod {
		pairs = append(pairs, SaleListPaymentTotal{Method: k.method, Currency: k.currency, Total: t})
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].Total != pairs[j].Total {
			return pairs[i].Total > pairs[j].Total
		}
		if pairs[i].Method != pairs[j].Method {
			return pairs[i].Method < pairs[j].Method
		}
		return pairs[i].Currency < pairs[j].Currency
	})
	if len(pairs) > 12 {
		pairs = pairs[:12]
	}
	out.PaymentTotals = append(out.PaymentTotals, pairs...)
	if spotTotal > 0 {
		out.SpotTotal = spotTotal
		// La detracción SPOT existe solo en facturas en PEN.
		out.PaymentTotals = append(out.PaymentTotals, SaleListPaymentTotal{Method: taxpayment.CodeDetraccionBN, Currency: "PEN", Total: spotTotal})
	}
	out.ByCurrency = make([]SaleListCurrencySummary, 0, len(curRows))
	for _, c := range curRows {
		out.ByCurrency = append(out.ByCurrency, SaleListCurrencySummary{
			Currency: c.Currency, SumTotal: c.SumTotal, SumSubtotal: c.SumSubtotal, SumTax: c.SumTax,
			SumCancelled: c.SumCancelled, SumActive: c.SumActive, CountCancelled: c.CountCancelled,
			CountActive: c.CountActive, SumChangeAmount: changeByCurrency[c.Currency],
			SumActivePEN: penByCurrency[c.Currency].pen, RateEstimatedCount: penByCurrency[c.Currency].estimated,
			RateMissingCount: penByCurrency[c.Currency].missing,
		})
		out.SumActivePEN += penByCurrency[c.Currency].pen
		out.RateEstimatedCount += penByCurrency[c.Currency].estimated
		out.RateMissingCount += penByCurrency[c.Currency].missing
	}
	out.SumActivePEN = money.RoundDisplay(out.SumActivePEN)
	return out, nil
}

// SalesByProductRow es una fila del reporte de ventas por producto — Fase 7J: cada fila
// representa una combinación INEQUÍVOCA producto+SaleUnit, nunca cantidades mezcladas de
// distintas unidades comerciales. SaleUnitID nil = línea legacy/venta en unidad base (se trata
// como una combinación propia, separada de cualquier SaleUnit real del mismo producto).
type SalesByProductRow struct {
	ProductID    uint   `json:"product_id"`
	ProductCode  string `json:"product_code"`
	ProductName  string `json:"product_name"`
	CategoryID   *uint  `json:"category_id,omitempty"`
	CategoryName string `json:"category_name"`
	// SaleUnitID: unidad de venta de ESTA combinación (nil = unidad base/legacy). El nombre
	// comercial se resuelve en el frontend (productsService.getSaleUnit / saleUnitNames.ts),
	// igual que ya se hace en Compras (7H) y Kardex (7I) — este endpoint no lo resuelve para no
	// duplicar esa lógica.
	SaleUnitID *uint `json:"sale_unit_id,omitempty"`
	// Unit: código SUNAT de la línea (fallback de UI cuando SaleUnitID es nil) — nunca representa
	// el nombre comercial de una SaleUnit. Dentro de una misma combinación producto+SaleUnit este
	// valor ya es homogéneo (MAX() deja de ser arbitrario porque el grupo es una sola escala).
	Unit string `json:"unit"`
	// QuantitySold: cantidad COMERCIAL sumada SOLO dentro de esta combinación — nunca se suma
	// contra otra fila de SaleUnit distinta del mismo producto.
	QuantitySold float64 `json:"quantity_sold"`
	TotalAmount  float64 `json:"total_amount"`
	// LinesCount/AvgLineAmount: métricas técnicas de granularidad interna (cuántas filas de
	// detalle tuvo esta combinación, no cuántas ventas) — no se muestran en el reporte del front
	// porque no tienen lectura de negocio clara, pero se dejan calculadas por si se reusan.
	LinesCount    int64   `json:"lines_count"`
	SalesCount    int64   `json:"sales_count"`
	AvgLineAmount float64 `json:"avg_line_amount"` // total_amount / lines_count
	// AvgUnitPrice: total_amount / quantity_sold — precio promedio, ahora siempre calculado DENTRO
	// de una combinación producto+SaleUnit homogénea (antes de 7J mezclaba escalas distintas).
	AvgUnitPrice float64 `json:"avg_unit_price"`
}

// SalesByProductSummary totales del período (mismos filtros que las filas). Fase 7J: se quitó
// TotalQuantity — sumar cantidades comerciales de todas las combinaciones (distintos productos Y
// distintas SaleUnits) no tiene ninguna lectura de negocio válida, decisión explícita del usuario.
// ProductsCount ahora es COUNT(DISTINCT product_id) real (antes de 7J coincidía con len(rows)
// porque había una fila por producto; con el desglose por SaleUnit, len(rows) pasó a ser el
// número de combinaciones, no de productos, así que se calcula aparte para que la tarjeta
// "Productos" siga significando lo que dice).
type SalesByProductSummary struct {
	TotalAmount   float64 `json:"total_amount"`
	LineItems     int64   `json:"line_items"`
	DistinctSales int64   `json:"distinct_sales"`
	ProductsCount int64   `json:"products_count"`
}

// SalesByProductParams filtros para el reporte de ventas por producto.
type SalesByProductParams struct {
	DateFrom   *time.Time
	DateTo     *time.Time
	BranchID   uint
	CategoryID uint
	// Q busca por código o nombre de producto (LIKE, case-insensitive).
	Q string
	// ProductType filtra por tenant_products.type: "product" o "service". Vacío = todos.
	// Ítems sin producto de catálogo (product_id=0, ítems manuales) se tratan como "product".
	ProductType string
}

func (s *SaleService) salesByProductBaseQuery(params SalesByProductParams) *gorm.DB {
	q := s.db.Table("tenant_sale_items").
		Joins("INNER JOIN tenant_sales ON tenant_sales.id = tenant_sale_items.sale_id AND tenant_sales.status != 'cancelled'").
		Joins("LEFT JOIN tenant_products p ON p.id = tenant_sale_items.product_id").
		Joins("LEFT JOIN tenant_categories c ON c.id = p.category_id").
		Scopes(salescope.ScopeEmittedNoNotes("tenant_sales"))
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
	if qStr := strings.TrimSpace(params.Q); qStr != "" {
		like := "%" + qStr + "%"
		q = q.Where("p.code LIKE ? OR p.name LIKE ? OR tenant_sale_items.description LIKE ?", like, like, like)
	}
	if pt := strings.TrimSpace(params.ProductType); pt != "" {
		q = q.Where("COALESCE(NULLIF(p.type, ''), 'product') = ?", pt)
	}
	return q
}

// SalesByProduct agrupa ítems de ventas por PRODUCTO + SALEUNIT (solo ventas no anuladas) —
// Fase 7J. Antes de esta fase agrupaba solo por product_id, mezclando en una sola suma cantidades
// comerciales de distintas unidades (ej. "1 Caja" + "5 Unidades" = "6", número sin sentido de
// negocio); ahora cada fila es una combinación homogénea — sale_unit_id=NULL (venta en unidad
// base/legacy) se trata como una combinación propia, nunca se mezcla con ninguna SaleUnit real
// del mismo producto. Ver FASE_7J_REPORTS_AUDIT_REPORT.md.
func (s *SaleService) SalesByProduct(params SalesByProductParams) ([]SalesByProductRow, SalesByProductSummary, error) {
	type row struct {
		ProductID    uint
		ProductCode  string
		ProductName  string
		CategoryID   *uint
		CategoryName string
		SaleUnitID   *uint
		Unit         string
		QuantitySold float64
		TotalAmount  float64
		LinesCount   int64
		SalesCount   int64
	}

	var meta struct {
		DistinctSales    int64
		LineItems        int64
		DistinctProducts int64
	}
	qMeta := s.salesByProductBaseQuery(params).
		Select(`COUNT(DISTINCT tenant_sale_items.sale_id) as distinct_sales,
			COUNT(*) as line_items,
			COUNT(DISTINCT tenant_sale_items.product_id) as distinct_products`)
	if err := qMeta.Scan(&meta).Error; err != nil {
		return nil, SalesByProductSummary{}, err
	}

	q := s.salesByProductBaseQuery(params).
		Select(`COALESCE(tenant_sale_items.product_id, 0) as product_id,
			COALESCE(MAX(p.code), '') as product_code,
			COALESCE(MAX(NULLIF(TRIM(p.name), '')), MAX(tenant_sale_items.description)) as product_name,
			MAX(p.category_id) as category_id,
			COALESCE(MAX(c.name), '') as category_name,
			tenant_sale_items.sale_unit_id as sale_unit_id,
			COALESCE(MAX(NULLIF(TRIM(p.unit), '')), MAX(tenant_sale_items.unit)) as unit,
			SUM(tenant_sale_items.quantity) as quantity_sold,
			SUM(` + salecurrency.TotalPENSQL("tenant_sales", "tenant_sale_items.total") + `) as total_amount,
			COUNT(*) as lines_count,
			COUNT(DISTINCT tenant_sale_items.sale_id) as sales_count`).
		Group("tenant_sale_items.product_id, tenant_sale_items.sale_unit_id")

	var raw []row
	if err := q.Scan(&raw).Error; err != nil {
		return nil, SalesByProductSummary{}, err
	}

	out := make([]SalesByProductRow, len(raw))
	var sumAmt float64
	for i, r := range raw {
		catName := strings.TrimSpace(r.CategoryName)
		if catName == "" {
			catName = "Sin categoría"
		}
		avgLine := float64(0)
		if r.LinesCount > 0 {
			avgLine = r.TotalAmount / float64(r.LinesCount)
		}
		avgUnit := float64(0)
		if r.QuantitySold > 0 {
			avgUnit = r.TotalAmount / r.QuantitySold
		}
		out[i] = SalesByProductRow{
			ProductID:     r.ProductID,
			ProductCode:   r.ProductCode,
			ProductName:   r.ProductName,
			CategoryID:    r.CategoryID,
			CategoryName:  catName,
			SaleUnitID:    r.SaleUnitID,
			Unit:          strings.TrimSpace(r.Unit),
			QuantitySold:  r.QuantitySold,
			TotalAmount:   r.TotalAmount,
			LinesCount:    r.LinesCount,
			SalesCount:    r.SalesCount,
			AvgLineAmount: avgLine,
			AvgUnitPrice:  avgUnit,
		}
		sumAmt += r.TotalAmount
	}

	// Orden: categoría, luego producto (agrupa entre sí las combinaciones de un mismo producto,
	// ej. "Caja" y "Unidad suelta" del mismo Arroz quedan una junto a la otra), y dentro del mismo
	// producto por monto descendente — decisión explícita del usuario (Fase 7J).
	sort.Slice(out, func(i, j int) bool {
		if out[i].CategoryName != out[j].CategoryName {
			return out[i].CategoryName < out[j].CategoryName
		}
		if out[i].ProductName != out[j].ProductName {
			return out[i].ProductName < out[j].ProductName
		}
		return out[i].TotalAmount > out[j].TotalAmount
	})

	summary := SalesByProductSummary{
		TotalAmount:   sumAmt,
		LineItems:     meta.LineItems,
		DistinctSales: meta.DistinctSales,
		ProductsCount: meta.DistinctProducts,
	}
	return out, summary, nil
}

// ProfitDetailRow una línea de venta con su ganancia — precio_venta - precio_compra, ganancia
// total = ganancia_unidad * cantidad. El costo es el snapshot guardado en
// TenantSaleItem.PurchasePrice al momento de vender esa línea (ver
// v145_sale_item_purchase_price_snapshot.go y resolveSaleItemPurchasePrice en
// sale_service_calc.go) — no cambia si el costo del producto cambia después. Las líneas de venta
// anteriores a esa migración no tienen snapshot (PurchasePrice=nil): para esas se usa el costo
// ACTUAL del catálogo como resguardo (join a tenant_products), así el reporte nunca queda en
// blanco para ventas viejas, aunque para esas sí puede diferir del costo real de ese día. Igual
// que en el reporte por producto: si el producto no tiene costo (0 o sin catálogo), la ganancia
// unidad es el precio de venta completo.
type ProfitDetailRow struct {
	SaleItemID    uint    `json:"sale_item_id"`
	SaleID        uint    `json:"sale_id"`
	IssueDate     string  `json:"issue_date"`
	DocType       string  `json:"doc_type"`
	Series        string  `json:"series"`
	Number        string  `json:"number"`
	ContactName   string  `json:"contact_name"`
	ContactDoc    string  `json:"contact_doc_number"`
	ProductName   string  `json:"product_name"`
	Quantity      float64 `json:"quantity"`
	PurchasePrice float64 `json:"purchase_price"`
	SalePrice     float64 `json:"sale_price"`
	ProfitUnit    float64 `json:"profit_unit"`
	ProfitTotal   float64 `json:"profit_total"`
}

type ProfitDetailSummary struct {
	LineItems     int64   `json:"line_items"`
	DistinctSales int64   `json:"distinct_sales"`
	TotalSales    float64 `json:"total_sales"`
	TotalProfit   float64 `json:"total_profit"`
	// TotalCost: costo de compra de lo vendido (precio de compra × cantidad); TotalSales - TotalCost = TotalProfit.
	TotalCost float64 `json:"total_cost"`
	// LinesWithoutCost: líneas sin precio de compra registrado (su utilidad es todo el precio).
	LinesWithoutCost int64 `json:"lines_without_cost"`
}

// ProfitDetailParams filtros para el reporte de utilidades (ganancia por línea de venta).
type ProfitDetailParams struct {
	DateFrom   *time.Time
	DateTo     *time.Time
	BranchID   uint
	CategoryID uint
	// ProductID: solo las líneas de ese producto (0 = todos).
	ProductID uint
	// UserID: solo las ventas de ese usuario (0 = todos).
	UserID uint
	// Q busca por código o nombre de producto (LIKE, case-insensitive).
	Q string
}

// ProfitDetail replica el reporte "Utilidades detallado" del sistema anterior (reports/
// commissions-detail): una fila por línea de venta con su ganancia. Mismo scope que
// SalesByProduct (ScopeCommercial + status != cancelled) — evita el doble conteo de una boleta/
// factura emitida por conversión de una NV ya contada, y excluye ventas anuladas.
func (s *SaleService) ProfitDetail(params ProfitDetailParams) ([]ProfitDetailRow, ProfitDetailSummary, error) {
	q := s.db.Table("tenant_sale_items").
		Select(`tenant_sale_items.id as sale_item_id,
			tenant_sale_items.sale_id as sale_id,
			tenant_sales.issue_date as issue_date,
			tenant_sales.doc_type as doc_type,
			tenant_sales.series as series,
			tenant_sales.number as number,
			COALESCE(NULLIF(TRIM(ct.business_name), ''), 'Clientes Varios') as contact_name,
			COALESCE(ct.doc_number, '') as contact_doc,
			COALESCE(NULLIF(TRIM(p.name), ''), tenant_sale_items.description) as product_name,
			tenant_sale_items.quantity as quantity,
			COALESCE(tenant_sale_items.purchase_price, p.purchase_price, 0) as purchase_price,
			tenant_sale_items.unit_price as sale_price,
			tenant_sale_items.subtotal as line_subtotal,
			tenant_sale_items.line_discount_subtotal as line_discount_subtotal,
			tenant_sale_items.global_discount_subtotal as global_discount_subtotal,
			tenant_sales.currency as currency,
			tenant_sales.exchange_rate as exchange_rate`).
		Joins("INNER JOIN tenant_sales ON tenant_sales.id = tenant_sale_items.sale_id AND tenant_sales.status != 'cancelled'").
		Joins("LEFT JOIN tenant_products p ON p.id = tenant_sale_items.product_id").
		Joins("LEFT JOIN tenant_contacts ct ON ct.id = tenant_sales.contact_id").
		Scopes(salescope.ScopeEmittedNoNotes("tenant_sales"))
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
	if qStr := strings.TrimSpace(params.Q); qStr != "" {
		like := "%" + qStr + "%"
		q = q.Where("p.code LIKE ? OR p.name LIKE ? OR tenant_sale_items.description LIKE ?", like, like, like)
	}
	if params.ProductID > 0 {
		q = q.Where("tenant_sale_items.product_id = ?", params.ProductID)
	}
	if params.UserID > 0 {
		q = q.Where("tenant_sales.user_id = ?", params.UserID)
	}
	q = q.Order("tenant_sales.issue_date ASC, tenant_sale_items.id ASC")

	type row struct {
		SaleItemID             uint
		SaleID                 uint
		IssueDate              time.Time
		DocType                string
		Series                 string
		Number                 string
		ContactName            string
		ContactDoc             string
		ProductName            string
		Quantity               float64
		PurchasePrice          float64
		SalePrice              float64
		LineSubtotal           float64
		LineDiscountSubtotal   float64
		GlobalDiscountSubtotal float64
		Currency               string
		ExchangeRate           *float64
	}
	var raw []row
	if err := q.Scan(&raw).Error; err != nil {
		return nil, ProfitDetailSummary{}, err
	}

	out := make([]ProfitDetailRow, len(raw))
	var summary ProfitDetailSummary
	distinctSales := map[uint]struct{}{}
	rateCache := dayRateCache{}
	for i, r := range raw {
		// SalePrice es el precio de lista de la línea; lo realmente cobrado descuenta los
		// descuentos de línea y el descuento global de la venta (ya prorrateado por línea en
		// subtotal/line_discount_subtotal/global_discount_subtotal). Sin esto la utilidad se
		// calculaba sobre el precio sin descuento y no coincidía con "Ventas por producto".
		// La proporción se calcula aquí (no en SQL): en SQL, según el motor, una división de
		// columnas decimal puede truncarse a entero.
		if listSubtotal := r.LineSubtotal + r.LineDiscountSubtotal + r.GlobalDiscountSubtotal; listSubtotal > 0 && r.LineSubtotal > 0 && r.LineSubtotal < listSubtotal {
			r.SalePrice = money.RoundSunat(r.SalePrice * (r.LineSubtotal / listSubtotal))
		}
		// El costo (purchase_price) siempre está en soles; el precio de una venta en USD se lleva a soles con el TC de su
		// venta (si no lo guardó, el del día de emisión) para que la utilidad no reste soles a dólares.
		if strings.ToUpper(strings.TrimSpace(r.Currency)) == salecurrency.CurrencyUSD {
			factor := 0.0
			if r.ExchangeRate != nil && *r.ExchangeRate > 0 {
				factor = *r.ExchangeRate
			} else if v := rateCache.get(salecurrency.DayOf(r.IssueDate)); v != nil {
				factor = *v
			}
			if factor > 0 {
				r.SalePrice = money.RoundSunat(r.SalePrice * factor)
			}
		}
		unitProfit := r.SalePrice - r.PurchasePrice
		totalProfit := unitProfit * r.Quantity
		out[i] = ProfitDetailRow{
			SaleItemID:    r.SaleItemID,
			SaleID:        r.SaleID,
			IssueDate:     r.IssueDate.Format("2006-01-02"),
			DocType:       r.DocType,
			Series:        r.Series,
			Number:        r.Number,
			ContactName:   r.ContactName,
			ContactDoc:    r.ContactDoc,
			ProductName:   r.ProductName,
			Quantity:      r.Quantity,
			PurchasePrice: r.PurchasePrice,
			SalePrice:     r.SalePrice,
			ProfitUnit:    unitProfit,
			ProfitTotal:   totalProfit,
		}
		summary.TotalSales += r.SalePrice * r.Quantity
		summary.TotalProfit += totalProfit
		summary.TotalCost += r.PurchasePrice * r.Quantity
		if r.PurchasePrice <= 0 {
			summary.LinesWithoutCost++
		}
		distinctSales[r.SaleID] = struct{}{}
	}
	summary.LineItems = int64(len(raw))
	summary.DistinctSales = int64(len(distinctSales))

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
	if err := s.db.Model(&database.TenantSale{}).Where("issued_from_nota_sale_id = ? AND status <> ?", id, "cancelled").Count(&electronicChild).Error; err != nil {
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
		if err := reverseSaleCashTx(tx, cashSvc, &sale, ref, reason, userID); err != nil {
			return err
		}
		if err := restoreStockFromKardexTx(tx, &sale, ref, userID); err != nil {
			return err
		}
		if err := releaseSerialsForSaleTx(tx, items); err != nil {
			return err
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
	cashSvc := cashbanksvc.NewCashBankService(s.db)
	return s.db.Transaction(func(tx *gorm.DB) error {
		if err := restoreStockFromKardexTx(tx, &sale, ref, userID); err != nil {
			return err
		}
		if err := releaseSerialsForSaleTx(tx, items); err != nil {
			return err
		}
		// La anulación por nota de crédito también devuelve el dinero: antes solo revertía
		// stock y el cobro quedaba vivo en caja, así que el arqueo cuadraba de más.
		if err := reverseSaleCashTx(tx, cashSvc, &sale, ref, reason, userID); err != nil {
			return err
		}
		cancelNotes := strings.TrimSpace(sale.Notes)
		if cancelNotes != "" {
			cancelNotes = cancelNotes + " | ANULADA: " + reason
		} else {
			cancelNotes = "ANULADA: " + reason
		}
		if err := tx.Model(&sale).Updates(map[string]interface{}{
			"status": "cancelled",
			"notes":  cancelNotes,
		}).Error; err != nil {
			return err
		}
		// Si la venta era un anticipo, deja de ofrecerse para deducir. El bloqueo por anticipos ya
		// deducidos se valida antes de emitir la nota de crédito (esta anulación llega cuando SUNAT
		// ya la aceptó y no puede rechazarse aquí).
		return prepaymentsvc.NewService(tx).VoidSourceVoucherTx(tx, sale.ID)
	})
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
		return []PaymentInput{{Method: pays[0].Method, Amount: roundedTotal, Reference: pays[0].Reference}}
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
		out = append(out, PaymentInput{Method: p.Method, Amount: amt, Reference: p.Reference})
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
	if err := s.db.Model(&database.TenantSale{}).Where("issued_from_nota_sale_id = ? AND status <> ?", notaSaleID, "cancelled").Count(&dup).Error; err != nil {
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
	if err := s.db.Select("sunat_enabled", "taxpayer_regime").First(&companyCfg).Error; err != nil || !companyCfg.SunatEnabled {
		return nil, errors.New("la facturación electrónica no está habilitada para este tenant")
	}
	// Gate por régimen tributario: p. ej. el Nuevo RUS no puede emitir factura (01).
	if !taxregime.For(companyCfg.TaxpayerRegime).CanEmit(code) {
		return nil, errors.New("el régimen tributario de la empresa no permite emitir este tipo de comprobante (el Nuevo RUS no emite facturas)")
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
			SaleUnitID:         it.SaleUnitID,
			Code:               it.Code,
			Description:        it.Description,
			Unit:               it.Unit,
			Quantity:           it.Quantity,
			UnitPrice:          it.UnitPrice,
			Discount:           it.Discount,
			IgvAffectationType: it.IgvAffectationType,
			PriceIncludesIgv:   inferPriceIncludesIgvFromSaleItem(s.db, it, taxCfg),
			ModifiersJSON:      it.ModifiersJSON,
			ItemNote:           it.ItemNote,
			PackLabel:          it.PackLabel,
			// Precio ya vetado cuando se creó la nota de venta original (pasó por
			// validateAuthorizedPrices en ese momento); esto es un snapshot de datos ya
			// persistidos, no un precio nuevo enviado por el cliente en esta llamada.
			PriceAuthorized: true,
		})
	}
	paymentsDB, err := s.GetPayments(nota.ID)
	if err != nil {
		return nil, err
	}
	var pays []PaymentInput
	for _, p := range paymentsDB {
		if p.Amount > 0 && strings.TrimSpace(p.Method) != "" && !paymentcondition.IsCreditCode(p.Method) {
			pays = append(pays, PaymentInput{Method: p.Method, Amount: p.Amount, Reference: p.Reference})
		}
	}
	notaInstallments, err := s.GetCreditInstallments(nota.ID)
	if err != nil {
		return nil, err
	}
	issueAt, dateErr := resolveElectronicIssueDate(issueYMD, nota.IssueDate, time.Now())
	if dateErr != nil {
		return nil, dateErr
	}
	// NV a crédito: el comprobante hereda la deuda (cuotas pendientes + abonos reales como pagos
	// sin caja). Ver buildNotaCreditPlan. Si ya está saldada se emite por el camino de contado.
	var creditPlan *notaCreditPlan
	if isCreditNota(&nota, notaInstallments) {
		creditPlan, err = buildNotaCreditPlan(&nota, notaInstallments, pays, issueAt)
		if err != nil {
			return nil, err
		}
	}
	if len(pays) == 0 && nota.Total > 0 && creditPlan == nil {
		method := strings.TrimSpace(nota.PaymentMethod)
		if method == "" {
			method = "cash"
		}
		pays = []PaymentInput{{Method: method, Amount: nota.Total}}
	}
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
	opCode := strings.TrimSpace(nota.OperationTypeCode)
	if opCode == "" {
		opCode = salecurrency.OpVentaInterna
	}
	if err := salecurrency.RequireExchangeRateForUSD(nota.Currency, nota.ExchangeRate); err != nil {
		return nil, fmt.Errorf("nota de venta en USD: %w", err)
	}
	nvID := notaSaleID
	dueDate := electronicDueDate(nota.DueDate, nota.IssueDate, issueAt)
	payCondCode := ""
	var creditInsts []CreditInstallmentInput
	if creditPlan != nil {
		payCondCode = paymentcondition.CodeCredit
		creditInsts = creditPlan.Installments
		due := creditPlan.LastDue
		dueDate = &due
	}
	issued, err := s.Create(CreateSaleInput{
		BranchID:                nota.BranchID,
		ContactID:               contactID,
		UserID:                  userID,
		CashSessionID:           nil,
		SeriesID:                targetSeriesID,
		DocType:                 strings.TrimSpace(target.DocType),
		IssueDate:               issueAt,
		DueDate:                 dueDate,
		PaymentConditionCode:    payCondCode,
		CreditInstallments:      creditInsts,
		Currency:                nota.Currency,
		OperationTypeCode:       opCode,
		ExchangeRate:            nota.ExchangeRate,
		PaymentMethod:           "",
		Payments:                pays,
		Notes:                   notes,
		Items:                   inputs,
		TaxConfig:               taxCfg,
		SkipInventory:           true,
		SkipPaymentDistribution: true,
		IssuedFromNotaSaleID:    &nvID,
		CentralTenantID:         centralTenantID,
		PresetServiceChargeRate: nota.ServiceChargeRate * boolToFloat(nota.ServiceChargeAmount > 0),
	})
	if err != nil {
		return nil, err
	}
	// Trazabilidad documental (no monetaria): esta factura/boleta no representa un nuevo
	// movimiento de dinero — el pago real ya se registró (y ya quedó vinculado a su sesión de
	// caja) cuando se creó la nota de venta original; por eso SkipPaymentDistribution=true arriba
	// y ningún RecordPayment se vuelve a llamar. Pero el comprobante SÍ debe quedar trazable a la
	// MISMA sesión que la nota, para que "en qué turno ocurrió esta venta" sea consultable desde
	// el documento fiscal final, no solo desde la nota interna.
	//
	// Se asigna por UPDATE directo (no vía CreateSaleInput.CashSessionID) a propósito: si se
	// pasara por ahí, sale_service.Create validaría la sesión con ValidateCashSessionForUser como
	// si fuera un cobro en vivo (sesión abierta + del mismo usuario) — pero aquí puede pasar horas
	// o días entre la nota y su emisión electrónica, la sesión original bien puede estar ya
	// cerrada, y quien emite el comprobante (p. ej. contabilidad) puede no ser el mismo usuario
	// que atendió la venta. Nada de eso invalida la trazabilidad histórica: solo se está anotando
	// en qué sesión ocurrió realmente la venta, no autorizando un cobro nuevo.
	if nota.CashSessionID != nil {
		if err := s.db.Model(&database.TenantSale{}).Where("id = ?", issued.ID).
			Update("cash_session_id", nota.CashSessionID).Error; err != nil {
			return issued, err
		}
		issued.CashSessionID = nota.CashSessionID
	}
	return issued, nil
}

// SummaryStats retorna estadísticas resumidas de ventas.
func (s *SaleService) SummaryStats(branchID uint, from, to time.Time) map[string]interface{} {
	q := salescope.EmittedSalesNoNotes(s.db.Model(&database.TenantSale{})).
		Where("issue_date >= ? AND issue_date <= ? AND status != ?", from, to, "cancelled")
	if branchID > 0 {
		q = q.Where("branch_id = ?", branchID)
	}

	var count int64
	var totalAmount, serviceCharge float64
	q = q.Session(&gorm.Session{})
	q.Count(&count)
	q.Select("COALESCE(SUM(total), 0)").Scan(&totalAmount)
	// Recargo al consumo incluido en «total»: no es ingreso del negocio (va a los trabajadores); se informa aparte para poder restarlo.
	q.Select("COALESCE(SUM(service_charge_amount), 0)").Scan(&serviceCharge)

	return map[string]interface{}{
		"count":                count,
		"total":                totalAmount,
		"service_charge_total": serviceCharge,
	}
}

func boolToFloat(b bool) float64 {
	if b {
		return 1
	}
	return 0
}
