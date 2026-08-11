package handler

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"tukifac/internal/ecommerce/service"
	salessvc "tukifac/internal/sales/service"
	"tukifac/pkg/database"
	"tukifac/pkg/tax"
	"tukifac/pkg/tenantstorage"
	"tukifac/pkg/uploadlimits"

	"github.com/gofiber/fiber/v3"
	"gorm.io/gorm"
)

type EcommerceHandler struct{}

func NewEcommerceHandler() *EcommerceHandler { return &EcommerceHandler{} }

func db(c fiber.Ctx) *gorm.DB {
	v, _ := c.Locals("tenantDB").(*gorm.DB)
	return v
}

// ── Admin: ajustes ───────────────────────────────────────────────────

func (h *EcommerceHandler) GetSettingsAPI(c fiber.Ctx) error {
	svc := service.NewEcommerceService(db(c))
	settings, err := svc.GetSettings()
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{
		"data":                     settings,
		"resolved_whatsapp_number": svc.ResolveWhatsAppNumber(settings),
	})
}

func (h *EcommerceHandler) UpdateSettingsAPI(c fiber.Ctx) error {
	var body struct {
		Enabled        *bool   `json:"enabled"`
		StoreName      *string `json:"store_name"`
		Tagline        *string `json:"tagline"`
		Description    *string `json:"description"`
		WhatsAppNumber *string `json:"whatsapp_number"` // "" = volver a heredar el teléfono general
		TemplateKey    *string `json:"template_key"`
		PrimaryColor   *string `json:"primary_color"`
		SecondaryColor *string `json:"secondary_color"`
		FontFamily     *string `json:"font_family"`
		CardStyle      *string `json:"card_style"`
		CategoryStyle  *string `json:"category_style"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "datos inválidos"})
	}
	input := service.UpdateSettingsInput{
		Enabled:        body.Enabled,
		StoreName:      body.StoreName,
		Tagline:        body.Tagline,
		Description:    body.Description,
		TemplateKey:    body.TemplateKey,
		PrimaryColor:   body.PrimaryColor,
		SecondaryColor: body.SecondaryColor,
		FontFamily:     body.FontFamily,
		CardStyle:      body.CardStyle,
		CategoryStyle:  body.CategoryStyle,
	}
	if body.WhatsAppNumber != nil {
		input.WhatsAppNumber = &body.WhatsAppNumber
	}
	svc := service.NewEcommerceService(db(c))
	settings, err := svc.UpdateSettings(input)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{
		"data":                     settings,
		"resolved_whatsapp_number": svc.ResolveWhatsAppNumber(settings),
	})
}

// uploadEcommerceImage guarda una imagen en uploads/tenants/{ruc}/ecommerce/ y devuelve su URL
// pública. Mismo patrón que la subida de logo de empresa / imagen de producto.
func uploadEcommerceImage(c fiber.Ctx, fieldName, filePrefix string) (string, error) {
	ruc, err := tenantstorage.ResolveTenantRUC(c)
	if err != nil {
		return "", err
	}
	file, err := c.FormFile(fieldName)
	if err != nil || file == nil {
		return "", fmt.Errorf("envía un archivo en el campo '%s'", fieldName)
	}
	if file.Size > uploadlimits.MaxFileBytes {
		return "", fmt.Errorf("la imagen no debe superar 10 MB")
	}
	ext := strings.ToLower(filepath.Ext(file.Filename))
	allowed := map[string]bool{".jpg": true, ".jpeg": true, ".png": true, ".webp": true}
	if !allowed[ext] {
		return "", fmt.Errorf("formato no permitido. Usa JPG, PNG o WebP")
	}
	dir := tenantstorage.TenantUploadDir(ruc, "ecommerce")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("no se pudo crear carpeta %s: %w", dir, err)
	}
	filename := fmt.Sprintf("%s-%d%s", filePrefix, time.Now().UnixMilli(), ext)
	savePath := filepath.Join(dir, filename)
	if err := c.SaveFile(file, savePath); err != nil {
		return "", fmt.Errorf("error guardando imagen: %w", err)
	}
	return tenantstorage.TenantUploadPublicURL(ruc, "ecommerce", filename), nil
}

func (h *EcommerceHandler) UploadLogoAPI(c fiber.Ctx) error {
	url, err := uploadEcommerceImage(c, "image", "logo")
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	if err := service.NewEcommerceService(db(c)).SetLogoURL(url); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true, "logo_url": url})
}

func (h *EcommerceHandler) UploadBackgroundAPI(c fiber.Ctx) error {
	url, err := uploadEcommerceImage(c, "image", "background")
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	if err := service.NewEcommerceService(db(c)).SetBackgroundURL(url); err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true, "background_image_url": url})
}

// ── Admin: sliders ───────────────────────────────────────────────────

func (h *EcommerceHandler) ListSlidersAPI(c fiber.Ctx) error {
	rows, err := service.NewEcommerceService(db(c)).ListSliders()
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": rows})
}

func (h *EcommerceHandler) CreateSliderAPI(c fiber.Ctx) error {
	url, err := uploadEcommerceImage(c, "image", "slider")
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	row, err := service.NewEcommerceService(db(c)).CreateSlider(service.CreateSliderInput{
		ImageURL:   url,
		LinkURL:    strings.TrimSpace(c.FormValue("link_url")),
		Title:      strings.TrimSpace(c.FormValue("title")),
		Subtitle:   strings.TrimSpace(c.FormValue("subtitle")),
		ButtonText: strings.TrimSpace(c.FormValue("button_text")),
	})
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.Status(201).JSON(fiber.Map{"data": row})
}

func (h *EcommerceHandler) UpdateSliderAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "ID inválido"})
	}
	var body struct {
		LinkURL    *string `json:"link_url"`
		Title      *string `json:"title"`
		Subtitle   *string `json:"subtitle"`
		ButtonText *string `json:"button_text"`
		Active     *bool   `json:"active"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "datos inválidos"})
	}
	input := service.UpdateSliderInput{
		LinkURL:    body.LinkURL,
		Title:      body.Title,
		Subtitle:   body.Subtitle,
		ButtonText: body.ButtonText,
		Active:     body.Active,
	}
	if err := service.NewEcommerceService(db(c)).UpdateSlider(uint(id), input); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true})
}

