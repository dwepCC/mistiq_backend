package service

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	invsvc "tukifac/internal/inventory/service"
	"tukifac/pkg/database"
	"tukifac/pkg/sunat"

	"gorm.io/gorm"
)

// Importación avanzada de productos (Excel con varias hojas). Cada ítem es un producto con sus
// hijos anidados; el producto y TODOS sus hijos se guardan en una sola transacción (o se guarda
// completo o falla con un error "hoja X, fila N: ...").
//
// Reglas de negocio — no se duplican, se reutilizan los servicios existentes:
//   - presentaciones: ProductService.syncPresentations (upsert estable por nombre, no se pueden
//     quitar las que tienen stock, las quitadas hacen soft-delete).
//   - unidades de venta: CreateSaleUnit / UpdateSaleUnit (validateSaleUnitInput).
//   - precios por sucursal: Create/Update(SaleUnit)BranchPrice y Create/UpdateProductBranchPrice.
//   - stock por sucursal: entrada inicial (producto nuevo) o ajuste absoluto (existente), igual que
//     la importación básica.
//
// Coexistencia presentaciones ↔ unidades de venta: NO pueden convivir en un mismo producto. El POS y
// la tienda ocultan las presentaciones cuando hay unidades de venta activas (ver
// ecommerce/service/order_options.go) y CreateSaleUnit rechaza agregar unidades si hay
// presentaciones activas. Por eso la importación bloquea la combinación con un mensaje claro, tanto
// dentro del mismo Excel como contra lo que ya tiene el producto.
//
// Semántica de "reemplazo": para un producto que aparece en una hoja hija, esa lista es la lista
// final; lo que ya existía y no aparece se desactiva (unidades de venta y precios por sucursal
// quedan con active=false; las presentaciones se dan de baja con soft-delete, como hace el editor).
// Nada se borra físicamente. Una hoja ausente o sin filas para el producto = no se toca.

const BulkImportAdvancedMaxItems = 200

type BulkImportAdvancedPresentation struct {
	RowNumber    int     `json:"row_number"`
	Name         string  `json:"name"`
	SalePrice    float64 `json:"sale_price"`
	InitialStock float64 `json:"initial_stock"`
	SortOrder    int     `json:"sort_order"`
}

type BulkImportAdvancedSaleUnit struct {
	RowNumber        int      `json:"row_number"`
	Name             string   `json:"name"`
	Unit             string   `json:"unit"` // código/alias SUNAT (KGM, GRM, NIU, BX...)
	ConversionFactor float64  `json:"conversion_factor"`
	AllowFraction    bool     `json:"allow_fraction"`
	IsBase           bool     `json:"is_base"`
	Price1           float64  `json:"price1"`
	Price2           *float64 `json:"price2"`
	Price3           *float64 `json:"price3"`
	SortOrder        int      `json:"sort_order"`
}

type BulkImportAdvancedBranchPrice struct {
	RowNumber    int     `json:"row_number"`
	BranchName   string  `json:"branch_name"`
	SaleUnitName string  `json:"sale_unit_name"` // vacío = precio del producto
	SalePrice    float64 `json:"sale_price"`
}

type BulkImportAdvancedBranchStock struct {
	RowNumber  int     `json:"row_number"`
	BranchName string  `json:"branch_name"`
	Quantity   float64 `json:"quantity"`
}

// BulkImportAdvancedItem producto + hijos. Listas vacías/ausentes = no tocar.
type BulkImportAdvancedItem struct {
	BulkImportItem
	Presentations []BulkImportAdvancedPresentation `json:"presentations"`
	SaleUnits     []BulkImportAdvancedSaleUnit     `json:"sale_units"`
	BranchPrices  []BulkImportAdvancedBranchPrice  `json:"branch_prices"`
	BranchStocks  []BulkImportAdvancedBranchStock  `json:"branch_stocks"`
	// Solo Tukichef (restaurante): ver bulk_import_advanced_restaurant.go.
	ModifierGroups []BulkImportAdvancedModifierGroup `json:"modifier_groups"`
	ComboGroups    []BulkImportAdvancedComboGroup    `json:"combo_groups"`
}

