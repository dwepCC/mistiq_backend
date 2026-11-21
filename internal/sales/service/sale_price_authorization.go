package service

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"tukifac/pkg/database"
	"tukifac/pkg/modifierkind"
	"tukifac/pkg/money"
	"tukifac/pkg/productprice"
	"tukifac/pkg/saleunit"
	"tukifac/pkg/salecurrency"

	"gorm.io/gorm"
)

// saleUnitPriceLevel: nivel de precio (Price1/2/3) que usa hoy la venta. No existe todavía
// selector de nivel en ningún frontend/POS (Tukifac ni Tukichef) — Price1 es el único nivel que
// se usa en la práctica (Fase 0-3). El resolver (pkg/saleunit.ResolvePrice) sí soporta 1/2/3, para
// no tener que tocarlo de nuevo cuando exista una decisión funcional de selección de nivel.
const saleUnitPriceLevel = 1

// saleModifierEntry lee únicamente los identificadores de una entrada de modifiers_json. El
// precio NUNCA se toma de este JSON (el cliente lo controla) — siempre se resuelve de nuevo
// contra TenantModifierOption/TenantProductPresentation en BD.
type saleModifierEntry struct {
	Type     string `json:"type"` // "variant" (presentación) | "modifier" (extra)
	OptionID uint   `json:"option_id"`
}

// validateAuthorizedPrices exige que el unit_price de cada línea con producto real coincida con
// el precio autorizado por el catálogo: product.SalePrice, o presentation.SalePrice cuando el
// producto vende por variante, más la suma de extras (modifiers_json) resueltos siempre desde BD.
//
// Líneas exentas de esta verificación (item.PriceAuthorized == true): el precio ya fue fijado por
// código de confianza antes de llegar a Create() —resolveComboItems (combo), el POS de Tukichef
// vía resolveRestaurantOrderItem/calcRestaurantUnitPrice (incluye el "precio acordado en caja/
// mesa" ya documentado en ese flujo), o la reconstrucción de IssueElectronicFromNota a partir de
// una nota de venta ya validada—. Esta función no puede ni debe reinterpretar esas políticas: solo
// cierra el camino que hoy no pasa por ninguna de ellas (venta directa vía API/POS sin resolver
// precio contra catálogo).
//
// Líneas manuales (ProductID == nil, ítems sin catálogo) no se validan aquí: ya exigen
// unit_price > 0 en validateSaleItemPrices, que es todo lo que se puede verificar sin catálogo.
//
// branchID: cuando la línea usa SaleUnit, el precio autorizado se resuelve con
// saleunit.ResolvePrice(branchID, ...) — override de la sucursal si existe y está activo, si no
// el precio global de la SaleUnit. Una línea "normal" (sin SaleUnit ni presentación) resuelve su
// propio override vía productprice.ResolveSalePrice(branchID, ...) — TenantProductBranchPrice,
// independiente de SaleUnit para no activar su selector de unidad en el POS. Presentación
// (variante) no tiene precio por sucursal todavía: usa presentation.SalePrice tal cual.
// ValidateAuthorizedPrices expone la validación de precio de catálogo a otros módulos que fijan
// precios que luego se convierten en venta (cotizaciones): sin ella, esos precios llegaban a la
// venta marcados PriceAuthorized y se saltaban el control.
func ValidateAuthorizedPrices(db *gorm.DB, branchID uint, items []SaleItemInput) error {
	return validateAuthorizedPrices(db, branchID, items)
}

// ValidateAuthorizedPricesInCurrency igual que ValidateAuthorizedPrices para un documento en otra moneda: el catálogo
// está en soles, así que con un documento en USD el precio autorizado es el de catálogo convertido con el TC del
// documento (precio ÷ TC). Con PEN (o sin TC válido) es idéntico a ValidateAuthorizedPrices.
func ValidateAuthorizedPricesInCurrency(db *gorm.DB, branchID uint, items []SaleItemInput, currency string, exchangeRate *float64) error {
	return validateAuthorizedPricesFX(db, branchID, items, currency, exchangeRate)
}

func validateAuthorizedPrices(db *gorm.DB, branchID uint, items []SaleItemInput) error {
	return validateAuthorizedPricesFX(db, branchID, items, salecurrency.CurrencyPEN, nil)
}

