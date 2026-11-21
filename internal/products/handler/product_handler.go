package handler

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	invsvc "tukifac/internal/inventory/service"
	"tukifac/internal/products/service"
	"tukifac/pkg/branch"
	"tukifac/pkg/categorybranch"
	"tukifac/pkg/database"
	"tukifac/pkg/imageprocess"
	"tukifac/pkg/middleware"
	"tukifac/pkg/productbranch"
	"tukifac/pkg/saas"
	"tukifac/pkg/tax"
	"tukifac/pkg/tenantstorage"
	"tukifac/pkg/uploadlimits"

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

	if _, _, err := svc.Create(input); err != nil {
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

	if _, err := svc.Update(uint(id), input); err != nil {
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
		CategoryID           *uint   `json:"category_id"`
		BrandID              *uint   `json:"brand_id"`
		Code                 string  `json:"code"`
		Name                 string  `json:"name"`
		Description          string  `json:"description"`
		Type                 string  `json:"type"`
		Unit                 string  `json:"unit"`
		UnitID               *uint   `json:"unit_id"`
		SalePrice            float64 `json:"sale_price"`
		PurchasePrice        float64 `json:"purchase_price"`
		IgvAffectationType   string  `json:"igv_affectation_type"`
		PriceIncludesIgv     bool    `json:"price_includes_igv"`
		ManageStock          bool    `json:"manage_stock"`
		ManageSeries         bool    `json:"manage_series"`
		HasVariants          bool    `json:"has_variants"`
		HasModifiers         bool    `json:"has_modifiers"`
		MinStock             float64 `json:"min_stock"`
		HasExpiryDate        bool    `json:"has_expiry_date"`
		ExpiryDate           string  `json:"expiry_date"`
		IsRestaurant         bool    `json:"is_restaurant"`
		ShowInDigitalCatalog bool    `json:"show_in_digital_catalog"`
		PreparationAreaID    *uint   `json:"preparation_area_id"`
		PreparationArea      string  `json:"preparation_area"`
		ImageURL             string  `json:"image_url"`
		ModifierGroupIDs     []uint  `json:"modifier_group_ids"`
		Presentations        []struct {
			Name         string  `json:"name"`
			SalePrice    float64 `json:"sale_price"`
			InitialStock float64 `json:"initial_stock"`
		} `json:"presentations"`
		ComboGroups  []comboGroupBody `json:"combo_groups"`
		InitialStock float64          `json:"initial_stock"`
		// BranchIDs: sucursales donde se vende. Vacío = todas (ver pkg/productbranch).
		BranchIDs []uint `json:"branch_ids"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "datos inválidos"})
	}
	if body.Name == "" {
		return c.Status(400).JSON(fiber.Map{"error": "El nombre del producto es requerido"})
	}
	if err := productbranch.ValidateBranchIDs(db(c), body.BranchIDs); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	if err := requireAccessibleBranchIDs(c, body.BranchIDs); err != nil {
		return c.Status(403).JSON(fiber.Map{"error": err.Error(), "code": branch.CodeBranchForbidden})
	}
	// Una categoría de sucursal solo admite productos que se vendan únicamente allí (pkg/categorybranch).
	// Un plato de la carta de Tukichef es de UNA sucursal (la activa) aunque no traiga «Disponible en».
	createBranchSet := body.BranchIDs
	if len(createBranchSet) == 0 && body.IsRestaurant {
		if activeID, aerr := branch.ResolveWriteBranchID(c, 0); aerr == nil && activeID > 0 {
			createBranchSet = []uint{activeID}
		}
	}
	if err := categorybranch.CheckProductCompatible(db(c), body.CategoryID, createBranchSet); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	// Cuota de productos del plan (los servicios no cuentan; ver CheckCreateQuota).
	if body.Type != "service" && middleware.EnforceCreateQuota(c, db(c), saas.QuotaProducts) {
		return nil
	}
	if body.InitialStock < 0 {
		return c.Status(400).JSON(fiber.Map{"error": "initial_stock no puede ser negativo"})
	}
	if body.InitialStock > 0 && !body.ManageStock {
		return c.Status(400).JSON(fiber.Map{"error": service.InitialStockRequiresManageStock})
	}
	expiryDate, err := service.ParseProductExpiryDate(body.ExpiryDate)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	manageStock := body.ManageStock
	taxCfg := tax.LoadFromDB(db(c))
	igvType := body.IgvAffectationType
	if igvType == "" {
		igvType = "10"
	}
	input := service.ProductInput{
		CategoryID:           body.CategoryID,
		BrandID:              body.BrandID,
		Code:                 body.Code,
		Name:                 body.Name,
		Description:          body.Description,
		Type:                 body.Type,
		Unit:                 body.Unit,
		UnitID:               body.UnitID,
		SalePrice:            body.SalePrice,
		PurchasePrice:        createCostFromBody(c, body.PurchasePrice),
		TaxRate:              taxCfg.EffectiveRate(igvType),
		IgvAffectationType:   igvType,
		PriceIncludesIgv:     body.PriceIncludesIgv,
		ManageStock:          manageStock,
		ManageSeries:         body.ManageSeries,
		HasVariants:          body.HasVariants,
		HasModifiers:         body.HasModifiers,
		MinStock:             body.MinStock,
		HasExpiryDate:        body.HasExpiryDate,
		ExpiryDate:           expiryDate,
		IsRestaurant:         body.IsRestaurant,
		ShowInDigitalCatalog: body.ShowInDigitalCatalog,
		PreparationAreaID:    body.PreparationAreaID,
		PreparationArea:      body.PreparationArea,
		ImageURL:             body.ImageURL,
		Active:               true,
		ModifierGroupIDs:     &body.ModifierGroupIDs,
	}
	if len(body.Presentations) > 0 {
		pres := make([]service.ProductPresentationInput, 0, len(body.Presentations))
		for _, row := range body.Presentations {
			if strings.TrimSpace(row.Name) == "" {
				continue
			}
			if row.InitialStock < 0 {
				return c.Status(400).JSON(fiber.Map{"error": "el stock inicial de una presentación no puede ser negativo"})
			}
			if row.InitialStock > 0 && !body.ManageStock {
				return c.Status(400).JSON(fiber.Map{"error": service.InitialStockRequiresManageStock})
			}
			pres = append(pres, service.ProductPresentationInput{
				Name:         strings.TrimSpace(row.Name),
				SalePrice:    row.SalePrice,
				InitialStock: row.InitialStock,
			})
		}
		if len(pres) > 0 {
			input.Presentations = &pres
			input.HasVariants = true
		}
	}
	if len(body.ComboGroups) > 0 {
		groups := toComboGroupInputs(body.ComboGroups)
		input.ComboGroups = &groups
	}
	branchID, berr := branch.ResolveWriteBranchID(c, 0)
	if body.IsRestaurant {
		if berr != nil {
			return c.Status(403).JSON(fiber.Map{"error": berr.Error(), "code": branch.CodeBranchRequired})
		}
		input.BranchID = branchID
	}
	p, presResults, err := service.NewProductService(db(c)).Create(input)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	if len(body.BranchIDs) > 0 {
		if err := productbranch.Set(db(c), p.ID, body.BranchIDs); err != nil {
			_ = service.NewProductService(db(c)).Delete(p.ID)
			return c.Status(400).JSON(fiber.Map{"error": err.Error()})
		}
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
	} else if p.ManageStock && berr == nil && branchID > 0 {
		if err := invsvc.NewInventoryService(db(c)).EnsureProductBranchLink(p.ID, branchID); err != nil {
			_ = service.NewProductService(db(c)).Delete(p.ID)
			return c.Status(400).JSON(fiber.Map{"error": err.Error()})
		}
	}
	// Stock inicial por presentación (ej. Rojo: 5, Azul: 3, Amarillo: 2): solo tiene sentido con
	// una sucursal resuelta; si no hay una (berr != nil), las presentaciones quedan en 0 y el
	// tenant las ajusta después con "Ajustar stock".
	if p.ManageStock && berr == nil && branchID > 0 {
		inv := invsvc.NewInventoryService(db(c))
		uid, _ := c.Locals("user_id").(uint)
		for _, res := range presResults {
			if !res.IsNew || res.InitialStock <= 0 {
				continue
			}
			if err := inv.RecordInitialPresentationStock(
				p.ID, res.Presentation.ID, branchID, res.InitialStock, uid, "Stock inicial — alta de producto",
			); err != nil {
				return c.Status(400).JSON(fiber.Map{"error": fmt.Sprintf("presentación '%s': %s", res.Presentation.Name, err.Error())})
			}
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
		CategoryID           *uint   `json:"category_id"`
		BrandID              *uint   `json:"brand_id"`
		Code                 string  `json:"code"`
		Name                 string  `json:"name"`
		Description          string  `json:"description"`
		Type                 string  `json:"type"`
		Unit                 string  `json:"unit"`
		UnitID               *uint   `json:"unit_id"`
		SalePrice            float64 `json:"sale_price"`
		PurchasePrice        float64 `json:"purchase_price"`
		IgvAffectationType   string  `json:"igv_affectation_type"`
		PriceIncludesIgv     bool    `json:"price_includes_igv"`
		ManageStock          bool    `json:"manage_stock"`
		ManageSeries         bool    `json:"manage_series"`
		HasVariants          bool    `json:"has_variants"`
		HasModifiers         bool    `json:"has_modifiers"`
		MinStock             float64 `json:"min_stock"`
		HasExpiryDate        bool    `json:"has_expiry_date"`
		ExpiryDate           string  `json:"expiry_date"`
		IsRestaurant         bool    `json:"is_restaurant"`
		ShowInDigitalCatalog bool    `json:"show_in_digital_catalog"`
		PreparationAreaID    *uint   `json:"preparation_area_id"`
		PreparationArea      string  `json:"preparation_area"`
		// nil = conservar la imagen actual; "" = quitarla.
		ImageURL         *string `json:"image_url"`
		Active           *bool   `json:"active"`
		ModifierGroupIDs *[]uint `json:"modifier_group_ids"`
		Presentations    *[]struct {
			ID        uint    `json:"id"`
			Name      string  `json:"name"`
			SalePrice float64 `json:"sale_price"`
			// InitialStock: solo aplica a filas nuevas (sin ID) agregadas durante la edición;
			// las existentes ya tienen su stock y se corrigen con "Ajustar stock".
			InitialStock float64 `json:"initial_stock"`
		} `json:"presentations"`
		// nil = no tocar el combo; [] = deja de ser combo.
		ComboGroups *[]comboGroupBody `json:"combo_groups"`
		// nil = no tocar las sucursales; [] = disponible en todas (ver pkg/productbranch).
		BranchIDs *[]uint `json:"branch_ids"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "datos inválidos"})
	}
	if body.BranchIDs != nil {
		if err := productbranch.ValidateBranchIDs(db(c), *body.BranchIDs); err != nil {
			return c.Status(400).JSON(fiber.Map{"error": err.Error()})
		}
	}
	svc := service.NewProductService(db(c))
	existing, err := svc.GetByID(uint(id))
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "producto no encontrado"})
	}
	if resp, denied := h.productDenied(c, existing); denied {
		return resp
	}
	// Sucursales del producto: solo las accesibles; las que ya tenía en sucursales ajenas se conservan.
	var scopedBranchIDs []uint
	if body.BranchIDs != nil {
		var serr error
		scopedBranchIDs, serr = scopedBranchSet(c, db(c), uint(id), *body.BranchIDs)
		if serr != nil {
			return c.Status(403).JSON(fiber.Map{"error": serr.Error(), "code": branch.CodeBranchForbidden})
		}
	}
	// Sucursales finales del producto (las nuevas si las manda; si no, las que ya tiene) frente a la categoría de sucursal.
	finalBranchIDs := scopedBranchIDs
	if body.BranchIDs == nil {
		// Las asignadas o, sin ninguna, su sucursal dueña (platos de la carta de Tukichef).
		finalBranchIDs, _ = categorybranch.EffectiveBranches(db(c), uint(id))
	}
	if err := categorybranch.CheckProductCompatible(db(c), body.CategoryID, finalBranchIDs); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	expiryDate, err := service.ParseProductExpiryDate(body.ExpiryDate)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	taxCfg := tax.LoadFromDB(db(c))
	igvType := body.IgvAffectationType
	if igvType == "" {
		igvType = "10"
	}
	input := service.ProductInput{
		CategoryID:           body.CategoryID,
		BrandID:              body.BrandID,
		Code:                 body.Code,
		Name:                 body.Name,
		Description:          body.Description,
		Type:                 body.Type,
		Unit:                 body.Unit,
		UnitID:               body.UnitID,
		SalePrice:            body.SalePrice,
		PurchasePrice:        updateCostFromBody(c, body.PurchasePrice, existing.PurchasePrice),
		TaxRate:              taxCfg.EffectiveRate(igvType),
		IgvAffectationType:   igvType,
		PriceIncludesIgv:     body.PriceIncludesIgv,
		ManageStock:          body.ManageStock,
		ManageSeries:         body.ManageSeries,
		HasVariants:          body.HasVariants,
		HasModifiers:         body.HasModifiers,
		MinStock:             body.MinStock,
		HasExpiryDate:        body.HasExpiryDate,
		ExpiryDate:           expiryDate,
		IsRestaurant:         body.IsRestaurant,
		ShowInDigitalCatalog: body.ShowInDigitalCatalog,
		PreparationAreaID:    body.PreparationAreaID,
		PreparationArea:      body.PreparationArea,
		ModifierGroupIDs:     body.ModifierGroupIDs,
	}
	if body.ImageURL != nil {
		input.ImageURL = *body.ImageURL
		input.ImageURLSet = true
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
			if row.InitialStock < 0 {
				return c.Status(400).JSON(fiber.Map{"error": "el stock inicial de una presentación no puede ser negativo"})
			}
			if row.InitialStock > 0 && !body.ManageStock {
				return c.Status(400).JSON(fiber.Map{"error": service.InitialStockRequiresManageStock})
			}
			p := service.ProductPresentationInput{
				Name:         strings.TrimSpace(row.Name),
				SalePrice:    row.SalePrice,
				InitialStock: row.InitialStock,
			}
			if row.ID > 0 {
				rid := row.ID
				p.ID = &rid
			}
			pres = append(pres, p)
		}
		input.Presentations = &pres
		if len(pres) > 0 {
			input.HasVariants = true
		}
	}
	if body.ComboGroups != nil {
		groups := toComboGroupInputs(*body.ComboGroups)
		input.ComboGroups = &groups
	}
	presResults, err := svc.Update(uint(id), input)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	if body.BranchIDs != nil {
		if err := productbranch.Set(db(c), uint(id), scopedBranchIDs); err != nil {
			return c.Status(400).JSON(fiber.Map{"error": err.Error()})
		}
	}
	branchIDForStock, branchErr := branch.ResolveWriteBranchID(c, 0)
	if body.IsRestaurant || existing.IsRestaurant {
		if branchErr != nil {
			return c.Status(403).JSON(fiber.Map{"error": branchErr.Error(), "code": branch.CodeBranchForbidden})
		}
		if branchIDForStock > 0 {
			if err := invsvc.NewInventoryService(db(c)).EnsureProductBranchLink(uint(id), branchIDForStock); err != nil {
				return c.Status(400).JSON(fiber.Map{"error": err.Error()})
			}
		}
	} else if body.ManageStock && branchErr == nil && branchIDForStock > 0 {
		// Comercio general (no restaurante): al activar control de stock en un producto YA
		// EXISTENTE, sin esto nunca se crea su fila en tenant_product_stocks — a diferencia
		// del alta (CreateAPI ya tiene este mismo fallback más abajo), que si el stock inicial
		// es 0 igual llama EnsureProductBranchLink. La query de listado filtrado por sucursal
		// exige "manage_stock = false OR EXISTS fila de stock para esa sucursal (u otra vía
		// presentaciones)" (ver applyProductListFilters) — sin la fila, el producto queda
		// invisible en cualquier listado por sucursal aunque siga activo y sin borrar. Bug real:
		// tenant industrialrafaz (RUC 20614234165), 07-sep-2026, 6 productos afectados.
		if err := invsvc.NewInventoryService(db(c)).EnsureProductBranchLink(uint(id), branchIDForStock); err != nil {
			return c.Status(400).JSON(fiber.Map{"error": err.Error()})
		}
	}
	// Stock inicial para presentaciones nuevas agregadas en esta edición (ej. se agrega un color
	// "Verde" a un producto ya existente): solo tiene sentido con una sucursal resuelta; si no hay
	// una, la presentación queda en 0 y el tenant la ajusta después con "Ajustar stock".
	if body.ManageStock && len(presResults) > 0 {
		branchID, berr := branch.ResolveWriteBranchID(c, 0)
		if berr == nil && branchID > 0 {
			inv := invsvc.NewInventoryService(db(c))
			uid, _ := c.Locals("user_id").(uint)
			for _, res := range presResults {
				if !res.IsNew || res.InitialStock <= 0 {
					continue
				}
				if err := inv.RecordInitialPresentationStock(
					uint(id), res.Presentation.ID, branchID, res.InitialStock, uid, "Stock inicial — alta de producto",
				); err != nil {
					return c.Status(400).JSON(fiber.Map{"error": fmt.Sprintf("presentación '%s': %s", res.Presentation.Name, err.Error())})
				}
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
	if resp, denied := h.productDenied(c, p); denied {
		return resp
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
	if resp, denied := h.productDenied(c, p); denied {
		return resp
	}
	if err := svc.Delete(uint(id)); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true})
}

func (h *ProductHandler) SearchAPI(c fiber.Ctx) error {
	svc := service.NewProductService(db(c))
	catID, _ := strconv.ParseUint(c.Query("category_id"), 10, 32)
	brandID, _ := strconv.ParseUint(c.Query("brand_id"), 10, 32)
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
		Query:             c.Query("q"),
		CategoryID:        uint(catID),
		BrandID:           uint(brandID),
		Type:              c.Query("type"),
		ActiveOnly:        !inactiveOnly && (activeOnly == "true" || activeOnly == "1"),
		InactiveOnly:      inactiveOnly,
		ManageStockOnly:   c.Query("manage_stock_only") == "true" || c.Query("manage_stock_only") == "1",
		NoManageStockOnly: c.Query("no_manage_stock_only") == "true" || c.Query("no_manage_stock_only") == "1",
		RestaurantOnly:    c.Query("restaurant_only") == "true" || c.Query("restaurant_only") == "1",
		CombosOnly:        c.Query("combos_only") == "true" || c.Query("combos_only") == "1",
		ExcludeCombos:     c.Query("exclude_combos") == "true" || c.Query("exclude_combos") == "1",
		PreparationArea:   c.Query("preparation_area"),
	}
	if prepAreaID, err := strconv.ParseUint(c.Query("preparation_area_id"), 10, 32); err == nil && prepAreaID > 0 {
		params.PreparationAreaID = uint(prepAreaID)
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
		// Sin branch_id el listado ya no devuelve el catálogo (ni stock_by_branch) de todas las sucursales a quien
		// no las ve todas: se acota a la sucursal activa. Quien ve todo (administrador) conserva la vista completa
		// salvo en los listados de stock/carta, que siempre son de la sucursal activa.
		if msOnly || params.RestaurantOnly || !branch.IsBranchAdmin(c) {
			params.BranchID = branch.ActiveBranchID(c)
		}
	} else if !branch.IsBranchAdmin(c) {
		params.BranchID = branch.NoBranchFilter // sin sucursal resuelta: falla cerrado
	}
	if perPage > 0 {
		params.Limit = perPage
		params.Offset = (page - 1) * perPage
	}
	params.SortBy = strings.TrimSpace(c.Query("sort_by"))
	params.SortDir = strings.TrimSpace(c.Query("sort_dir"))
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
	p, err := svc.FindProductByBarcode(code, branchID)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	if p == nil {
		return c.Status(404).JSON(fiber.Map{"error": "producto no encontrado"})
	}
	if resp, denied := h.productDenied(c, p); denied {
		return resp
	}
	// Con el precio de la sucursal activa: es una consulta de venta (escáner), no la edición del producto.
	return c.JSON(fiber.Map{"data": svc.ProductListItemForBranch(*p, branchID)})
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
	if resp, denied := h.productDenied(c, p); denied {
		return resp
	}
	modIds := svc.GetProductModifierGroupIDs(p.ID)
	presentations, _ := svc.ListProductPresentations(p.ID)
	comboGroups, _ := svc.ListComboGroups(p.ID)
	branchIDs, _ := productbranch.BranchIDs(db(c), []uint{p.ID})
	assigned := branchIDs[p.ID]
	if assigned == nil {
		assigned = []uint{}
	}
	resp := fiber.Map{
		"data":               p,
		"modifier_group_ids": modIds,
		"presentations":      presentations,
		"combo_groups":       comboGroups,
		// [] = disponible en todas las sucursales.
		"branch_ids": assigned,
	}
	if p.HasCombo {
		// Referencia para mostrar el ahorro frente al precio fijo del combo.
		if total, err := svc.ComboComponentsTotal(p.ID); err == nil {
			resp["combo_components_total"] = total
		}
	}
	return c.JSON(resp)
}