type BulkImportAdvancedResult struct {
	BulkImportResult
	PresentationsCreated     int `json:"presentations_created"`
	PresentationsUpdated     int `json:"presentations_updated"`
	PresentationsDeactivated int `json:"presentations_deactivated"`
	SaleUnitsCreated         int `json:"sale_units_created"`
	SaleUnitsUpdated         int `json:"sale_units_updated"`
	SaleUnitsDeactivated     int `json:"sale_units_deactivated"`
	BranchPricesCreated      int `json:"branch_prices_created"`
	BranchPricesUpdated      int `json:"branch_prices_updated"`
	BranchPricesDeactivated  int `json:"branch_prices_deactivated"`
	appliedGroups            map[string]bool
	BranchStocksSet          int `json:"branch_stocks_set"`
	// Restaurante.
	ModifierGroupsCreated      int `json:"modifier_groups_created"`
	ModifierGroupsUpdated      int `json:"modifier_groups_updated"`
	ModifierOptionsCreated     int `json:"modifier_options_created"`
	ModifierOptionsUpdated     int `json:"modifier_options_updated"`
	ModifierOptionsDeactivated int `json:"modifier_options_deactivated"`
	ModifierLinksCreated       int `json:"modifier_links_created"`
	ModifierLinksRemoved       int `json:"modifier_links_removed"`
	ComboGroupsCreated         int `json:"combo_groups_created"`
	ComboGroupsUpdated         int `json:"combo_groups_updated"`
	ComboGroupsDeactivated     int `json:"combo_groups_deactivated"`
}

// BulkImportAdvancedOpts variantes del motor: Tukifac (catálogo) o Tukichef (platos).
type BulkImportAdvancedOpts struct {
	// Restaurant fuerza is_restaurant, exige sucursal activa y habilita extras y combos; las
	// unidades de venta, precios por sucursal y stock por sucursal no existen en Tukichef.
	Restaurant bool
	// MaxItems: 0 = BulkImportAdvancedMaxItems. El dry_run puede subirlo para simular el archivo
	// completo en una sola transacción (los combos referencian platos de otros lotes).
	MaxItems int
}

func advErr(sheet string, row int, format string, args ...any) error {
	return fmt.Errorf("hoja %s, fila %d: %s", sheet, row, fmt.Sprintf(format, args...))
}

// BulkImportAdvanced importa productos con presentaciones, unidades de venta, precios y stock por
// sucursal. Máximo BulkImportAdvancedMaxItems productos por llamada.
func (s *ProductService) BulkImportAdvanced(items []BulkImportAdvancedItem, branchID, userID uint) (*BulkImportAdvancedResult, error) {
	return s.BulkImportAdvancedWith(items, branchID, userID, BulkImportAdvancedOpts{})
}

