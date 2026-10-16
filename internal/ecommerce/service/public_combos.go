package service

import (
	"fmt"
	"strings"

	"tukifac/internal/catalog/combos"
	productservice "tukifac/internal/products/service"
	"tukifac/pkg/database"
	"tukifac/pkg/stockreserve"
)

// PublicComboOption una opción de un grupo del combo, tal como la ve el cliente.
type PublicComboOption struct {
	ProductID       uint    `json:"product_id"`
	Name            string  `json:"name"`
	ExtraPrice      float64 `json:"extra_price"`
	DefaultQuantity float64 `json:"default_quantity"`
	MaxQuantity     float64 `json:"max_quantity"`
	IsDefault       bool    `json:"is_default"`
	// Available false = no alcanza el stock de este componente (solo se calcula si la tienda muestra stock).
	Available bool `json:"available"`
}

// PublicComboGroup un grupo del combo: fijo (siempre va), elegir uno, o elegir varios.
type PublicComboGroup struct {
	ID            uint                `json:"id"`
	Name          string              `json:"name"`
	SelectionType string              `json:"selection_type"` // fixed | single | multiple
	MinSelect     int                 `json:"min_select"`
	MaxSelect     int                 `json:"max_select"`
	AllowQuantity bool                `json:"allow_quantity"`
	Options       []PublicComboOption `json:"options"`
}

// PublicCombo la configuración de un combo que el cliente necesita para armarlo.
type PublicCombo struct {
	Groups []PublicComboGroup `json:"groups"`
	// Available false = el combo no se puede armar con el stock actual.
	Available bool `json:"available"`
}

// PublicProduct producto del catálogo público; los combos traen además sus grupos.
type PublicProduct struct {
	productservice.ProductReportItem
	Combo *PublicCombo `json:"combo,omitempty"`
}

// attachPublicCombos convierte la lista del catálogo y, para los combos, agrega sus grupos y opciones. Un
// combo sin grupos u opciones no se puede vender y no se muestra.
func (s *EcommerceService) attachPublicCombos(items []productservice.ProductReportItem, showStock bool) []PublicProduct {
	out := make([]PublicProduct, 0, len(items))

	// Stock disponible (almacén de todas las sucursales menos lo reservado) de los componentes.
	available := map[uint]float64{}
	managed := map[uint]bool{}
	type pending struct {
		idx    int
		groups []database.TenantComboGroup
		items  map[uint][]combos.CatalogItem
	}
	var combosFound []pending
	var componentIDs []uint

	for _, it := range items {
		out = append(out, PublicProduct{ProductReportItem: it})
		if !it.HasCombo {
			continue
		}
		groups, byGroup, err := combos.LoadCatalog(s.db, it.ID)
		if err != nil || len(groups) == 0 {
			out[len(out)-1].Combo = &PublicCombo{Groups: nil, Available: false}
			continue
		}
		combosFound = append(combosFound, pending{idx: len(out) - 1, groups: groups, items: byGroup})
		for _, g := range groups {
			for _, ci := range byGroup[g.ID] {
				componentIDs = append(componentIDs, ci.Product.ID)
				managed[ci.Product.ID] = ci.Product.ManageStock && ci.Product.Type != "service"
			}
		}
	}
	if showStock && len(componentIDs) > 0 {
		var stocks []struct {
			ProductID uint
			Qty       float64
		}
		s.db.Model(&database.TenantProductStock{}).
			Select("product_id AS product_id, SUM(quantity) AS qty").
			Where("product_id IN ?", componentIDs).Group("product_id").Scan(&stocks)
		for _, st := range stocks {
			available[st.ProductID] = st.Qty
		}
		if reserved, err := stockreserve.ReservedByProduct(s.db, 0, componentIDs); err == nil {
			for id, r := range reserved {
				available[id] -= r
			}
		}
	}

	for _, p := range combosFound {
		combo := &PublicCombo{Available: true}
		for _, g := range p.groups {
			pg := PublicComboGroup{
				ID: g.ID, Name: g.Name, SelectionType: g.SelectionType,
				MinSelect: g.MinSelect, MaxSelect: g.MaxSelect, AllowQuantity: g.AllowQuantity,
			}
			availableOptions := 0
			for _, ci := range p.items[g.ID] {
				ok := true
				if showStock && managed[ci.Product.ID] {
					need := ci.Item.DefaultQuantity
					if need <= 0 {
						need = 1
					}
					ok = available[ci.Product.ID] >= need
				}
				if ok {
					availableOptions++
				}
				pg.Options = append(pg.Options, PublicComboOption{
					ProductID: ci.Product.ID, Name: ci.Product.Name, ExtraPrice: ci.Item.ExtraPrice,
					DefaultQuantity: ci.Item.DefaultQuantity, MaxQuantity: ci.Item.MaxQuantity,
					IsDefault: ci.Item.IsDefault, Available: ok,
				})
			}
			switch {
			case len(pg.Options) == 0:
				combo.Available = false
			case g.SelectionType == database.ComboSelectionFixed && availableOptions < len(pg.Options):
				combo.Available = false
			case g.SelectionType == database.ComboSelectionSingle && availableOptions == 0:
				combo.Available = false
			case g.SelectionType == database.ComboSelectionMultiple && availableOptions < g.MinSelect:
				combo.Available = false
			}
			combo.Groups = append(combo.Groups, pg)
		}
		out[p.idx].Combo = combo
	}

	// Los combos que no se pueden armar (sin grupos u opciones) no se ofrecen.
	filtered := out[:0]
	for _, pp := range out {
		if pp.HasCombo && (pp.Combo == nil || len(pp.Combo.Groups) == 0) {
			continue
		}
		filtered = append(filtered, pp)
	}
	return filtered
}

// comboIncludes resume los componentes de una línea de combo para mostrarlos al cliente.
func comboIncludes(raw string) string {
	comps := stockreserve.ParseComponents(raw)
	parts := make([]string, 0, len(comps))
	for _, c := range comps {
		if c.Quantity > 1 {
			parts = append(parts, fmt.Sprintf("%g × %s", c.Quantity, c.ProductName))
		} else {
			parts = append(parts, c.ProductName)
		}
	}
	return strings.Join(parts, ", ")
}