// ProductSerialsAPI devuelve los números de serie del producto (todas las sucursales) para el detalle.
// GET /api/products/:id/serials
func (h *ProductHandler) ProductSerialsAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "ID inválido"})
	}
	svc := service.NewProductService(db(c))
	if p, perr := svc.GetByID(uint(id)); perr != nil {
		return c.Status(404).JSON(fiber.Map{"error": "producto no encontrado"})
	} else if resp, denied := h.productDenied(c, p); denied {
		return resp
	}
	serials, err := svc.ListProductSerials(uint(id))
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	// Quien no ve todo recibe solo los series de sus sucursales accesibles.
	if !branch.IsBranchAdmin(c) {
		filtered := serials[:0]
		for _, s := range serials {
			if branch.CanAccessBranch(c, s.BranchID) {
				filtered = append(filtered, s)
			}
		}
		serials = filtered
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
	if resp, denied := h.productDenied(c, p); denied {
		return resp
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

	opened, err := file.Open()
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "no se pudo leer la imagen"})
	}
	data, err := io.ReadAll(opened)
	_ = opened.Close()
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "no se pudo leer la imagen"})
	}

	// Reduce peso/dimensiones antes de guardar — una foto de celular en alta resolución no debe
	// quedar tal cual en disco, ni servirse completa a vistas que la muestran en una tarjeta de
	// 40-60px (POS, catálogo, selector de "Nuevo comprobante"). Si no se puede optimizar (webp,
	// formato inválido, o el resultado no queda más liviano), se guarda el original sin tocar —
	// nunca bloquea la subida.
	saveExt := ext
	if optimized, ok := imageprocess.Optimize(data, ext); ok {
		data = optimized.Data
		saveExt = optimized.Extension
	}

	dir := tenantstorage.TenantUploadDir(ruc, "products")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "no se pudo crear la carpeta de imágenes"})
	}
	filename := fmt.Sprintf("%d_%s_%d%s", p.ID, uuid.New().String()[:8], time.Now().Unix(), saveExt)
	savePath := filepath.Join(dir, filename)
	if err := os.WriteFile(savePath, data, 0644); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "error guardando la imagen"})
	}
	imageURL := tenantstorage.TenantUploadPublicURL(ruc, "products", filename)
	previousImageURL := p.ImageURL
	if err := db(c).Model(&database.TenantProduct{}).Where("id = ?", p.ID).Update("image_url", imageURL).Error; err != nil {
		return c.Status(500).JSON(fiber.Map{"error": "error actualizando el producto"})
	}
	// Best-effort: la imagen anterior queda huérfana en disco si esto falla, pero nunca debe
	// romper la subida de la nueva (mismo criterio que contact_handler.go al reemplazar foto).
	_ = tenantstorage.DeleteUploadByPublicURL(previousImageURL)
	return c.JSON(fiber.Map{"image_url": imageURL})
}

