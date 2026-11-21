package service

import (
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"time"

	invsvc "tukifac/internal/inventory/service"
	"tukifac/pkg/database"
	"tukifac/pkg/categorybranch"
	"tukifac/pkg/productbranch"
	"tukifac/pkg/sunat"
	"tukifac/pkg/tax"

	"gorm.io/gorm"
)

const (
	BulkImportMaxItems   = 2000
	BulkImportBatchPause = 15 * time.Millisecond // pausa ligera entre lotes para no saturar MySQL
	BulkImportBatchSize  = 50
)

// ErrInitialStockWithoutManageStock conflicto control_stock=no con stock_inicial>0 (alias legible).
const ErrInitialStockWithoutManageStock = InitialStockRequiresManageStock

// BulkImportItem fila normalizada para importación masiva.
type BulkImportItem struct {
	RowNumber          int      `json:"row_number"`
	Name               string   `json:"name"`
	Code               string   `json:"code"`
	Description        string   `json:"description"`
	SalePrice          float64  `json:"sale_price"`
	PurchasePrice      *float64 `json:"purchase_price"` // opcional; nil = no enviar / no sobrescribir en update
	Unit               string   `json:"unit"`
	CategoryName       string   `json:"category_name"`
	BrandName          string   `json:"brand_name"` // opcional; find-or-create por nombre, igual que CategoryName
	IgvAffectationType string   `json:"igv_affectation_type"`
	PriceIncludesIgv   bool     `json:"price_includes_igv"`
	ManageStock        bool     `json:"manage_stock"`
	InitialStock       float64  `json:"initial_stock"`
	IsRestaurant       bool     `json:"is_restaurant"`
	PreparationArea    string   `json:"preparation_area"`
	CatalogType        string   `json:"type"` // product | service (solo catálogo tenant)
	// ExpiryDate: nil = no cambiar vencimiento en update; "" = sin vencimiento; YYYY-MM-DD = con vencimiento.
	ExpiryDate *string `json:"expiry_date"`
	// BranchNames: columna "sucursales" — nombres separados por coma. nil/vacío = no cambiar (un
	// producto nuevo queda en todas); "todas" = todas las sucursales. Ver pkg/productbranch.
	BranchNames *string `json:"branch_names"`
	// MinStock: columna "stock_minimo". nil = no cambiar (un producto nuevo queda en 0).
	MinStock *float64 `json:"min_stock"`
}

type BulkImportFail struct {
	Row   int    `json:"row"`
	Name  string `json:"name"`
	Error string `json:"error"`
}

type BulkImportResult struct {
	Created         int              `json:"created"`
	Updated         int              `json:"updated"`
	StockRegistered int              `json:"stock_registered"`
	Failed          []BulkImportFail `json:"failed"`
	// LimitSkipped: filas de productos NUEVOS que no se importaron porque el plan ya no tenía cupo (no son errores del Excel:
	// se informan aparte). PlanLimit describe el tope vigente; nil = el plan no limita productos.
	LimitSkipped int                  `json:"limit_skipped"`
	PlanLimit    *BulkImportPlanLimit `json:"plan_limit,omitempty"`
}

// BulkImportPlanLimit el tope de productos del plan frente a esta importación.
type BulkImportPlanLimit struct {
	Max int `json:"max"`
	// Used: productos que ya ocupaban cupo al empezar la importación.
	Used int64 `json:"used"`
	// AllowedNew: productos nuevos que cabían en el plan al empezar (los que superen esa cantidad se omiten).
	AllowedNew int `json:"allowed_new"`
}

// ProductQuota es el cupo restante de productos del plan durante una importación masiva. Los productos que ya existen
// (se actualizan) no consumen cupo; solo los NUEVOS que no son servicio. nil = el plan no limita productos.
type ProductQuota struct {
	Max       int
	Used      int64
	Remaining int
}

// NewProductQuota arma el cupo a partir del tope del plan y los productos actuales. max<=0 = ilimitado → nil.
func NewProductQuota(max int, used int64) *ProductQuota {
	if max <= 0 {
		return nil
	}
	rem := max - int(used)
	if rem < 0 {
		rem = 0
	}
	return &ProductQuota{Max: max, Used: used, Remaining: rem}
}

// SetImportQuota fija el cupo de productos que respetarán las importaciones masivas de este servicio.
func (s *ProductService) SetImportQuota(q *ProductQuota) { s.importQuota = q }

