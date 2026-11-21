package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"

	"tukifac/pkg/database"
	"tukifac/pkg/saleunit"

)

// Opciones de venta de un producto en la tienda pública: presentaciones, unidades de venta y
// extras (modificadores). Es la misma elección que hace el POS en su modal de configuración, pero
// el PRECIO lo resuelve siempre el servidor (el cliente solo manda IDs): esta ruta no lleva login.

type PublicPresentation struct {
	ID        uint     `json:"id"`
	Name      string   `json:"name"`
	SalePrice float64  `json:"sale_price"`
	Stock     *float64 `json:"stock,omitempty"` // solo si la tienda muestra stock y el producto lo controla
}

type PublicSaleUnit struct {
	ID               uint    `json:"id"`
	Name             string  `json:"name"`
	Price            float64 `json:"price"`
	ConversionFactor float64 `json:"conversion_factor"`
	AllowFraction    bool    `json:"allow_fraction"`
}

type PublicModifierOption struct {
	ID         uint    `json:"id"`
	Name       string  `json:"name"`
	ExtraPrice float64 `json:"extra_price"`
}

type PublicModifierGroup struct {
	ID          uint                   `json:"id"`
	Name        string                 `json:"name"`
	Required    bool                   `json:"required"`
	MultiSelect bool                   `json:"multi_select"`
	Options     []PublicModifierOption `json:"options"`
}

type PublicProductOptions struct {
	BasePrice     float64              `json:"base_price"`
	Presentations []PublicPresentation `json:"presentations"`
	SaleUnits     []PublicSaleUnit     `json:"sale_units"`
	// BaseUnitName: cuando hay unidades de venta y ninguna es la base, el cliente puede elegir la
	// unidad base del producto (precio base, sin sale_unit_id), igual que en el POS.
	BaseUnitName   string                `json:"base_unit_name,omitempty"`
	ModifierGroups []PublicModifierGroup `json:"modifier_groups"`
}

var errProductUnavailable = errors.New("el producto no está disponible en la tienda")

func (s *EcommerceService) loadPublicProduct(productID uint) (*database.TenantProduct, error) {
	var p database.TenantProduct
	err := s.db.Where("id = ? AND active = ? AND show_in_digital_catalog = ?", productID, true, true).First(&p).Error
	if err != nil || p.HasCombo {
		return nil, errProductUnavailable
	}
	return &p, nil
}

func (s *EcommerceService) activePresentations(productID uint) []database.TenantProductPresentation {
	var rows []database.TenantProductPresentation
	s.db.Where("product_id = ? AND active = ?", productID, true).Order("sort_order ASC, id ASC").Find(&rows)
	out := rows[:0]
	for _, r := range rows {
		if strings.TrimSpace(r.Name) != "" {
			out = append(out, r)
		}
	}
	return out
}

func (s *EcommerceService) activeSaleUnits(productID uint) []database.TenantProductSaleUnit {
	var rows []database.TenantProductSaleUnit
	s.db.Where("product_id = ? AND active = ?", productID, true).Order("sort_order ASC, id ASC").Find(&rows)
	return rows
}

func (s *EcommerceService) productModifierGroups(productID uint) []database.TenantModifierGroup {
	var groups []database.TenantModifierGroup
	s.db.Table("tenant_modifier_groups g").
		Select("g.*").
		Joins("JOIN tenant_product_modifier_groups pg ON pg.group_id = g.id").
		Where("pg.product_id = ? AND g.active = ? AND g.deleted_at IS NULL", productID, true).
		Order("g.id ASC").
		Scan(&groups)
	return groups
}

func (s *EcommerceService) groupOptions(groupID uint) []database.TenantModifierOption {
	var opts []database.TenantModifierOption
	s.db.Where("group_id = ? AND active = ?", groupID, true).Order("id ASC").Find(&opts)
	return opts
}