// categoryListScope acota el listado de categorías por sucursal (pkg/categorybranch). Con branch_id explícito, o para quien no
// ve todas las sucursales, solo las de todas y las de esa sucursal; el administrador sin branch_id ve todas (vista de
// administración, con la columna «Sucursal»). Mismo criterio que el listado de productos.
func categoryListScope(c fiber.Ctx) func(*gorm.DB) *gorm.DB {
	reqB, _ := strconv.ParseUint(c.Query("branch_id"), 10, 32)
	if reqB == 0 && branch.IsBranchAdmin(c) {
		return nil
	}
	return categorybranch.VisibleIn(db(c), branch.ResolveReadBranchFilter(c, uint(reqB)))
}

// CategoryListAPI devuelve categorías activas (POS y selects).
func (h *ProductHandler) CategoryListAPI(c fiber.Ctx) error {
	svc := service.NewProductService(db(c))
	scope := categoryListScope(c)
	if c.Query("with_counts") == "true" || c.Query("with_counts") == "1" {
		items, err := svc.ListCategoriesWithCountsScoped(scope)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"error": err.Error()})
		}
		return c.JSON(fiber.Map{"data": items})
	}
	cats, err := svc.ListCategoriesScoped(scope)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": cats})
}

// categoryScopeFromBody interpreta branch_id del cuerpo: ausente (set=false), null/0 = todas las sucursales, N = esa sucursal.
func categoryScopeFromBody(raw json.RawMessage) (id *uint, set bool, err error) {
	if len(raw) == 0 {
		return nil, false, nil
	}
	s := strings.TrimSpace(string(raw))
	if s == "null" || s == "0" || s == `""` {
		return nil, true, nil
	}
	n, perr := strconv.ParseUint(strings.Trim(s, `"`), 10, 32)
	if perr != nil || n == 0 {
		return nil, true, errors.New("sucursal inválida")
	}
	v := uint(n)
	return &v, true, nil
}

