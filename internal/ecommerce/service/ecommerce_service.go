package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	companyservice "tukifac/internal/company/service"
	productservice "tukifac/internal/products/service"
	"tukifac/pkg/database"

	"gorm.io/gorm"
)

type EcommerceService struct {
	db *gorm.DB
}

func NewEcommerceService(db *gorm.DB) *EcommerceService {
	return &EcommerceService{db: db}
}

// GetSettings carga la fila única (id=1); la crea con defaults si no existe.
func (s *EcommerceService) GetSettings() (*database.TenantEcommerceSettings, error) {
	var row database.TenantEcommerceSettings
	if err := s.db.First(&row, 1).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			row = database.TenantEcommerceSettings{ID: 1}
			if err := s.db.Create(&row).Error; err != nil {
				return nil, err
			}
			return &row, nil
		}
		return nil, err
	}
	return &row, nil
}

// ResolveWhatsAppNumber usa el número propio de la tienda si está configurado; si no, el
// teléfono general de la empresa (Ajustes → Empresa).
func (s *EcommerceService) ResolveWhatsAppNumber(settings *database.TenantEcommerceSettings) string {
	if settings.WhatsAppNumber != nil && strings.TrimSpace(*settings.WhatsAppNumber) != "" {
		return strings.TrimSpace(*settings.WhatsAppNumber)
	}
	cfg, err := companyservice.NewCompanyService(s.db).GetConfig()
	if err != nil || cfg == nil {
		return ""
	}
	return strings.TrimSpace(cfg.Phone)
}

// UpdateSettingsInput campos nil = no tocar (update parcial). WhatsAppNumber: puntero a puntero
// para distinguir "no enviado" (nil) de "enviado vacío = volver a heredar el teléfono general".
type UpdateSettingsInput struct {
	Enabled        *bool
	StoreName      *string
	Tagline        *string
	Description    *string
	WhatsAppNumber **string
	TemplateKey    *string
	PrimaryColor   *string
	SecondaryColor *string
	FontFamily     *string
	CardStyle      *string
	CategoryStyle  *string
}

func (s *EcommerceService) UpdateSettings(input UpdateSettingsInput) (*database.TenantEcommerceSettings, error) {
	if _, err := s.GetSettings(); err != nil {
		return nil, err
	}
	upd := map[string]interface{}{}
	if input.Enabled != nil {
		upd["enabled"] = *input.Enabled
	}
	if input.StoreName != nil {
		upd["store_name"] = strings.TrimSpace(*input.StoreName)
	}
	if input.Tagline != nil {
		upd["tagline"] = strings.TrimSpace(*input.Tagline)
	}
	if input.Description != nil {
		upd["description"] = *input.Description
	}
	if input.WhatsAppNumber != nil {
		v := *input.WhatsAppNumber
		if v != nil && strings.TrimSpace(*v) == "" {
			v = nil
		}
		upd["whatsapp_number"] = v
	}
	if input.TemplateKey != nil {
		upd["template_key"] = strings.TrimSpace(*input.TemplateKey)
	}
	if input.PrimaryColor != nil {
		upd["primary_color"] = strings.TrimSpace(*input.PrimaryColor)
	}
	if input.SecondaryColor != nil {
		upd["secondary_color"] = strings.TrimSpace(*input.SecondaryColor)
	}
	if input.FontFamily != nil {
		upd["font_family"] = strings.TrimSpace(*input.FontFamily)
	}
	if input.CardStyle != nil {
		upd["card_style"] = strings.TrimSpace(*input.CardStyle)
	}
	if input.CategoryStyle != nil {
		upd["category_style"] = strings.TrimSpace(*input.CategoryStyle)
	}
	if len(upd) > 0 {
		if err := s.db.Model(&database.TenantEcommerceSettings{}).Where("id = ?", 1).Updates(upd).Error; err != nil {
			return nil, err
		}
	}
	return s.GetSettings()
}

