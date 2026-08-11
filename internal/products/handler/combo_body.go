package handler

import (
	"tukifac/internal/products/service"
)

// comboGroupBody grupo de combo tal como llega del panel.
type comboGroupBody struct {
	Name          string              `json:"name"`
	SelectionType string              `json:"selection_type"` // fixed | single | multiple
	MinSelect     int                 `json:"min_select"`
	MaxSelect     int                 `json:"max_select"`
	AllowQuantity bool                `json:"allow_quantity"`
	SortOrder     int                 `json:"sort_order"`
	Items         []comboGroupItemBody `json:"items"`
}

type comboGroupItemBody struct {
	ProductID       uint    `json:"product_id"`
	DefaultQuantity float64 `json:"default_quantity"`
	MaxQuantity     float64 `json:"max_quantity"`
	ExtraPrice      float64 `json:"extra_price"`
	IsDefault       bool    `json:"is_default"`
	SortOrder       int     `json:"sort_order"`
}

// toComboGroupInputs traduce el body a inputs del service. La validación real vive en el
// service (buildComboGroupDrafts), que es quien consulta el catálogo.
func toComboGroupInputs(bodies []comboGroupBody) []service.ComboGroupInput {
	out := make([]service.ComboGroupInput, 0, len(bodies))
	for _, b := range bodies {
		items := make([]service.ComboGroupItemInput, 0, len(b.Items))
		for _, it := range b.Items {
			items = append(items, service.ComboGroupItemInput{
				ProductID:       it.ProductID,
				DefaultQuantity: it.DefaultQuantity,
				MaxQuantity:     it.MaxQuantity,
				ExtraPrice:      it.ExtraPrice,
				IsDefault:       it.IsDefault,
				SortOrder:       it.SortOrder,
			})
		}
		out = append(out, service.ComboGroupInput{
			Name:          b.Name,
			SelectionType: b.SelectionType,
			MinSelect:     b.MinSelect,
			MaxSelect:     b.MaxSelect,
			AllowQuantity: b.AllowQuantity,
			SortOrder:     b.SortOrder,
			Items:         items,
		})
	}
	return out
}