// requireCategoryBranchAccess: quien no puede administrar el catálogo global solo crea/edita/elimina categorías de SU sucursal
// activa (nunca «todas» ni las de otra). El administrador del catálogo no tiene esa restricción.
func requireCategoryBranchAccess(c fiber.Ctx, categoryBranch *uint) error {
	if middleware.CanManageGlobalCatalog(c) {
		return nil
	}
	active := branch.ActiveBranchID(c)
	if categoryBranch == nil || active == 0 || *categoryBranch != active {
		return errors.New("solo puede administrar categorías de su sucursal; las de todas las sucursales las gestiona quien administra el catálogo global")
	}
	return nil
}

// CategoryCreateAPI crea una categoría inline desde el formulario de producto o panel.
func (h *ProductHandler) CategoryCreateAPI(c fiber.Ctx) error {
	var body struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		SortOrder   *int            `json:"sort_order"`
		BranchID    json.RawMessage `json:"branch_id"` // ausente/null = todas las sucursales
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "datos inválidos"})
	}
	branchID, _, err := categoryScopeFromBody(body.BranchID)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	if err := requireCategoryBranchAccess(c, branchID); err != nil {
		return c.Status(403).JSON(fiber.Map{"error": err.Error(), "code": branch.CodeBranchForbidden})
	}
	if branchID != nil && !branch.CanAccessBranch(c, *branchID) {
		return c.Status(403).JSON(fiber.Map{"error": "no tiene acceso a esa sucursal", "code": branch.CodeBranchForbidden})
	}
	cat, err := service.NewProductService(db(c)).CreateCategoryInScope(body.Name, body.Description, body.SortOrder, branchID)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	return c.Status(201).JSON(fiber.Map{"data": cat})
}