type bulkImportRunOpts struct {
	ForceRestaurant bool
	BranchID        uint
	UserID          uint
	StockNotes      string
	// AfterSave (importación avanzada): corre DENTRO de la transacción del producto, después de
	// guardarlo y de registrar su stock inicial. Si devuelve error el producto entero se revierte.
	// idx es la posición del ítem en la lista recibida por bulkImport. El func devuelto (opcional)
	// se ejecuta solo si la transacción hizo commit (para acumular contadores sin contar rollbacks).
	AfterSave func(idx int, tx *gorm.DB, productID uint, isNew bool) (onCommit func(), err error)
}

// BulkImportRestaurant importa platos (is_restaurant=true, con área de preparación).
func (s *ProductService) BulkImportRestaurant(items []BulkImportItem, branchID, userID uint) (*BulkImportResult, error) {
	for i := range items {
		items[i].IsRestaurant = true
	}
	return s.bulkImport(items, bulkImportRunOpts{
		ForceRestaurant: true,
		BranchID:        branchID,
		UserID:          userID,
		StockNotes:      "Stock inicial — importación masiva restaurante",
	})
}

// BulkImportCatalog importa productos/servicios del panel tenant (is_restaurant según fila).
func (s *ProductService) BulkImportCatalog(items []BulkImportItem, branchID, userID uint) (*BulkImportResult, error) {
	return s.bulkImport(items, bulkImportRunOpts{
		ForceRestaurant: false,
		BranchID:        branchID,
		UserID:          userID,
		StockNotes:      "Stock inicial — importación masiva catálogo",
	})
}

