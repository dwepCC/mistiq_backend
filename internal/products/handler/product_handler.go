package handler

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	invsvc "tukifac/internal/inventory/service"
	"tukifac/internal/products/service"
	"tukifac/pkg/branch"
	"tukifac/pkg/database"
	"tukifac/pkg/tenantstorage"
	"tukifac/pkg/uploadlimits"
	"tukifac/pkg/tax"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

type ProductHandler struct{}

func NewProductHandler() *ProductHandler { return &ProductHandler{} }

func db(c fiber.Ctx) *gorm.DB {
	v, _ := c.Locals("tenantDB").(*gorm.DB)
	return v
}
func email(c fiber.Ctx) string {
	v, _ := c.Locals("user_email").(string)
	return v
}

func (h *ProductHandler) ListPage(c fiber.Ctx) error {
	svc := service.NewProductService(db(c))
	catID, _ := strconv.ParseUint(c.Query("category_id"), 10, 32)
	products, _, _ := svc.List(service.ProductListParams{
		Query:      c.Query("q"),
		CategoryID: uint(catID),
		Type:       c.Query("type"),
		ActiveOnly: true,
	})
	cats, _ := svc.ListCategories()

	return c.Render("products/index", fiber.Map{
		"Title":      "Productos",
		"UserEmail":  email(c),
		"Products":   products,
		"Categories": cats,
		"Query":      c.Query("q"),
		"Success":    c.Query("success"),
	}, "layouts/base")
}

func (h *ProductHandler) NewPage(c fiber.Ctx) error {
	svc := service.NewProductService(db(c))
	cats, _ := svc.ListCategories()
	modGroups, _ := svc.ListModifierGroups()
	taxCfg := tax.LoadFromDB(db(c))
	return c.Render("products/form", fiber.Map{
		"Title":          "Nuevo Producto",
		"UserEmail":      email(c),
		"IsEdit":         false,
		"Categories":     cats,
		"ModGroups":      modGroups,
		"AssignedGroups": []uint{},
		"CompanyTaxRate": taxCfg.TaxRate,
		"IgvRegime":      taxCfg.IgvRegime,
		"TaxBenefitZone": taxCfg.TaxBenefitZone,
	}, "layouts/base")
}

func (h *ProductHandler) CreateForm(c fiber.Ctx) error {
	svc := service.NewProductService(db(c))
	taxCfg := tax.LoadFromDB(db(c))
	input := buildProductInput(c, taxCfg)

	if _, err := svc.Create(input); err != nil {
		cats, _ := svc.ListCategories()
		modGroups, _ := svc.ListModifierGroups()
		return c.Render("products/form", fiber.Map{
			"Title":          "Nuevo Producto",
			"UserEmail":      email(c),
			"IsEdit":         false,
			"Error":          err.Error(),
			"Input":          input,
			"Categories":     cats,
			"ModGroups":      modGroups,
			"AssignedGroups": input.ModifierGroupIDs,
			"CompanyTaxRate": taxCfg.TaxRate,
			"IgvRegime":      taxCfg.IgvRegime,
			"TaxBenefitZone": taxCfg.TaxBenefitZone,
		}, "layouts/base")
	}
	return c.Redirect().To("/products?success=created")
}

func (h *ProductHandler) EditPage(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).SendString("ID inválido")
	}
	svc := service.NewProductService(db(c))
	product, err := svc.GetByID(uint(id))
	if err != nil {
		return c.Status(fiber.StatusNotFound).SendString("Producto no encontrado")
	}
	cats, _ := svc.ListCategories()
	modGroups, _ := svc.ListModifierGroups()
	assignedGroups := svc.GetProductModifierGroupIDs(uint(id))
	taxCfg := tax.LoadFromDB(db(c))

	return c.Render("products/form", fiber.Map{
		"Title":          "Editar Producto",
		"UserEmail":      email(c),
		"IsEdit":         true,
		"Product":        product,
		"Categories":     cats,
		"ModGroups":      modGroups,
		"AssignedGroups": assignedGroups,
		"CompanyTaxRate": taxCfg.TaxRate,
		"IgvRegime":      taxCfg.IgvRegime,
		"TaxBenefitZone": taxCfg.TaxBenefitZone,
	}, "layouts/base")
}

