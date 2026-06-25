package service

import (
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"time"

	invsvc "tukifac/internal/inventory/service"
	"tukifac/pkg/database"
	"tukifac/pkg/sunat"
	"tukifac/pkg/tax"

	"gorm.io/gorm"
)

const (
	BulkImportMaxItems    = 2000
	BulkImportBatchPause  = 15 * time.Millisecond // pausa ligera entre lotes para no saturar MySQL
	BulkImportBatchSize   = 50
)

// ErrInitialStockWithoutManageStock conflicto control_stock=no con stock_inicial>0 (alias legible).
const ErrInitialStockWithoutManageStock = InitialStockRequiresManageStock

// BulkImportItem fila normalizada para importación masiva.
type BulkImportItem struct {
	RowNumber          int     `json:"row_number"`
	Name               string  `json:"name"`
	Code               string  `json:"code"`
	Description        string  `json:"description"`
	SalePrice          float64 `json:"sale_price"`
	Unit               string  `json:"unit"`
	CategoryName       string  `json:"category_name"`
	IgvAffectationType string  `json:"igv_affectation_type"`
	PriceIncludesIgv   bool    `json:"price_includes_igv"`
	ManageStock        bool    `json:"manage_stock"`
	InitialStock       float64 `json:"initial_stock"`
	IsRestaurant       bool    `json:"is_restaurant"`
	PreparationArea    string  `json:"preparation_area"`
	CatalogType        string  `json:"type"` // product | service (solo catálogo tenant)
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
}

type bulkImportRunOpts struct {
	ForceRestaurant bool
	BranchID        uint
	UserID          uint
	StockNotes      string
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
	catCache := make(map[string]uint)
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

		code := strings.TrimSpace(item.Code)
		autoCode := code == ""
		if autoCode {
			code = generateImportEAN13(codesProcessedInBatch, nil)
		}

		var existing database.TenantProduct
		hasExisting := false
		if !autoCode {
			var found *database.TenantProduct
			var err error
			if (item.IsRestaurant || opts.ForceRestaurant) && opts.BranchID > 0 {
				found, err = s.GetByCodeInBranch(code, opts.BranchID)
			} else {
				found, err = s.GetByCode(code)
			}
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

		igvType := strings.TrimSpace(item.IgvAffectationType)
		if igvType == "" {
			igvType = "10"
		}

		var catID *uint
		categoryProvided := strings.TrimSpace(item.CategoryName) != ""
		if categoryProvided {
			id, err := s.resolveCategoryIDByName(item.CategoryName, catCache)
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

		catalogType := strings.TrimSpace(strings.ToLower(item.CatalogType))
		if catalogType == "" {
			if hasExisting && strings.TrimSpace(existing.Type) != "" {
				catalogType = strings.ToLower(strings.TrimSpace(existing.Type))
			} else {
				catalogType = "product"
			}
		}
		unit := sunat.NormalizeUnit(item.Unit, catalogType)
		if strings.TrimSpace(item.Unit) == "" && hasExisting && existing.Unit != "" {
			unit = existing.Unit
		}

		input := ProductInput{
			CategoryID:         catID,
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
			TaxRate:            taxCfg.EffectiveRate(igvType),
		}
		if hasExisting {
			input.PurchasePrice = existing.PurchasePrice
			input.ManageSeries = existing.ManageSeries
			input.HasVariants = existing.HasVariants
			input.HasModifiers = existing.HasModifiers
			input.MinStock = existing.MinStock
			input.ImageURL = existing.ImageURL
		}

		var productID uint
		isNewProduct := !hasExisting
		err := s.db.Transaction(func(tx *gorm.DB) error {
			ps := NewProductService(tx)
			inv := invsvc.NewInventoryService(tx)
			if hasExisting {
				if err := ps.Update(existing.ID, input); err != nil {
					return err
				}
				productID = existing.ID
			} else {
				p, err := ps.Create(input)
				if err != nil {
					return err
				}
				productID = p.ID
			}
			if item.InitialStock > 0 && manageStock && opts.BranchID > 0 && isNewProduct {
				return inv.RecordInitialStock(
					productID, opts.BranchID, item.InitialStock, opts.UserID, opts.StockNotes,
				)
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

		codesProcessedInBatch[code] = struct{}{}
		if hasExisting {
			result.Updated++
		} else {
			result.Created++
		}
		if isNewProduct && item.InitialStock > 0 && manageStock {
			result.StockRegistered++
		}
		_ = productID
	}

	return result, nil
}

func branchIDForImport(isRestaurant bool, branchID uint) uint {
	if isRestaurant && branchID > 0 {
		return branchID
	}
	return 0
}

func (s *ProductService) resolveCategoryIDByName(name string, cache map[string]uint) (uint, error) {
	key := strings.ToLower(strings.TrimSpace(name))
	if key == "" {
		return 0, nil
	}
	if id, ok := cache[key]; ok {
		return id, nil
	}
	var cat database.TenantCategory
	err := s.db.Where("LOWER(name) = ?", key).First(&cat).Error
	if err == nil {
		cache[key] = cat.ID
		return cat.ID, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, err
	}
	created, err := s.CreateCategory(strings.TrimSpace(name), "")
	if err != nil {
		return 0, fmt.Errorf("crear categoría %q: %w", name, err)
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
