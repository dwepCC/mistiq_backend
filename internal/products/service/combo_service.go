package service

import (
	"errors"
	"fmt"
	"strings"

	"tukifac/pkg/database"
	"tukifac/pkg/money"
)

// ComboGroupInput sección de un combo enviada desde el panel.
type ComboGroupInput struct {
	Name          string
	SelectionType string // fixed | single | multiple (default fixed)
	MinSelect     int
	MaxSelect     int
	AllowQuantity bool
	SortOrder     int
	Items         []ComboGroupItemInput
}

// ComboGroupItemInput producto componente dentro de un grupo.
type ComboGroupItemInput struct {
	ProductID       uint
	DefaultQuantity float64
	MaxQuantity     float64
	ExtraPrice      float64
	IsDefault       bool
	SortOrder       int
}

// ComboGroupItemView item + datos vivos del producto componente (respuesta del API).
type ComboGroupItemView struct {
	database.TenantComboGroupItem
	ProductName      string  `json:"product_name"`
	ProductCode      string  `json:"product_code"`
	ProductSalePrice float64 `json:"product_sale_price"`
	ProductImageURL  string  `json:"product_image_url"`
	PreparationArea  string  `json:"preparation_area"`
}

// ComboGroupView grupo con sus items resueltos.
type ComboGroupView struct {
	database.TenantComboGroup
	Items []ComboGroupItemView `json:"items"`
}

// comboItemDraft item ya validado contra el catálogo vivo.
type comboItemDraft struct {
	row database.TenantComboGroupItem
}

// comboGroupDraft grupo ya normalizado y validado.
type comboGroupDraft struct {
	row   database.TenantComboGroup
	items []comboItemDraft
}

// ListComboGroups devuelve los grupos activos de un combo con sus componentes.
func (s *ProductService) ListComboGroups(productID uint) ([]ComboGroupView, error) {
	var groups []database.TenantComboGroup
	err := s.db.Where("product_id = ? AND active = ?", productID, true).
		Order("sort_order ASC, id ASC").
		Find(&groups).Error
	if err != nil {
		return nil, err
	}
	if len(groups) == 0 {
		return []ComboGroupView{}, nil
	}

	groupIDs := make([]uint, 0, len(groups))
	for _, g := range groups {
		groupIDs = append(groupIDs, g.ID)
	}
	var items []database.TenantComboGroupItem
	if err := s.db.Where("group_id IN ? AND active = ?", groupIDs, true).
		Order("sort_order ASC, id ASC").
		Find(&items).Error; err != nil {
		return nil, err
	}

	products, err := s.comboComponentsByID(items)
	if err != nil {
		return nil, err
	}

	byGroup := make(map[uint][]ComboGroupItemView, len(groups))
	for _, it := range items {
		view := ComboGroupItemView{TenantComboGroupItem: it}
		if p, ok := products[it.ProductID]; ok {
			view.ProductName = p.Name
			view.ProductCode = p.Code
			view.ProductSalePrice = p.SalePrice
			view.ProductImageURL = p.ImageURL
			view.PreparationArea = p.PreparationArea
		}
		byGroup[it.GroupID] = append(byGroup[it.GroupID], view)
	}

	out := make([]ComboGroupView, 0, len(groups))
	for _, g := range groups {
		items := byGroup[g.ID]
		if items == nil {
			items = []ComboGroupItemView{}
		}
		out = append(out, ComboGroupView{TenantComboGroup: g, Items: items})
	}
	return out, nil
}

