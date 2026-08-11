package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"tukifac/internal/catalog/combos"
	invsvc "tukifac/internal/inventory/service"
	"tukifac/pkg/database"
	"tukifac/pkg/gormutil"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// La lógica de combos (validar la elección, calcular precio, resolver componentes) es común
// al ERP y al restaurante, así que vive en internal/catalog/combos. Aquí solo queda lo
// propio de cocina: rutear cada componente a su área y explotar la comanda.
//
// Alias para no arrastrar el rename por todo el paquete: son los mismos tipos.
type comboSelectionInput = combos.Selection
type comboSelectionItemInput = combos.SelectionItem
type comboComponentPayload = combos.ComponentPayload
type comboChosenComponent = combos.ChosenComponent
type comboCatalogItem = combos.CatalogItem

// comboPayload snapshot histórico guardado en TenantComanda.ComboJSON. La parte del combo es
// idéntica en las N comandas del mismo combo_parent_key; Component cambia en cada una.
// No depende del catálogo vivo: si mañana cambia el combo, lo ya pedido no se altera.
type comboPayload struct {
	ComboID            uint                  `json:"combo_id"`
	ComboCode          string                `json:"combo_code"`
	ComboName          string                `json:"combo_name"`
	ComboPrice         float64               `json:"combo_price"`    // unitario, ya con sobreprecios
	ComboQuantity      float64               `json:"combo_quantity"` // combos pedidos en esta línea
	Selection          string                `json:"selection"`      // firma: agrupa líneas idénticas al facturar
	IgvAffectationType string                `json:"igv_affectation_type"`
	PriceIncludesIgv   bool                  `json:"price_includes_igv"`
	Component          comboComponentPayload `json:"component"`
	Snapshot           bool                  `json:"snapshot"`
}

// comboComponentDraft una comanda a crear por cada componente del combo.
type comboComponentDraft struct {
	ProductID         uint
	ProductCode       string
	ProductName       string
	Quantity          float64 // por combo
	PreparationArea   string
	PreparationAreaID *uint
	Payload           comboPayload
}

func parseComboSelections(raw string) ([]comboSelectionInput, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var out []comboSelectionInput
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, errors.New("combo_json inválido")
	}
	return out, nil
}

func parseComboPayload(raw string) (*comboPayload, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var p comboPayload
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return nil, errors.New("combo_json de la comanda inválido")
	}
	if p.ComboID == 0 {
		return nil, nil
	}
	return &p, nil
}

// resolveComboOrderItem valida la elección del cliente contra el combo del catálogo y devuelve
// un draft por componente. Devuelve nil si el producto no es un combo.
//
// Precio: precio fijo del combo + Σ (sobreprecio × cantidad) de las opciones elegidas.
// Igual que en el resto del flujo, un unit_price > 0 del cliente (precio acordado en caja) manda.
// El producto ya viene resuelto por resolveRestaurantOrderItem: releerlo aquí era una
// consulta por ítem que no aportaba nada.
func resolveComboOrderItem(
	tx *gorm.DB,
	item *NewOrderItem,
	resolved *database.TenantProduct,
) ([]comboComponentDraft, error) {
	if item.ProductID == nil || *item.ProductID == 0 || resolved == nil {
		return nil, nil
	}
	combo := *resolved
	if !combo.HasCombo {
		return nil, nil
	}

	// La validación y el precio los resuelve el paquete compartido: es la misma regla en el
	// ERP y en el restaurante.
	res, err := combos.Resolve(tx, combo, item.ComboJSON, item.UnitPrice)
	if err != nil {
		return nil, err
	}
	if res == nil {
		return nil, nil
	}

	item.UnitPrice = res.UnitPrice
	if strings.TrimSpace(item.ProductName) == "" {
		item.ProductName = res.Name
	}
	if strings.TrimSpace(item.ProductCode) == "" {
		item.ProductCode = res.Code
	}
	if strings.TrimSpace(item.IgvAffectationType) == "" {
		item.IgvAffectationType = res.IgvAffectationType
	}
	item.PriceIncludesIgv = res.PriceIncludesIgv

	base := comboPayload{
		ComboID:            combo.ID,
		ComboCode:          res.Code,
		ComboName:          res.Name,
		ComboPrice:         res.UnitPrice,
		ComboQuantity:      item.Quantity,
		Selection:          res.Signature,
		IgvAffectationType: item.IgvAffectationType,
		PriceIncludesIgv:   res.PriceIncludesIgv,
		Snapshot:           true,
	}

	// Lo propio de cocina: cada componente va a su área de preparación.
	payloads := res.ComponentsPayload()
	drafts := make([]comboComponentDraft, 0, len(res.Components))
	for i, c := range res.Components {
		payload := base
		payload.Component = payloads[i]
		area, areaID := resolveComboComponentArea(tx, c)
		drafts = append(drafts, comboComponentDraft{
			ProductID:         c.Product.ID,
			ProductCode:       c.Product.Code,
			ProductName:       c.Product.Name,
			Quantity:          c.Quantity,
			PreparationArea:   area,
			PreparationAreaID: areaID,
			Payload:           payload,
		})
	}
	return drafts, nil
}