// ProductOptions arma lo que la tienda muestra al elegir un producto con opciones.
func (s *EcommerceService) ProductOptions(productID uint, showStock bool) (*PublicProductOptions, error) {
	p, err := s.loadPublicProduct(productID)
	if err != nil {
		return nil, err
	}
	out := &PublicProductOptions{
		BasePrice:      p.SalePrice,
		Presentations:  []PublicPresentation{},
		SaleUnits:      []PublicSaleUnit{},
		ModifierGroups: []PublicModifierGroup{},
	}

	units := s.activeSaleUnits(p.ID)
	if len(units) > 0 {
		// Con unidades de venta activas las presentaciones no se ofrecen (mismo criterio que el POS).
		hasBase := false
		for _, u := range units {
			if u.IsBase {
				hasBase = true
			}
			out.SaleUnits = append(out.SaleUnits, PublicSaleUnit{
				ID: u.ID, Name: u.Name, Price: u.Price1, ConversionFactor: u.ConversionFactor, AllowFraction: u.AllowFraction,
			})
		}
		if !hasBase {
			out.BaseUnitName = strings.TrimSpace(p.Unit)
		}
		return out, nil
	}

	if p.HasVariants {
		for _, pr := range s.activePresentations(p.ID) {
			item := PublicPresentation{ID: pr.ID, Name: pr.Name, SalePrice: pr.SalePrice}
			if showStock && p.ManageStock {
				var total float64
				s.db.Model(&database.TenantProductPresentationStock{}).
					Where("presentation_id = ?", pr.ID).
					Select("COALESCE(SUM(quantity), 0)").Scan(&total)
				item.Stock = &total
			}
			out.Presentations = append(out.Presentations, item)
		}
	}

	if p.HasModifiers {
		for _, g := range s.productModifierGroups(p.ID) {
			opts := s.groupOptions(g.ID)
			if len(opts) == 0 {
				continue
			}
			pg := PublicModifierGroup{ID: g.ID, Name: g.Name, Required: g.Required, MultiSelect: g.MultiSelect}
			for _, o := range opts {
				pg.Options = append(pg.Options, PublicModifierOption{ID: o.ID, Name: o.Name, ExtraPrice: o.ExtraPrice})
			}
			out.ModifierGroups = append(out.ModifierGroups, pg)
		}
	}
	return out, nil
}

type orderModifierSnapshot struct {
	GroupID    uint    `json:"group_id"`
	GroupName  string  `json:"group_name"`
	Type       string  `json:"type"`
	GroupType  string  `json:"group_type"`
	OptionID   uint    `json:"option_id"`
	OptionName string  `json:"option_name"`
	ExtraPrice float64 `json:"extra_price"`
	Snapshot   bool    `json:"snapshot"`
}

func round2(n float64) float64 { return math.Round(n*100) / 100 }

// resolveOrderItem valida la elección del cliente contra el catálogo y devuelve la línea con el
// nombre, el precio y el snapshot de modificadores calculados por el servidor.
func (s *EcommerceService) resolveOrderItem(it OrderItemInput) (OrderItemInput, error) {
	p, err := s.loadPublicProduct(it.ProductID)
	if err != nil {
		label := strings.TrimSpace(it.Name)
		if label == "" {
			label = "un producto del pedido"
		}
		return it, fmt.Errorf("'%s' ya no está disponible en la tienda", label)
	}

	res := OrderItemInput{ProductID: p.ID, Name: p.Name, Quantity: it.Quantity, UnitPrice: p.SalePrice}
	var snaps []orderModifierSnapshot

	units := s.activeSaleUnits(p.ID)
	switch {
	case len(units) > 0:
		if len(it.ModifierOptionIDs) > 0 {
			return it, fmt.Errorf("'%s' no admite extras con unidad de venta", p.Name)
		}
		if it.SaleUnitID != nil && *it.SaleUnitID > 0 {
			var chosen *database.TenantProductSaleUnit
			for i := range units {
				if units[i].ID == *it.SaleUnitID {
					chosen = &units[i]
				}
			}
			if chosen == nil {
				return it, fmt.Errorf("la unidad de venta elegida de '%s' no existe", p.Name)
			}
			if _, err := saleunit.ResolveForLine(s.db, p.ID, it.SaleUnitID, it.Quantity); err != nil {
				return it, err
			}
			id := chosen.ID
			res.SaleUnitID = &id
			res.UnitPrice = chosen.Price1
			res.Name = p.Name + " - " + chosen.Name
		} else {
			hasBase := false
			for _, u := range units {
				if u.IsBase {
					hasBase = true
				}
			}
			if hasBase {
				return it, fmt.Errorf("elige la unidad de venta de '%s'", p.Name)
			}
			// Unidad base sintética: precio base, sin sale_unit_id.
		}
	case p.HasVariants && len(s.activePresentations(p.ID)) > 0:
		if it.PresentationID == nil || *it.PresentationID == 0 {
			return it, fmt.Errorf("elige la presentación de '%s'", p.Name)
		}
		var chosen *database.TenantProductPresentation
		pres := s.activePresentations(p.ID)
		for i := range pres {
			if pres[i].ID == *it.PresentationID {
				chosen = &pres[i]
			}
		}
		if chosen == nil {
			return it, fmt.Errorf("la presentación elegida de '%s' ya no está disponible", p.Name)
		}
		id := chosen.ID
		res.PresentationID = &id
		res.UnitPrice = chosen.SalePrice
		res.Name = p.Name + " - " + strings.TrimSpace(chosen.Name)
		snaps = append(snaps, orderModifierSnapshot{
			GroupName: "Presentación", Type: "variant", GroupType: "variant",
			OptionID: chosen.ID, OptionName: strings.TrimSpace(chosen.Name), ExtraPrice: chosen.SalePrice, Snapshot: true,
		})
	}

	if res.SaleUnitID == nil && p.HasModifiers {
		chosenIDs := map[uint]bool{}
		for _, id := range it.ModifierOptionIDs {
			chosenIDs[id] = true
		}
		used := 0
		for _, g := range s.productModifierGroups(p.ID) {
			picked := 0
			for _, o := range s.groupOptions(g.ID) {
				if !chosenIDs[o.ID] {
					continue
				}
				picked++
				used++
				res.UnitPrice += o.ExtraPrice
				snaps = append(snaps, orderModifierSnapshot{
					GroupID: g.ID, GroupName: g.Name, Type: "modifier", GroupType: "modifier",
					OptionID: o.ID, OptionName: o.Name, ExtraPrice: o.ExtraPrice, Snapshot: true,
				})
			}
			if g.Required && picked == 0 {
				return it, fmt.Errorf("elige una opción de «%s» para '%s'", g.Name, p.Name)
			}
			if !g.MultiSelect && picked > 1 {
				return it, fmt.Errorf("«%s» admite una sola opción", g.Name)
			}
		}
		if used != len(chosenIDs) {
			return it, fmt.Errorf("alguna opción elegida de '%s' ya no está disponible", p.Name)
		}
	} else if len(it.ModifierOptionIDs) > 0 {
		return it, fmt.Errorf("'%s' no admite extras", p.Name)
	}

	res.UnitPrice = round2(res.UnitPrice)
	if len(snaps) > 0 {
		raw, err := json.Marshal(snaps)
		if err != nil {
			return it, err
		}
		res.ModifiersJSON = string(raw)
		extras := []string{}
		for _, m := range snaps {
			if m.Type == "modifier" {
				extras = append(extras, m.OptionName)
			}
		}
		if len(extras) > 0 {
			res.Detail = "+ " + strings.Join(extras, ", ")
		}
	}
	return res, nil
}

