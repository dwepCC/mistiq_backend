package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"tukifac/pkg/database"
	"tukifac/pkg/money"

	"gorm.io/gorm"
)

// modifierPayloadEntry snapshot histórico en comanda/venta (no depende del catálogo vivo).
type modifierPayloadEntry struct {
	GroupID       uint    `json:"group_id"`
	GroupName     string  `json:"group_name"`
	Type          string  `json:"type"`       // variant | modifier
	GroupType     string  `json:"group_type"` // alias estable para reportes
	GroupRequired bool    `json:"group_required,omitempty"`
	OptionID      uint    `json:"option_id"`
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

func isVariantGroup(g database.TenantModifierGroup, product *database.TenantProduct) bool {
	return (product.HasVariants || product.HasModifiers) && g.Required && !g.MultiSelect
}

func isModifierGroup(g database.TenantModifierGroup, product *database.TenantProduct) bool {
	return product.HasModifiers && !(g.Required && !g.MultiSelect)
}

// resolveRestaurantOrderItem recalcula precio unitario y modifiers_json desde catálogo (no confía en el cliente).
// Ítems manuales (sin product_id) conservan unit_price enviado.
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

	unit, canonJSON, err := calcRestaurantUnitPrice(tx, &product, item.ModifiersJSON)
	if err != nil {
		return err
	}
	item.UnitPrice = unit
	item.ModifiersJSON = canonJSON
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

	groups, groupByID, err := loadProductModifierGroups(tx, product.ID)
	if err != nil {
		return 0, "", err
	}

	if len(entries) == 0 {
		if err := validateRequiredSelections(product, groups, groupByID, nil); err != nil {
			return 0, "", err
		}
		return base, "", nil
	}

	optionIDs := make([]uint, 0, len(entries))
	for _, e := range entries {
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

	canonical := make([]modifierPayloadEntry, 0, len(entries))
	variantsByGroup := map[uint]uint{}
	modifiersByGroup := map[uint][]uint{}

	for _, e := range entries {
		opt, ok := optByID[e.OptionID]
		if !ok {
			return 0, "", fmt.Errorf("opción de modificador inválida (id %d)", e.OptionID)
		}
		g, ok := groupByID[opt.GroupID]
		if !ok {
			return 0, "", fmt.Errorf("el modificador no pertenece al producto")
		}

		wantVariant := e.Type == "variant"
		if wantVariant && !isVariantGroup(g, product) {
			return 0, "", fmt.Errorf("opción inválida para variante en «%s»", g.Name)
		}
		if !wantVariant && !isModifierGroup(g, product) {
			return 0, "", fmt.Errorf("opción inválida para extra en «%s»", g.Name)
		}

		entryType := "modifier"
		if wantVariant {
			entryType = "variant"
			if prev, exists := variantsByGroup[g.ID]; exists && prev != opt.ID {
				return 0, "", fmt.Errorf("solo una variante permitida en «%s»", g.Name)
			}
			variantsByGroup[g.ID] = opt.ID
		} else {
			for _, id := range modifiersByGroup[g.ID] {
				if id == opt.ID {
					return 0, "", fmt.Errorf("opción duplicada en «%s»", g.Name)
				}
			}
			if !g.MultiSelect && len(modifiersByGroup[g.ID]) >= 1 {
				return 0, "", fmt.Errorf("solo una opción permitida en «%s»", g.Name)
			}
			modifiersByGroup[g.ID] = append(modifiersByGroup[g.ID], opt.ID)
		}

		canonical = append(canonical, modifierPayloadEntry{
			GroupID:       g.ID,
			GroupName:     g.Name,
			Type:          entryType,
			GroupType:     entryType,
			GroupRequired: g.Required,
			OptionID:      opt.ID,
			OptionName:    opt.Name,
			ExtraPrice:    money.RoundDisplay(opt.ExtraPrice),
			Snapshot:      true,
		})
	}

	if err := validateRequiredSelections(product, groups, groupByID, canonical); err != nil {
		return 0, "", err
	}

	var extras float64
	for _, c := range canonical {
		extras += c.ExtraPrice
	}
	unit := money.RoundDisplay(base + extras)

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
	byID := make(map[uint]database.TenantModifierGroup, len(groups))
	for _, g := range groups {
		byID[g.ID] = g
	}
	return groups, byID, nil
}

func validateRequiredSelections(
	product *database.TenantProduct,
	groups []database.TenantModifierGroup,
	groupByID map[uint]database.TenantModifierGroup,
	selected []modifierPayloadEntry,
) error {
	variantPicked := map[uint]bool{}
	modifierPicked := map[uint]int{}
	for _, s := range selected {
		if s.Type == "variant" {
			variantPicked[s.GroupID] = true
		} else {
			modifierPicked[s.GroupID]++
		}
	}

	for _, g := range groups {
		if isVariantGroup(g, product) {
			if g.Required && !variantPicked[g.ID] {
				return fmt.Errorf("falta elegir variante en «%s»", g.Name)
			}
			continue
		}
		if isModifierGroup(g, product) {
			n := modifierPicked[g.ID]
			if g.Required && n == 0 {
				return fmt.Errorf("falta elegir extra en «%s»", g.Name)
			}
			if !g.MultiSelect && n > 1 {
				return fmt.Errorf("solo una opción en «%s»", g.Name)
			}
		}
	}
	_ = groupByID
	return nil
}