func (s *ProductService) bulkImport(items []BulkImportItem, opts bulkImportRunOpts) (*BulkImportResult, error) {
	if len(items) == 0 {
		return nil, errors.New("no hay filas para importar")
	}
	if len(items) > BulkImportMaxItems {
		return nil, fmt.Errorf("máximo %d filas por solicitud", BulkImportMaxItems)
	}
	needsBranch := false
	needsBranchForRestaurant := false
	for _, item := range items {
		if item.InitialStock > 0 {
			needsBranch = true
		}
		if item.IsRestaurant || opts.ForceRestaurant {
			needsBranchForRestaurant = true
		}
	}
	if needsBranch && opts.BranchID == 0 {
		return nil, errors.New("sucursal activa requerida cuando hay stock_inicial")
	}
	if needsBranchForRestaurant && opts.BranchID == 0 {
		return nil, errors.New("sucursal activa requerida para productos de restaurante (asignación a carta Tukichef)")
	}

	taxCfg := tax.LoadFromDB(s.db)
	result := &BulkImportResult{Failed: make([]BulkImportFail, 0)}
	quota := s.importQuota
	if quota != nil {
		result.PlanLimit = &BulkImportPlanLimit{Max: quota.Max, Used: quota.Used, AllowedNew: quota.Remaining}
	}
	catCache := make(map[string]uint)
	brandCache := make(map[string]uint)
	var branchNameCache map[string]uint // nombre normalizado → id; se carga al primer uso
	codesProcessedInBatch := make(map[string]struct{})

	for i := 0; i < len(items); i++ {
		if BulkImportBatchPause > 0 && i > 0 && i%BulkImportBatchSize == 0 {
			time.Sleep(BulkImportBatchPause)
		}
		item := items[i]
		if item.Name == "" {
			result.Failed = append(result.Failed, BulkImportFail{
				Row: item.RowNumber, Name: item.Name, Error: "nombre requerido",
			})
			continue
		}
		if item.SalePrice <= 0 {
			result.Failed = append(result.Failed, BulkImportFail{
				Row: item.RowNumber, Name: item.Name, Error: "precio_venta debe ser mayor a 0",
			})
			continue
		}
		if item.PurchasePrice != nil && *item.PurchasePrice < 0 {
			result.Failed = append(result.Failed, BulkImportFail{
				Row: item.RowNumber, Name: item.Name, Error: "precio_compra no puede ser negativo",
			})
			continue
		}

		code := strings.TrimSpace(item.Code)
		autoCode := code == ""
		if autoCode {
			code = generateImportEAN13(codesProcessedInBatch, nil)
		}

		var existing database.TenantProduct
		hasExisting := false
		if !autoCode {
			restaurantScoped := (item.IsRestaurant || opts.ForceRestaurant) && opts.BranchID > 0
			found, err := s.findProductByCodeUnscoped(code, opts.BranchID, restaurantScoped)
			if err != nil {
				result.Failed = append(result.Failed, BulkImportFail{
					Row: item.RowNumber, Name: item.Name, Error: err.Error(),
				})
				continue
			}
			if found != nil {
				existing = *found
				hasExisting = true
			}
		}

		if !item.ManageStock && item.InitialStock > 0 {
			result.Failed = append(result.Failed, BulkImportFail{
				Row:   item.RowNumber,
				Name:  item.Name,
				Error: InitialStockRequiresManageStock,
			})
			continue
		}
		manageStock := item.ManageStock
		isRestaurant := item.IsRestaurant
		if opts.ForceRestaurant {
			isRestaurant = true
		}
		prepArea := strings.TrimSpace(strings.ToLower(item.PreparationArea))
		if !isRestaurant {
			prepArea = ""
		}

		igvType, igvErr := normalizeBulkIgvAffectation(item.IgvAffectationType)
		if igvErr != nil {
			result.Failed = append(result.Failed, BulkImportFail{
				Row: item.RowNumber, Name: item.Name, Error: igvErr.Error(),
			})
			continue
		}

		var catID *uint
		categoryProvided := strings.TrimSpace(item.CategoryName) != ""
		if categoryProvided {
			id, err := s.resolveCategoryIDByName(item.CategoryName, opts.BranchID, catCache)
			if err != nil {
				result.Failed = append(result.Failed, BulkImportFail{
					Row: item.RowNumber, Name: item.Name, Error: err.Error(),
				})
				continue
			}
			if id > 0 {
				catID = &id
			}
		} else if hasExisting {
			catID = existing.CategoryID
		}

		var brandID *uint
		brandProvided := strings.TrimSpace(item.BrandName) != ""
		if brandProvided {
			id, err := s.resolveBrandIDByName(item.BrandName, brandCache)
			if err != nil {
				result.Failed = append(result.Failed, BulkImportFail{
					Row: item.RowNumber, Name: item.Name, Error: err.Error(),
				})
				continue
			}
			if id > 0 {
				brandID = &id
			}
		} else if hasExisting {
			brandID = existing.BrandID
		}

		catalogType := strings.TrimSpace(strings.ToLower(item.CatalogType))
		if catalogType == "" {
			if hasExisting && strings.TrimSpace(existing.Type) != "" {
				catalogType = strings.ToLower(strings.TrimSpace(existing.Type))
			} else {
				catalogType = "product"
			}
		}
		// Cupo del plan: solo los productos que pasan a ocupar un lugar nuevo (alta, o reactivar uno eliminado) y no son servicio.
		consumesSeat := (!hasExisting || isProductSoftDeleted(&existing)) && catalogType != "service"
		if consumesSeat && quota != nil && quota.Remaining <= 0 {
			result.LimitSkipped++
			continue
		}
		unit := sunat.NormalizeUnit(item.Unit, catalogType)
		if strings.TrimSpace(item.Unit) == "" && hasExisting && existing.Unit != "" {
			unit = existing.Unit
		}

		input := ProductInput{
			CategoryID:         catID,
			BrandID:            brandID,
			Code:               code,
			Name:               strings.TrimSpace(item.Name),
			Description:        strings.TrimSpace(item.Description),
			Type:               catalogType,
			Unit:               unit,
			SalePrice:          item.SalePrice,
			IgvAffectationType: igvType,
			PriceIncludesIgv:   item.PriceIncludesIgv,
			ManageStock:        manageStock,
			IsRestaurant:       isRestaurant,
			BranchID:           branchIDForImport(isRestaurant, opts.BranchID),
			PreparationArea:    prepArea,
			Active:             true,
			ActiveSet:          true,
			TaxRate:            taxCfg.EffectiveRate(igvType),
		}
		if item.PurchasePrice != nil {
			input.PurchasePrice = *item.PurchasePrice
		} else if hasExisting {
			input.PurchasePrice = existing.PurchasePrice
		}
		if item.ExpiryDate != nil {
			expiryDate, expiryErr := ParseProductExpiryDate(*item.ExpiryDate)
			if expiryErr != nil {
				result.Failed = append(result.Failed, BulkImportFail{
					Row: item.RowNumber, Name: item.Name, Error: expiryErr.Error(),
				})
				continue
			}
			input.HasExpiryDate = expiryDate != nil
			input.ExpiryDate = expiryDate
		} else if hasExisting {
			input.HasExpiryDate = existing.HasExpiryDate
			input.ExpiryDate = existing.ExpiryDate
		}
		if hasExisting {
			input.ManageSeries = existing.ManageSeries
			input.HasVariants = existing.HasVariants
			input.HasModifiers = existing.HasModifiers
			input.MinStock = existing.MinStock
			input.ImageURL = existing.ImageURL
		}
		if item.MinStock != nil {
			if *item.MinStock < 0 {
				result.Failed = append(result.Failed, BulkImportFail{
					Row: item.RowNumber, Name: item.Name, Error: "stock_minimo no puede ser negativo",
				})
				continue
			}
			input.MinStock = *item.MinStock
		}

		branchIDs, setBranches, branchErr := s.resolveImportBranches(item.BranchNames, &branchNameCache)
		if branchErr != nil {
			result.Failed = append(result.Failed, BulkImportFail{
				Row: item.RowNumber, Name: item.Name, Error: branchErr.Error(),
			})
			continue
		}

		// Categoría de sucursal: el producto debe quedar vendiéndose solo allí (sucursales de la fila, o las que ya tenía).
		if catID != nil {
			effective := branchIDs
			if !setBranches {
				switch {
				case hasExisting:
					effective, _ = categorybranch.EffectiveBranches(s.db, existing.ID)
				case isRestaurant && opts.BranchID > 0:
					effective = []uint{opts.BranchID} // plato de la carta: de la sucursal de la importación
				}
			}
			if cerr := categorybranch.CheckProductCompatible(s.db, catID, effective); cerr != nil {
				result.Failed = append(result.Failed, BulkImportFail{
					Row: item.RowNumber, Name: item.Name, Error: cerr.Error(),
				})
				continue
			}
		}

		var productID uint
		var onCommit func()
		isNewProduct := !hasExisting
		err := s.db.Transaction(func(tx *gorm.DB) error {
			ps := NewProductService(tx)
			inv := invsvc.NewInventoryService(tx)
			if hasExisting {
				if isProductSoftDeleted(&existing) {
					if err := ps.restoreSoftDeletedProduct(existing.ID); err != nil {
						return err
					}
				}
				if _, err := ps.Update(existing.ID, input); err != nil {
					return err
				}
				productID = existing.ID
			} else {
				p, _, err := ps.Create(input)
				if err != nil {
					return err
				}
				productID = p.ID
			}
			// Antes del stock: si la sucursal del import no está entre las indicadas, la entrada de
			// stock la agrega (misma regla que compras/transferencias).
			if setBranches {
				if err := productbranch.Set(tx, productID, branchIDs); err != nil {
					return err
				}
			}
			if item.InitialStock > 0 && manageStock && opts.BranchID > 0 {
				if isNewProduct {
					// Producto nuevo: registra el stock inicial (entrada al kardex).
					if err := inv.RecordInitialStock(
						productID, opts.BranchID, item.InitialStock, opts.UserID, opts.StockNotes,
					); err != nil {
						return err
					}
				} else {
					// Producto existente: fija el stock al valor del Excel (ajuste a valor absoluto).
					if err := inv.RecordMovementTx(tx, invsvc.MovementInput{
						ProductID:     productID,
						BranchID:      opts.BranchID,
						Type:          "adjustment",
						Quantity:      item.InitialStock,
						Reference:     "STOCK_INICIAL",
						Notes:         opts.StockNotes + " (ajuste a valor de importación)",
						UserID:        opts.UserID,
						OperationCode: "INITIAL_STOCK",
					}); err != nil {
						return err
					}
				}
			}
			if opts.AfterSave != nil {
				cb, err := opts.AfterSave(i, tx, productID, isNewProduct)
				if err != nil {
					return err
				}
				onCommit = cb
			}
			if manageStock && opts.BranchID > 0 {
				return inv.EnsureProductBranchLink(productID, opts.BranchID)
			}
			if isRestaurant && opts.BranchID > 0 {
				return inv.EnsureProductBranchLink(productID, opts.BranchID)
			}
			return nil
		})
		if err != nil {
			result.Failed = append(result.Failed, BulkImportFail{
				Row: item.RowNumber, Name: item.Name, Error: err.Error(),
			})
			continue
		}

		if onCommit != nil {
			onCommit()
		}
		codesProcessedInBatch[code] = struct{}{}
		if consumesSeat && quota != nil {
			quota.Remaining--
		}
		if hasExisting {
			result.Updated++
		} else {
			result.Created++
		}
		if item.InitialStock > 0 && manageStock {
			result.StockRegistered++
		}
		_ = productID
	}

	return result, nil
}