// BulkImportAdvancedWith ver BulkImportAdvancedOpts.
func (s *ProductService) BulkImportAdvancedWith(items []BulkImportAdvancedItem, branchID, userID uint, opts BulkImportAdvancedOpts) (*BulkImportAdvancedResult, error) {
	if len(items) == 0 {
		return nil, errors.New("no hay filas para importar")
	}
	maxItems := opts.MaxItems
	if maxItems <= 0 {
		maxItems = BulkImportAdvancedMaxItems
	}
	if len(items) > maxItems {
		return nil, fmt.Errorf("máximo %d productos por solicitud", maxItems)
	}
	res := &BulkImportAdvancedResult{}
	res.Failed = make([]BulkImportFail, 0)

	branches, err := s.branchNameMap()
	if err != nil {
		return nil, err
	}

	// 1) Validación previa (sin escribir): los productos con errores estructurales se reportan y
	// no se envían a la transacción.
	valid := make([]BulkImportAdvancedItem, 0, len(items))
	seenCodes := map[string]int{}
	comboCodes := map[string]bool{}
	for _, it := range items {
		if len(it.ComboGroups) > 0 {
			comboCodes[strings.ToLower(strings.TrimSpace(it.Code))] = true
		}
	}
	for _, it := range items {
		if verr := validateAdvancedItem(&it, branches, seenCodes); verr != nil {
			res.Failed = append(res.Failed, BulkImportFail{Row: it.RowNumber, Name: it.Name, Error: verr.Error()})
			continue
		}
		if verr := validateAdvancedRestaurantItem(&it, opts.Restaurant, comboCodes); verr != nil {
			res.Failed = append(res.Failed, BulkImportFail{Row: it.RowNumber, Name: it.Name, Error: verr.Error()})
			continue
		}
		valid = append(valid, it)
	}
	if len(valid) == 0 {
		return res, nil
	}
	if opts.Restaurant {
		// Los combos van al final: sus componentes (platos) deben existir cuando se arma el combo.
		sort.SliceStable(valid, func(i, j int) bool { return len(valid[i].ComboGroups) == 0 && len(valid[j].ComboGroups) > 0 })
	}

	base := make([]BulkImportItem, len(valid))
	for i := range valid {
		base[i] = valid[i].BulkImportItem
	}

	stockNotes := "Stock inicial — importación avanzada"
	r, err := s.bulkImport(base, bulkImportRunOpts{
		ForceRestaurant: opts.Restaurant,
		BranchID:        branchID,
		UserID:          userID,
		StockNotes:      stockNotes,
		AfterSave: func(idx int, tx *gorm.DB, productID uint, isNew bool) (func(), error) {
			return s.applyAdvancedChildren(tx, &valid[idx], productID, isNew, branchID, userID, branches, res, opts.Restaurant)
		},
	})
	if err != nil {
		return nil, err
	}
	res.Created = r.Created
	res.Updated = r.Updated
	res.StockRegistered = r.StockRegistered
	res.LimitSkipped = r.LimitSkipped
	res.PlanLimit = r.PlanLimit
	res.Failed = append(res.Failed, r.Failed...)
	return res, nil
}

func (s *ProductService) branchNameMap() (map[string]uint, error) {
	var rows []database.TenantBranch
	if err := s.db.Select("id", "name").Find(&rows).Error; err != nil {
		return nil, err
	}
	m := make(map[string]uint, len(rows))
	for _, b := range rows {
		m[strings.ToLower(strings.TrimSpace(b.Name))] = b.ID
	}
	return m, nil
}

func lookupBranch(branches map[string]uint, sheet string, row int, name string) (uint, error) {
	n := strings.ToLower(strings.TrimSpace(name))
	if n == "" {
		return 0, advErr(sheet, row, "sucursal requerida")
	}
	id, ok := branches[n]
	if !ok {
		return 0, advErr(sheet, row, "sucursal «%s» no existe", strings.TrimSpace(name))
	}
	return id, nil
}