func (h *ProductHandler) UpdateForm(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).SendString("ID inválido")
	}
	svc := service.NewProductService(db(c))
	taxCfg := tax.LoadFromDB(db(c))
	input := buildProductInput(c, taxCfg)

	if err := svc.Update(uint(id), input); err != nil {
		cats, _ := svc.ListCategories()
		modGroups, _ := svc.ListModifierGroups()
		p, _ := svc.GetByID(uint(id))
		return c.Render("products/form", fiber.Map{
			"Title":          "Editar Producto",
			"UserEmail":      email(c),
			"IsEdit":         true,
			"Product":        p,
			"Error":          err.Error(),
			"Categories":     cats,
			"ModGroups":      modGroups,
			"AssignedGroups": input.ModifierGroupIDs,
			"CompanyTaxRate": taxCfg.TaxRate,
			"IgvRegime":      taxCfg.IgvRegime,
			"TaxBenefitZone": taxCfg.TaxBenefitZone,
		}, "layouts/base")
	}
	return c.Redirect().To("/products?success=updated")
}

func (h *ProductHandler) DeleteForm(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).SendString("ID inválido")
	}
	service.NewProductService(db(c)).Delete(uint(id))
	return c.Redirect().To("/products?success=deleted")
}

// CreateAPI crea un producto vía JSON.
func (h *ProductHandler) CreateAPI(c fiber.Ctx) error {
	var body struct {
		CategoryID         *uint   `json:"category_id"`
		Code               string  `json:"code"`
		Name               string  `json:"name"`
		Description        string  `json:"description"`
		Type               string  `json:"type"`
		Unit               string  `json:"unit"`
		SalePrice          float64 `json:"sale_price"`
		PurchasePrice      float64 `json:"purchase_price"`
		IgvAffectationType string  `json:"igv_affectation_type"`
		PriceIncludesIgv   bool    `json:"price_includes_igv"`
		ManageStock        bool    `json:"manage_stock"`
		ManageSeries       bool    `json:"manage_series"`
		HasVariants        bool    `json:"has_variants"`
		HasModifiers       bool    `json:"has_modifiers"`
		MinStock           float64 `json:"min_stock"`
		IsRestaurant       bool    `json:"is_restaurant"`
		PreparationArea    string  `json:"preparation_area"`
		ImageURL           string  `json:"image_url"`
		ModifierGroupIDs   []uint  `json:"modifier_group_ids"`
		Presentations      []struct {
			Name      string  `json:"name"`
			SalePrice float64 `json:"sale_price"`
		} `json:"presentations"`
		InitialStock float64 `json:"initial_stock"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "datos inválidos"})
	}
	if body.Name == "" {
		return c.Status(400).JSON(fiber.Map{"error": "El nombre del producto es requerido"})
	}
	if body.InitialStock < 0 {
		return c.Status(400).JSON(fiber.Map{"error": "initial_stock no puede ser negativo"})
	}
	if body.InitialStock > 0 && !body.ManageStock {
		return c.Status(400).JSON(fiber.Map{"error": service.InitialStockRequiresManageStock})
	}
	manageStock := body.ManageStock
	taxCfg := tax.LoadFromDB(db(c))
	igvType := body.IgvAffectationType
	if igvType == "" {
		igvType = "10"
	}
	input := service.ProductInput{
		CategoryID:         body.CategoryID,
		Code:               body.Code,
		Name:               body.Name,
		Description:        body.Description,
		Type:               body.Type,
		Unit:               body.Unit,
		SalePrice:          body.SalePrice,
		PurchasePrice:      body.PurchasePrice,
		TaxRate:            taxCfg.EffectiveRate(igvType),
		IgvAffectationType: igvType,
		PriceIncludesIgv:   body.PriceIncludesIgv,
		ManageStock:        manageStock,
		ManageSeries:       body.ManageSeries,
		HasVariants:        body.HasVariants,
		HasModifiers:       body.HasModifiers,
		MinStock:           body.MinStock,
		IsRestaurant:       body.IsRestaurant,
		PreparationArea:    body.PreparationArea,
		ImageURL:           body.ImageURL,
		Active:             true,
		ModifierGroupIDs: &body.ModifierGroupIDs,
	}
	if len(body.Presentations) > 0 {
		pres := make([]service.ProductPresentationInput, 0, len(body.Presentations))
		for _, row := range body.Presentations {
			if strings.TrimSpace(row.Name) == "" {
				continue
			}
			pres = append(pres, service.ProductPresentationInput{
				Name:      strings.TrimSpace(row.Name),
				SalePrice: row.SalePrice,
			})
		}
		if len(pres) > 0 {
			input.Presentations = &pres
			input.HasVariants = true
		}
	}
	branchID, berr := branch.ResolveWriteBranchID(c, 0)
	if body.IsRestaurant {
		if berr != nil {
			return c.Status(403).JSON(fiber.Map{"error": berr.Error(), "code": branch.CodeBranchRequired})
		}
		input.BranchID = branchID
	}
	p, err := service.NewProductService(db(c)).Create(input)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	if body.IsRestaurant && berr == nil && branchID > 0 {
		inv := invsvc.NewInventoryService(db(c))
		if body.InitialStock > 0 {
			if !p.ManageStock {
				_ = service.NewProductService(db(c)).Delete(p.ID)
				return c.Status(400).JSON(fiber.Map{"error": service.InitialStockRequiresManageStock})
			}
			uid, _ := c.Locals("user_id").(uint)
			if err := inv.RecordInitialStock(
				p.ID, branchID, body.InitialStock, uid, "Stock inicial — alta de producto",
			); err != nil {
				_ = service.NewProductService(db(c)).Delete(p.ID)
				return c.Status(400).JSON(fiber.Map{"error": err.Error()})
			}
		} else if err := inv.EnsureProductBranchLink(p.ID, branchID); err != nil {
			_ = service.NewProductService(db(c)).Delete(p.ID)
			return c.Status(400).JSON(fiber.Map{"error": err.Error()})
		}
	} else if body.InitialStock > 0 {
		if !p.ManageStock {
			_ = service.NewProductService(db(c)).Delete(p.ID)
			return c.Status(400).JSON(fiber.Map{"error": service.InitialStockRequiresManageStock})
		}
		if berr != nil {
			_ = service.NewProductService(db(c)).Delete(p.ID)
			return c.Status(403).JSON(fiber.Map{"error": berr.Error(), "code": branch.CodeBranchForbidden})
		}
		uid, _ := c.Locals("user_id").(uint)
		if err := invsvc.NewInventoryService(db(c)).RecordInitialStock(
			p.ID, branchID, body.InitialStock, uid, "Stock inicial — alta de producto",
		); err != nil {
			_ = service.NewProductService(db(c)).Delete(p.ID)
			return c.Status(400).JSON(fiber.Map{"error": err.Error()})
		}
	}
	return c.Status(201).JSON(fiber.Map{"data": p})
}

// UpdateAPI actualiza un producto vía JSON.
func (h *ProductHandler) UpdateAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "ID inválido"})
	}
	var body struct {
		CategoryID         *uint   `json:"category_id"`
		Code               string  `json:"code"`
		Name               string  `json:"name"`
		Description        string  `json:"description"`
		Type               string  `json:"type"`
		Unit               string  `json:"unit"`
		SalePrice          float64 `json:"sale_price"`
		PurchasePrice      float64 `json:"purchase_price"`
		IgvAffectationType string  `json:"igv_affectation_type"`
		PriceIncludesIgv   bool    `json:"price_includes_igv"`
		ManageStock        bool    `json:"manage_stock"`
		ManageSeries       bool    `json:"manage_series"`
		HasVariants        bool    `json:"has_variants"`
		HasModifiers       bool    `json:"has_modifiers"`
		MinStock           float64 `json:"min_stock"`
		IsRestaurant       bool    `json:"is_restaurant"`
		PreparationArea    string  `json:"preparation_area"`
		ImageURL           string  `json:"image_url"`
		Active             *bool   `json:"active"`
		ModifierGroupIDs *[]uint `json:"modifier_group_ids"`
		Presentations    *[]struct {
			Name      string  `json:"name"`
			SalePrice float64 `json:"sale_price"`
		} `json:"presentations"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "datos inválidos"})
	}
	svc := service.NewProductService(db(c))
	existing, err := svc.GetByID(uint(id))
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "producto no encontrado"})
	}
	if msg, denied := h.productBranchDenied(c, existing); denied {
		return c.Status(403).JSON(fiber.Map{"error": msg})
	}
	taxCfg := tax.LoadFromDB(db(c))
	igvType := body.IgvAffectationType
	if igvType == "" {
		igvType = "10"
	}
	input := service.ProductInput{
		CategoryID:         body.CategoryID,
		Code:               body.Code,
		Name:               body.Name,
		Description:        body.Description,
		Type:               body.Type,
		Unit:               body.Unit,
		SalePrice:          body.SalePrice,
		PurchasePrice:      body.PurchasePrice,
		TaxRate:            taxCfg.EffectiveRate(igvType),
		IgvAffectationType: igvType,
		PriceIncludesIgv:   body.PriceIncludesIgv,
		ManageStock:        body.ManageStock,
		ManageSeries:       body.ManageSeries,
		HasVariants:        body.HasVariants,
		HasModifiers:       body.HasModifiers,
		MinStock:           body.MinStock,
		IsRestaurant:       body.IsRestaurant,
		PreparationArea:    body.PreparationArea,
		ImageURL:           body.ImageURL,
		ModifierGroupIDs:   body.ModifierGroupIDs,
	}
	if body.Active != nil {
		input.Active = *body.Active
		input.ActiveSet = true
	}
	if body.Presentations != nil {
		pres := make([]service.ProductPresentationInput, 0, len(*body.Presentations))
		for _, row := range *body.Presentations {
			if strings.TrimSpace(row.Name) == "" {
				continue
			}
			pres = append(pres, service.ProductPresentationInput{
				Name:      strings.TrimSpace(row.Name),
				SalePrice: row.SalePrice,
			})
		}
		input.Presentations = &pres
		if len(pres) > 0 {
			input.HasVariants = true
		}
	}
	if err := svc.Update(uint(id), input); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	if body.IsRestaurant || existing.IsRestaurant {
		branchID, berr := branch.ResolveWriteBranchID(c, 0)
		if berr != nil {
			return c.Status(403).JSON(fiber.Map{"error": berr.Error(), "code": branch.CodeBranchForbidden})
		}
		if branchID > 0 {
			if err := invsvc.NewInventoryService(db(c)).EnsureProductBranchLink(uint(id), branchID); err != nil {
				return c.Status(400).JSON(fiber.Map{"error": err.Error()})
			}
		}
	}
	return c.JSON(fiber.Map{"success": true})
}

