package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	salessvc "tukifac/internal/sales/service"
	"tukifac/pkg/database"
	"tukifac/pkg/money"
	"tukifac/pkg/salecurrency"
	"tukifac/pkg/sunat"
	"tukifac/pkg/tax"

	"gorm.io/gorm"
)

// Afectaciones IGV válidas (catálogo SUNAT N°07). Cualquier otro código se rechaza: antes una
// "99" se guardaba y se calculaba como gravado sin avisar.
var validIgvAffectations = map[string]bool{
	"10": true, "11": true, "12": true, "13": true, "14": true, "15": true, "16": true, "17": true,
	"20": true, "21": true,
	"30": true, "31": true, "32": true, "33": true, "34": true, "35": true, "36": true, "37": true,
	"40": true,
}

const (
	discountModePercent = "percent"
	discountModeAmount  = "amount"

	// Tope de cantidad: evita desbordes y totales absurdos por un cero de más; ninguna cotización
	// real lo alcanza (la columna es decimal(15,3)).
	maxQuotationQuantity = 999_999_999
)

type quotationTotals struct {
	items        []database.TenantQuotationItem
	subtotal     float64
	taxAmount    float64
	total        float64
	globalMode   string
	globalValue  float64
	globalAmount float64
}

func quotationLineLabel(idx int, it QuotationItemInput) string {
	if d := strings.TrimSpace(it.Description); d != "" {
		return fmt.Sprintf("'%s'", d)
	}
	if c := strings.TrimSpace(it.Code); c != "" {
		return fmt.Sprintf("'%s'", c)
	}
	return fmt.Sprintf("la línea %d", idx+1)
}

func finitePositive(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) && v > 0 }
func finiteNonNegative(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v >= 0
}

func normalizeDiscountMode(mode string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "":
		return "", nil
	case "percent", "percentage", "%":
		return discountModePercent, nil
	case "amount", "fixed", "monto":
		return discountModeAmount, nil
	default:
		return "", fmt.Errorf("modo de descuento inválido (%q): use percent o amount", mode)
	}
}

// validateDiscountValue valida un descuento ya normalizado (sin tope contra el importe: ese lo
// comprueba cada llamador porque depende de la base de la línea o del documento).
func validateDiscountValue(label, mode string, value float64) error {
	if !finiteNonNegative(value) {
		return fmt.Errorf("%s: el descuento no puede ser negativo", label)
	}
	if mode == discountModePercent && value > 100 {
		return fmt.Errorf("%s: el descuento en porcentaje no puede superar 100%%", label)
	}
	return nil
}

