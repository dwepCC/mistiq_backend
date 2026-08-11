// Package combos resuelve combos/promociones: productos agrupados que se venden a un
// precio fijo (p. ej. «Promoción verano» = polo + pantalón por S/ 20 en vez de S/ 40).
//
// Vive fuera de internal/restaurant porque la lógica es la misma para el panel ERP y para
// el de restaurante: lo único propio del restaurante es rutear cada componente a su área
// de preparación al mandar la comanda. Aquí solo se valida la elección, se calcula el
// precio y se resuelven los componentes.
package combos

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"tukifac/pkg/database"
	"tukifac/pkg/money"

	"gorm.io/gorm"
)

// Selection: lo que el cliente eligió en un grupo. Los grupos fijos no hace falta enviarlos.
type Selection struct {
	GroupID uint            `json:"group_id"`
	Items   []SelectionItem `json:"items"`
}

type SelectionItem struct {
	ProductID uint    `json:"product_id"`
	Quantity  float64 `json:"quantity"`
}

// ChosenComponent: componente ya resuelto contra el catálogo.
type ChosenComponent struct {
	GroupID       uint
	GroupName     string
	SelectionType string
	Product       database.TenantProduct
	Quantity      float64
	ExtraPrice    float64
	// AreaIDSnap: área de preparación snapshotada al armar el combo. Solo la usa el flujo
	// de restaurante para rutear la comanda; en el ERP se ignora.
	AreaIDSnap *uint
}

// ComponentPayload: snapshot histórico de un componente (no depende del catálogo vivo).
type ComponentPayload struct {
	GroupID       uint    `json:"group_id"`
	GroupName     string  `json:"group_name"`
	SelectionType string  `json:"selection_type"`
	ProductID     uint    `json:"product_id"`
	ProductName   string  `json:"product_name"`
	Quantity      float64 `json:"quantity"`
	ExtraPrice    float64 `json:"extra_price"`
}

// Resolved: resultado de resolver un combo contra la elección del cliente.
type Resolved struct {
	Components []ChosenComponent
	// UnitPrice: precio fijo del combo + Σ (sobreprecio × cantidad) de lo elegido.
	UnitPrice          float64
	Signature          string
	IgvAffectationType string
	PriceIncludesIgv   bool
	Name               string
	Code               string
}

// ComponentsPayload traduce los componentes elegidos a su snapshot.
func (r *Resolved) ComponentsPayload() []ComponentPayload {
	out := make([]ComponentPayload, 0, len(r.Components))
	for _, c := range r.Components {
		out = append(out, ComponentPayload{
			GroupID:       c.GroupID,
			GroupName:     c.GroupName,
			SelectionType: c.SelectionType,
			ProductID:     c.Product.ID,
			ProductName:   c.Product.Name,
			Quantity:      c.Quantity,
			ExtraPrice:    money.RoundDisplay(c.ExtraPrice),
		})
	}
	return out
}

// StockMovements: cuánto sale de almacén por cada componente, para `quantity` combos
// vendidos. El combo no tiene stock propio.
func (r *Resolved) StockMovements(quantity float64) map[uint]float64 {
	out := make(map[uint]float64, len(r.Components))
	for _, c := range r.Components {
		out[c.Product.ID] += quantity * c.Quantity
	}
	return out
}

func ParseSelections(raw string) ([]Selection, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var out []Selection
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, errors.New("combo_json inválido")
	}
	return out, nil
}