// ToggleAPI activa/desactiva un producto.
func (h *ProductHandler) ToggleAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "ID inválido"})
	}
	svc := service.NewProductService(db(c))
	p, err := svc.GetByID(uint(id))
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "producto no encontrado"})
	}
	if msg, denied := h.productBranchDenied(c, p); denied {
		return c.Status(403).JSON(fiber.Map{"error": msg})
	}
	if err := db(c).Model(p).Update("active", !p.Active).Error; err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true, "active": !p.Active})
}

// DeleteAPI elimina un producto.
func (h *ProductHandler) DeleteAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "ID inválido"})
	}
	svc := service.NewProductService(db(c))
	p, err := svc.GetByID(uint(id))
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "producto no encontrado"})
	}
	if msg, denied := h.productBranchDenied(c, p); denied {
		return c.Status(403).JSON(fiber.Map{"error": msg})
	}
	if err := svc.Delete(uint(id)); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true})
}

func (h *ProductHandler) SearchAPI(c fiber.Ctx) error {
	svc := service.NewProductService(db(c))
	catID, _ := strconv.ParseUint(c.Query("category_id"), 10, 32)
	inactiveOnly := c.Query("inactive_only") == "true" || c.Query("inactive_only") == "1"
	activeOnly := c.Query("active_only")
	if inactiveOnly {
		activeOnly = "false"
	} else if activeOnly == "" {
		activeOnly = "true"
	}
	perPage, _ := strconv.Atoi(c.Query("per_page"))
	page, _ := strconv.Atoi(c.Query("page"))
	if page < 1 {
		page = 1
	}
	report := c.Query("report") == "true" || c.Query("report") == "1"
	params := service.ProductListParams{
		Query:           c.Query("q"),
		CategoryID:      uint(catID),
		Type:            c.Query("type"),
		ActiveOnly:      !inactiveOnly && (activeOnly == "true" || activeOnly == "1"),
		InactiveOnly:    inactiveOnly,
		ManageStockOnly:    c.Query("manage_stock_only") == "true" || c.Query("manage_stock_only") == "1",
		NoManageStockOnly:  c.Query("no_manage_stock_only") == "true" || c.Query("no_manage_stock_only") == "1",
		RestaurantOnly:     c.Query("restaurant_only") == "true" || c.Query("restaurant_only") == "1",
		PreparationArea: c.Query("preparation_area"),
	}
	if v := strings.TrimSpace(c.Query("stock_less_than")); v != "" {
		if x, err := strconv.ParseFloat(v, 64); err == nil {
			params.StockLessThan = &x
		}
	}
	if reqB, err := strconv.ParseUint(c.Query("branch_id"), 10, 32); err == nil && reqB > 0 {
		params.BranchID = branch.ResolveReadBranchFilter(c, uint(reqB))
	} else if branch.ActiveBranchID(c) > 0 {
		msOnly := c.Query("manage_stock_only") == "true" || c.Query("manage_stock_only") == "1"
		if msOnly || params.RestaurantOnly {
			params.BranchID = branch.ActiveBranchID(c)
		}
	}
	if perPage > 0 {
		params.Limit = perPage
		params.Offset = (page - 1) * perPage
	}
	if report {
		items, total, err := svc.ListReport(params)
		if err != nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
		}
		if perPage > 0 {
			return c.JSON(fiber.Map{"data": items, "total": total})
		}
		return c.JSON(fiber.Map{"data": items})
	}
	products, total, err := svc.ListWithCategoryNames(params)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	if perPage > 0 {
		return c.JSON(fiber.Map{"data": products, "total": total})
	}
	return c.JSON(fiber.Map{"data": products})
}