// buildQuotation valida las líneas y calcula los totales con EXACTAMENTE la misma lógica con la
// que SaleService.Create calculará la venta al convertir (motor CalcSaleCheckout si hay descuentos
// estructurados, CalcItem legado si no). Así el total de la cotización es el de la venta, al
// céntimo, y no hay un segundo cálculo que pueda desviarse.
func (s *QuotationService) buildQuotation(
	inputItems []QuotationItemInput,
	globalModeRaw string,
	globalValue float64,
	taxCfg tax.Config,
) (quotationTotals, error) {
	var out quotationTotals
	if len(inputItems) == 0 {
		return out, errors.New("la cotización debe tener al menos un ítem")
	}

	globalMode, err := normalizeDiscountMode(globalModeRaw)
	if err != nil {
		return out, fmt.Errorf("descuento global: %w", err)
	}
	if globalMode == "" && globalValue > 0 {
		globalMode = discountModeAmount
	}
	if err := validateDiscountValue("descuento global", globalMode, globalValue); err != nil {
		return out, err
	}

	// ¿La petición trae descuentos ESTRUCTURADOS (modo+valor, de línea o global)? Entonces el campo
	// heredado `discount` de cada línea es solo informativo: el formulario lo manda siempre con el
	// descuento ya calculado, que INCLUYE la parte repartida del descuento global. Tomarlo como
	// descuento propio de una línea sin descuento aplicaba el global dos veces (50 con 5 % global
	// quedaba como 2.12 de línea + 2.38 de global y el total bajaba de 218.50 a 216.13). Solo un
	// cliente anterior, que no conoce los modos, depende de `discount`.
	requestStructured := globalMode != "" || globalValue > 0
	for _, in := range inputItems {
		if strings.TrimSpace(in.LineDiscountMode) != "" || in.LineDiscountValue > 0 {
			requestStructured = true
		}
	}

	// Normalizar y validar cada línea.
	items := make([]QuotationItemInput, len(inputItems))
	copy(items, inputItems)
	for i := range items {
		it := &items[i]
		label := quotationLineLabel(i, *it)

		hasProduct := it.ProductID != nil && *it.ProductID > 0
		if strings.TrimSpace(it.Description) == "" && !hasProduct {
			return out, fmt.Errorf("la línea %d no tiene descripción", i+1)
		}
		if !finitePositive(it.Quantity) {
			return out, fmt.Errorf("%s tiene una cantidad inválida (%.3f); debe ser mayor a cero", label, it.Quantity)
		}
		if it.Quantity > maxQuotationQuantity {
			return out, fmt.Errorf("%s tiene una cantidad demasiado grande (%.0f)", label, it.Quantity)
		}
		// Precio real obligatorio: si no se corrige aquí, la cotización se guarda igual y el error
		// solo aparece tarde, al convertirla a venta.
		if !finitePositive(it.UnitPrice) {
			return out, fmt.Errorf("%s no tiene un precio de venta válido (S/ %.2f)", label, it.UnitPrice)
		}
		aff := strings.TrimSpace(it.IgvAffectationType)
		if aff == "" {
			aff = "10"
		}
		if !validIgvAffectations[aff] {
			return out, fmt.Errorf("%s tiene un tipo de afectación IGV inválido (%q)", label, aff)
		}
		it.IgvAffectationType = aff

		if hasProduct {
			var prod database.TenantProduct
			if err := s.db.Select("id", "name", "active").First(&prod, *it.ProductID).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return out, fmt.Errorf("%s: el producto no existe", label)
				}
				return out, err
			}
			if !prod.Active {
				return out, fmt.Errorf("%s: el producto está inactivo", label)
			}
			// Línea de catálogo sin descripción: se completa con el nombre del producto.
			if strings.TrimSpace(it.Description) == "" {
				it.Description = prod.Name
			}
		}

		// Descuento de la línea: el modo+valor es el dato de verdad. Un cliente anterior que solo
		// manda `discount` (monto bruto en formato CalcItem) se convierte a monto sobre la base.
		mode, err := normalizeDiscountMode(it.LineDiscountMode)
		if err != nil {
			return out, fmt.Errorf("%s: %w", label, err)
		}
		if !finiteNonNegative(it.Discount) {
			return out, fmt.Errorf("%s: el descuento no puede ser negativo", label)
		}
		if mode == "" && it.LineDiscountValue > 0 {
			mode = discountModeAmount
		}
		legacyGross := 0.0 // descuento bruto original (cliente anterior), para citarlo tal cual en errores
		if mode == "" && it.Discount > 0 && !requestStructured {
			mode = discountModeAmount
			legacyGross = it.Discount
			it.LineDiscountValue = legacyDiscountToBase(it.Discount, aff, it.PriceIncludesIgv, taxCfg)
		}
		if err := validateDiscountValue(label, mode, it.LineDiscountValue); err != nil {
			return out, err
		}
		if mode == "" {
			it.LineDiscountValue = 0
		}
		it.LineDiscountMode = mode

		if mode == discountModeAmount {
			grossSub, _, _ := tax.CalcItem(it.UnitPrice, it.Quantity, 0, aff, it.PriceIncludesIgv, taxCfg)
			if it.LineDiscountValue > money.RoundSunat(grossSub)+0.005 {
				shown, limit := it.LineDiscountValue, money.RoundSunat(grossSub)
				if legacyGross > 0 {
					// El cliente anterior habló en importe bruto (con IGV): se cita en esos términos.
					shown, limit = legacyGross, money.RoundSunat(it.Quantity*it.UnitPrice)
				}
				return out, fmt.Errorf("%s: el descuento (S/ %.2f) supera el importe de la línea (S/ %.2f)", label, shown, limit)
			}
		}
	}

	// Unidad de venta y combos: se validan y resuelven con las MISMAS funciones que usa la venta,
	// para que la línea cotizada sea la que luego se vende (precio del combo, unidad válida).
	saleLike := make([]salessvc.SaleItemInput, len(items))
	for i, it := range items {
		saleLike[i] = salessvc.SaleItemInput{
			ProductID: it.ProductID, PresentationID: it.PresentationID, SaleUnitID: it.SaleUnitID,
			Code: it.Code, Description: it.Description, Unit: it.Unit, Quantity: it.Quantity,
			UnitPrice: it.UnitPrice, IgvAffectationType: it.IgvAffectationType,
			PriceIncludesIgv: it.PriceIncludesIgv, ComboJSON: it.ComboJSON, ModifiersJSON: it.ModifiersJSON,
		}
	}
	if err := salessvc.ValidateSaleUnits(s.db, saleLike); err != nil {
		return out, err
	}
	resolved, _, err := salessvc.ResolveComboItems(s.db, saleLike)
	if err != nil {
		return out, err
	}
	if len(resolved) == len(items) {
		for i := range items {
			items[i].UnitPrice = resolved[i].UnitPrice
			items[i].IgvAffectationType = resolved[i].IgvAffectationType
			items[i].PriceIncludesIgv = resolved[i].PriceIncludesIgv
		}
	}

	structured := globalMode != "" || globalValue > 0
	for _, it := range items {
		if it.LineDiscountMode != "" || it.LineDiscountValue > 0 {
			structured = true
		}
	}

	lines := make([]database.TenantQuotationItem, len(items))
	if structured {
		engineLines := make([]tax.SaleLineInput, len(items))
		for i, it := range items {
			engineLines[i] = tax.SaleLineInput{
				UnitPrice:          it.UnitPrice,
				Quantity:           it.Quantity,
				IgvAffectationType: it.IgvAffectationType,
				PriceIncludesIgv:   it.PriceIncludesIgv,
				LineDiscountMode:   it.LineDiscountMode,
				LineDiscountValue:  it.LineDiscountValue,
			}
		}
		res := tax.CalcSaleCheckout(tax.SaleCheckoutInput{
			Lines:               engineLines,
			GlobalDiscountMode:  globalMode,
			GlobalDiscountValue: globalValue,
			TaxCfg:              taxCfg,
		})
		if globalMode == discountModeAmount && globalValue > res.GlobalDiscountAmount+0.005 {
			return out, fmt.Errorf("el descuento global (S/ %.2f) supera el subtotal de la cotización (S/ %.2f)",
				globalValue, money.RoundSunat(res.GlobalDiscountAmount))
		}
		for i, it := range items {
			lr := res.Lines[i]
			lines[i] = buildQuotationItemRow(s.db, it, lr.StoredDiscount, lr.TaxRate, lr.Subtotal, lr.TaxAmount, lr.Total)
		}
		out.subtotal, out.taxAmount, out.total = res.Subtotal, res.TaxAmount, res.Total
		out.globalAmount = res.GlobalDiscountAmount
	} else {
		for i, it := range items {
			rate := taxCfg.EffectiveRate(it.IgvAffectationType)
			itemSub, itemTax, itemTotal := tax.CalcItem(
				it.UnitPrice, it.Quantity, 0, it.IgvAffectationType, it.PriceIncludesIgv, taxCfg,
			)
			chargeable := itemTotal
			if tax.IsBonificacionGravada(it.IgvAffectationType) {
				// Bonificación gravada: se muestra la línea pero no suma al total cobrable.
				chargeable = 0
			} else {
				out.subtotal = money.RoundSunat(out.subtotal + itemSub)
				out.taxAmount = money.RoundSunat(out.taxAmount + itemTax)
				out.total = money.RoundSunat(out.total + chargeable)
			}
			lines[i] = buildQuotationItemRow(s.db, it, 0, rate, itemSub, itemTax, chargeable)
		}
	}
	if out.total < 0 {
		return out, errors.New("el total de la cotización no puede ser negativo")
	}
	out.items = lines
	out.globalMode = globalMode
	out.globalValue = globalValue
	if globalMode == "" {
		out.globalValue = 0
	}
	return out, nil
}