// Resolve valida la elección del cliente contra el combo y calcula precio y componentes.
//
// clientUnitPrice > 0 respeta el precio acordado en caja; si es 0 se usa el del catálogo.
func Resolve(
	tx *gorm.DB,
	combo database.TenantProduct,
	comboJSON string,
	clientUnitPrice float64,
) (*Resolved, error) {
	if !combo.HasCombo {
		return nil, nil
	}

	groups, itemsByGroup, err := LoadCatalog(tx, combo.ID)
	if err != nil {
		return nil, err
	}
	if len(groups) == 0 {
		return nil, fmt.Errorf("el combo «%s» no tiene grupos configurados", combo.Name)
	}

	selections, err := ParseSelections(comboJSON)
	if err != nil {
		return nil, err
	}
	selByGroup := make(map[uint][]SelectionItem, len(selections))
	for _, s := range selections {
		selByGroup[s.GroupID] = append(selByGroup[s.GroupID], s.Items...)
	}

	chosen := make([]ChosenComponent, 0, len(groups))
	for _, g := range groups {
		picks, err := ResolveGroupSelection(g, itemsByGroup[g.ID], selByGroup[g.ID])
		if err != nil {
			return nil, err
		}
		chosen = append(chosen, picks...)
	}
	if len(chosen) == 0 {
		return nil, fmt.Errorf("el combo «%s» no tiene componentes seleccionados", combo.Name)
	}

	extras := 0.0
	for _, c := range chosen {
		extras += c.ExtraPrice * c.Quantity
	}
	unit := money.RoundDisplay(money.RoundDisplay(combo.SalePrice) + extras)
	if money.RoundDisplay(clientUnitPrice) > 0 {
		unit = money.RoundDisplay(clientUnitPrice)
	}

	affType := strings.TrimSpace(combo.IgvAffectationType)
	if affType == "" {
		affType = "10"
	}

	return &Resolved{
		Components:         chosen,
		UnitPrice:          unit,
		Signature:          Signature(chosen),
		IgvAffectationType: affType,
		PriceIncludesIgv:   combo.PriceIncludesIgv,
		Name:               combo.Name,
		Code:               combo.Code,
	}, nil
}

// CatalogItem: item del combo junto al producto componente.
type CatalogItem struct {
	Item    database.TenantComboGroupItem
	Product database.TenantProduct
}

// LoadCatalog carga grupos e items activos del combo (3 consultas, sin N+1).
func LoadCatalog(tx *gorm.DB, comboID uint) ([]database.TenantComboGroup, map[uint][]CatalogItem, error) {
	var groups []database.TenantComboGroup
	if err := tx.Where("product_id = ? AND active = ?", comboID, true).
		Order("sort_order ASC, id ASC").Find(&groups).Error; err != nil {
		return nil, nil, err
	}
	if len(groups) == 0 {
		return nil, map[uint][]CatalogItem{}, nil
	}
	groupIDs := make([]uint, 0, len(groups))
	for _, g := range groups {
		groupIDs = append(groupIDs, g.ID)
	}
	var items []database.TenantComboGroupItem
	if err := tx.Where("group_id IN ? AND active = ?", groupIDs, true).
		Order("sort_order ASC, id ASC").Find(&items).Error; err != nil {
		return nil, nil, err
	}
	productIDs := make([]uint, 0, len(items))
	for _, it := range items {
		productIDs = append(productIDs, it.ProductID)
	}
	products := map[uint]database.TenantProduct{}
	if len(productIDs) > 0 {
		var rows []database.TenantProduct
		if err := tx.Where("id IN ?", productIDs).Find(&rows).Error; err != nil {
			return nil, nil, err
		}
		for _, p := range rows {
			products[p.ID] = p
		}
	}
	byGroup := make(map[uint][]CatalogItem, len(groups))
	for _, it := range items {
		p, ok := products[it.ProductID]
		if !ok || !p.Active {
			// Componente borrado o desactivado tras armar el combo: no se puede servir.
			continue
		}
		byGroup[it.GroupID] = append(byGroup[it.GroupID], CatalogItem{Item: it, Product: p})
	}
	return groups, byGroup, nil
}

