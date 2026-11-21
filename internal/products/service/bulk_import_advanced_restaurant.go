package service

import (
	"strings"

	"tukifac/pkg/database"
	"tukifac/pkg/modifierkind"

	"gorm.io/gorm"
)

// Importación avanzada de Tukichef (platos): extras y combos sobre el mismo motor de la
// importación avanzada de Tukifac (bulk_import_advanced.go).
//
// Qué soporta realmente un plato en Tukichef (verificado en el código):
//   - Presentaciones (tamaños): TenantProductPresentation vía syncPresentations — igual que Tukifac.
//   - Extras: grupos de modificadores GLOBALES (TenantModifierGroup, siempre kind=extra; las opciones
//     llevan extra_price) vinculados al plato (TenantProductModifierGroup) + has_modifiers=true.
//   - Combos: TenantComboGroup/Item con syncComboGroups. Un combo NO tiene presentaciones ni extras
//     (ver restaurant_modifiers.go: «Un combo no tiene presentaciones ni extras») y no puede contener
//     otro combo (validateComboComponent).
//   - NO usa unidades de venta, precios por sucursal ni stock por sucursal: el plato pertenece a una
//     sola sucursal (branch_id) y su stock es el stock_inicial de la sucursal activa. Esas hojas se
//     rechazan en modo restaurante.
//
// Reemplazo (decisión 2): la lista de grupos de extras de un plato es la lista final; los vínculos que
// faltan se quitan (el vínculo no tiene historial propio: los pedidos guardan snapshot en
// modifiers_json). Las opciones de un grupo que se define en el Excel y ya no aparecen se DESACTIVAN
// (active=false). Los combos los reemplaza syncComboGroups (soft delete de los grupos anteriores).

// BulkImportAdvancedModifierOption opción de un grupo de extras.
type BulkImportAdvancedModifierOption struct {
	RowNumber  int     `json:"row_number"`
	Name       string  `json:"name"`
	ExtraPrice float64 `json:"extra_price"`
}

// BulkImportAdvancedModifierGroup vínculo plato→grupo de extras (hoja «Extras»). Si Define=true
// también trae la definición del grupo (hoja «Grupos de extras»): se crea o se actualiza (global,
// afecta a todos los platos que lo usan). Con Define=false el grupo debe existir ya.
type BulkImportAdvancedModifierGroup struct {
	RowNumber    int                                `json:"row_number"`     // fila en la hoja Extras
	DefRowNumber int                                `json:"def_row_number"` // fila de la definición (Grupos de extras)
	Name         string                             `json:"name"`
	Define       bool                               `json:"define"`
	Required     bool                               `json:"required"`
	MultiSelect  bool                               `json:"multi_select"`
	Options      []BulkImportAdvancedModifierOption `json:"options"`
}

type BulkImportAdvancedComboItem struct {
	RowNumber       int     `json:"row_number"`
	ProductCode     string  `json:"product_code"`
	DefaultQuantity float64 `json:"default_quantity"`
	MaxQuantity     float64 `json:"max_quantity"`
	ExtraPrice      float64 `json:"extra_price"`
	IsDefault       bool    `json:"is_default"`
	SortOrder       int     `json:"sort_order"`
}

type BulkImportAdvancedComboGroup struct {
	RowNumber     int                           `json:"row_number"` // primera fila del grupo en la hoja Combos
	Name          string                        `json:"name"`
	SelectionType string                        `json:"selection_type"` // fixed | single | multiple
	MinSelect     int                           `json:"min_select"`
	MaxSelect     int                           `json:"max_select"`
	AllowQuantity bool                          `json:"allow_quantity"`
	SortOrder     int                           `json:"sort_order"`
	Items         []BulkImportAdvancedComboItem `json:"items"`
}

type restaurantCounters struct {
	modGroupsCreated, modGroupsUpdated           int
	optCreated, optUpdated, optDeactivated       int
	linksCreated, linksRemoved                   int
	comboCreated, comboUpdated, comboDeactivated int
}

func (c *restaurantCounters) addTo(r *BulkImportAdvancedResult) {
	r.ModifierGroupsCreated += c.modGroupsCreated
	r.ModifierGroupsUpdated += c.modGroupsUpdated
	r.ModifierOptionsCreated += c.optCreated
	r.ModifierOptionsUpdated += c.optUpdated
	r.ModifierOptionsDeactivated += c.optDeactivated
	r.ModifierLinksCreated += c.linksCreated
	r.ModifierLinksRemoved += c.linksRemoved
	r.ComboGroupsCreated += c.comboCreated
	r.ComboGroupsUpdated += c.comboUpdated
	r.ComboGroupsDeactivated += c.comboDeactivated
}