// CategoryUpdateAPI actualiza nombre, descripción, orden y (si viene branch_id) la sucursal.
func (h *ProductHandler) CategoryUpdateAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "ID inválido"})
	}
	var body struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		SortOrder   int             `json:"sort_order"`
		BranchID    json.RawMessage `json:"branch_id"` // ausente = no cambiar; null = todas; N = esa sucursal
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "datos inválidos"})
	}
	branchID, setBranch, err := categoryScopeFromBody(body.BranchID)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	svc := service.NewProductService(db(c))
	current, gerr := svc.GetCategory(uint(id))
	if gerr != nil {
		return c.Status(404).JSON(fiber.Map{"error": "categoría no encontrada"})
	}
	// Debe poder administrar la categoría actual y, si la cambia de ámbito, también el destino.
	if err := requireCategoryBranchAccess(c, current.BranchID); err != nil {
		return c.Status(403).JSON(fiber.Map{"error": err.Error(), "code": branch.CodeBranchForbidden})
	}
	if setBranch {
		if err := requireCategoryBranchAccess(c, branchID); err != nil {
			return c.Status(403).JSON(fiber.Map{"error": err.Error(), "code": branch.CodeBranchForbidden})
		}
	}
	cat, err := svc.UpdateCategoryInScope(uint(id), body.Name, body.Description, body.SortOrder, setBranch, branchID)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": cat})
}

// CategoryDeleteAPI elimina categoría si no tiene productos vinculados.
func (h *ProductHandler) CategoryDeleteAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "ID inválido"})
	}
	if cur, gerr := service.NewProductService(db(c)).GetCategory(uint(id)); gerr == nil {
		if err := requireCategoryBranchAccess(c, cur.BranchID); err != nil {
			return c.Status(403).JSON(fiber.Map{"error": err.Error(), "code": branch.CodeBranchForbidden})
		}
	}
	if err := service.NewProductService(db(c)).DeleteCategory(uint(id)); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true})
}

// BrandListAPI devuelve marcas activas (selects de producto).
func (h *ProductHandler) BrandListAPI(c fiber.Ctx) error {
	svc := service.NewProductService(db(c))
	if c.Query("with_counts") == "true" || c.Query("with_counts") == "1" {
		items, err := svc.ListBrandsWithCounts()
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"error": err.Error()})
		}
		return c.JSON(fiber.Map{"data": items})
	}
	brands, err := svc.ListBrands()
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": brands})
}

// BrandCreateAPI crea una marca inline desde el formulario de producto o panel.
func (h *ProductHandler) BrandCreateAPI(c fiber.Ctx) error {
	var body struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		SortOrder   *int   `json:"sort_order"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "datos inválidos"})
	}
	b, err := service.NewProductService(db(c)).CreateBrand(body.Name, body.Description, body.SortOrder)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	return c.Status(201).JSON(fiber.Map{"data": b})
}

// BrandUpdateAPI actualiza nombre, descripción y orden.
func (h *ProductHandler) BrandUpdateAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "ID inválido"})
	}
	var body struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		SortOrder   int    `json:"sort_order"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "datos inválidos"})
	}
	b, err := service.NewProductService(db(c)).UpdateBrand(uint(id), body.Name, body.Description, body.SortOrder)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": b})
}

// BrandDeleteAPI elimina marca si no tiene productos vinculados.
func (h *ProductHandler) BrandDeleteAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "ID inválido"})
	}
	if err := service.NewProductService(db(c)).DeleteBrand(uint(id)); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true})
}

// ── Unidades de venta (TenantProductSaleUnit) ───────────────────────────────────────────────────
//
// Fase 1: solo administración de la entidad, todavía no conectada a ventas/compras/Kardex.

type saleUnitRequestBody struct {
	Name             string   `json:"name"`
	UnitID           *uint    `json:"unit_id"`
	ConversionFactor float64  `json:"conversion_factor"`
	IsBase           bool     `json:"is_base"`
	AllowFraction    bool     `json:"allow_fraction"`
	Price1           float64  `json:"price1"`
	Price2           *float64 `json:"price2"`
	Price3           *float64 `json:"price3"`
	SortOrder        int      `json:"sort_order"`
	Active           bool     `json:"active"`
}

func (b saleUnitRequestBody) toInput() service.SaleUnitInput {
	return service.SaleUnitInput{
		Name:             b.Name,
		UnitID:           b.UnitID,
		ConversionFactor: b.ConversionFactor,
		IsBase:           b.IsBase,
		AllowFraction:    b.AllowFraction,
		Price1:           b.Price1,
		Price2:           b.Price2,
		Price3:           b.Price3,
		SortOrder:        b.SortOrder,
		Active:           b.Active,
	}
}

// SaleUnitListAPI lista las unidades de venta de un producto (activas por defecto; ?all=1 trae
// también inactivas, para la pantalla de administración).
func (h *ProductHandler) SaleUnitListAPI(c fiber.Ctx) error {
	productID, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "producto inválido"})
	}
	svc := service.NewProductService(db(c))
	if c.Query("all") == "true" || c.Query("all") == "1" {
		units, err := svc.ListAllSaleUnits(uint(productID))
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"error": err.Error()})
		}
		return c.JSON(fiber.Map{"data": units})
	}
	units, err := svc.ListSaleUnits(uint(productID))
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": units})
}

// SaleUnitGetAPI obtiene una unidad de venta puntual del producto.
func (h *ProductHandler) SaleUnitGetAPI(c fiber.Ctx) error {
	productID, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "producto inválido"})
	}
	suID, err := strconv.ParseUint(c.Params("suid"), 10, 32)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "ID inválido"})
	}
	u, err := service.NewProductService(db(c)).GetSaleUnit(uint(productID), uint(suID))
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": u})
}

// SaleUnitCreateAPI crea una unidad de venta para el producto indicado.
func (h *ProductHandler) SaleUnitCreateAPI(c fiber.Ctx) error {
	productID, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "producto inválido"})
	}
	var body saleUnitRequestBody
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "datos inválidos"})
	}
	u, err := service.NewProductService(db(c)).CreateSaleUnit(uint(productID), body.toInput())
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	return c.Status(201).JSON(fiber.Map{"data": u})
}

// SaleUnitUpdateAPI actualiza una unidad de venta existente del producto.
func (h *ProductHandler) SaleUnitUpdateAPI(c fiber.Ctx) error {
	productID, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "producto inválido"})
	}
	suID, err := strconv.ParseUint(c.Params("suid"), 10, 32)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "ID inválido"})
	}
	var body saleUnitRequestBody
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "datos inválidos"})
	}
	u, err := service.NewProductService(db(c)).UpdateSaleUnit(uint(productID), uint(suID), body.toInput())
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": u})
}