// catalogToDocPrice lleva un precio de catálogo (soles) a la moneda del documento. ok=false si el documento está en
// USD pero no hay un TC válido (no se puede autorizar un precio convertido sin TC).
func catalogToDocPrice(pen float64, currency string, rate *float64) (price float64, converted bool, ok bool) {
	if !strings.EqualFold(strings.TrimSpace(currency), salecurrency.CurrencyUSD) {
		return pen, false, true
	}
	if rate == nil || *rate <= 0 {
		return 0, true, false
	}
	return pen / *rate, true, true
}

func validateAuthorizedPricesFX(db *gorm.DB, branchID uint, items []SaleItemInput, currency string, exchangeRate *float64) error {
	for _, item := range items {
		if item.PriceAuthorized {
			continue
		}
		if item.ProductID == nil || *item.ProductID == 0 {
			continue
		}

		var product database.TenantProduct
		if err := db.First(&product, *item.ProductID).Error; err != nil {
			return fmt.Errorf("producto no encontrado para validar el precio de '%s'", saleItemLabel(item))
		}
		if product.HasCombo {
			// El precio del combo ya lo fija resolveComboItems (marca PriceAuthorized ahí);
			// si llegó hasta acá sin la marca, no hay nada más que este código pueda validar.
			continue
		}

		// Producto "normal" (sin SaleUnit): resuelve su propio precio por sucursal si existe
		// (TenantProductBranchPrice) — nunca activa el selector de unidad del POS, a diferencia
		// de un SaleUnit con override (ver comentario del modelo). Las ramas de abajo
		// (usesSaleUnit / presentación) sobrescriben este valor incondicionalmente cuando aplican.
		basePrice, err := productprice.ResolveSalePrice(db, product.ID, branchID, product.SalePrice)
		if err != nil {
			return fmt.Errorf("no se pudo resolver el precio de '%s': %w", saleItemLabel(item), err)
		}
		usesSaleUnit := item.SaleUnitID != nil && *item.SaleUnitID > 0
		switch {
		case usesSaleUnit:
			// Precio autorizado de una unidad de venta = su precio de sucursal (si existe y está
			// activo) o su precio global — nunca se multiplica por ConversionFactor. No combina
			// con presentación ni con extras — validateSaleUnits ya rechaza esa combinación antes
			// de llegar acá. resolveSaleUnitForLine (llamado en validateSaleUnits) ya confirmó que
			// la SaleUnit pertenece a este producto; se le pasa product.ID también a ResolvePrice
			// como defensa adicional (Fase 6.1), no porque haga falta en este camino.
			resolved, err := saleunit.ResolvePrice(db, product.ID, *item.SaleUnitID, branchID, saleUnitPriceLevel)
			if err != nil {
				return fmt.Errorf("unidad de venta inválida para '%s': %w", saleItemLabel(item), err)
			}
			basePrice = resolved
		case product.HasVariants && item.PresentationID != nil && *item.PresentationID > 0:
			var presentation database.TenantProductPresentation
			if err := db.Where("id = ? AND product_id = ?", *item.PresentationID, product.ID).
				First(&presentation).Error; err != nil {
				return fmt.Errorf("presentación inválida para '%s'", saleItemLabel(item))
			}
			basePrice = presentation.SalePrice
		}

		var extrasSum float64
		if !usesSaleUnit {
			var err error
			extrasSum, err = sumModifierExtras(db, product.ID, item.ModifiersJSON)
			if err != nil {
				return err
			}
		}

		// Las unidades de venta pueden costar menos de un céntimo por unidad (gramo, mililitro: S/ 0.0045),
		// así que ahí el precio se compara con la precisión completa y no redondeado a 2 decimales.
		round := money.RoundDisplay
		if usesSaleUnit {
			round = money.RoundSunat
		}
		docPrice, converted, ok := catalogToDocPrice(basePrice+extrasSum, currency, exchangeRate)
		if !ok {
			return fmt.Errorf("'%s': falta el tipo de cambio para validar el precio en dólares", saleItemLabel(item))
		}
		if converted {
			// Documento en USD: el precio de catálogo (S/) se divide entre el TC. El cliente lo calcula con muchos
			// decimales o lo teclea redondeado a centavos, así que se admite el redondeo: ±0.005 US$ por unidad; en
			// unidades de venta fraccionarias (gramo, mililitro: precios de milésimas) el margen es relativo (0.1 %).
			tol := 0.005 + 1e-6
			if usesSaleUnit {
				tol = 1e-6 + 0.001*docPrice
			}
			if math.Abs(item.UnitPrice-docPrice) > tol {
				return fmt.Errorf("'%s' no tiene un precio autorizado (precio de catálogo: S/ %s = US$ %s al TC %s)",
					saleItemLabel(item), formatCatalogPrice(round(basePrice+extrasSum)), formatCatalogPrice(round(docPrice)), formatCatalogPrice(*exchangeRate))
			}
			continue
		}
		expected := round(docPrice)
		got := round(item.UnitPrice)
		if got != expected {
			return fmt.Errorf("'%s' no tiene un precio autorizado (precio de catálogo: S/ %s)", saleItemLabel(item), formatCatalogPrice(expected))
		}
	}
	return nil
}

