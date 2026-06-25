package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"tukifac/pkg/database"
	"tukifac/pkg/modifierkind"
	"tukifac/pkg/money"

	"gorm.io/gorm"
)

// modifierPayloadEntry snapshot histórico en comanda/venta (no depende del catálogo vivo).
type modifierPayloadEntry struct {
	GroupID       uint    `json:"group_id"`
	GroupName     string  `json:"group_name"`
	Type          string  `json:"type"`       // variant = presentación del producto; modifier = extra global
	GroupType     string  `json:"group_type"` // alias estable para reportes
	GroupRequired bool    `json:"group_required,omitempty"`
	OptionID      uint    `json:"option_id"` // presentación: tenant_product_presentations.id; extra: modifier_option.id
	OptionName    string  `json:"option_name"`
	ExtraPrice    float64 `json:"extra_price"`
	Snapshot      bool    `json:"snapshot"`
}

func parseModifierPayload(raw string) ([]modifierPayloadEntry, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	var entries []modifierPayloadEntry
	if err := json.Unmarshal([]byte(raw), &entries); err != nil {
		return nil, errors.New("modifiers_json inválido")
	}
	out := make([]modifierPayloadEntry, 0, len(entries))
	for _, e := range entries {
		if e.OptionName == "" && e.OptionID == 0 {
			continue
		}
		if e.Type != "variant" {
			e.Type = "modifier"
		}
		out = append(out, e)
	}
	return out, nil
}

func isExtraModifierGroup(g database.TenantModifierGroup) bool {
	return modifierkind.IsExtra(g.Kind, g.Required, g.MultiSelect)
}

// resolveRestaurantOrderItem valida producto/modificadores y canonicaliza modifiers_json.
// Si el cliente envía unit_price > 0 (precio acordado en caja/mesa), se conserva; si no, se usa el catálogo.
func resolveRestaurantOrderItem(tx *gorm.DB, item *NewOrderItem) error {
	if item.ProductID == nil || *item.ProductID == 0 {
		if strings.TrimSpace(item.IgvAffectationType) == "" {
			item.IgvAffectationType = "10"
		}
		return nil
	}

	var product database.TenantProduct
	if err := tx.First(&product, *item.ProductID).Error; err != nil {
		return errors.New("producto no encontrado")
	}
	if !product.Active {
		return errors.New("producto inactivo")
	}

	clientUnit := money.RoundDisplay(item.UnitPrice)
	unit, canonJSON, err := calcRestaurantUnitPrice(tx, &product, item.ModifiersJSON)
	if err != nil {
		return err
	}
	item.ModifiersJSON = canonJSON
	if clientUnit > 0 {
		item.UnitPrice = clientUnit
	} else {
		item.UnitPrice = unit
	}
	if strings.TrimSpace(item.ProductName) == "" {
		item.ProductName = product.Name
	}
	if strings.TrimSpace(item.ProductCode) == "" {
		item.ProductCode = product.Code
	}
	if strings.TrimSpace(item.IgvAffectationType) == "" {
		if product.IgvAffectationType != "" {
			item.IgvAffectationType = product.IgvAffectationType
		} else {
			item.IgvAffectationType = "10"
		}
	}
	item.PriceIncludesIgv = product.PriceIncludesIgv
	return nil
}