// validateAdvancedRestaurantItem chequeos estructurales de las hojas de restaurante (sin BD).
// Con restaurant=false rechaza las hojas que solo existen en Tukichef.
func validateAdvancedRestaurantItem(it *BulkImportAdvancedItem, restaurant bool, comboCodes map[string]bool) error {
	if !restaurant {
		if len(it.ModifierGroups) > 0 {
			return advErr("Extras", it.ModifierGroups[0].RowNumber, "los extras solo existen en la importación de Tukichef")
		}
		if len(it.ComboGroups) > 0 {
			return advErr("Combos", it.ComboGroups[0].RowNumber, "los combos solo existen en la importación de Tukichef")
		}
		return nil
	}
	if len(it.SaleUnits) > 0 {
		return advErr("Unidades de venta", it.SaleUnits[0].RowNumber, "Tukichef no usa unidades de venta")
	}
	if len(it.BranchPrices) > 0 {
		return advErr("Precios por sucursal", it.BranchPrices[0].RowNumber, "Tukichef no usa precios por sucursal: el plato pertenece a una sola sucursal")
	}
	if len(it.BranchStocks) > 0 {
		return advErr("Stock por sucursal", it.BranchStocks[0].RowNumber, "Tukichef no usa stock por sucursal: use stock_inicial (sucursal activa)")
	}
	code := strings.TrimSpace(it.Code)

	seenMod := map[string]bool{}
	for _, g := range it.ModifierGroups {
		n := strings.ToLower(strings.TrimSpace(g.Name))
		if n == "" {
			return advErr("Extras", g.RowNumber, "grupo de extras requerido")
		}
		if seenMod[n] {
			return advErr("Extras", g.RowNumber, "el grupo «%s» está repetido para el plato «%s»", strings.TrimSpace(g.Name), code)
		}
		seenMod[n] = true
		if g.Define {
			if len(g.Options) == 0 {
				return advErr("Grupos de extras", g.DefRowNumber, "el grupo «%s» necesita al menos una opción", strings.TrimSpace(g.Name))
			}
			seenOpt := map[string]bool{}
			for _, o := range g.Options {
				on := strings.ToLower(strings.TrimSpace(o.Name))
				if on == "" {
					return advErr("Grupos de extras", o.RowNumber, "opción requerida")
				}
				if seenOpt[on] {
					return advErr("Grupos de extras", o.RowNumber, "la opción «%s» está repetida en el grupo «%s»", strings.TrimSpace(o.Name), strings.TrimSpace(g.Name))
				}
				seenOpt[on] = true
				if o.ExtraPrice < 0 {
					return advErr("Grupos de extras", o.RowNumber, "precio_extra no puede ser negativo")
				}
			}
		}
	}

	if len(it.ComboGroups) > 0 {
		if len(it.Presentations) > 0 {
			return advErr("Presentaciones", it.Presentations[0].RowNumber, "«%s» es un combo: un combo no tiene presentaciones", code)
		}
		if len(it.ModifierGroups) > 0 {
			return advErr("Extras", it.ModifierGroups[0].RowNumber, "«%s» es un combo: un combo no tiene extras", code)
		}
		seenGroup := map[string]bool{}
		for _, g := range it.ComboGroups {
			gn := strings.ToLower(strings.TrimSpace(g.Name))
			if gn == "" {
				return advErr("Combos", g.RowNumber, "cada grupo del combo necesita un nombre")
			}
			if seenGroup[gn] {
				return advErr("Combos", g.RowNumber, "el grupo «%s» está repetido en el combo «%s»", strings.TrimSpace(g.Name), code)
			}
			seenGroup[gn] = true
			if len(g.Items) == 0 {
				return advErr("Combos", g.RowNumber, "el grupo «%s» necesita al menos un producto", strings.TrimSpace(g.Name))
			}
			seenItem := map[string]bool{}
			for _, ci := range g.Items {
				cc := strings.ToLower(strings.TrimSpace(ci.ProductCode))
				if cc == "" {
					return advErr("Combos", ci.RowNumber, "producto (código del componente) requerido")
				}
				if cc == strings.ToLower(code) {
					return advErr("Combos", ci.RowNumber, "un combo no puede contenerse a sí mismo")
				}
				if comboCodes[cc] {
					return advErr("Combos", ci.RowNumber, "«%s» también es un combo: un combo no puede contener otro combo", strings.TrimSpace(ci.ProductCode))
				}
				if seenItem[cc] {
					return advErr("Combos", ci.RowNumber, "el producto «%s» está repetido en el grupo «%s»", strings.TrimSpace(ci.ProductCode), strings.TrimSpace(g.Name))
				}
				seenItem[cc] = true
			}
		}
	}
	return nil
}