func (s *EcommerceService) SetLogoURL(url string) error {
	return s.db.Model(&database.TenantEcommerceSettings{}).Where("id = ?", 1).Update("logo_url", url).Error
}

func (s *EcommerceService) SetBackgroundURL(url string) error {
	return s.db.Model(&database.TenantEcommerceSettings{}).Where("id = ?", 1).Update("background_image_url", url).Error
}

// ── Sliders ──────────────────────────────────────────────────────────

func (s *EcommerceService) ListSliders() ([]database.TenantEcommerceSlider, error) {
	var rows []database.TenantEcommerceSlider
	err := s.db.Order("sort_order ASC, id ASC").Find(&rows).Error
	return rows, err
}

func (s *EcommerceService) ListActiveSliders() ([]database.TenantEcommerceSlider, error) {
	var rows []database.TenantEcommerceSlider
	err := s.db.Where("active = ?", true).Order("sort_order ASC, id ASC").Find(&rows).Error
	return rows, err
}

type CreateSliderInput struct {
	ImageURL   string
	LinkURL    string
	Title      string
	Subtitle   string
	ButtonText string
}

func (s *EcommerceService) CreateSlider(input CreateSliderInput) (*database.TenantEcommerceSlider, error) {
	var maxOrder int
	s.db.Model(&database.TenantEcommerceSlider{}).Select("COALESCE(MAX(sort_order), 0)").Scan(&maxOrder)
	row := &database.TenantEcommerceSlider{
		ImageURL:   input.ImageURL,
		LinkURL:    input.LinkURL,
		Title:      input.Title,
		Subtitle:   input.Subtitle,
		ButtonText: input.ButtonText,
		SortOrder:  maxOrder + 1,
		Active:     true,
	}
	if err := s.db.Create(row).Error; err != nil {
		return nil, err
	}
	return row, nil
}

type UpdateSliderInput struct {
	LinkURL    *string
	Title      *string
	Subtitle   *string
	ButtonText *string
	Active     *bool
}

func (s *EcommerceService) UpdateSlider(id uint, input UpdateSliderInput) error {
	upd := map[string]interface{}{}
	if input.LinkURL != nil {
		upd["link_url"] = *input.LinkURL
	}
	if input.Title != nil {
		upd["title"] = *input.Title
	}
	if input.Subtitle != nil {
		upd["subtitle"] = *input.Subtitle
	}
	if input.ButtonText != nil {
		upd["button_text"] = *input.ButtonText
	}
	if input.Active != nil {
		upd["active"] = *input.Active
	}
	if len(upd) == 0 {
		return nil
	}
	return s.db.Model(&database.TenantEcommerceSlider{}).Where("id = ?", id).Updates(upd).Error
}

func (s *EcommerceService) DeleteSlider(id uint) error {
	return s.db.Delete(&database.TenantEcommerceSlider{}, id).Error
}