// comboComponentsByID carga los productos componentes en una sola query.
func (s *ProductService) comboComponentsByID(items []database.TenantComboGroupItem) (map[uint]database.TenantProduct, error) {
	ids := make([]uint, 0, len(items))
	seen := make(map[uint]struct{}, len(items))
	for _, it := range items {
		if _, ok := seen[it.ProductID]; ok {
			continue
		}
		seen[it.ProductID] = struct{}{}
		ids = append(ids, it.ProductID)
	}
	out := make(map[uint]database.TenantProduct, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	var rows []database.TenantProduct
	if err := s.db.Where("id IN ?", ids).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, p := range rows {
		out[p.ID] = p
	}
	return out, nil
}

// syncComboGroups reemplaza los grupos del combo. Lista vacía = el producto deja de ser combo.
// Espeja a syncPresentations: borra (soft delete) y recrea, y mantiene el flag has_combo.
func (s *ProductService) syncComboGroups(product *database.TenantProduct, inputs []ComboGroupInput) error {
	drafts, err := s.buildComboGroupDrafts(product, inputs)
	if err != nil {
		return err
	}

	var oldGroupIDs []uint
	if err := s.db.Model(&database.TenantComboGroup{}).
		Where("product_id = ?", product.ID).
		Pluck("id", &oldGroupIDs).Error; err != nil {
		return err
	}
	if len(oldGroupIDs) > 0 {
		if err := s.db.Where("group_id IN ?", oldGroupIDs).
			Delete(&database.TenantComboGroupItem{}).Error; err != nil {
			return err
		}
		if err := s.db.Where("product_id = ?", product.ID).
			Delete(&database.TenantComboGroup{}).Error; err != nil {
			return err
		}
	}

	for _, d := range drafts {
		group := d.row
		group.ProductID = product.ID
		if err := s.db.Create(&group).Error; err != nil {
			return err
		}
		for _, it := range d.items {
			row := it.row
			row.GroupID = group.ID
			if err := s.db.Create(&row).Error; err != nil {
				return err
			}
		}
	}

	hasCombo := len(drafts) > 0
	if err := s.db.Model(&database.TenantProduct{}).
		Where("id = ?", product.ID).
		Update("has_combo", hasCombo).Error; err != nil {
		return err
	}
	product.HasCombo = hasCombo
	return nil
}

// buildComboGroupDrafts valida los grupos contra el catálogo vivo y los normaliza.
func (s *ProductService) buildComboGroupDrafts(product *database.TenantProduct, inputs []ComboGroupInput) ([]comboGroupDraft, error) {
	if len(inputs) == 0 {
		return nil, nil
	}

	components, err := s.loadComboComponents(product, inputs)
	if err != nil {
		return nil, err
	}

	drafts := make([]comboGroupDraft, 0, len(inputs))
	sortOrder := 0
	for _, in := range inputs {
		name := strings.TrimSpace(in.Name)
		if name == "" {
			return nil, errors.New("cada grupo del combo necesita un nombre")
		}

		items, err := buildComboItemDrafts(name, in, components)
		if err != nil {
			return nil, err
		}
		if len(items) == 0 {
			return nil, fmt.Errorf("el grupo '%s' necesita al menos un producto", name)
		}

		group, err := normalizeComboGroup(name, in, len(items))
		if err != nil {
			return nil, err
		}
		group.SortOrder = sortOrder
		if in.SortOrder > 0 {
			group.SortOrder = in.SortOrder
		}
		sortOrder++

		drafts = append(drafts, comboGroupDraft{row: group, items: items})
	}
	return drafts, nil
}

// normalizeComboGroup aplica las reglas de cada tipo de selección.
func normalizeComboGroup(name string, in ComboGroupInput, itemCount int) (database.TenantComboGroup, error) {
	g := database.TenantComboGroup{
		Name:          name,
		SelectionType: strings.ToLower(strings.TrimSpace(in.SelectionType)),
		AllowQuantity: in.AllowQuantity,
		Active:        true,
	}
	if g.SelectionType == "" {
		g.SelectionType = database.ComboSelectionFixed
	}

	switch g.SelectionType {
	case database.ComboSelectionFixed:
		// Componente obligatorio sin elección: un solo producto, siempre incluido.
		if itemCount != 1 {
			return g, fmt.Errorf("el grupo '%s' es de tipo fijo: debe tener exactamente un producto", name)
		}
		g.MinSelect, g.MaxSelect = 1, 1
		g.AllowQuantity = false
	case database.ComboSelectionSingle:
		g.MinSelect, g.MaxSelect = 1, 1
		g.AllowQuantity = false
	case database.ComboSelectionMultiple:
		g.MinSelect, g.MaxSelect = in.MinSelect, in.MaxSelect
		if g.MaxSelect <= 0 {
			g.MaxSelect = itemCount
		}
		if g.MinSelect < 0 {
			g.MinSelect = 0
		}
		if g.MinSelect > g.MaxSelect {
			return g, fmt.Errorf("el grupo '%s': el mínimo a elegir (%d) no puede superar el máximo (%d)", name, g.MinSelect, g.MaxSelect)
		}
		if g.MaxSelect > itemCount {
			return g, fmt.Errorf("el grupo '%s': el máximo a elegir (%d) supera los %d productos del grupo", name, g.MaxSelect, itemCount)
		}
	default:
		return g, fmt.Errorf("el grupo '%s': tipo de selección '%s' inválido (use fijo, única o múltiple)", name, in.SelectionType)
	}
	return g, nil
}

// buildComboItemDrafts valida los componentes de un grupo.
func buildComboItemDrafts(groupName string, in ComboGroupInput, components map[uint]database.TenantProduct) ([]comboItemDraft, error) {
	out := make([]comboItemDraft, 0, len(in.Items))
	seen := make(map[uint]struct{}, len(in.Items))
	sortOrder := 0

	for _, item := range in.Items {
		if item.ProductID == 0 {
			continue
		}
		comp, ok := components[item.ProductID]
		if !ok {
			return nil, fmt.Errorf("el grupo '%s': el producto %d no existe", groupName, item.ProductID)
		}
		if _, dup := seen[item.ProductID]; dup {
			return nil, fmt.Errorf("el grupo '%s': el producto '%s' está repetido", groupName, comp.Name)
		}
		seen[item.ProductID] = struct{}{}

		qty := item.DefaultQuantity
		if qty <= 0 {
			qty = 1
		}
		maxQty := item.MaxQuantity
		if maxQty <= 0 {
			maxQty = qty
		}
		if maxQty < qty {
			return nil, fmt.Errorf("el grupo '%s': la cantidad máxima de '%s' no puede ser menor que la cantidad por defecto", groupName, comp.Name)
		}
		if item.ExtraPrice < 0 {
			return nil, fmt.Errorf("el grupo '%s': el sobreprecio de '%s' no puede ser negativo", groupName, comp.Name)
		}

		row := database.TenantComboGroupItem{
			ProductID: comp.ID,
			// Snapshot del área: la comanda del componente se rutea por este id al explotar el combo.
			PreparationAreaID: comp.PreparationAreaID,
			DefaultQuantity:   qty,
			MaxQuantity:       maxQty,
			ExtraPrice:        money.RoundDisplay(item.ExtraPrice),
			IsDefault:         item.IsDefault,
			SortOrder:         sortOrder,
			Active:            true,
		}
		if item.SortOrder > 0 {
			row.SortOrder = item.SortOrder
		}
		sortOrder++
		out = append(out, comboItemDraft{row: row})
	}
	return out, nil
}

// loadComboComponents carga y valida en bloque los productos referenciados por el combo.
func (s *ProductService) loadComboComponents(product *database.TenantProduct, inputs []ComboGroupInput) (map[uint]database.TenantProduct, error) {
	ids := make([]uint, 0)
	seen := make(map[uint]struct{})
	for _, g := range inputs {
		for _, it := range g.Items {
			if it.ProductID == 0 {
				continue
			}
			if _, ok := seen[it.ProductID]; ok {
				continue
			}
			seen[it.ProductID] = struct{}{}
			ids = append(ids, it.ProductID)
		}
	}
	if len(ids) == 0 {
		return map[uint]database.TenantProduct{}, nil
	}

	var rows []database.TenantProduct
	if err := s.db.Where("id IN ?", ids).Find(&rows).Error; err != nil {
		return nil, err
	}

	out := make(map[uint]database.TenantProduct, len(rows))
	for _, comp := range rows {
		if err := validateComboComponent(product, comp); err != nil {
			return nil, err
		}
		out[comp.ID] = comp
	}
	return out, nil
}

// validateComboComponent reglas de un producto usado como componente de un combo.
func validateComboComponent(product *database.TenantProduct, comp database.TenantProduct) error {
	if product.ID != 0 && comp.ID == product.ID {
		return errors.New("un combo no puede contenerse a sí mismo")
	}
	if comp.HasCombo {
		return fmt.Errorf("'%s' ya es un combo: un combo no puede contener otro combo", comp.Name)
	}
	if !comp.Active {
		return fmt.Errorf("'%s' está inactivo y no puede formar parte de un combo", comp.Name)
	}
	// La carta de restaurante es exclusiva por sucursal: un combo con componentes de otra
	// sucursal se rompería al armar la comanda.
	if product.BranchID > 0 && comp.BranchID > 0 && comp.BranchID != product.BranchID {
		return fmt.Errorf("'%s' pertenece a otra sucursal y no puede formar parte de este combo", comp.Name)
	}
	return nil
}

// ComboComponentsTotal suma el precio de lista de los componentes por defecto del combo.
// Sirve para mostrar el ahorro frente al precio fijo del combo.
func (s *ProductService) ComboComponentsTotal(productID uint) (float64, error) {
	groups, err := s.ListComboGroups(productID)
	if err != nil {
		return 0, err
	}
	total := 0.0
	for _, g := range groups {
		switch g.SelectionType {
		case database.ComboSelectionFixed:
			for _, it := range g.Items {
				total += it.ProductSalePrice * it.DefaultQuantity
			}
		default:
			// Selección del cliente: se toma la opción por defecto, o la primera como referencia.
			ref := comboReferenceItem(g.Items)
			if ref == nil {
				continue
			}
			qty := ref.DefaultQuantity
			if g.MinSelect > 1 {
				qty *= float64(g.MinSelect)
			}
			total += ref.ProductSalePrice * qty
		}
	}
	return money.RoundDisplay(total), nil
}

func comboReferenceItem(items []ComboGroupItemView) *ComboGroupItemView {
	if len(items) == 0 {
		return nil
	}
	for i := range items {
		if items[i].IsDefault {
			return &items[i]
		}
	}
	return &items[0]
}