// applyRestaurantChildren aplica extras y combos dentro de la transacción del plato.
// El func devuelto marca como aplicadas las definiciones de grupo solo si la transacción confirma
// (varios platos comparten el mismo grupo: se define una vez por corrida).
func (s *ProductService) applyRestaurantChildren(
	tx *gorm.DB, ps *ProductService, it *BulkImportAdvancedItem, productID, branchID uint,
	c *restaurantCounters, res *BulkImportAdvancedResult,
) (func(), error) {
	var product database.TenantProduct
	if err := tx.First(&product, productID).Error; err != nil {
		return nil, err
	}
	var applied []string

	// ── Extras ────────────────────────────────────────────────────────────────────────────────
	if len(it.ModifierGroups) > 0 {
		if product.HasCombo {
			return nil, advErr("Extras", it.ModifierGroups[0].RowNumber, "«%s» ya es un combo: un combo no tiene extras", it.Code)
		}
		var existingLinks []database.TenantProductModifierGroup
		if err := tx.Where("product_id = ?", productID).Find(&existingLinks).Error; err != nil {
			return nil, err
		}
		had := map[uint]bool{}
		for _, l := range existingLinks {
			had[l.GroupID] = true
		}
		want := map[uint]bool{}
		ids := make([]uint, 0, len(it.ModifierGroups))
		for _, g := range it.ModifierGroups {
			key := strings.ToLower(strings.TrimSpace(g.Name))
			gid, wasApplied, err := s.upsertImportModifierGroup(tx, ps, g, res.modGroupApplied(key), c)
			if err != nil {
				return nil, err
			}
			if wasApplied {
				applied = append(applied, key)
			}
			want[gid] = true
			ids = append(ids, gid)
		}
		for gid := range want {
			if !had[gid] {
				c.linksCreated++
			}
		}
		for gid := range had {
			if !want[gid] {
				c.linksRemoved++
			}
		}
		ps.syncModifierGroups(productID, ids)
		var n int64
		tx.Model(&database.TenantProductModifierGroup{}).Where("product_id = ?", productID).Count(&n)
		if int(n) != len(want) {
			return nil, advErr("Extras", it.ModifierGroups[0].RowNumber, "no se pudieron vincular todos los grupos de extras al plato «%s»", it.Code)
		}
		if err := tx.Model(&database.TenantProduct{}).Where("id = ?", productID).Update("has_modifiers", len(want) > 0).Error; err != nil {
			return nil, err
		}
	}

	// ── Combos ────────────────────────────────────────────────────────────────────────────────
	if len(it.ComboGroups) > 0 {
		var presN, linkN int64
		tx.Model(&database.TenantProductPresentation{}).Where("product_id = ? AND active = ?", productID, true).Count(&presN)
		tx.Model(&database.TenantProductModifierGroup{}).Where("product_id = ?", productID).Count(&linkN)
		if presN > 0 {
			return nil, advErr("Combos", it.ComboGroups[0].RowNumber, "«%s» tiene presentaciones: un combo no tiene presentaciones. Quítalas antes de convertirlo en combo", it.Code)
		}
		if linkN > 0 {
			return nil, advErr("Combos", it.ComboGroups[0].RowNumber, "«%s» tiene extras: un combo no tiene extras. Quítalos antes de convertirlo en combo", it.Code)
		}
		var oldGroups []database.TenantComboGroup
		if err := tx.Where("product_id = ?", productID).Find(&oldGroups).Error; err != nil {
			return nil, err
		}
		oldByName := map[string]bool{}
		for _, g := range oldGroups {
			oldByName[strings.ToLower(strings.TrimSpace(g.Name))] = true
		}

		inputs := make([]ComboGroupInput, 0, len(it.ComboGroups))
		for _, g := range it.ComboGroups {
			in := ComboGroupInput{
				Name: g.Name, SelectionType: g.SelectionType, MinSelect: g.MinSelect, MaxSelect: g.MaxSelect,
				AllowQuantity: g.AllowQuantity, SortOrder: g.SortOrder,
			}
			for _, ci := range g.Items {
				var comp database.TenantProduct
				err := tx.Where("code = ? AND branch_id = ?", strings.TrimSpace(ci.ProductCode), branchID).First(&comp).Error
				if err != nil {
					return nil, advErr("Combos", ci.RowNumber, "el producto «%s» no existe en esta sucursal (debe estar en la hoja Productos o ya creado)", strings.TrimSpace(ci.ProductCode))
				}
				in.Items = append(in.Items, ComboGroupItemInput{
					ProductID: comp.ID, DefaultQuantity: ci.DefaultQuantity, MaxQuantity: ci.MaxQuantity,
					ExtraPrice: ci.ExtraPrice, IsDefault: ci.IsDefault, SortOrder: ci.SortOrder,
				})
			}
			inputs = append(inputs, in)
		}
		if err := ps.syncComboGroups(&product, inputs); err != nil {
			// El servicio no sabe de hojas: se atribuye al combo completo (fila del primer grupo).
			return nil, advErr("Combos", it.ComboGroups[0].RowNumber, "%s", err.Error())
		}
		matched := map[string]bool{}
		for _, g := range it.ComboGroups {
			n := strings.ToLower(strings.TrimSpace(g.Name))
			matched[n] = true
			if oldByName[n] {
				c.comboUpdated++
			} else {
				c.comboCreated++
			}
		}
		for n := range oldByName {
			if !matched[n] {
				c.comboDeactivated++
			}
		}
	}

	return func() {
		for _, k := range applied {
			res.markModGroupApplied(k)
		}
	}, nil
}

