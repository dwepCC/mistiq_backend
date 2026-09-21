package handler

import (
	"fmt"
	"html"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"tukifac/config"
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
		ShowStock      *bool   `json:"show_stock"`
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
		ShowStock:      body.ShowStock,
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

// ListOrdersAPI filtros del panel (Contrato v2 Fase 5): estado, sucursal, búsqueda por
// nombre/teléfono/N° de pedido, rango de fechas — todos opcionales y combinables.
func (h *EcommerceHandler) ListOrdersAPI(c fiber.Ctx) error {
	params := service.ListOrdersParams{
		Status: c.Query("status"),
		Query:  c.Query("q"),
		Limit:  200,
	}
	if bid, err := strconv.ParseUint(c.Query("branch_id"), 10, 32); err == nil {
		params.BranchID = uint(bid)
	}
	if from := strings.TrimSpace(c.Query("date_from")); from != "" {
		if t, err := time.Parse("2006-01-02", from); err == nil {
			params.DateFrom = &t
		}
	}
	if to := strings.TrimSpace(c.Query("date_to")); to != "" {
		if t, err := time.Parse("2006-01-02", to); err == nil {
			endOfDay := t.Add(24*time.Hour - time.Second)
			params.DateTo = &endOfDay
		}
	}
	rows, err := service.NewEcommerceService(db(c)).ListOrders(params)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": rows})
}

// GetOrderAPI detalle completo de un pedido para el panel: pedido + líneas normalizadas
// (con fallback a ItemsJSON si es un pedido legacy) + historial de transiciones real — Contrato v2
// Fase 5. No fabrica historial: si no hay filas en TenantEcommerceOrderStatusHistory, "history"
// viene vacío, nunca inventado.
func (h *EcommerceHandler) GetOrderAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "ID inválido"})
	}
	order, items, history, err := service.NewEcommerceService(db(c)).GetOrderDetail(uint(id))
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": order, "items": items, "history": history})
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

// UpdateOrderStatusAPI ejecuta una transición del pedido (Contrato v2 §5/§6.2). El mismo endpoint
// sirve distintos roles (confirmar, preparar, despachar, devolver...) según la transición pedida
// en el body — por eso la ruta solo exige un permiso "de entrada" (ver routes.go) y ACÁ se valida
// el permiso ESPECÍFICO que exige esa transición puntual, nunca confiando en que el frontend solo
// muestre el botón correcto.
func (h *EcommerceHandler) UpdateOrderStatusAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "ID inválido"})
	}
	var body struct {
		Status   string `json:"status"`
		Notes    string `json:"notes"`
		BranchID *uint  `json:"branch_id"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "datos inválidos"})
	}
	svc := service.NewEcommerceService(db(c))
	order, err := svc.GetOrder(uint(id))
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "pedido no encontrado"})
	}
	newStatus := strings.ToUpper(strings.TrimSpace(body.Status))
	transition, ok := service.FindOrderTransition(order.Status, newStatus)
	if !ok {
		return c.Status(422).JSON(fiber.Map{
			"error": fmt.Sprintf("no se puede pasar de %s a %s", order.Status, newStatus),
		})
	}
	if !hasEcommercePermission(c, transition.Permission) {
		return c.Status(403).JSON(fiber.Map{
			"error":      "no tienes permiso para esta transición",
			"permission": transition.Permission,
		})
	}
	if err := svc.UpdateOrderStatus(uint(id), service.UpdateOrderStatusInput{
		NewStatus: newStatus,
		UserID:    orderUserID(c),
		Notes:     body.Notes,
		BranchID:  body.BranchID,
	}); err != nil {
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
		"show_stock":           settings.ShowStock,
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

// PublicPreviewAPI: HTML mínimo con meta tags Open Graph reales del tenant (nombre, descripción,
// logo). Existe SOLO para crawlers de redes sociales (WhatsApp/Facebook/Twitter) — Nginx detecta
// su User-Agent y reenvía acá en vez de servir el SPA estático, cuyo index.html es el mismo
// archivo para todos los tenants y nunca puede tener meta tags dinámicas (los crawlers no
// ejecutan JS). Un visitante real nunca llega a esta ruta.
func (h *EcommerceHandler) PublicPreviewAPI(c fiber.Ctx) error {
	svc := service.NewEcommerceService(db(c))
	settings, err := svc.GetSettings()
	if err != nil {
		return c.Status(500).SendString("error interno")
	}

	title := strings.TrimSpace(settings.StoreName)
	if title == "" {
		title = "Catálogo virtual"
	}
	description := strings.TrimSpace(settings.Description)
	if description == "" {
		description = strings.TrimSpace(settings.Tagline)
	}
	if description == "" {
		description = "Explora el catálogo y haz tu pedido directo por WhatsApp."
	}

	// Ruta pública fija (no c.OriginalURL()): Nginx reescribe la petición del bot hacia este
	// endpoint interno, así que OriginalURL() acá sería la ruta de la API, no "/ecommerce".
	// Protocolo fijo "https" (no c.Protocol()): esta ruta solo se alcanza vía el proxy interno
	// de Nginx sobre HTTP plano — c.Protocol() reportaría "http" aunque el bot real llegó por
	// HTTPS público.
	pageURL := fmt.Sprintf("https://%s/ecommerce", c.Hostname())
	imageURL := ""
	if settings.LogoURL != "" {
		imageURL = config.AppConfig.APIPublicURL + settings.LogoURL
	}

	c.Set("Content-Type", "text/html; charset=utf-8")
	return c.SendString(buildPreviewHTML(previewMeta{
		Title:       title,
		Description: description,
		URL:         pageURL,
		Image:       imageURL,
	}))
}

type previewMeta struct {
	Title       string
	Description string
	URL         string
	Image       string
}

func buildPreviewHTML(m previewMeta) string {
	esc := html.EscapeString
	imageTag := ""
	if m.Image != "" {
		imageTag = fmt.Sprintf(`
    <meta property="og:image" content="%s">
    <meta name="twitter:image" content="%s">`, esc(m.Image), esc(m.Image))
	}
	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="es">
<head>
<meta charset="UTF-8">
<title>%s</title>
<meta name="description" content="%s">
<meta property="og:type" content="website">
<meta property="og:title" content="%s">
<meta property="og:description" content="%s">
<meta property="og:url" content="%s">
<meta name="twitter:card" content="summary">
<meta name="twitter:title" content="%s">
<meta name="twitter:description" content="%s">%s
<meta http-equiv="refresh" content="0; url=%s">
</head>
<body></body>
</html>`, esc(m.Title), esc(m.Description), esc(m.Title), esc(m.Description), esc(m.URL), esc(m.Title), esc(m.Description), imageTag, esc(m.URL))
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
	svc := service.NewEcommerceService(db(c))
	settings, err := svc.GetSettings()
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	items, total, err := svc.PublicProducts(c.Query("q"), uint(catID), minPrice, maxPrice, page, perPage, settings.ShowStock)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": items, "total": total})
}