// createComboComandas crea una comanda por componente, cada una ruteada a su área de preparación
// y todas atadas por un mismo combo_parent_key. Van a unit_price 0: el dinero del combo vive en
// su línea de venta (ver comandasToBillLines), no repartido entre los componentes.
func createComboComandas(
	tx *gorm.DB,
	orderID, sessionID uint,
	item *NewOrderItem,
	drafts []comboComponentDraft,
) ([]database.TenantComanda, error) {
	parentKey := uuid.NewString()
	out := make([]database.TenantComanda, 0, len(drafts))
	for _, d := range drafts {
		payload := d.Payload
		payload.ComboQuantity = item.Quantity
		raw, err := json.Marshal(payload)
		if err != nil {
			return nil, errors.New("no se pudo serializar combo_json")
		}
		productID := d.ProductID
		c := database.TenantComanda{
			OrderID:            orderID,
			SessionID:          sessionID,
			ProductID:          &productID,
			ProductCode:        d.ProductCode,
			ProductName:        d.ProductName,
			PreparationArea:    d.PreparationArea,
			PreparationAreaID:  d.PreparationAreaID,
			Quantity:           item.Quantity * d.Quantity,
			UnitPrice:          0,
			Notes:              item.Notes,
			IgvAffectationType: payload.IgvAffectationType,
			PriceIncludesIgv:   payload.PriceIncludesIgv,
			ComboParentKey:     parentKey,
			ComboJSON:          string(raw),
			Status:             "pendiente",
		}
		if err := tx.Create(&c).Error; err != nil {
			return nil, err
		}
		if err := gormutil.PersistBoolWithDefault(tx, &c, "price_includes_igv", payload.PriceIncludesIgv); err != nil {
			return nil, err
		}
		c.PriceIncludesIgv = payload.PriceIncludesIgv
		out = append(out, c)
	}
	return out, nil
}

// comboStockContext datos de la venta que originan el movimiento de kardex.
type comboStockContext struct {
	BranchID  uint
	Reference string
	UserID    uint
}

// recordComboComponentStock descuenta el stock de los componentes de los combos vendidos.
//
// El combo se factura como una línea con su product_id, pero el que sale del almacén es cada
// componente: si el pollo lleva control de stock y el agua no, solo se mueve el pollo. Las
// comandas de componentes ya traen la cantidad multiplicada por los combos pedidos.
func recordComboComponentStock(
	tx *gorm.DB,
	inv comboStockRecorder,
	comandas []database.TenantComanda,
	ctx comboStockContext,
) error {
	// Un mismo componente puede venir en varias comandas (varios combos): se acumula para
	// dejar un solo asiento por producto, como hace el flujo de líneas normales.
	totals := map[uint]float64{}
	order := make([]uint, 0, len(comandas))
	for _, c := range comandas {
		if strings.TrimSpace(c.ComboParentKey) == "" || c.ProductID == nil {
			continue
		}
		if _, seen := totals[*c.ProductID]; !seen {
			order = append(order, *c.ProductID)
		}
		totals[*c.ProductID] += c.Quantity
	}

	for _, productID := range order {
		var product database.TenantProduct
		if tx.First(&product, productID).Error != nil {
			continue
		}
		if !product.ManageStock {
			continue
		}
		if err := inv.RecordMovementTx(tx, invsvc.MovementInput{
			ProductID:     productID,
			BranchID:      ctx.BranchID,
			Type:          "out",
			Quantity:      totals[productID],
			Reference:     ctx.Reference,
			UserID:        ctx.UserID,
			OperationCode: "SALE",
		}); err != nil {
			return err
		}
	}
	return nil
}

// comboStockRecorder lo que necesitamos del servicio de inventario (facilita el test).
type comboStockRecorder interface {
	RecordMovementTx(tx *gorm.DB, in invsvc.MovementInput) error
}


// resolveComboComponentArea rutea el componente a su área de preparación. Prioriza el id
// snapshotado en el item del combo y cae al área viva del producto.
func resolveComboComponentArea(tx *gorm.DB, c comboChosenComponent) (string, *uint) {
	areaID := c.AreaIDSnap
	if areaID == nil || *areaID == 0 {
		areaID = c.Product.PreparationAreaID
	}
	if areaID != nil && *areaID > 0 {
		var area database.TenantPreparationArea
		if err := tx.Select("slug").First(&area, *areaID).Error; err == nil {
			if slug := strings.TrimSpace(strings.ToLower(area.Slug)); slug != "" {
				return slug, areaID
			}
		}
	}
	if slug := strings.TrimSpace(strings.ToLower(c.Product.PreparationArea)); slug != "" {
		return slug, areaID
	}
	return "cocina", areaID
}