func (h *EcommerceHandler) DeleteSliderAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "ID inválido"})
	}
	if err := service.NewEcommerceService(db(c)).DeleteSlider(uint(id)); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true})
}

func (h *EcommerceHandler) ReorderSlidersAPI(c fiber.Ctx) error {
	var body struct {
		IDs []uint `json:"ids"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "datos inválidos"})
	}
	if err := service.NewEcommerceService(db(c)).ReorderSliders(body.IDs); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true})
}

// ── Admin: pedidos web ───────────────────────────────────────────────

func (h *EcommerceHandler) ListOrdersAPI(c fiber.Ctx) error {
	status := c.Query("status")
	rows, err := service.NewEcommerceService(db(c)).ListOrders(status, 200)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": rows})
}

func (h *EcommerceHandler) OrderPrintDataAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "ID inválido"})
	}
	pd, err := service.BuildPrintDataForOrder(db(c), uint(id))
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "pedido no encontrado"})
	}
	return c.JSON(fiber.Map{"print_data": pd})
}

func (h *EcommerceHandler) ConvertOrderAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "ID inválido"})
	}
	var body struct {
		Target    string `json:"target"`
		SeriesID  uint   `json:"series_id"`
		BranchID  uint   `json:"branch_id"`
		IssueDate string `json:"issue_date"`
		ContactID *uint  `json:"contact_id"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "datos inválidos"})
	}
	if body.SeriesID == 0 {
		return c.Status(400).JSON(fiber.Map{"error": "series_id es obligatorio"})
	}
	if body.BranchID == 0 {
		return c.Status(400).JSON(fiber.Map{"error": "branch_id es obligatorio"})
	}
	svc := service.NewEcommerceService(db(c))
	sale, err := svc.ConvertToSale(uint(id), service.ConvertInput{
		Target:        body.Target,
		SeriesID:      body.SeriesID,
		BranchID:      body.BranchID,
		IssueDate:     parseOrderIssueDate(body.IssueDate),
		ContactID:     body.ContactID,
		UserID:        orderUserID(c),
		CentralTenant: orderCentralTenantID(c),
		TaxConfig:     tax.LoadFromDB(db(c)),
	})
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	out := fiber.Map{"sale": sale}
	if printData, err := salessvc.BuildPrintDataForSale(db(c), sale.ID); err == nil {
		out["print_data"] = printData
	}
	return c.JSON(out)
}

func orderUserID(c fiber.Ctx) uint {
	v, _ := c.Locals("user_id").(uint)
	return v
}