// legacyDiscountToBase convierte el `discount` bruto (formato CalcItem: se resta de qty×precio,
// con IGV si el precio lo incluye) a monto sobre la base imponible, que es lo que entiende el
// motor de descuentos. Inversa de tax.SubtotalDiscountToLineDiscount.
func legacyDiscountToBase(discount float64, aff string, includesIgv bool, cfg tax.Config) float64 {
	rate := cfg.EffectiveRate(aff)
	if rate == 0 || !includesIgv {
		return money.RoundSunat(discount)
	}
	return money.RoundSunat(discount / (1 + rate/100))
}

func buildQuotationItemRow(
	db *gorm.DB,
	it QuotationItemInput,
	storedDiscount, rate, subtotal, taxAmount, total float64,
) database.TenantQuotationItem {
	itemType := "product"
	if it.ProductID != nil && *it.ProductID > 0 {
		var prod database.TenantProduct
		if db.Select("type").First(&prod, *it.ProductID).Error == nil && productIsCatalogService(&prod) {
			itemType = "service"
		}
	} else if strings.EqualFold(strings.TrimSpace(it.Unit), "ZZ") {
		itemType = "service"
	}
	return database.TenantQuotationItem{
		ProductID:          it.ProductID,
		PresentationID:     it.PresentationID,
		Code:               it.Code,
		Description:        it.Description,
		Unit:               sunat.NormalizeUnit(it.Unit, itemType),
		Quantity:           it.Quantity,
		UnitPrice:          it.UnitPrice,
		Discount:           storedDiscount,
		LineDiscountMode:   it.LineDiscountMode,
		LineDiscountValue:  it.LineDiscountValue,
		SaleUnitID:         it.SaleUnitID,
		ComboJSON:          strings.TrimSpace(it.ComboJSON),
		SerialsJSON:        serialsToJSON(it.Serials),
		TaxRate:            rate,
		IgvAffectationType: it.IgvAffectationType,
		PriceIncludesIgv:   it.PriceIncludesIgv,
		Subtotal:           subtotal,
		TaxAmount:          taxAmount,
		Total:              total,
		ModifiersJSON:      it.ModifiersJSON,
		ItemNote:           it.ItemNote,
		PackLabel:          salessvc.ResolvePackLabel(db, it.ProductID, it.SaleUnitID),
	}
}