// SaleUnitDeleteAPI elimina (soft delete) una unidad de venta del producto.
func (h *ProductHandler) SaleUnitDeleteAPI(c fiber.Ctx) error {
	productID, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "producto inválido"})
	}
	suID, err := strconv.ParseUint(c.Params("suid"), 10, 32)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "ID inválido"})
	}
	if err := service.NewProductService(db(c)).DeleteSaleUnit(uint(productID), uint(suID)); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true})
}

// ── Precios por sucursal de unidades de venta (TenantProductSaleUnitBranchPrice) ────────────────
//
// Fase 4: override de precio de una SaleUnit para una sucursal puntual.

type saleUnitBranchPriceRequestBody struct {
	Price1 float64  `json:"price1"`
	Price2 *float64 `json:"price2"`
	Price3 *float64 `json:"price3"`
	Active bool     `json:"active"`
}

func (b saleUnitBranchPriceRequestBody) toInput() service.SaleUnitBranchPriceInput {
	return service.SaleUnitBranchPriceInput{Price1: b.Price1, Price2: b.Price2, Price3: b.Price3, Active: b.Active}
}

// parseProductAndSaleUnitParams parsea :id (producto) y :suid (unidad de venta) — repetido en
// todos los handlers de precios por sucursal, que cuelgan de la misma ruta anidada.
func parseProductAndSaleUnitParams(c fiber.Ctx) (productID, saleUnitID uint, err error) {
	pid, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return 0, 0, errors.New("producto inválido")
	}
	suid, err := strconv.ParseUint(c.Params("suid"), 10, 32)
	if err != nil {
		return 0, 0, errors.New("ID de unidad de venta inválido")
	}
	return uint(pid), uint(suid), nil
}

// SaleUnitBranchPriceListAPI lista los overrides de sucursal de una unidad de venta.
func (h *ProductHandler) SaleUnitBranchPriceListAPI(c fiber.Ctx) error {
	productID, saleUnitID, err := parseProductAndSaleUnitParams(c)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	if resp, ok := h.guardProductByID(c, productID); !ok {
		return resp
	}
	rows, err := service.NewProductService(db(c)).ListSaleUnitBranchPrices(productID, saleUnitID)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	if !branch.IsBranchAdmin(c) {
		filtered := rows[:0]
		for _, r := range rows {
			if branch.CanAccessBranch(c, r.BranchID) {
				filtered = append(filtered, r)
			}
		}
		rows = filtered
	}
	return c.JSON(fiber.Map{"data": rows})
}

// SaleUnitBranchPriceGetAPI obtiene el override de una sucursal puntual.
func (h *ProductHandler) SaleUnitBranchPriceGetAPI(c fiber.Ctx) error {
	productID, saleUnitID, err := parseProductAndSaleUnitParams(c)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	branchID, err := strconv.ParseUint(c.Params("branchId"), 10, 32)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "sucursal inválida"})
	}
	if resp, ok := h.guardProductByID(c, productID); !ok {
		return resp
	}
	if resp, ok := guardBranchPrice(c, uint(branchID), false); !ok {
		return resp
	}
	row, err := service.NewProductService(db(c)).GetSaleUnitBranchPrice(productID, saleUnitID, uint(branchID))
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": row})
}

// SaleUnitBranchPriceCreateAPI crea el override de precio de una unidad de venta para una sucursal.
func (h *ProductHandler) SaleUnitBranchPriceCreateAPI(c fiber.Ctx) error {
	productID, saleUnitID, err := parseProductAndSaleUnitParams(c)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	branchID, err := strconv.ParseUint(c.Params("branchId"), 10, 32)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "sucursal inválida"})
	}
	var body saleUnitBranchPriceRequestBody
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "datos inválidos"})
	}
	if resp, ok := h.guardProductByID(c, productID); !ok {
		return resp
	}
	if resp, ok := guardBranchPrice(c, uint(branchID), true); !ok {
		return resp
	}
	row, err := service.NewProductService(db(c)).CreateSaleUnitBranchPrice(productID, saleUnitID, uint(branchID), body.toInput())
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	return c.Status(201).JSON(fiber.Map{"data": row})
}

// SaleUnitBranchPriceUpdateAPI actualiza el override de precio de una sucursal.
func (h *ProductHandler) SaleUnitBranchPriceUpdateAPI(c fiber.Ctx) error {
	productID, saleUnitID, err := parseProductAndSaleUnitParams(c)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	branchID, err := strconv.ParseUint(c.Params("branchId"), 10, 32)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "sucursal inválida"})
	}
	var body saleUnitBranchPriceRequestBody
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "datos inválidos"})
	}
	if resp, ok := h.guardProductByID(c, productID); !ok {
		return resp
	}
	if resp, ok := guardBranchPrice(c, uint(branchID), true); !ok {
		return resp
	}
	row, err := service.NewProductService(db(c)).UpdateSaleUnitBranchPrice(productID, saleUnitID, uint(branchID), body.toInput())
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": row})
}

// SaleUnitBranchPriceDeleteAPI elimina el override de precio de una sucursal.
func (h *ProductHandler) SaleUnitBranchPriceDeleteAPI(c fiber.Ctx) error {
	productID, saleUnitID, err := parseProductAndSaleUnitParams(c)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	branchID, err := strconv.ParseUint(c.Params("branchId"), 10, 32)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "sucursal inválida"})
	}
	if resp, ok := h.guardProductByID(c, productID); !ok {
		return resp
	}
	if resp, ok := guardBranchPrice(c, uint(branchID), true); !ok {
		return resp
	}
	if err := service.NewProductService(db(c)).DeleteSaleUnitBranchPrice(productID, saleUnitID, uint(branchID)); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true})
}

// ── Precio por sucursal de un producto "normal" (TenantProductBranchPrice) ──────────────────────

type productBranchPriceRequestBody struct {
	SalePrice float64 `json:"sale_price"`
	Active    bool    `json:"active"`
}

func (b productBranchPriceRequestBody) toInput() service.ProductBranchPriceInput {
	return service.ProductBranchPriceInput{SalePrice: b.SalePrice, Active: b.Active}
}

// ProductBranchPriceListAPI lista los overrides de sucursal de un producto.
func (h *ProductHandler) ProductBranchPriceListAPI(c fiber.Ctx) error {
	productID, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "producto inválido"})
	}
	if resp, ok := h.guardProductByID(c, uint(productID)); !ok {
		return resp
	}
	rows, err := service.NewProductService(db(c)).ListProductBranchPrices(uint(productID))
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	if !branch.IsBranchAdmin(c) {
		filtered := rows[:0]
		for _, r := range rows {
			if branch.CanAccessBranch(c, r.BranchID) {
				filtered = append(filtered, r)
			}
		}
		rows = filtered
	}
	return c.JSON(fiber.Map{"data": rows})
}