// modGroupApplied / markModGroupApplied: definiciones de grupo ya confirmadas en esta corrida.
func (r *BulkImportAdvancedResult) modGroupApplied(key string) bool { return r.appliedGroups[key] }
func (r *BulkImportAdvancedResult) markModGroupApplied(key string) {
	if r.appliedGroups == nil {
		r.appliedGroups = map[string]bool{}
	}
	r.appliedGroups[key] = true
}

// upsertImportModifierGroup busca el grupo por nombre (sin distinguir mayúsculas) y lo crea o actualiza
// según Define. Devuelve su id y si en esta llamada se aplicó la definición.
func (s *ProductService) upsertImportModifierGroup(
	tx *gorm.DB, ps *ProductService, g BulkImportAdvancedModifierGroup, alreadyApplied bool, c *restaurantCounters,
) (uint, bool, error) {
	name := strings.TrimSpace(g.Name)
	var grp database.TenantModifierGroup
	err := tx.Where("LOWER(name) = ?", strings.ToLower(name)).First(&grp).Error
	found := err == nil
	if err != nil && err != gorm.ErrRecordNotFound {
		return 0, false, err
	}
	if !found {
		if !g.Define {
			return 0, false, advErr("Extras", g.RowNumber, "el grupo de extras «%s» no existe: defínelo en la hoja Grupos de extras", name)
		}
		opts := make([]ModifierOptionInput, 0, len(g.Options))
		for _, o := range g.Options {
			opts = append(opts, ModifierOptionInput{Name: o.Name, ExtraPrice: o.ExtraPrice})
		}
		created, cerr := ps.CreateModifierGroup(name, modifierkind.Extra, g.Required, g.MultiSelect, opts)
		if cerr != nil {
			return 0, false, advErr("Grupos de extras", g.DefRowNumber, "%s", cerr.Error())
		}
		c.modGroupsCreated++
		c.optCreated += len(created.Options)
		return created.ID, true, nil
	}
	if !modifierkind.IsExtra(grp.Kind, grp.Required, grp.MultiSelect) {
		return 0, false, advErr("Extras", g.RowNumber, "el grupo «%s» es un grupo de presentación heredado, no un grupo de extras", name)
	}
	if !g.Define || alreadyApplied {
		return grp.ID, false, nil
	}

	if err := tx.Model(&database.TenantModifierGroup{}).Where("id = ?", grp.ID).Updates(map[string]interface{}{
		"required": g.Required, "multi_select": g.MultiSelect, "kind": modifierkind.Extra, "active": true,
	}).Error; err != nil {
		return 0, false, err
	}
	c.modGroupsUpdated++

	var olds []database.TenantModifierOption
	if err := tx.Where("group_id = ?", grp.ID).Find(&olds).Error; err != nil {
		return 0, false, err
	}
	byName := map[string]database.TenantModifierOption{}
	for _, o := range olds {
		byName[strings.ToLower(strings.TrimSpace(o.Name))] = o
	}
	matched := map[uint]bool{}
	var fresh []ModifierOptionInput
	for _, o := range g.Options {
		if old, ok := byName[strings.ToLower(strings.TrimSpace(o.Name))]; ok {
			price := o.ExtraPrice
			if err := tx.Model(&database.TenantModifierOption{}).Where("id = ?", old.ID).
				Updates(map[string]interface{}{"extra_price": price, "active": true}).Error; err != nil {
				return 0, false, err
			}
			matched[old.ID] = true
			c.optUpdated++
			continue
		}
		fresh = append(fresh, ModifierOptionInput{Name: o.Name, ExtraPrice: o.ExtraPrice})
	}
	c.optCreated += len(ps.createModifierOptions(grp.ID, fresh))
	for _, o := range olds {
		if matched[o.ID] || !o.Active {
			continue
		}
		if err := tx.Model(&database.TenantModifierOption{}).Where("id = ?", o.ID).Update("active", false).Error; err != nil {
			return 0, false, err
		}
		c.optDeactivated++
	}
	return grp.ID, true, nil
}