// comboSaleLineKey clave de agrupación en la venta. No usa combo_parent_key: dos combos
// idénticos pedidos por separado deben fundirse en una sola línea, como el resto del flujo.
func comboSaleLineKey(p *comboPayload) string {
	return fmt.Sprintf("combo|%d|%s|%.4f", p.ComboID, p.Selection, p.ComboPrice)
}

// comboLineModifiersSnapshot describe los componentes del combo para la línea de venta,
// reutilizando la forma de modifiers_json que el frontend ya sabe pintar.
func comboLineModifiersSnapshot(comandas []database.TenantComanda) string {
	components := make([]comboComponentPayload, 0, len(comandas))
	for _, c := range comandas {
		p, err := parseComboPayload(c.ComboJSON)
		if err != nil || p == nil {
			continue
		}
		components = append(components, p.Component)
	}
	return combos.ComponentsSnapshot(components)
}

// comboDraftsSnapshot igual que comboLineModifiersSnapshot pero desde los drafts, para la
// venta directa (que no crea comandas). Ambos caminos deben producir el mismo detalle: el
// ticket de una venta rápida y el de una mesa tienen que verse igual.
func comboDraftsSnapshot(drafts []comboComponentDraft) string {
	components := make([]comboComponentPayload, 0, len(drafts))
	for _, d := range drafts {
		components = append(components, d.Payload.Component)
	}
	return combos.ComponentsSnapshot(components)
}


// billLine línea cobrable derivada de las comandas. Un combo son N comandas (una por área de
// preparación) pero una sola línea de dinero: la de su precio fijo.
type billLine struct {
	Key                string
	ProductID          *uint
	Code               string
	Name               string
	Quantity           float64
	UnitPrice          float64
	Notes              string
	ModifiersJSON      string
	IgvAffectationType string
	PriceIncludesIgv   bool
	IsCombo            bool
	// Comanda origen (solo líneas sueltas): el IGV se resuelve con comandaIgvForCalc.
	Comanda *database.TenantComanda
}

// comandasToBillLines colapsa las comandas de cada combo en una línea y deja el resto igual.
// El combo aparece en la posición de su primer componente, así que el orden se conserva.
// Fuente única de verdad para precuenta y facturación: si divergieran, el cliente vería un
// precio y pagaría otro.
func comandasToBillLines(comandas []database.TenantComanda) []billLine {
	byParent := make(map[string][]database.TenantComanda)
	for _, c := range comandas {
		if pk := strings.TrimSpace(c.ComboParentKey); pk != "" {
			byParent[pk] = append(byParent[pk], c)
		}
	}

	out := make([]billLine, 0, len(comandas))
	seen := make(map[string]struct{}, len(byParent))
	for i := range comandas {
		c := comandas[i]
		pk := strings.TrimSpace(c.ComboParentKey)
		if pk == "" {
			out = append(out, billLine{
				Key:           comandaSaleLineKey(c),
				ProductID:     c.ProductID,
				Code:          c.ProductCode,
				Name:          c.ProductName,
				Quantity:      c.Quantity,
				UnitPrice:     c.UnitPrice,
				Notes:         c.Notes,
				ModifiersJSON: strings.TrimSpace(c.ModifiersJSON),
				Comanda:       &comandas[i],
			})
			continue
		}
		if _, ok := seen[pk]; ok {
			continue // ya contabilizado por el primer componente del combo
		}
		seen[pk] = struct{}{}

		group := byParent[pk]
		payload, err := parseComboPayload(c.ComboJSON)
		if err != nil || payload == nil {
			// combo_json ilegible: se degrada a línea suelta antes que perder el ítem.
			out = append(out, billLine{
				Key:           comandaSaleLineKey(c),
				ProductID:     c.ProductID,
				Code:          c.ProductCode,
				Name:          c.ProductName,
				Quantity:      c.Quantity,
				UnitPrice:     c.UnitPrice,
				Notes:         c.Notes,
				ModifiersJSON: strings.TrimSpace(c.ModifiersJSON),
				Comanda:       &comandas[i],
			})
			continue
		}
		comboID := payload.ComboID
		out = append(out, billLine{
			Key:                comboSaleLineKey(payload),
			ProductID:          &comboID,
			Code:               payload.ComboCode,
			Name:               payload.ComboName,
			Quantity:           payload.ComboQuantity,
			UnitPrice:          payload.ComboPrice,
			Notes:              c.Notes,
			ModifiersJSON:      comboLineModifiersSnapshot(group),
			IgvAffectationType: payload.IgvAffectationType,
			PriceIncludesIgv:   payload.PriceIncludesIgv,
			IsCombo:            true,
		})
	}
	return out
}