// LookupByCodeAPI — búsqueda exacta por código de barras (POS / cámara). GET /api/products/lookup-by-code?code=
func (h *ProductHandler) LookupByCodeAPI(c fiber.Ctx) error {
	code := strings.TrimSpace(c.Query("code"))
	if code == "" {
		return c.Status(400).JSON(fiber.Map{"error": "código requerido"})
	}
	svc := service.NewProductService(db(c))
	var branchID uint
	if reqB, err := strconv.ParseUint(c.Query("branch_id"), 10, 32); err == nil && reqB > 0 {
		branchID = branch.ResolveReadBranchFilter(c, uint(reqB))
	} else if branch.ActiveBranchID(c) > 0 {
		branchID = branch.ActiveBranchID(c)
	}
	p, err := svc.FindRestaurantProductByBarcode(code, branchID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	if p == nil {
		return c.Status(404).JSON(fiber.Map{"error": "producto no encontrado"})
	}
	if msg, denied := h.productBranchDenied(c, p); denied {
		return c.Status(403).JSON(fiber.Map{"error": msg})
	}
	return c.JSON(fiber.Map{"data": svc.ProductListItemFrom(*p)})
}

// GetAPI devuelve un producto por ID con modifier_group_ids (para edición y panel avanzado).
func (h *ProductHandler) GetAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "ID inválido"})
	}
	svc := service.NewProductService(db(c))
	p, err := svc.GetByID(uint(id))
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "producto no encontrado"})
	}
	if msg, denied := h.productBranchDenied(c, p); denied {
		return c.Status(403).JSON(fiber.Map{"error": msg})
	}
	modIds := svc.GetProductModifierGroupIDs(p.ID)
	presentations, _ := svc.ListProductPresentations(p.ID)
	return c.JSON(fiber.Map{
		"data":               p,
		"modifier_group_ids": modIds,
		"presentations":      presentations,
	})
}