// normalizeBulkIgvAffectation códigos SUNAT de afectación IGV permitidos en importación.
// 10 gravado, 20 exonerado, 30 inafecto, 40 exportación. Vacío → 10.
func normalizeBulkIgvAffectation(raw string) (string, error) {
	c := strings.TrimSpace(raw)
	if c == "" {
		return "10", nil
	}
	// Excel / JSON a veces envían "10.0"
	if i := strings.IndexByte(c, '.'); i > 0 {
		c = c[:i]
	}
	switch c {
	case "10", "15", "20", "30", "40":
		return c, nil
	default:
		return "", errors.New("afectacion_igv debe ser 10 (gravado), 15 (gravado bonificaciones), 20 (exonerado), 30 (inafecto) o 40 (exportación)")
	}
}

func branchIDForImport(isRestaurant bool, branchID uint) uint {
	if isRestaurant && branchID > 0 {
		return branchID
	}
	return 0
}

// resolveCategoryIDByName busca la categoría por nombre visible desde la sucursal de la importación (primero la de esa
// sucursal, después la de todas) y, si no existe, la crea para todas las sucursales. Si el nombre solo existe como categoría
// de OTRA sucursal, la creación falla con el aviso de nombre repetido (categorybranch.EnsureNameFree) y la fila se informa.
func (s *ProductService) resolveCategoryIDByName(name string, branchID uint, cache map[string]uint) (uint, error) {
	key := strings.ToLower(strings.TrimSpace(name))
	if key == "" {
		return 0, nil
	}
	if id, ok := cache[key]; ok {
		return id, nil
	}
	cat, found, err := categorybranch.FindByName(s.db, name, branchID)
	if err != nil {
		return 0, err
	}
	if found {
		cache[key] = cat.ID
		return cat.ID, nil
	}
	created, err := s.CreateCategory(strings.TrimSpace(name), "", nil)
	if err != nil {
		return 0, fmt.Errorf("crear categoría %q: %w", name, err)
	}
	cache[key] = created.ID
	return created.ID, nil
}