// ProductBranchPriceGetAPI obtiene el override de una sucursal puntual.
func (h *ProductHandler) ProductBranchPriceGetAPI(c fiber.Ctx) error {
	productID, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "producto inválido"})
	}
	branchID, err := strconv.ParseUint(c.Params("branchId"), 10, 32)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "sucursal inválida"})
	}
	if resp, ok := h.guardProductByID(c, uint(productID)); !ok {
		return resp
	}
	if resp, ok := guardBranchPrice(c, uint(branchID), false); !ok {
		return resp
	}
	row, err := service.NewProductService(db(c)).GetProductBranchPrice(uint(productID), uint(branchID))
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": row})
}

// ProductBranchPriceCreateAPI crea el override de precio de un producto para una sucursal.
func (h *ProductHandler) ProductBranchPriceCreateAPI(c fiber.Ctx) error {
	productID, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "producto inválido"})
	}
	branchID, err := strconv.ParseUint(c.Params("branchId"), 10, 32)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "sucursal inválida"})
	}
	var body productBranchPriceRequestBody
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "datos inválidos"})
	}
	if resp, ok := h.guardProductByID(c, uint(productID)); !ok {
		return resp
	}
	if resp, ok := guardBranchPrice(c, uint(branchID), true); !ok {
		return resp
	}
	row, err := service.NewProductService(db(c)).CreateProductBranchPrice(uint(productID), uint(branchID), body.toInput())
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	return c.Status(201).JSON(fiber.Map{"data": row})
}

// ProductBranchPriceUpdateAPI actualiza el override de precio de una sucursal.
func (h *ProductHandler) ProductBranchPriceUpdateAPI(c fiber.Ctx) error {
	productID, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "producto inválido"})
	}
	branchID, err := strconv.ParseUint(c.Params("branchId"), 10, 32)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "sucursal inválida"})
	}
	var body productBranchPriceRequestBody
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "datos inválidos"})
	}
	if resp, ok := h.guardProductByID(c, uint(productID)); !ok {
		return resp
	}
	if resp, ok := guardBranchPrice(c, uint(branchID), true); !ok {
		return resp
	}
	row, err := service.NewProductService(db(c)).UpdateProductBranchPrice(uint(productID), uint(branchID), body.toInput())
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": row})
}

// ProductBranchPriceDeleteAPI elimina el override de precio de una sucursal.
func (h *ProductHandler) ProductBranchPriceDeleteAPI(c fiber.Ctx) error {
	productID, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "producto inválido"})
	}
	branchID, err := strconv.ParseUint(c.Params("branchId"), 10, 32)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "sucursal inválida"})
	}
	if resp, ok := h.guardProductByID(c, uint(productID)); !ok {
		return resp
	}
	if resp, ok := guardBranchPrice(c, uint(branchID), true); !ok {
		return resp
	}
	if err := service.NewProductService(db(c)).DeleteProductBranchPrice(uint(productID), uint(branchID)); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true})
}

// ── Atributos descriptivos de producto (TenantProductAttribute) ─────────────────────────────────
//
// Fase 5: puramente informativo, sin CRUD propio en Tukichef ni efecto en precio/stock/Kardex.

type productAttributeRequestBody struct {
	Name      string `json:"name"`
	Value     string `json:"value"`
	SortOrder int    `json:"sort_order"`
	Active    bool   `json:"active"`
}

func (b productAttributeRequestBody) toInput() service.ProductAttributeInput {
	return service.ProductAttributeInput{Name: b.Name, Value: b.Value, SortOrder: b.SortOrder, Active: b.Active}
}

// ProductAttributeListAPI lista los atributos de un producto (activos por defecto; ?all=1 trae
// también inactivos, para la pantalla de administración).
func (h *ProductHandler) ProductAttributeListAPI(c fiber.Ctx) error {
	productID, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "producto inválido"})
	}
	svc := service.NewProductService(db(c))
	if c.Query("all") == "true" || c.Query("all") == "1" {
		rows, err := svc.ListAllProductAttributes(uint(productID))
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"error": err.Error()})
		}
		return c.JSON(fiber.Map{"data": rows})
	}
	rows, err := svc.ListProductAttributes(uint(productID))
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": rows})
}

// ProductAttributeGetAPI obtiene un atributo puntual del producto.
func (h *ProductHandler) ProductAttributeGetAPI(c fiber.Ctx) error {
	productID, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "producto inválido"})
	}
	attrID, err := strconv.ParseUint(c.Params("attrId"), 10, 32)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "ID inválido"})
	}
	row, err := service.NewProductService(db(c)).GetProductAttribute(uint(productID), uint(attrID))
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": row})
}

// ProductAttributeCreateAPI crea un atributo descriptivo para el producto indicado.
func (h *ProductHandler) ProductAttributeCreateAPI(c fiber.Ctx) error {
	productID, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "producto inválido"})
	}
	var body productAttributeRequestBody
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "datos inválidos"})
	}
	row, err := service.NewProductService(db(c)).CreateProductAttribute(uint(productID), body.toInput())
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	return c.Status(201).JSON(fiber.Map{"data": row})
}

// ProductAttributeUpdateAPI actualiza un atributo existente del producto.
func (h *ProductHandler) ProductAttributeUpdateAPI(c fiber.Ctx) error {
	productID, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "producto inválido"})
	}
	attrID, err := strconv.ParseUint(c.Params("attrId"), 10, 32)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "ID inválido"})
	}
	var body productAttributeRequestBody
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "datos inválidos"})
	}
	row, err := service.NewProductService(db(c)).UpdateProductAttribute(uint(productID), uint(attrID), body.toInput())
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": row})
}

// ProductAttributeDeleteAPI elimina (físico) un atributo del producto.
func (h *ProductHandler) ProductAttributeDeleteAPI(c fiber.Ctx) error {
	productID, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "producto inválido"})
	}
	attrID, err := strconv.ParseUint(c.Params("attrId"), 10, 32)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "ID inválido"})
	}
	if err := service.NewProductService(db(c)).DeleteProductAttribute(uint(productID), uint(attrID)); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true})
}

// UnitListAPI devuelve unidades de medida (activas por defecto; ?all=1 trae también inactivas,
// para la pantalla de gestión en Tukifac). Usado por Tukifac y Tukichef en el select del producto.
func (h *ProductHandler) UnitListAPI(c fiber.Ctx) error {
	svc := service.NewProductService(db(c))
	if c.Query("all") == "true" || c.Query("all") == "1" {
		units, err := svc.ListAllUnits()
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"error": err.Error()})
		}
		return c.JSON(fiber.Map{"data": units})
	}
	units, err := svc.ListUnits()
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": units})
}