// ResolveGroupSelection aplica las reglas del grupo a lo que eligió el cliente.
func ResolveGroupSelection(
	g database.TenantComboGroup,
	catalog []CatalogItem,
	picks []SelectionItem,
) ([]ChosenComponent, error) {
	byProduct := make(map[uint]CatalogItem, len(catalog))
	for _, it := range catalog {
		byProduct[it.Item.ProductID] = it
	}

	switch g.SelectionType {
	case database.ComboSelectionFixed:
		// Sin elección: TODOS los componentes del grupo van siempre, en su cantidad por
		// defecto. Antes solo entraba el primero, mientras el precio de referencia
		// (comboReferencePrice) ya sumaba todos: el combo cobraba por más de lo que
		// entregaba y el panel mostraba componentes que nunca se descontaban.
		if len(catalog) == 0 {
			return nil, fmt.Errorf("el grupo «%s» no tiene componente configurado", g.Name)
		}
		out := make([]ChosenComponent, 0, len(catalog))
		for _, it := range catalog {
			out = append(out, newChosen(g, it, it.Item.DefaultQuantity))
		}
		return out, nil

	case database.ComboSelectionSingle:
		if len(picks) != 1 {
			return nil, fmt.Errorf("debe elegir una opción en «%s»", g.Name)
		}
		it, ok := byProduct[picks[0].ProductID]
		if !ok {
			return nil, fmt.Errorf("la opción elegida no pertenece a «%s»", g.Name)
		}
		return []ChosenComponent{newChosen(g, it, it.Item.DefaultQuantity)}, nil

	case database.ComboSelectionMultiple:
		seen := make(map[uint]struct{}, len(picks))
		out := make([]ChosenComponent, 0, len(picks))
		for _, p := range picks {
			it, ok := byProduct[p.ProductID]
			if !ok {
				return nil, fmt.Errorf("una de las opciones elegidas no pertenece a «%s»", g.Name)
			}
			if _, dup := seen[p.ProductID]; dup {
				return nil, fmt.Errorf("opción duplicada en «%s»", g.Name)
			}
			seen[p.ProductID] = struct{}{}

			qty := p.Quantity
			if !g.AllowQuantity || qty <= 0 {
				qty = it.Item.DefaultQuantity
			}
			if qty <= 0 {
				qty = 1
			}
			if g.AllowQuantity && it.Item.MaxQuantity > 0 && qty > it.Item.MaxQuantity {
				return nil, fmt.Errorf("«%s»: la cantidad máxima de «%s» es %g", g.Name, it.Product.Name, it.Item.MaxQuantity)
			}
			out = append(out, newChosen(g, it, qty))
		}
		if len(out) < g.MinSelect {
			return nil, fmt.Errorf("debe elegir al menos %d opción(es) en «%s»", g.MinSelect, g.Name)
		}
		if g.MaxSelect > 0 && len(out) > g.MaxSelect {
			return nil, fmt.Errorf("solo puede elegir hasta %d opción(es) en «%s»", g.MaxSelect, g.Name)
		}
		return out, nil

	default:
		return nil, fmt.Errorf("el grupo «%s» tiene un tipo de selección inválido", g.Name)
	}
}

func newChosen(g database.TenantComboGroup, it CatalogItem, qty float64) ChosenComponent {
	if qty <= 0 {
		qty = 1
	}
	return ChosenComponent{
		GroupID:       g.ID,
		GroupName:     g.Name,
		SelectionType: g.SelectionType,
		Product:       it.Product,
		Quantity:      qty,
		ExtraPrice:    it.Item.ExtraPrice,
		AreaIDSnap:    it.Item.PreparationAreaID,
	}
}

// Signature: firma determinista de la elección. Dos combos con la misma selección comparten
// línea de venta; con distinta, no.
func Signature(chosen []ChosenComponent) string {
	parts := make([]string, 0, len(chosen))
	for _, c := range chosen {
		parts = append(parts, fmt.Sprintf("%d:%d:%g:%.2f", c.GroupID, c.Product.ID, c.Quantity, c.ExtraPrice))
	}
	sort.Strings(parts)
	return strings.Join(parts, ",")
}

// snapshotEntry reutiliza la forma de modifiers_json que el frontend ya sabe pintar, para
// que los componentes se listen bajo la línea del combo sin tocar el visor de comprobantes.
type snapshotEntry struct {
	GroupID    uint    `json:"group_id"`
	GroupName  string  `json:"group_name"`
	Type       string  `json:"type"`
	GroupType  string  `json:"group_type"`
	OptionID   uint    `json:"option_id"`
	OptionName string  `json:"option_name"`
	ExtraPrice float64 `json:"extra_price"`
	Snapshot   bool    `json:"snapshot"`
}

// ComponentsSnapshot describe los componentes para la línea de venta.
func ComponentsSnapshot(components []ComponentPayload) string {
	entries := make([]snapshotEntry, 0, len(components))
	for _, c := range components {
		entries = append(entries, snapshotEntry{
			GroupID:    c.GroupID,
			GroupName:  c.GroupName,
			Type:       "combo",
			GroupType:  "combo",
			OptionID:   c.ProductID,
			OptionName: ComponentLabel(c),
			ExtraPrice: c.ExtraPrice,
			Snapshot:   true,
		})
	}
	if len(entries) == 0 {
		return ""
	}
	b, err := json.Marshal(entries)
	if err != nil {
		return ""
	}
	return string(b)
}

// ComponentLabel: «2 x Papas fritas» o «Agua mineral».
func ComponentLabel(c ComponentPayload) string {
	if c.Quantity > 1 {
		return fmt.Sprintf("%g x %s", c.Quantity, c.ProductName)
	}
	return c.ProductName
}