type stockKey struct {
	productID      uint
	presentationID uint
}

// validateOrderStock rechaza un pedido que pide más de lo que hay. NO descuenta ni reserva nada:
// el stock solo se descuenta al convertir el pedido en venta (ahí se vuelve a validar por sucursal).
// Al crear el pedido todavía no hay sucursal, así que se compara contra el stock total de todas.
func (s *EcommerceService) validateOrderStock(items []OrderItemInput) error {
	need := map[stockKey]float64{}
	label := map[stockKey]string{}
	for _, it := range items {
		var p database.TenantProduct
		if s.db.Select("id", "name", "type", "manage_stock", "has_variants").First(&p, it.ProductID).Error != nil {
			continue
		}
		if !p.ManageStock || strings.EqualFold(strings.TrimSpace(p.Type), "service") {
			continue
		}
		qty := it.Quantity
		k := stockKey{productID: p.ID}
		switch {
		case it.PresentationID != nil && *it.PresentationID > 0:
			k.presentationID = *it.PresentationID
		case it.SaleUnitID != nil && *it.SaleUnitID > 0:
			res, err := saleunit.ResolveForLine(s.db, p.ID, it.SaleUnitID, it.Quantity)
			if err != nil {
				return err
			}
			qty = res.BaseQuantity
		}
		need[k] += qty
		label[k] = it.Name
	}
	for k, qty := range need {
		var have float64
		if k.presentationID > 0 {
			s.db.Model(&database.TenantProductPresentationStock{}).
				Where("presentation_id = ?", k.presentationID).
				Select("COALESCE(SUM(quantity), 0)").Scan(&have)
		} else {
			s.db.Model(&database.TenantProductStock{}).
				Where("product_id = ?", k.productID).
				Select("COALESCE(SUM(quantity), 0)").Scan(&have)
		}
		if have < qty-0.0001 {
			return fmt.Errorf("no hay stock suficiente de '%s': pediste %s y hay %s", label[k], fmtQty(qty), fmtQty(have))
		}
	}
	return nil
}

func fmtQty(n float64) string {
	if n == math.Trunc(n) {
		return fmt.Sprintf("%.0f", n)
	}
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.3f", n), "0"), ".")
}