// checkAuthorizedPrices exige que los precios de líneas de catálogo coincidan con el catálogo
// (misma regla que la venta), salvo que el usuario tenga sales.override_price. `existing` son las
// líneas ya guardadas de la cotización que se edita: una línea con el mismo producto, presentación
// y precio que antes no se vuelve a exigir (quien edita no cambió ese precio, p. ej. pactado por
// otro usuario con permiso).
func (s *QuotationService) checkAuthorizedPrices(
	branchID uint,
	inputs []QuotationItemInput,
	canOverride bool,
	existing []database.TenantQuotationItem,
	currency string,
	exchangeRate *float64,
) error {
	if canOverride {
		return nil
	}
	if err := salecurrency.EnsureRateNearOfficial(currency, exchangeRate, time.Now()); err != nil {
		return err
	}
	items := make([]salessvc.SaleItemInput, 0, len(inputs))
	for _, in := range inputs {
		unchanged := false
		for _, ex := range existing {
			if sameProductRef(ex.ProductID, in.ProductID) && sameProductRef(ex.PresentationID, in.PresentationID) &&
				sameProductRef(ex.SaleUnitID, in.SaleUnitID) && math.Abs(ex.UnitPrice-in.UnitPrice) < 0.005 {
				unchanged = true
				break
			}
		}
		items = append(items, salessvc.SaleItemInput{
			ProductID:       in.ProductID,
			PresentationID:  in.PresentationID,
			SaleUnitID:      in.SaleUnitID,
			Code:            in.Code,
			Description:     in.Description,
			Quantity:        in.Quantity,
			UnitPrice:       in.UnitPrice,
			ModifiersJSON:   in.ModifiersJSON,
			PriceAuthorized: unchanged,
		})
	}
	// Cotización en USD: el precio autorizado es el de catálogo (soles) convertido con el TC de la cotización.
	return salessvc.ValidateAuthorizedPricesInCurrency(s.db, branchID, items, currency, exchangeRate)
}