func calcRestaurantUnitPrice(tx *gorm.DB, product *database.TenantProduct, modifiersJSON string) (float64, string, error) {
	base := money.RoundDisplay(product.SalePrice)

	entries, err := parseModifierPayload(modifiersJSON)
	if err != nil {
		return 0, "", err
	}

	presentations, err := loadProductPresentations(tx, product.ID)
	if err != nil {
		return 0, "", err
	}
	groups, groupByID, err := loadProductModifierGroups(tx, product.ID)
	if err != nil {
		return 0, "", err
	}

	if len(entries) == 0 {
		if err := validateRequiredSelections(product, presentations, groups, groupByID, nil); err != nil {
			return 0, "", err
		}
		return base, "", nil
	}

	var variantEntries []modifierPayloadEntry
	var modifierEntries []modifierPayloadEntry
	for _, e := range entries {
		if e.Type == "variant" {
			variantEntries = append(variantEntries, e)
		} else {
			modifierEntries = append(modifierEntries, e)
		}
	}

	presByID := make(map[uint]database.TenantProductPresentation, len(presentations))
	for _, p := range presentations {
		presByID[p.ID] = p
	}

	canonical := make([]modifierPayloadEntry, 0, len(entries))

	if len(variantEntries) > 1 {
		return 0, "", errors.New("solo se permite una presentación por producto")
	}
	if len(variantEntries) == 1 {
		e := variantEntries[0]
		pres, ok := presByID[e.OptionID]
		if !ok {
			return 0, "", fmt.Errorf("presentación inválida (id %d)", e.OptionID)
		}
		canonical = append(canonical, modifierPayloadEntry{
			GroupID:       0,
			GroupName:     "Presentación",
			Type:          "variant",
			GroupType:     "variant",
			OptionID:      pres.ID,
			OptionName:    pres.Name,
			ExtraPrice:    money.RoundDisplay(pres.SalePrice),
			Snapshot:      true,
		})
	}

	if len(modifierEntries) > 0 {
		optionIDs := make([]uint, 0, len(modifierEntries))
		for _, e := range modifierEntries {
			optionIDs = append(optionIDs, e.OptionID)
		}
		var options []database.TenantModifierOption
		if err := tx.Where("id IN ? AND active = ?", optionIDs, true).Find(&options).Error; err != nil {
			return 0, "", err
		}
		optByID := make(map[uint]database.TenantModifierOption, len(options))
		for _, o := range options {
			optByID[o.ID] = o
		}
		modifiersByGroup := map[uint][]uint{}

		for _, e := range modifierEntries {
			opt, ok := optByID[e.OptionID]
			if !ok {
				return 0, "", fmt.Errorf("opción de extra inválida (id %d)", e.OptionID)
			}
			g, ok := groupByID[opt.GroupID]
			if !ok || !isExtraModifierGroup(g) {
				return 0, "", fmt.Errorf("el extra no pertenece al producto")
			}
			for _, id := range modifiersByGroup[g.ID] {
				if id == opt.ID {
					return 0, "", fmt.Errorf("opción duplicada en «%s»", g.Name)
				}
			}
			if !g.MultiSelect && len(modifiersByGroup[g.ID]) >= 1 {
				return 0, "", fmt.Errorf("solo una opción permitida en «%s»", g.Name)
			}
			modifiersByGroup[g.ID] = append(modifiersByGroup[g.ID], opt.ID)

			canonical = append(canonical, modifierPayloadEntry{
				GroupID:       g.ID,
				GroupName:     g.Name,
				Type:          "modifier",
				GroupType:     "modifier",
				GroupRequired: g.Required,
				OptionID:      opt.ID,
				OptionName:    opt.Name,
				ExtraPrice:    money.RoundDisplay(opt.ExtraPrice),
				Snapshot:      true,
			})
		}
	}

	if err := validateRequiredSelections(product, presentations, groups, groupByID, canonical); err != nil {
		return 0, "", err
	}

	unit := base
	var presentationPrice float64
	var hasPresentation bool
	var extrasSum float64
	for _, c := range canonical {
		if c.Type == "variant" {
			hasPresentation = true
			presentationPrice = c.ExtraPrice
		} else {
			extrasSum += c.ExtraPrice
		}
	}
	if hasPresentation && presentationPrice > 0 {
		unit = presentationPrice
	}
	unit = money.RoundDisplay(unit + extrasSum)

	canonJSON := ""
	if len(canonical) > 0 {
		b, err := json.Marshal(canonical)
		if err != nil {
			return 0, "", errors.New("no se pudo serializar modifiers_json")
		}
		canonJSON = string(b)
	}

	return unit, canonJSON, nil
}

func loadProductPresentations(tx *gorm.DB, productID uint) ([]database.TenantProductPresentation, error) {
	var rows []database.TenantProductPresentation
	err := tx.Where("product_id = ? AND active = ?", productID, true).
		Order("sort_order ASC, id ASC").
		Find(&rows).Error
	return rows, err
}

func loadProductModifierGroups(tx *gorm.DB, productID uint) ([]database.TenantModifierGroup, map[uint]database.TenantModifierGroup, error) {
	var links []database.TenantProductModifierGroup
	if err := tx.Where("product_id = ?", productID).Find(&links).Error; err != nil {
		return nil, nil, err
	}
	if len(links) == 0 {
		return nil, map[uint]database.TenantModifierGroup{}, nil
	}
	groupIDs := make([]uint, 0, len(links))
	for _, l := range links {
		groupIDs = append(groupIDs, l.GroupID)
	}
	var groups []database.TenantModifierGroup
	if err := tx.Where("id IN ? AND active = ?", groupIDs, true).Find(&groups).Error; err != nil {
		return nil, nil, err
	}
	filtered := make([]database.TenantModifierGroup, 0, len(groups))
	byID := make(map[uint]database.TenantModifierGroup, len(groups))
	for _, g := range groups {
		if !isExtraModifierGroup(g) {
			continue
		}
		filtered = append(filtered, g)
		byID[g.ID] = g
	}
	return filtered, byID, nil
}

func validateRequiredSelections(
	product *database.TenantProduct,
	presentations []database.TenantProductPresentation,
	groups []database.TenantModifierGroup,
	groupByID map[uint]database.TenantModifierGroup,
	selected []modifierPayloadEntry,
) error {
	hasPresentation := false
	for _, s := range selected {
		if s.Type == "variant" {
			hasPresentation = true
			break
		}
	}
	if product.HasVariants && len(presentations) > 0 && !hasPresentation {
		return errors.New("debe elegir una presentación del producto")
	}

	modifierPicked := map[uint]int{}
	for _, s := range selected {
		if s.Type == "modifier" {
			modifierPicked[s.GroupID]++
		}
	}
	for _, g := range groups {
		if !isExtraModifierGroup(g) {
			continue
		}
		n := modifierPicked[g.ID]
		if g.Required && n == 0 {
			return fmt.Errorf("falta elegir extra en «%s»", g.Name)
		}
		if !g.MultiSelect && n > 1 {
			return fmt.Errorf("solo una opción en «%s»", g.Name)
		}
	}
	_ = groupByID
	return nil
}