func orderCentralTenantID(c fiber.Ctx) uint {
	if tenant, ok := c.Locals("tenant").(*database.Tenant); ok && tenant != nil {
		return tenant.ID
	}
	return 0
}

func parseOrderIssueDate(bodyDate string) time.Time {
	loc, err := time.LoadLocation("America/Lima")
	if err != nil || loc == nil {
		loc = time.Local
	}
	nowPe := time.Now().In(loc)
	fallback := time.Date(nowPe.Year(), nowPe.Month(), nowPe.Day(), 12, 0, 0, 0, loc)
	if strings.TrimSpace(bodyDate) == "" {
		return fallback
	}
	if t, err := time.ParseInLocation("2006-01-02", bodyDate, loc); err == nil {
		return time.Date(t.Year(), t.Month(), t.Day(), 12, 0, 0, 0, loc)
	}
	return fallback
}

func (h *EcommerceHandler) UpdateOrderStatusAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "ID inválido"})
	}
	var body struct {
		Status string `json:"status"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "datos inválidos"})
	}
	if err := service.NewEcommerceService(db(c)).UpdateOrderStatus(uint(id), body.Status); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true})
}

// ── Público (sin JWT) ────────────────────────────────────────────────

func (h *EcommerceHandler) PublicSettingsAPI(c fiber.Ctx) error {
	svc := service.NewEcommerceService(db(c))
	settings, err := svc.GetSettings()
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	sliders, _ := svc.ListActiveSliders()
	return c.JSON(fiber.Map{
		"store_name":           settings.StoreName,
		"tagline":              settings.Tagline,
		"description":          settings.Description,
		"logo_url":             settings.LogoURL,
		"background_image_url": settings.BackgroundImageURL,
		"whatsapp_number":      svc.ResolveWhatsAppNumber(settings),
		"template_key":         settings.TemplateKey,
		"primary_color":        settings.PrimaryColor,
		"secondary_color":      settings.SecondaryColor,
		"font_family":          settings.FontFamily,
		"card_style":           settings.CardStyle,
		"category_style":       settings.CategoryStyle,
		"sliders":              sliders,
	})
}

func (h *EcommerceHandler) PublicCategoriesAPI(c fiber.Ctx) error {
	rows, err := service.NewEcommerceService(db(c)).PublicCategories()
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": rows})
}

func (h *EcommerceHandler) PublicPriceBoundsAPI(c fiber.Ctx) error {
	min, max, err := service.NewEcommerceService(db(c)).PriceBounds()
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"min": min, "max": max})
}

func (h *EcommerceHandler) PublicProductsAPI(c fiber.Ctx) error {
	catID, _ := strconv.ParseUint(c.Query("category_id"), 10, 32)
	page, _ := strconv.Atoi(c.Query("page"))
	perPage, _ := strconv.Atoi(c.Query("per_page"))
	if perPage <= 0 {
		perPage = 24
	}
	var minPrice, maxPrice *float64
	if v, err := strconv.ParseFloat(c.Query("min_price"), 64); err == nil {
		minPrice = &v
	}
	if v, err := strconv.ParseFloat(c.Query("max_price"), 64); err == nil {
		maxPrice = &v
	}
	items, total, err := service.NewEcommerceService(db(c)).PublicProducts(c.Query("q"), uint(catID), minPrice, maxPrice, page, perPage)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": items, "total": total})
}

func (h *EcommerceHandler) CreatePublicOrderAPI(c fiber.Ctx) error {
	var body struct {
		CustomerName  string `json:"customer_name"`
		CustomerPhone string `json:"customer_phone"`
		Items         []struct {
			ProductID uint    `json:"product_id"`
			Name      string  `json:"name"`
			Quantity  float64 `json:"quantity"`
			UnitPrice float64 `json:"unit_price"`
		} `json:"items"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "datos inválidos"})
	}
	items := make([]service.OrderItemInput, 0, len(body.Items))
	for _, it := range body.Items {
		if it.ProductID == 0 || it.Quantity <= 0 {
			continue
		}
		items = append(items, service.OrderItemInput{
			ProductID: it.ProductID,
			Name:      it.Name,
			Quantity:  it.Quantity,
			UnitPrice: it.UnitPrice,
		})
	}
	order, err := service.NewEcommerceService(db(c)).CreateOrder(service.CreateOrderInput{
		CustomerName:  body.CustomerName,
		CustomerPhone: body.CustomerPhone,
		Items:         items,
	})
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	return c.Status(201).JSON(fiber.Map{"data": order, "order_number": order.ID})
}