func (s *EcommerceService) ReorderSliders(orderedIDs []uint) error {
	return s.db.Transaction(func(tx *gorm.DB) error {
		for i, id := range orderedIDs {
			if err := tx.Model(&database.TenantEcommerceSlider{}).Where("id = ?", id).Update("sort_order", i+1).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// ── Catálogo público ─────────────────────────────────────────────────

type PublicCategory struct {
	ID   uint   `json:"id"`
	Name string `json:"name"`
}

// PriceBounds min/max de precio entre los productos publicados en el Catálogo Digital. Se
// calcula sin filtros (categoría/búsqueda/rango) para que el slider de precio tenga un rango
// estable en vez de saltar cada vez que el cliente filtra.
func (s *EcommerceService) PriceBounds() (float64, float64, error) {
	var row struct {
		Min float64
		Max float64
	}
	err := s.db.Table("tenant_products").
		Select("COALESCE(MIN(sale_price), 0) AS min, COALESCE(MAX(sale_price), 0) AS max").
		Where("show_in_digital_catalog = ? AND active = ? AND deleted_at IS NULL", true, true).
		Scan(&row).Error
	return row.Min, row.Max, err
}

// PublicCategories categorías con al menos un producto activo publicado en el catálogo.
func (s *EcommerceService) PublicCategories() ([]PublicCategory, error) {
	var rows []PublicCategory
	err := s.db.Table("tenant_categories c").
		Select("DISTINCT c.id, c.name").
		Joins("JOIN tenant_products p ON p.category_id = c.id").
		Where("p.show_in_digital_catalog = ? AND p.active = ? AND p.deleted_at IS NULL", true, true).
		Where("c.deleted_at IS NULL").
		Order("c.name ASC").
		Scan(&rows).Error
	return rows, err
}

// PublicProducts reusa ProductService.ListReport (ya trae stock_total/stock_by_branch) filtrando
// solo lo publicado en el Catálogo Digital.
func (s *EcommerceService) PublicProducts(query string, categoryID uint, minPrice, maxPrice *float64, page, perPage int) ([]productservice.ProductReportItem, int64, error) {
	psvc := productservice.NewProductService(s.db)
	params := productservice.ProductListParams{
		Query:                    query,
		CategoryID:               categoryID,
		ActiveOnly:               true,
		ShowInDigitalCatalogOnly: true,
		ExcludeCombos:            true, // v1: sin combos/configurables en la tienda pública
		MinPrice:                 minPrice,
		MaxPrice:                 maxPrice,
	}
	if perPage > 0 {
		if page < 1 {
			page = 1
		}
		params.Limit = perPage
		params.Offset = (page - 1) * perPage
	}
	return psvc.ListReport(params)
}

// ── Pedidos ──────────────────────────────────────────────────────────

type OrderItemInput struct {
	ProductID uint    `json:"product_id"`
	Name      string  `json:"name"`
	Quantity  float64 `json:"quantity"`
	UnitPrice float64 `json:"unit_price"`
}

type CreateOrderInput struct {
	CustomerName  string
	CustomerPhone string
	Items         []OrderItemInput
}

func (s *EcommerceService) CreateOrder(input CreateOrderInput) (*database.TenantEcommerceOrder, error) {
	if len(input.Items) == 0 {
		return nil, fmt.Errorf("el pedido no tiene productos")
	}
	if strings.TrimSpace(input.CustomerName) == "" {
		return nil, fmt.Errorf("el nombre del cliente es obligatorio")
	}
	if strings.TrimSpace(input.CustomerPhone) == "" {
		return nil, fmt.Errorf("el celular del cliente es obligatorio")
	}
	total := 0.0
	for _, it := range input.Items {
		total += it.Quantity * it.UnitPrice
	}
	itemsJSON, err := json.Marshal(input.Items)
	if err != nil {
		return nil, err
	}
	order := &database.TenantEcommerceOrder{
		CustomerName:  strings.TrimSpace(input.CustomerName),
		CustomerPhone: strings.TrimSpace(input.CustomerPhone),
		ItemsJSON:     string(itemsJSON),
		Total:         total,
		Status:        "nuevo",
	}
	if err := s.db.Create(order).Error; err != nil {
		return nil, err
	}
	return order, nil
}

func (s *EcommerceService) ListOrders(status string, limit int) ([]database.TenantEcommerceOrder, error) {
	q := s.db.Model(&database.TenantEcommerceOrder{})
	if status != "" && status != "all" {
		q = q.Where("status = ?", status)
	}
	if limit <= 0 {
		limit = 100
	}
	var rows []database.TenantEcommerceOrder
	err := q.Order("created_at DESC").Limit(limit).Find(&rows).Error
	return rows, err
}

var validOrderStatuses = map[string]bool{"nuevo": true, "atendido": true, "cerrado": true, "cancelado": true}

func (s *EcommerceService) UpdateOrderStatus(id uint, status string) error {
	if !validOrderStatuses[status] {
		return fmt.Errorf("estado inválido")
	}
	return s.db.Model(&database.TenantEcommerceOrder{}).Where("id = ?", id).Update("status", status).Error
}