// validateAdvancedItem chequeos estructurales que no necesitan la BD (salvo el mapa de sucursales).
func validateAdvancedItem(it *BulkImportAdvancedItem, branches map[string]uint, seenCodes map[string]int) error {
	code := strings.TrimSpace(it.Code)
	if code == "" {
		return advErr("Productos", it.RowNumber, "el código es obligatorio en la importación avanzada")
	}
	key := strings.ToLower(code)
	if prev, dup := seenCodes[key]; dup {
		return advErr("Productos", it.RowNumber, "código «%s» repetido (ya aparece en la fila %d)", code, prev)
	}
	seenCodes[key] = it.RowNumber
	it.Code = code

	if len(it.Presentations) > 0 && len(it.SaleUnits) > 0 {
		return advErr("Unidades de venta", it.SaleUnits[0].RowNumber,
			"el producto «%s» tiene presentaciones y unidades de venta: no pueden convivir (el POS oculta las presentaciones cuando hay unidades de venta). Usa solo una de las dos hojas", code)
	}
	if len(it.Presentations) > 0 && len(it.BranchStocks) > 0 {
		return advErr("Stock por sucursal", it.BranchStocks[0].RowNumber,
			"el producto «%s» tiene presentaciones: su stock se maneja por presentación (columna stock_inicial de la hoja Presentaciones), no por producto", code)
	}
	if len(it.Presentations) > 0 && it.InitialStock > 0 {
		return advErr("Productos", it.RowNumber,
			"el producto «%s» tiene presentaciones: su stock va en la columna stock_inicial de la hoja Presentaciones", code)
	}

	names := map[string]bool{}
	for _, p := range it.Presentations {
		n := strings.ToLower(strings.TrimSpace(p.Name))
		if n == "" {
			return advErr("Presentaciones", p.RowNumber, "nombre requerido")
		}
		if names[n] {
			return advErr("Presentaciones", p.RowNumber, "presentación «%s» repetida para el mismo producto", strings.TrimSpace(p.Name))
		}
		names[n] = true
		if p.SalePrice <= 0 {
			return advErr("Presentaciones", p.RowNumber, "la presentación «%s» debe tener un precio de venta mayor a 0", strings.TrimSpace(p.Name))
		}
		if p.InitialStock < 0 {
			return advErr("Presentaciones", p.RowNumber, "stock_inicial no puede ser negativo")
		}
		if p.InitialStock > 0 && !it.ManageStock {
			return advErr("Presentaciones", p.RowNumber, "stock_inicial requiere que el producto controle stock (control_stock = si)")
		}
	}

	names = map[string]bool{}
	bases := 0
	for _, u := range it.SaleUnits {
		n := strings.ToLower(strings.TrimSpace(u.Name))
		if n == "" {
			return advErr("Unidades de venta", u.RowNumber, "nombre requerido")
		}
		if names[n] {
			return advErr("Unidades de venta", u.RowNumber, "unidad de venta «%s» repetida para el mismo producto", strings.TrimSpace(u.Name))
		}
		names[n] = true
		if strings.TrimSpace(u.Unit) == "" {
			return advErr("Unidades de venta", u.RowNumber, "la unidad de venta requiere una unidad de medida (Catálogo SUNAT N°03)")
		}
		if u.IsBase {
			bases++
			if bases > 1 {
				return advErr("Unidades de venta", u.RowNumber, "solo una unidad de venta puede ser la base por producto")
			}
		}
	}

	seenPrice := map[string]bool{}
	saleUnitNames := names
	for _, bp := range it.BranchPrices {
		bid, err := lookupBranch(branches, "Precios por sucursal", bp.RowNumber, bp.BranchName)
		if err != nil {
			return err
		}
		k := fmt.Sprintf("%d|%s", bid, strings.ToLower(strings.TrimSpace(bp.SaleUnitName)))
		if seenPrice[k] {
			return advErr("Precios por sucursal", bp.RowNumber, "precio repetido para la misma sucursal/unidad de venta")
		}
		seenPrice[k] = true
		if bp.SalePrice <= 0 {
			return advErr("Precios por sucursal", bp.RowNumber, "el precio debe ser mayor a cero")
		}
		// Si el Excel trae unidades de venta para este producto, la unidad citada debe estar ahí;
		// si no las trae, se valida contra la BD dentro de la transacción.
		if sn := strings.ToLower(strings.TrimSpace(bp.SaleUnitName)); sn != "" && len(it.SaleUnits) > 0 && !saleUnitNames[sn] {
			return advErr("Precios por sucursal", bp.RowNumber, "la unidad de venta «%s» no está en la hoja Unidades de venta para este producto", strings.TrimSpace(bp.SaleUnitName))
		}
	}

	seenStock := map[uint]bool{}
	for _, bs := range it.BranchStocks {
		bid, err := lookupBranch(branches, "Stock por sucursal", bs.RowNumber, bs.BranchName)
		if err != nil {
			return err
		}
		if seenStock[bid] {
			return advErr("Stock por sucursal", bs.RowNumber, "sucursal «%s» repetida para el mismo producto", strings.TrimSpace(bs.BranchName))
		}
		seenStock[bid] = true
		if bs.Quantity < 0 {
			return advErr("Stock por sucursal", bs.RowNumber, "la cantidad no puede ser negativa")
		}
		if !it.ManageStock {
			return advErr("Stock por sucursal", bs.RowNumber, "el producto no controla stock (control_stock = no): no se puede cargar stock por sucursal")
		}
	}
	return nil
}

