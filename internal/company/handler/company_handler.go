package handler

import (
	"strconv"
	"strings"

	"tukifac/internal/company/service"
	"tukifac/pkg/database"
	"tukifac/pkg/utils"

	"github.com/gofiber/fiber/v3"
	"gorm.io/gorm"
)

type CompanyHandler struct{}

func NewCompanyHandler() *CompanyHandler { return &CompanyHandler{} }

func db(c fiber.Ctx) *gorm.DB {
	v, _ := c.Locals("tenantDB").(*gorm.DB)
	return v
}
func email(c fiber.Ctx) string {
	v, _ := c.Locals("user_email").(string)
	return v
}

func (h *CompanyHandler) ConfigPage(c fiber.Ctx) error {
	svc := service.NewCompanyService(db(c))
	cfg, _ := svc.GetConfig()
	return c.Render("company/config", fiber.Map{
		"Title":     "Configuración de Empresa",
		"UserEmail": email(c),
		"Config":    cfg,
		"Themes":    utils.AllThemes(),
		"Success":   c.Query("success"),
	}, "layouts/base")
}

func (h *CompanyHandler) ConfigSubmit(c fiber.Ctx) error {
	svc := service.NewCompanyService(db(c))
	taxRate, _ := strconv.ParseFloat(c.FormValue("tax_rate"), 64)
	colorTheme := c.FormValue("color_theme")
	if colorTheme == "" {
		colorTheme = "blue"
	}
	input := database.TenantCompanyConfig{
		BusinessName: c.FormValue("business_name"),
		TradeName:    c.FormValue("trade_name"),
		RUC:          c.FormValue("ruc"),
		Address:      c.FormValue("address"),
		Ubigeo:       c.FormValue("ubigeo"),
		Country:      c.FormValue("country"),
		Phone:        c.FormValue("phone"),
		Email:        c.FormValue("email"),
		Website:      c.FormValue("website"),
		LogoURL:      c.FormValue("logo_url"),
		Currency:     c.FormValue("currency"),
		TaxRate:      taxRate,
		ColorTheme:   colorTheme,
	}
	if err := svc.SaveConfig(input); err != nil {
		cfg, _ := svc.GetConfig()
		return c.Render("company/config", fiber.Map{
			"Title":     "Configuración de Empresa",
			"UserEmail": email(c),
			"Config":    cfg,
			"Themes":    utils.AllThemes(),
			"Error":     err.Error(),
		}, "layouts/base")
	}
	return c.Redirect().To("/company/config?success=1")
}

func (h *CompanyHandler) SunatPage(c fiber.Ctx) error {
	svc := service.NewCompanyService(db(c))
	cfg, _ := svc.GetConfig()
	return c.Render("company/sunat", fiber.Map{
		"Title":     "Configuración SUNAT",
		"UserEmail": email(c),
		"Config":    cfg,
		"Success":   c.Query("success"),
	}, "layouts/base")
}

func (h *CompanyHandler) SunatSubmit(c fiber.Ctx) error {
	svc := service.NewCompanyService(db(c))
	taxRate, _ := strconv.ParseFloat(c.FormValue("tax_rate"), 64)
	if taxRate <= 0 {
		taxRate = 18
	}
	if err := svc.SaveSunatConfigTenant(taxRate, c.FormValue("igv_regime"), c.FormValue("tax_benefit_zone") == "1"); err != nil {
		cfg, _ := svc.GetConfig()
		return c.Render("company/sunat", fiber.Map{
			"Title":     "Configuración SUNAT",
			"UserEmail": email(c),
			"Config":    cfg,
			"Error":     err.Error(),
		}, "layouts/base")
	}
	return c.Redirect().To("/company/sunat?success=1")
}

func (h *CompanyHandler) BranchesPage(c fiber.Ctx) error {
	svc := service.NewCompanyService(db(c))
	branches, _ := svc.ListBranches()
	return c.Render("company/branches", fiber.Map{
		"Title":     "Sucursales",
		"UserEmail": email(c),
		"Branches":  branches,
		"Success":   c.Query("success"),
	}, "layouts/base")
}