// UnitCreateAPI agrega una unidad propia del tenant.
func (h *ProductHandler) UnitCreateAPI(c fiber.Ctx) error {
	var body struct {
		Code   string `json:"code"`
		Name   string `json:"name"`
		Symbol string `json:"symbol"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "datos inválidos"})
	}
	u, err := service.NewProductService(db(c)).CreateUnit(body.Code, body.Name, body.Symbol)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	return c.Status(201).JSON(fiber.Map{"data": u})
}

// UnitUpdateAPI actualiza nombre/símbolo/activo (código bloqueado si es una unidad del sistema).
func (h *ProductHandler) UnitUpdateAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "ID inválido"})
	}
	var body struct {
		Code   string `json:"code"`
		Name   string `json:"name"`
		Symbol string `json:"symbol"`
		Active bool   `json:"active"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "datos inválidos"})
	}
	u, err := service.NewProductService(db(c)).UpdateUnit(uint(id), body.Code, body.Name, body.Symbol, body.Active)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": u})
}

// UnitDeleteAPI elimina una unidad propia del tenant sin productos vinculados.
func (h *ProductHandler) UnitDeleteAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "ID inválido"})
	}
	if err := service.NewProductService(db(c)).DeleteUnit(uint(id)); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true})
}

// PreparationAreaListAPI devuelve áreas de preparación activas.
func (h *ProductHandler) PreparationAreaListAPI(c fiber.Ctx) error {
	svc := service.NewProductService(db(c))
	if c.Query("with_counts") == "true" || c.Query("with_counts") == "1" {
		items, err := svc.ListPreparationAreasWithCounts()
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"error": err.Error()})
		}
		return c.JSON(fiber.Map{"data": items})
	}
	areas, err := svc.ListPreparationAreas()
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": areas})
}

// PreparationAreaCreateAPI crea un área de preparación.
func (h *ProductHandler) PreparationAreaCreateAPI(c fiber.Ctx) error {
	var body struct {
		Name      string `json:"name"`
		Slug      string `json:"slug"`
		SortOrder *int   `json:"sort_order"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "datos inválidos"})
	}
	area, err := service.NewProductService(db(c)).CreatePreparationArea(body.Name, body.Slug, body.SortOrder)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	return c.Status(201).JSON(fiber.Map{"data": area})
}

// PreparationAreaUpdateAPI actualiza nombre y orden (slug inmutable).
func (h *ProductHandler) PreparationAreaUpdateAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "ID inválido"})
	}
	var body struct {
		Name      string `json:"name"`
		SortOrder int    `json:"sort_order"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "datos inválidos"})
	}
	area, err := service.NewProductService(db(c)).UpdatePreparationArea(uint(id), body.Name, body.SortOrder)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": area})
}

// PreparationAreaDeleteAPI elimina área si no tiene productos vinculados.
func (h *ProductHandler) PreparationAreaDeleteAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "ID inválido"})
	}
	if err := service.NewProductService(db(c)).DeletePreparationArea(uint(id)); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true})
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

// productDenied aplica las dos reglas de visibilidad por sucursal de un producto ya cargado y, si lo deniega,
// ya escribió la respuesta: (1) dueña legacy tenant_products.branch_id (403, contrato previo) y (2) el puente
// tenant_product_branches: un producto asignado solo a otras sucursales no existe para quien no ve todas (404,
// para no revelar su existencia). Quien ve todas las sucursales (administrador) conserva la vista completa del
// catálogo compartido.
func (h *ProductHandler) productDenied(c fiber.Ctx, p *database.TenantProduct) (error, bool) {
	if p == nil {
		return nil, false
	}
	if msg, denied := h.productBranchDenied(c, p); denied {
		return c.Status(403).JSON(fiber.Map{"error": msg}), true
	}
	if !productVisibleToUser(c, db(c), p.ID) {
		return c.Status(404).JSON(fiber.Map{"error": "producto no encontrado"}), true
	}
	return nil, false
}

// productVisibleToUser: quien ve todo ve cualquier producto; el resto, solo los disponibles en su sucursal activa
// (sin filas en tenant_product_branches = disponible en todas).
func productVisibleToUser(c fiber.Ctx, tdb *gorm.DB, productID uint) bool {
	if branch.IsBranchAdmin(c) {
		return true
	}
	active := branch.ActiveBranchID(c)
	if active == 0 {
		// Sin sucursal activa (el middleware no lo permite): solo los productos sin restricción de sucursal.
		assigned, err := productbranch.BranchIDs(tdb, []uint{productID})
		return err == nil && len(assigned[productID]) == 0
	}
	ok, err := productbranch.IsAvailable(tdb, productID, active)
	return err == nil && ok
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
		CategoryID:         catID,
		Code:               c.FormValue("code"),
		Name:               c.FormValue("name"),
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
		// El form siempre trae el campo (aunque sea vacío): conserva el comportamiento previo.
		ImageURLSet:      true,
		Active:           c.FormValue("active") == "1",
		ModifierGroupIDs: &modGroupIDs,
	}
}

// NextCodeAPI sugiere un código libre para el formulario de alta.
//
// El formulario lo prellena y el usuario puede reemplazarlo por el suyo; el backend lo
// completa igual si llega vacío, así que esto es comodidad, no la validación.
func (h *ProductHandler) NextCodeAPI(c fiber.Ctx) error {
	svc := service.NewProductService(db(c))
	branchID := branch.ActiveBranchID(c)
	if reqB, err := strconv.ParseUint(c.Query("branch_id"), 10, 32); err == nil && reqB > 0 {
		branchID = branch.ResolveReadBranchFilter(c, uint(reqB))
	}
	isRestaurant := strings.EqualFold(strings.TrimSpace(c.Query("scope")), "restaurant")
	code, err := svc.NextProductCode(branchID, isRestaurant && branchID > 0)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"code": code})
}

// createCostFromBody / updateCostFromBody: quien no tiene products.view_cost no ve el costo (el
// servidor se lo omite en las respuestas), así que su formulario lo manda en 0. Ese 0 no es una
// decisión suya: al crear se guarda 0 y al editar se conserva el costo real, para no pisarlo.
func createCostFromBody(c fiber.Ctx, sent float64) float64 {
	if !middleware.CanViewProductCost(c) {
		return 0
	}
	return sent
}

func updateCostFromBody(c fiber.Ctx, sent, current float64) float64 {
	if !middleware.CanViewProductCost(c) {
		return current
	}
	return sent
}

// CostAPI GET /api/products/:id/cost — costo actual para el botón «Costo» del POS. La clave es
// "cost" (no "purchase_price") a propósito: StripProductCost pone en 0 purchase_price en toda
// respuesta de quien no tiene products.view_cost, y aquí el permiso que manda es pos.view_cost.
func (h *ProductHandler) CostAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "ID inválido"})
	}
	p, err := service.NewProductService(db(c)).GetByID(uint(id))
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "producto no encontrado"})
	}
	if resp, denied := h.productDenied(c, p); denied {
		return resp
	}
	return c.JSON(fiber.Map{"data": fiber.Map{"id": p.ID, "cost": p.PurchasePrice}})
}