// ProductSerialsAPI devuelve los números de serie del producto (todas las sucursales) para el detalle.
// GET /api/products/:id/serials
func (h *ProductHandler) ProductSerialsAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "ID inválido"})
	}
	svc := service.NewProductService(db(c))
	serials, err := svc.ListProductSerials(uint(id))
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": serials})
}
// POST /api/products/:id/image — multipart/form-data, campo "image".
const maxProductImageSize = uploadlimits.MaxFileBytes

func (h *ProductHandler) UploadImageAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "ID inválido"})
	}
	ruc, err := tenantstorage.ResolveTenantRUC(c)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	svc := service.NewProductService(db(c))
	p, err := svc.GetByID(uint(id))
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "producto no encontrado"})
	}
	if msg, denied := h.productBranchDenied(c, p); denied {
		return c.Status(403).JSON(fiber.Map{"error": msg})
	}

	file, err := c.FormFile("image")
	if err != nil || file == nil {
		return c.Status(400).JSON(fiber.Map{"error": "envía un archivo en el campo 'image'"})
	}
	if file.Size > maxProductImageSize {
		return c.Status(400).JSON(fiber.Map{"error": "la imagen no debe superar 10 MB"})
	}
	ext := strings.ToLower(filepath.Ext(file.Filename))
	allowed := map[string]bool{".jpg": true, ".jpeg": true, ".png": true, ".webp": true}
	if !allowed[ext] {
		return c.Status(400).JSON(fiber.Map{"error": "formato no permitido. Usa JPG, PNG o WebP"})
	}

	dir := tenantstorage.TenantUploadDir(ruc, "products")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "no se pudo crear la carpeta de imágenes"})
	}
	filename := fmt.Sprintf("%d_%s_%d%s", p.ID, uuid.New().String()[:8], time.Now().Unix(), ext)
	savePath := filepath.Join(dir, filename)
	if err := c.SaveFile(file, savePath); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "error guardando la imagen"})
	}
	imageURL := tenantstorage.TenantUploadPublicURL(ruc, "products", filename)
	if err := db(c).Model(&database.TenantProduct{}).Where("id = ?", p.ID).Update("image_url", imageURL).Error; err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "error actualizando el producto"})
	}
	return c.JSON(fiber.Map{"image_url": imageURL})
}