// applyAdvancedChildren aplica los hijos dentro de la transacción del producto. Los contadores se
// acumulan en una copia local y solo se suman al resultado si la transacción hace commit.
func (s *ProductService) applyAdvancedChildren(
	tx *gorm.DB, it *BulkImportAdvancedItem, productID uint, isNew bool,
	branchID, userID uint, branches map[string]uint, res *BulkImportAdvancedResult, restaurant bool,
) (func(), error) {
	ps := NewProductService(tx)
	inv := invsvc.NewInventoryService(tx)
	var c BulkImportAdvancedResult

	// ── Presentaciones ────────────────────────────────────────────────────────────────────────
	if len(it.Presentations) > 0 {
		var activeUnits int64
		tx.Model(&database.TenantProductSaleUnit{}).Where("product_id = ? AND active = ?", productID, true).Count(&activeUnits)
		if activeUnits > 0 {
			return nil, advErr("Presentaciones", it.Presentations[0].RowNumber,
				"el producto «%s» ya tiene unidades de venta activas: no se le pueden agregar presentaciones (no pueden convivir). Desactiva las unidades de venta desde el sistema o usa solo la hoja Unidades de venta", it.Code)
		}
		var before int64
		tx.Model(&database.TenantProductPresentation{}).Where("product_id = ?", productID).Count(&before)
		inputs := make([]ProductPresentationInput, 0, len(it.Presentations))
		for _, p := range it.Presentations {
			inputs = append(inputs, ProductPresentationInput{
				Name: p.Name, SalePrice: p.SalePrice, SortOrder: p.SortOrder, InitialStock: p.InitialStock,
			})
		}
		results, err := ps.syncPresentations(productID, inputs)
		if err != nil {
			return nil, fmt.Errorf("hoja Presentaciones: %w", err)
		}
		updated := 0
		for i, r := range results {
			if r.IsNew {
				c.PresentationsCreated++
				if r.InitialStock > 0 && it.ManageStock && branchID > 0 {
					if err := inv.RecordInitialPresentationStock(productID, r.Presentation.ID, branchID, r.InitialStock, userID,
						"Stock inicial — importación avanzada"); err != nil {
						return nil, advErr("Presentaciones", presRow(it, i), "%s", err.Error())
					}
				} else if r.InitialStock > 0 && branchID == 0 {
					return nil, advErr("Presentaciones", presRow(it, i), "sucursal activa requerida para cargar stock inicial")
				}
			} else {
				c.PresentationsUpdated++
				updated++
			}
		}
		if d := int(before) - updated; d > 0 {
			c.PresentationsDeactivated += d
		}
	}

	// ── Extras y combos (solo Tukichef) ──────────────────────────────────────────────────────
	var rc restaurantCounters
	var commitRestaurant func()
	if restaurant {
		var err error
		commitRestaurant, err = s.applyRestaurantChildren(tx, ps, it, productID, branchID, &rc, res)
		if err != nil {
			return nil, err
		}
	}

	// ── Unidades de venta ─────────────────────────────────────────────────────────────────────
	if len(it.SaleUnits) > 0 {
		var existing []database.TenantProductSaleUnit
		if err := tx.Where("product_id = ?", productID).Find(&existing).Error; err != nil {
			return nil, err
		}
		byName := map[string]database.TenantProductSaleUnit{}
		for _, e := range existing {
			byName[strings.ToLower(strings.TrimSpace(e.Name))] = e
		}
		matched := map[uint]bool{}
		for _, u := range it.SaleUnits {
			code := sunat.NormalizeUnit(u.Unit, "product")
			unitID, err := database.EnsureUnitByCode(tx, code)
			if err != nil {
				return nil, advErr("Unidades de venta", u.RowNumber, "%s", err.Error())
			}
			in := SaleUnitInput{
				Name: u.Name, UnitID: &unitID, ConversionFactor: u.ConversionFactor, IsBase: u.IsBase,
				AllowFraction: u.AllowFraction, Price1: u.Price1, Price2: u.Price2, Price3: u.Price3,
				SortOrder: u.SortOrder, Active: true,
			}
			if e, ok := byName[strings.ToLower(strings.TrimSpace(u.Name))]; ok {
				if _, err := ps.UpdateSaleUnit(productID, e.ID, in); err != nil {
					return nil, advErr("Unidades de venta", u.RowNumber, "%s", err.Error())
				}
				matched[e.ID] = true
				c.SaleUnitsUpdated++
				continue
			}
			if _, err := ps.CreateSaleUnit(productID, in); err != nil {
				return nil, advErr("Unidades de venta", u.RowNumber, "%s", err.Error())
			}
			c.SaleUnitsCreated++
		}
		for _, e := range existing {
			if matched[e.ID] || !e.Active {
				continue
			}
			if err := tx.Model(&database.TenantProductSaleUnit{}).Where("id = ?", e.ID).Update("active", false).Error; err != nil {
				return nil, err
			}
			c.SaleUnitsDeactivated++
		}
	}

	// ── Precios por sucursal ──────────────────────────────────────────────────────────────────
	if len(it.BranchPrices) > 0 {
		var units []database.TenantProductSaleUnit
		if err := tx.Where("product_id = ? AND active = ?", productID, true).Find(&units).Error; err != nil {
			return nil, err
		}
		unitByName := map[string]uint{}
		var unitIDs []uint
		for _, u := range units {
			unitByName[strings.ToLower(strings.TrimSpace(u.Name))] = u.ID
			unitIDs = append(unitIDs, u.ID)
		}
		type key struct{ branch, unit uint }
		touched := map[key]bool{}
		for _, bp := range it.BranchPrices {
			bid, err := lookupBranch(branches, "Precios por sucursal", bp.RowNumber, bp.BranchName)
			if err != nil {
				return nil, err
			}
			if sn := strings.ToLower(strings.TrimSpace(bp.SaleUnitName)); sn != "" {
				suID, ok := unitByName[sn]
				if !ok {
					return nil, advErr("Precios por sucursal", bp.RowNumber, "el producto no tiene la unidad de venta activa «%s»", strings.TrimSpace(bp.SaleUnitName))
				}
				touched[key{bid, suID}] = true
				var row database.TenantProductSaleUnitBranchPrice
				findErr := tx.Where("sale_unit_id = ? AND branch_id = ?", suID, bid).First(&row).Error
				if findErr == nil {
					_, err = ps.UpdateSaleUnitBranchPrice(productID, suID, bid, SaleUnitBranchPriceInput{
						Price1: bp.SalePrice, Price2: row.Price2, Price3: row.Price3, Active: true,
					})
					if err == nil {
						c.BranchPricesUpdated++
					}
				} else if errors.Is(findErr, gorm.ErrRecordNotFound) {
					_, err = ps.CreateSaleUnitBranchPrice(productID, suID, bid, SaleUnitBranchPriceInput{Price1: bp.SalePrice})
					if err == nil {
						c.BranchPricesCreated++
					}
				} else {
					err = findErr
				}
				if err != nil {
					return nil, advErr("Precios por sucursal", bp.RowNumber, "%s", err.Error())
				}
				continue
			}
			touched[key{bid, 0}] = true
			var row database.TenantProductBranchPrice
			findErr := tx.Where("product_id = ? AND branch_id = ?", productID, bid).First(&row).Error
			if findErr == nil {
				_, err = ps.UpdateProductBranchPrice(productID, bid, ProductBranchPriceInput{SalePrice: bp.SalePrice, Active: true})
				if err == nil {
					c.BranchPricesUpdated++
				}
			} else if errors.Is(findErr, gorm.ErrRecordNotFound) {
				_, err = ps.CreateProductBranchPrice(productID, bid, ProductBranchPriceInput{SalePrice: bp.SalePrice})
				if err == nil {
					c.BranchPricesCreated++
				}
			} else {
				err = findErr
			}
			if err != nil {
				return nil, advErr("Precios por sucursal", bp.RowNumber, "%s", err.Error())
			}
		}
		// Lo que ya existía y no vino en la hoja se desactiva (lista final = la del Excel).
		var oldProd []database.TenantProductBranchPrice
		tx.Where("product_id = ? AND active = ?", productID, true).Find(&oldProd)
		for _, o := range oldProd {
			if touched[key{o.BranchID, 0}] {
				continue
			}
			if err := tx.Model(&database.TenantProductBranchPrice{}).Where("id = ?", o.ID).Update("active", false).Error; err != nil {
				return nil, err
			}
			c.BranchPricesDeactivated++
		}
		if len(unitIDs) > 0 {
			var oldUnits []database.TenantProductSaleUnitBranchPrice
			tx.Where("sale_unit_id IN ? AND active = ?", unitIDs, true).Find(&oldUnits)
			for _, o := range oldUnits {
				if touched[key{o.BranchID, o.SaleUnitID}] {
					continue
				}
				if err := tx.Model(&database.TenantProductSaleUnitBranchPrice{}).Where("id = ?", o.ID).Update("active", false).Error; err != nil {
					return nil, err
				}
				c.BranchPricesDeactivated++
			}
		}
	}

	// ── Stock por sucursal ────────────────────────────────────────────────────────────────────
	// Producto nuevo: entrada inicial. Existente: ajuste absoluto al valor del Excel (también a 0).
	// Si la sucursal activa ya recibió stock_inicial en este mismo import, esta fila manda (ajuste).
	for _, bs := range it.BranchStocks {
		bid, err := lookupBranch(branches, "Stock por sucursal", bs.RowNumber, bs.BranchName)
		if err != nil {
			return nil, err
		}
		var cur database.TenantProductStock
		hasRow := tx.Where("product_id = ? AND branch_id = ?", productID, bid).First(&cur).Error == nil
		switch {
		case bs.Quantity > 0 && isNew && !hasRow:
			err = inv.RecordInitialStock(productID, bid, bs.Quantity, userID, "Stock inicial — importación avanzada")
		case bs.Quantity > 0:
			err = inv.RecordMovementTx(tx, invsvc.MovementInput{
				ProductID: productID, BranchID: bid, Type: "adjustment", Quantity: bs.Quantity,
				Reference: "STOCK_INICIAL", Notes: "Stock inicial — importación avanzada (ajuste a valor de importación)",
				UserID: userID, OperationCode: "INITIAL_STOCK",
			})
		case hasRow && cur.Quantity > 0:
			err = inv.RecordMovementTx(tx, invsvc.MovementInput{
				ProductID: productID, BranchID: bid, Type: "adjustment_out", Quantity: cur.Quantity,
				Reference: "STOCK_INICIAL", Notes: "Stock puesto en 0 por importación avanzada",
				UserID: userID, OperationCode: "INITIAL_STOCK",
			})
		default:
			err = inv.EnsureProductBranchLink(productID, bid)
		}
		if err != nil {
			return nil, advErr("Stock por sucursal", bs.RowNumber, "%s", err.Error())
		}
		c.BranchStocksSet++
	}

	return func() {
		if commitRestaurant != nil {
			commitRestaurant()
		}
		rc.addTo(res)
		res.PresentationsCreated += c.PresentationsCreated
		res.PresentationsUpdated += c.PresentationsUpdated
		res.PresentationsDeactivated += c.PresentationsDeactivated
		res.SaleUnitsCreated += c.SaleUnitsCreated
		res.SaleUnitsUpdated += c.SaleUnitsUpdated
		res.SaleUnitsDeactivated += c.SaleUnitsDeactivated
		res.BranchPricesCreated += c.BranchPricesCreated
		res.BranchPricesUpdated += c.BranchPricesUpdated
		res.BranchPricesDeactivated += c.BranchPricesDeactivated
		res.BranchStocksSet += c.BranchStocksSet
	}, nil
}

// presRow fila Excel de la presentación en la posición i del resultado de syncPresentations (que
// respeta el orden de la entrada, ignorando nombres vacíos que la validación previa ya descartó).
func presRow(it *BulkImportAdvancedItem, i int) int {
	if i >= 0 && i < len(it.Presentations) {
		return it.Presentations[i].RowNumber
	}
	return it.RowNumber
}