func (h *CompanyHandler) CreateBranchForm(c fiber.Ctx) error {
	svc := service.NewCompanyService(db(c))
	_, err := svc.CreateBranch(
		c.FormValue("name"),
		c.FormValue("address"),
		c.FormValue("phone"),
		c.FormValue("fiscal_domicile_code"),
		c.FormValue("is_main") == "1",
	)
	if err != nil {
		branches, _ := svc.ListBranches()
		return c.Render("company/branches", fiber.Map{
			"Title":     "Sucursales",
			"UserEmail": email(c),
			"Branches":  branches,
			"Error":     err.Error(),
		}, "layouts/base")
	}
	return c.Redirect().To("/company/branches?success=created")
}

func (h *CompanyHandler) UpdateBranchForm(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).SendString("ID inválido")
	}
	svc := service.NewCompanyService(db(c))
	if err := svc.UpdateBranch(uint(id),
		c.FormValue("name"),
		c.FormValue("address"),
		c.FormValue("phone"),
		c.FormValue("fiscal_domicile_code"),
		c.FormValue("is_main") == "1",
	); err != nil {
		return c.Status(fiber.StatusBadRequest).SendString(err.Error())
	}
	return c.Redirect().To("/company/branches?success=updated")
}

func (h *CompanyHandler) DeleteBranchForm(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).SendString("ID inválido")
	}
	service.NewCompanyService(db(c)).DeleteBranch(uint(id))
	return c.Redirect().To("/company/branches?success=deleted")
}

// SeriesRow aplana los datos de una serie para la plantilla
type SeriesRow struct {
	ID          uint
	BranchID    uint
	BranchName  string
	DocType     string
	SunatCode   string
	Category    string
	Series      string
	Correlative uint
	IsActive    bool
}

func (h *CompanyHandler) SeriesPage(c fiber.Ctx) error {
	svc := service.NewCompanyService(db(c))
	branchID, _ := strconv.ParseUint(c.Query("branch_id"), 10, 32)
	series, _ := svc.ListSeries(uint(branchID))
	branches, _ := svc.ListBranches()

	// Construir mapa de sucursales para el lookup por ID
	branchMap := make(map[uint]string)
	for _, b := range branches {
		branchMap[b.ID] = b.Name
	}

	rows := make([]SeriesRow, 0, len(series))
	for _, s := range series {
		rows = append(rows, SeriesRow{
			ID:          s.ID,
			BranchID:    s.BranchID,
			BranchName:  branchMap[s.BranchID],
			DocType:     s.DocType,
			SunatCode:   s.SunatCode,
			Category:    s.Category,
			Series:      s.Series,
			Correlative: s.Correlative,
			IsActive:    s.Active,
		})
	}

	return c.Render("company/series", fiber.Map{
		"Title":          "Series y Correlativos",
		"UserEmail":      email(c),
		"Series":         rows,
		"Branches":       branches,
		"SelectedBranch": branchID,
		"Success":        c.Query("success"),
	}, "layouts/base")
}

func (h *CompanyHandler) CreateSeriesForm(c fiber.Ctx) error {
	svc := service.NewCompanyService(db(c))
	branchID, _ := strconv.ParseUint(c.FormValue("branch_id"), 10, 32)
	var corr *uint
	if v := strings.TrimSpace(c.FormValue("correlative")); v != "" {
		n, err := strconv.ParseUint(v, 10, 32)
		if err != nil {
			return c.Status(fiber.StatusBadRequest).SendString("correlativo inválido")
		}
		u := uint(n)
		corr = &u
	}
	if err := svc.CreateSeries(
		uint(branchID),
		c.FormValue("doc_type"),
		c.FormValue("series"),
		corr,
	); err != nil {
		return c.Status(fiber.StatusBadRequest).SendString(err.Error())
	}
	return c.Redirect().To("/company/series?success=created")
}

func (h *CompanyHandler) UpdateSeriesForm(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).SendString("ID inválido")
	}
	active := c.FormValue("is_active") == "true" || c.FormValue("is_active") == "on"
	if err := service.NewCompanyService(db(c)).UpdateSeries(uint(id), c.FormValue("series"), active, "", nil); err != nil {
		return c.Status(fiber.StatusBadRequest).SendString(err.Error())
	}
	return c.Redirect().To("/company/series?success=updated")
}