// CategoryListAPI devuelve todas las categorías activas.
func (h *ProductHandler) CategoryListAPI(c fiber.Ctx) error {
	cats, err := service.NewProductService(db(c)).ListCategories()
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": cats})
}

// CategoryCreateAPI crea una categoría inline desde el formulario de producto.
func (h *ProductHandler) CategoryCreateAPI(c fiber.Ctx) error {
	var body struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "datos inválidos"})
	}
	cat, err := service.NewProductService(db(c)).CreateCategory(body.Name, body.Description)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	return c.Status(201).JSON(fiber.Map{"data": cat})
}

// ModifierGroupsAPI devuelve todos los grupos con opciones.
func (h *ProductHandler) ModifierGroupsAPI(c fiber.Ctx) error {
	groups, err := service.NewProductService(db(c)).ListModifierGroups()
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": groups})
}

// ModifierGroupCreateAPI crea un grupo de modificadores inline.
func (h *ProductHandler) ModifierGroupCreateAPI(c fiber.Ctx) error {
	var body modifierGroupBody
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "datos inválidos"})
	}
	opts, err := body.parsedOptions()
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	g, err := service.NewProductService(db(c)).CreateModifierGroup(body.Name, body.Kind, body.Required, body.MultiSelect, opts)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true, "group": g})
}