func serialsToJSON(serials []string) string {
	clean := make([]string, 0, len(serials))
	for _, s := range serials {
		if v := strings.TrimSpace(s); v != "" {
			clean = append(clean, v)
		}
	}
	if len(clean) == 0 {
		return ""
	}
	b, err := json.Marshal(clean)
	if err != nil {
		return ""
	}
	return string(b)
}

func parseSerialsJSON(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil
	}
	return out
}

func sameProductRef(a, b *uint) bool {
	av, bv := uint(0), uint(0)
	if a != nil {
		av = *a
	}
	if b != nil {
		bv = *b
	}
	return av == bv
}

// validateHeader comprueba lo que no depende de las líneas: cliente, moneda y vigencia.
func (s *QuotationService) validateHeader(
	contactID *uint,
	currency string,
	exchangeRate *float64,
	issueDate time.Time,
	validUntil *time.Time,
) (cur string, rate *float64, err error) {
	if contactID != nil && *contactID > 0 {
		var c database.TenantContact
		if err := s.db.Select("id", "active", "type").First(&c, *contactID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return "", nil, errors.New("el cliente seleccionado no existe")
			}
			return "", nil, err
		}
		if !c.Active {
			return "", nil, errors.New("el cliente seleccionado no está activo")
		}
		ct := strings.ToLower(strings.TrimSpace(c.Type))
		if ct != "customer" && ct != "both" {
			return "", nil, errors.New("el contacto seleccionado no es un cliente válido")
		}
	}
	// Moneda y TC: en USD sin TC se completa con el TC guardado de la fecha de emisión (ResolveDocumentRate).
	cur, rate, _, err = salecurrency.ResolveDocumentRate(currency, exchangeRate, issueDate)
	if err != nil {
		return "", nil, err
	}
	// En moneda extranjera el tipo de cambio es obligatorio: sin él la venta convertida no puede
	// emitirse a SUNAT ni valorarse en soles.
	if cur != salecurrency.CurrencyPEN && (rate == nil || !finitePositive(*rate)) {
		return "", nil, errors.New("indique el tipo de cambio para cotizar en moneda extranjera")
	}
	if validUntil != nil && !issueDate.IsZero() {
		y1, m1, d1 := validUntil.Date()
		y2, m2, d2 := issueDate.Date()
		if time.Date(y1, m1, d1, 0, 0, 0, 0, time.UTC).Before(time.Date(y2, m2, d2, 0, 0, 0, 0, time.UTC)) {
			return "", nil, errors.New("la vigencia no puede ser anterior a la fecha de emisión")
		}
	}
	return cur, rate, nil
}