// resolveBrandIDByName busca la marca por nombre (case-insensitive) y la crea si no existe —
// misma lógica que resolveCategoryIDByName, columna "marca" opcional en la plantilla de Excel.
func (s *ProductService) resolveBrandIDByName(name string, cache map[string]uint) (uint, error) {
	key := strings.ToLower(strings.TrimSpace(name))
	if key == "" {
		return 0, nil
	}
	if id, ok := cache[key]; ok {
		return id, nil
	}
	var brand database.TenantBrand
	err := s.db.Where("LOWER(name) = ?", key).First(&brand).Error
	if err == nil {
		cache[key] = brand.ID
		return brand.ID, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, err
	}
	created, err := s.CreateBrand(strings.TrimSpace(name), "", nil)
	if err != nil {
		return 0, fmt.Errorf("crear marca %q: %w", name, err)
	}
	cache[key] = created.ID
	return created.ID, nil
}

func generateImportEAN13(usedCodes map[string]struct{}, _ map[string]int) string {
	for attempt := 0; attempt < 32; attempt++ {
		raw := fmt.Sprintf("%d%d", time.Now().UnixNano(), rand.Intn(1_000_000))
		base12 := raw
		if len(base12) > 12 {
			base12 = base12[len(base12)-12:]
		}
		for len(base12) < 12 {
			base12 = "0" + base12
		}
		sum := 0
		for i := 0; i < 12; i++ {
			d := int(base12[i] - '0')
			if i%2 == 0 {
				sum += d
			} else {
				sum += 3 * d
			}
		}
		check := (10 - (sum % 10)) % 10
		code := base12 + fmt.Sprintf("%d", check)
		if _, used := usedCodes[code]; used {
			continue
		}
		return code
	}
	return fmt.Sprintf("%012d", time.Now().UnixNano()%1_000_000_000_000)
}

// resolveImportBranches interpreta la columna "sucursales" del import. Devuelve set=false cuando
// no hay que tocar la asignación (columna ausente o vacía); ids vacío con set=true = "todas".
func (s *ProductService) resolveImportBranches(raw *string, cache *map[string]uint) (ids []uint, set bool, err error) {
	if raw == nil || strings.TrimSpace(*raw) == "" {
		return nil, false, nil
	}
	val := strings.TrimSpace(*raw)
	if norm := strings.ToLower(val); norm == "todas" || norm == "todas las sucursales" || norm == "*" {
		return []uint{}, true, nil
	}
	if *cache == nil {
		var branches []database.TenantBranch
		if err := s.db.Select("id", "name").Find(&branches).Error; err != nil {
			return nil, false, err
		}
		m := make(map[string]uint, len(branches))
		for _, b := range branches {
			m[strings.ToLower(strings.TrimSpace(b.Name))] = b.ID
		}
		*cache = m
	}
	for _, part := range strings.Split(val, ",") {
		name := strings.ToLower(strings.TrimSpace(part))
		if name == "" {
			continue
		}
		id, ok := (*cache)[name]
		if !ok {
			return nil, false, fmt.Errorf("sucursal «%s» no existe", strings.TrimSpace(part))
		}
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil, false, nil
	}
	return ids, true, nil
}