// ModifierGroupUpdateAPI actualiza grupo y opciones (incluye extra_price por opción).
func (h *ProductHandler) ModifierGroupUpdateAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "ID inválido"})
	}
	var body modifierGroupBody
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "datos inválidos"})
	}
	opts, err := body.parsedOptions()
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	g, err := service.NewProductService(db(c)).UpdateModifierGroup(uint(id), body.Name, body.Kind, body.Required, body.MultiSelect, opts)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true, "group": g})
}

// ModifierGroupDeleteAPI elimina un grupo de modificadores.
func (h *ProductHandler) ModifierGroupDeleteAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "ID inválido"})
	}
	if err := service.NewProductService(db(c)).DeleteModifierGroup(uint(id)); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true})
}

func (h *ProductHandler) productBranchDenied(c fiber.Ctx, p *database.TenantProduct) (string, bool) {
	if p == nil {
		return "", false
	}
	bid := branch.ResolveReadBranchFilter(c, 0)
	if err := service.NewProductService(db(c)).EnsureRestaurantBranchAccess(p, bid); err != nil {
		return err.Error(), true
	}
	return "", false
}

func buildProductInput(c fiber.Ctx, taxCfg tax.Config) service.ProductInput {
	salePrice, _ := strconv.ParseFloat(c.FormValue("sale_price"), 64)
	purchasePrice, _ := strconv.ParseFloat(c.FormValue("purchase_price"), 64)
	minStock, _ := strconv.ParseFloat(c.FormValue("min_stock"), 64)

	// El % IGV viene de la configuración de la empresa, no del formulario del producto
	igvTypeForRate := c.FormValue("igv_affectation_type")
	if igvTypeForRate == "" {
		igvTypeForRate = "10"
	}
	taxRate := taxCfg.EffectiveRate(igvTypeForRate)

	var catID *uint
	if cid, err := strconv.ParseUint(c.FormValue("category_id"), 10, 32); err == nil && cid > 0 {
		v := uint(cid)
		catID = &v
	}

	// Leer IDs de grupos de modificadores (multi-value: modifier_group_ids[])
	var modGroupIDs []uint
	for _, raw := range strings.Split(c.FormValue("modifier_group_ids"), ",") {
		raw = strings.TrimSpace(raw)
		if id, err := strconv.ParseUint(raw, 10, 32); err == nil && id > 0 {
			modGroupIDs = append(modGroupIDs, uint(id))
		}
	}

	igvType := c.FormValue("igv_affectation_type")
	if igvType == "" {
		igvType = "10"
	}

	return service.ProductInput{
		CategoryID: catID,
		Code:       c.FormValue("code"),
		Name:       c.FormValue("name"),
		Description:        c.FormValue("description"),
		Type:               c.FormValue("type"),
		Unit:               c.FormValue("unit"),
		SalePrice:          salePrice,
		PurchasePrice:      purchasePrice,
		TaxRate:            taxRate,
		IgvAffectationType: igvType,
		PriceIncludesIgv:   c.FormValue("price_includes_igv") == "1",
		ManageStock:        c.FormValue("manage_stock") == "1",
		ManageSeries:       c.FormValue("manage_series") == "1",
		HasModifiers:       c.FormValue("has_modifiers") == "1",
		MinStock:           minStock,
		ImageURL:           c.FormValue("image_url"),
		Active:             c.FormValue("active") == "1",
		ModifierGroupIDs:   &modGroupIDs,
	}
}