// CreatePublicOrderAPI Contrato ecommerce v2 Fase 3/4: el cliente SOLO puede mandar identidad de
// producto/presentación + cantidad por línea — nombre/precio/subtotal/total nunca vienen del
// cliente, EcommerceService.CreateOrder los resuelve siempre contra el catálogo real del tenant.
// Sirve tanto a invitados como a clientes logueados por el MISMO endpoint (RequireEcommerceAvailable
// + EcommerceCustomerAuthOptional en routes.go): si hay sesión de cliente válida, CustomerAccountID
// se toma SIEMPRE del token (Locals), nunca de un campo del body — un body no tiene forma de
// enviarlo porque el struct de bind no lo declara.
func (h *EcommerceHandler) CreatePublicOrderAPI(c fiber.Ctx) error {
	var body struct {
		CustomerName      string `json:"customer_name"`
		CustomerPhone     string `json:"customer_phone"`
		DeliveryMethod    string `json:"delivery_method"`
		DeliveryAddressID *uint  `json:"delivery_address_id"` // solo válido con sesión de cliente
		Address           *struct {
			AddressLine string `json:"address_line"`
			Reference   string `json:"reference"`
			Ubigeo      string `json:"ubigeo"`
		} `json:"address"`
		Items []struct {
			ProductID      uint    `json:"product_id"`
			PresentationID *uint   `json:"presentation_id"`
			Quantity       float64 `json:"quantity"`
		} `json:"items"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "datos inválidos"})
	}
	items := make([]service.CreateOrderItemInput, 0, len(body.Items))
	for _, it := range body.Items {
		items = append(items, service.CreateOrderItemInput{
			ProductID:      it.ProductID,
			PresentationID: it.PresentationID,
			Quantity:       it.Quantity,
		})
	}
	input := service.CreateOrderInput{
		CustomerName:   body.CustomerName,
		CustomerPhone:  body.CustomerPhone,
		DeliveryMethod: body.DeliveryMethod,
		Items:          items,
	}
	if customerID, ok := currentCustomerID(c); ok {
		input.CustomerAccountID = &customerID
		// delivery_address_id solo se acepta si hay sesión — CreateOrder valida ownership contra
		// ESE customerID (nunca contra uno del body), ver EcommerceService.CreateOrder.
		if body.DeliveryAddressID != nil {
			input.DeliveryAddressID = body.DeliveryAddressID
		}
	}
	if body.Address != nil {
		input.GuestAddressLine = body.Address.AddressLine
		input.GuestReference = body.Address.Reference
		input.GuestUbigeo = body.Address.Ubigeo
	}
	order, orderItems, err := service.NewEcommerceService(db(c)).CreateOrder(input)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	return c.Status(201).JSON(fiber.Map{"data": order, "items": orderItems, "order_number": order.ID})
}