// sumModifierExtras suma el ExtraPrice real (siempre leído de BD) de las opciones tipo "modifier"
// elegidas en modifiers_json. Las entradas tipo "variant" no suman aquí: su precio ya se resolvió
// como basePrice (la presentación reemplaza el precio base, no se suma — mismo criterio que
// calcRestaurantUnitPrice en internal/restaurant/service/restaurant_modifiers.go).
func sumModifierExtras(db *gorm.DB, productID uint, modifiersJSON string) (float64, error) {
	raw := strings.TrimSpace(modifiersJSON)
	if raw == "" {
		return 0, nil
	}
	var entries []saleModifierEntry
	if err := json.Unmarshal([]byte(raw), &entries); err != nil {
		return 0, fmt.Errorf("modifiers_json inválido")
	}
	var optionIDs []uint
	for _, e := range entries {
		if e.Type == "modifier" && e.OptionID > 0 {
			optionIDs = append(optionIDs, e.OptionID)
		}
	}
	if len(optionIDs) == 0 {
		return 0, nil
	}

	var links []database.TenantProductModifierGroup
	if err := db.Where("product_id = ?", productID).Find(&links).Error; err != nil {
		return 0, err
	}
	allowedGroups := make(map[uint]bool, len(links))
	for _, l := range links {
		allowedGroups[l.GroupID] = true
	}

	var options []database.TenantModifierOption
	if err := db.Where("id IN ? AND active = ?", optionIDs, true).Find(&options).Error; err != nil {
		return 0, err
	}
	if len(options) != len(optionIDs) {
		return 0, fmt.Errorf("opción de extra inválida en la venta")
	}

	groupIDs := make([]uint, 0, len(options))
	for _, o := range options {
		groupIDs = append(groupIDs, o.GroupID)
	}
	var groups []database.TenantModifierGroup
	if err := db.Where("id IN ?", groupIDs).Find(&groups).Error; err != nil {
		return 0, err
	}
	groupByID := make(map[uint]database.TenantModifierGroup, len(groups))
	for _, g := range groups {
		groupByID[g.ID] = g
	}

	var sum float64
	for _, o := range options {
		if !allowedGroups[o.GroupID] {
			return 0, fmt.Errorf("el extra elegido no pertenece al producto")
		}
		g, ok := groupByID[o.GroupID]
		if !ok || !modifierkind.IsExtra(g.Kind, g.Required, g.MultiSelect) {
			return 0, fmt.Errorf("el extra elegido no es válido para este producto")
		}
		sum += o.ExtraPrice
	}
	return sum, nil
}

// formatCatalogPrice muestra el precio con 2 decimales y solo agrega más (hasta 6) cuando hacen
// falta, para no mostrar "S/ 0.00" en un precio de S/ 0.0045.
func formatCatalogPrice(v float64) string {
	s := strconv.FormatFloat(v, 'f', -1, 64)
	if i := strings.IndexByte(s, '.'); i < 0 {
		return s + ".00"
	} else if len(s)-i-1 < 2 {
		return s + "0"
	}
	return s
}
