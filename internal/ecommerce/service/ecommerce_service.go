package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	companyservice "tukifac/internal/company/service"
	productservice "tukifac/internal/products/service"
	"tukifac/pkg/database"
	"tukifac/pkg/money"
	"tukifac/pkg/notificationevents"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
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
	ShowStock      *bool
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
	if input.ShowStock != nil {
		upd["show_stock"] = *input.ShowStock
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
// solo lo publicado en el Catálogo Digital. Cuando showStock=false (Módulos → Tienda Virtual →
// General → "Mostrar stock") el stock/disponibilidad se despoja de la respuesta pública: no basta
// con ocultarlo solo en el frontend, esto viaja sin autenticación.
func (s *EcommerceService) PublicProducts(query string, categoryID uint, minPrice, maxPrice *float64, page, perPage int, showStock bool) ([]productservice.ProductReportItem, int64, error) {
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
	items, total, err := psvc.ListReport(params)
	if err != nil {
		return items, total, err
	}
	for i := range items {
		// PurchasePrice (costo/precio de compra) es dato interno del tenant: nunca debe viajar en
		// esta respuesta pública sin autenticación, sin importar la config de "Mostrar stock".
		items[i].PurchasePrice = 0
		if !showStock {
			items[i].StockTotal = 0
			items[i].StockByBranch = nil
			items[i].Serials = nil
			items[i].SerialCount = 0
			// Las presentaciones en sí (id/name/sale_price) se mantienen — el cliente sigue
			// necesitando elegir una para agregar al carrito, "Mostrar stock" solo oculta el
			// número de disponibilidad, igual que ya hace con StockTotal.
			for j := range items[i].Presentations {
				items[i].Presentations[j].Stock = 0
			}
		}
	}
	return items, total, nil
}

// ── Pedidos ──────────────────────────────────────────────────────────

// OrderItemInput representación INTERNA de una línea ya resuelta (nombre/precio reales, no lo que
// mandó el cliente) — es lo que se serializa en ItemsJSON y lo que lee ConvertToSale/print_data.go.
// No confundir con CreateOrderItemInput (lo que sí puede mandar el cliente).
type OrderItemInput struct {
	ProductID uint    `json:"product_id"`
	Name      string  `json:"name"`
	Quantity  float64 `json:"quantity"`
	UnitPrice float64 `json:"unit_price"`
}

// CreateOrderItemInput lo único que el cliente público puede enviar por línea: identidad +
// cantidad. Deliberadamente SIN name/unit_price/subtotal — CreateOrder los resuelve siempre desde
// el catálogo real del tenant (Contrato v2 Fase 3, "el backend nunca confía en precio/nombre
// enviado por el cliente").
type CreateOrderItemInput struct {
	ProductID uint
	// PresentationID: nil = producto simple. Si viene informado, debe pertenecer a ProductID,
	// estar activo, y existir en ESTE tenant — todo se valida contra la BD, nunca se confía en el
	// nombre/precio que el frontend ya tenía cacheado del catálogo.
	PresentationID *uint
	Quantity       float64
}

const (
	DeliveryMethodPickup   = "RECOJO_TIENDA"
	DeliveryMethodShipping = "ENVIO_DOMICILIO"
)

// CreateOrderInput CustomerAccountID/ContactID/DeliveryAddressID: soportados a nivel de servicio
// para cuando exista checkout autenticado (Fase 4), pero el endpoint público (sin login) nunca los
// llena — no hay forma de autenticar a un cliente final todavía.
type CreateOrderInput struct {
	CustomerName      string
	CustomerPhone     string
	DeliveryMethod    string // RECOJO_TIENDA | ENVIO_DOMICILIO
	GuestAddressLine  string
	GuestReference    string
	GuestUbigeo       string
	CustomerAccountID *uint
	ContactID         *uint
	DeliveryAddressID *uint
	Items             []CreateOrderItemInput
	// CentralTenantID solo para la señal SSE post-commit (Fase 6, notificationevents.PublishChanged)
	// — nunca se usa para resolver datos, s.db ya está scopeado a la BD de este tenant.
	CentralTenantID uint
}

// resolvedOrderLine línea ya validada/resuelta contra el catálogo real — nunca construida a partir
// de datos crudos del cliente.
type resolvedOrderLine struct {
	ProductID      uint
	PresentationID *uint
	Name           string
	Quantity       float64
	UnitPrice      float64
	Subtotal       float64
}

// resolveOrderLine valida y resuelve UNA línea contra el catálogo del tenant actual (s.db, ya
// scopeado a esa BD — no existe forma de que un product_id de otro tenant "exista" acá, cada
// tenant vive en su propia base). Reglas: el producto debe existir, estar activo y publicado en el
// Catálogo Digital (no se puede pedir un producto oculto adivinando su ID); si se manda
// presentation_id, debe pertenecer a ESE producto y estar activa; si el producto tiene variantes,
// presentation_id es obligatorio (no se puede pedir "a ciegas" sin elegir una).
func (s *EcommerceService) resolveOrderLine(it CreateOrderItemInput) (resolvedOrderLine, error) {
	if it.ProductID == 0 {
		return resolvedOrderLine{}, fmt.Errorf("producto inválido en el pedido")
	}
	if !(it.Quantity > 0) {
		return resolvedOrderLine{}, fmt.Errorf("la cantidad debe ser mayor a cero")
	}
	var product database.TenantProduct
	if err := s.db.Where("id = ? AND active = ? AND show_in_digital_catalog = ?", it.ProductID, true, true).
		First(&product).Error; err != nil {
		return resolvedOrderLine{}, fmt.Errorf("uno de los productos del pedido ya no está disponible")
	}

	name := product.Name
	unitPrice := product.SalePrice
	var presentationID *uint
	if it.PresentationID != nil && *it.PresentationID > 0 {
		var pres database.TenantProductPresentation
		if err := s.db.Where("id = ? AND product_id = ? AND active = ?", *it.PresentationID, product.ID, true).
			First(&pres).Error; err != nil {
			return resolvedOrderLine{}, fmt.Errorf("la presentación elegida para '%s' ya no está disponible", product.Name)
		}
		name = product.Name + " — " + pres.Name
		unitPrice = pres.SalePrice
		id := pres.ID
		presentationID = &id
	} else if product.HasVariants {
		return resolvedOrderLine{}, fmt.Errorf("'%s' requiere elegir una presentación", product.Name)
	}
	if !(unitPrice > 0) {
		return resolvedOrderLine{}, fmt.Errorf("'%s' no tiene un precio de venta válido (S/ 0.00)", name)
	}

	qty := it.Quantity
	return resolvedOrderLine{
		ProductID: product.ID, PresentationID: presentationID, Name: name,
		Quantity: qty, UnitPrice: unitPrice, Subtotal: money.RoundDisplay(qty * unitPrice),
	}, nil
}

// CreateOrder crea el pedido y sus líneas dentro de una única transacción: si cualquier
// validación/resolución falla, no queda ningún registro a medias (nunca un pedido "fantasma" sin
// líneas, ni líneas sin pedido). Devuelve las líneas ya resueltas para que el caller (handler) las
// use en la respuesta pública sin tener que releerlas.
func (s *EcommerceService) CreateOrder(input CreateOrderInput) (*database.TenantEcommerceOrder, []database.TenantEcommerceOrderItem, error) {
	if len(input.Items) == 0 {
		return nil, nil, fmt.Errorf("el pedido no tiene productos")
	}
	if strings.TrimSpace(input.CustomerName) == "" {
		return nil, nil, fmt.Errorf("el nombre del cliente es obligatorio")
	}
	if strings.TrimSpace(input.CustomerPhone) == "" {
		return nil, nil, fmt.Errorf("el celular del cliente es obligatorio")
	}
	deliveryMethod := strings.ToUpper(strings.TrimSpace(input.DeliveryMethod))
	if deliveryMethod != DeliveryMethodPickup && deliveryMethod != DeliveryMethodShipping {
		return nil, nil, fmt.Errorf("método de entrega inválido")
	}
	hasAuthenticatedAddress := input.DeliveryAddressID != nil && *input.DeliveryAddressID > 0
	if hasAuthenticatedAddress {
		// La dirección debe pertenecer al cliente autenticado — nunca se confía en el
		// delivery_address_id "porque sí": si CustomerAccountID no viene (no debería poder pasar,
		// el handler solo lo llena desde el token, nunca del body) o la dirección es de otro
		// cliente, se rechaza acá, no en el handler (defensa en profundidad).
		if input.CustomerAccountID == nil {
			return nil, nil, fmt.Errorf("dirección inválida")
		}
		if _, err := s.loadOwnedAddress(*input.CustomerAccountID, *input.DeliveryAddressID); err != nil {
			return nil, nil, fmt.Errorf("la dirección seleccionada no es válida")
		}
	}
	if deliveryMethod == DeliveryMethodShipping && !hasAuthenticatedAddress && strings.TrimSpace(input.GuestAddressLine) == "" {
		return nil, nil, fmt.Errorf("la dirección de entrega es obligatoria para envío a domicilio")
	}
	if ubigeo := strings.TrimSpace(input.GuestUbigeo); ubigeo != "" && len(ubigeo) != 6 {
		return nil, nil, fmt.Errorf("el ubigeo debe tener 6 dígitos")
	}

	lines := make([]resolvedOrderLine, 0, len(input.Items))
	for _, it := range input.Items {
		line, err := s.resolveOrderLine(it)
		if err != nil {
			return nil, nil, err
		}
		lines = append(lines, line)
	}

	total := 0.0
	for _, l := range lines {
		total += l.Subtotal
	}
	total = money.RoundDisplay(total)

	legacyItems := make([]OrderItemInput, len(lines))
	for i, l := range lines {
		legacyItems[i] = OrderItemInput{ProductID: l.ProductID, Name: l.Name, Quantity: l.Quantity, UnitPrice: l.UnitPrice}
	}
	itemsJSON, err := json.Marshal(legacyItems)
	if err != nil {
		return nil, nil, err
	}

	order := &database.TenantEcommerceOrder{
		CustomerName:      strings.TrimSpace(input.CustomerName),
		CustomerPhone:     strings.TrimSpace(input.CustomerPhone),
		CustomerAccountID: input.CustomerAccountID,
		ContactID:         input.ContactID,
		DeliveryMethod:    deliveryMethod,
		DeliveryAddressID: input.DeliveryAddressID,
		ItemsJSON:         string(itemsJSON), // dual-write: se sigue llenando, ver comentario en el modelo
		Subtotal:          total,             // sin descuentos/impuestos a nivel de pedido todavía
		Total:             total,
		// PaymentStatus NO se toma de input (CreateOrderInput no lo expone): el cliente público no
		// puede enviarlo ni modificarlo. Fijo en NO_APLICA mientras no exista pasarela de pago.
		PaymentStatus: "NO_APLICA",
		Status:        OrderStatusPendiente,
	}
	if !hasAuthenticatedAddress {
		order.GuestAddressLine = strings.TrimSpace(input.GuestAddressLine)
		order.GuestReference = strings.TrimSpace(input.GuestReference)
		order.GuestUbigeo = strings.TrimSpace(input.GuestUbigeo)
	}

	var createdItems []database.TenantEcommerceOrderItem
	if err := s.db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(order).Error; err != nil {
			return err
		}
		createdItems = make([]database.TenantEcommerceOrderItem, len(lines))
		for i, l := range lines {
			createdItems[i] = database.TenantEcommerceOrderItem{
				OrderID: order.ID, ProductID: l.ProductID, PresentationID: l.PresentationID,
				Name: l.Name, Quantity: l.Quantity, UnitPrice: l.UnitPrice, Subtotal: l.Subtotal,
			}
		}
		if err := tx.Create(&createdItems).Error; err != nil {
			return err
		}
		// FromStatus="" (no "transición" real, es la creación) — igual documenta desde cuándo el
		// pedido existe en PENDIENTE (Contrato v2 §5, fila "Cliente finaliza checkout").
		if err := tx.Create(&database.TenantEcommerceOrderStatusHistory{
			OrderID: order.ID, FromStatus: "", ToStatus: OrderStatusPendiente,
		}).Error; err != nil {
			return err
		}
		// Notificación interna (Contrato v2 §1.7). Solo la fila: el hub SSE/badge que la entrega en
		// vivo al panel es Fase 6, todavía no implementado — acá únicamente se persiste.
		return tx.Create(&database.TenantNotification{
			Type:     "ecommerce.order.created",
			Title:    fmt.Sprintf("Nuevo pedido online #%d", order.ID),
			Body:     fmt.Sprintf("%s — S/ %.2f", order.CustomerName, total),
			LinkPath: fmt.Sprintf("/sales/pedidos-web?id=%d", order.ID),
		}).Error
	}); err != nil {
		// Nada de lo de arriba queda persistido (rollback de la transacción) — jamás se abre
		// WhatsApp para un pedido que no llegó a existir de verdad.
		return nil, nil, err
	}
	// Señal SSE DESPUÉS del commit (Fase 6): si se publicara antes y la transacción hiciera
	// rollback, el panel refrescaría para una notificación que nunca llegó a existir. Fire-and-
	// forget: nunca bloquea ni falla la respuesta al cliente público.
	notificationevents.PublishChanged(context.Background(), input.CentralTenantID)
	return order, createdItems, nil
}

func (s *EcommerceService) GetOrder(id uint) (*database.TenantEcommerceOrder, error) {
	var order database.TenantEcommerceOrder
	if err := s.db.First(&order, id).Error; err != nil {
		return nil, fmt.Errorf("pedido no encontrado")
	}
	return &order, nil
}

// ListOrdersParams filtros del panel de pedidos web (Contrato v2 Fase 5) — todos opcionales,
// combinables. Query busca por nombre/teléfono (LIKE) o por ID exacto si es numérico.
type ListOrdersParams struct {
	Status   string
	BranchID uint
	Query    string
	DateFrom *time.Time
	DateTo   *time.Time
	Limit    int
}

func (s *EcommerceService) ListOrders(params ListOrdersParams) ([]database.TenantEcommerceOrder, error) {
	q := s.db.Model(&database.TenantEcommerceOrder{})
	if params.Status != "" && params.Status != "all" {
		q = q.Where("status = ?", strings.ToUpper(params.Status))
	}
	if params.BranchID > 0 {
		q = q.Where("branch_id = ?", params.BranchID)
	}
	if query := strings.TrimSpace(params.Query); query != "" {
		term := "%" + query + "%"
		if id, err := strconv.ParseUint(query, 10, 32); err == nil {
			q = q.Where("id = ? OR customer_name LIKE ? OR customer_phone LIKE ?", id, term, term)
		} else {
			q = q.Where("customer_name LIKE ? OR customer_phone LIKE ?", term, term)
		}
	}
	if params.DateFrom != nil {
		q = q.Where("created_at >= ?", params.DateFrom)
	}
	if params.DateTo != nil {
		q = q.Where("created_at <= ?", params.DateTo)
	}
	limit := params.Limit
	if limit <= 0 {
		limit = 100
	}
	var rows []database.TenantEcommerceOrder
	err := q.Order("created_at DESC").Limit(limit).Find(&rows).Error
	return rows, err
}

// GetOrderDetail pedido + líneas + historial de transiciones + despacho (si existe), para el
// detalle del panel (Contrato v2 Fase 5/8). Líneas: prioriza TenantEcommerceOrderItem (normalizado,
// con PresentationID real); si un pedido legacy no tiene filas ahí, cae a deserializar ItemsJSON —
// mismo criterio de fallback que ya usa ConvertToSale (internal/ecommerce/service/convert.go
// loadOrderItems), nunca se deja un pedido sin poder mostrar sus productos. Dispatch: nil sin error
// si el pedido todavía no tiene uno — nunca un 404 solo por no estar despachado todavía.
func (s *EcommerceService) GetOrderDetail(id uint) (*database.TenantEcommerceOrder, []database.TenantEcommerceOrderItem, []database.TenantEcommerceOrderStatusHistory, *database.TenantEcommerceDispatch, error) {
	var order database.TenantEcommerceOrder
	if err := s.db.First(&order, id).Error; err != nil {
		return nil, nil, nil, nil, fmt.Errorf("pedido no encontrado")
	}
	var items []database.TenantEcommerceOrderItem
	if err := s.db.Where("order_id = ?", id).Order("id ASC").Find(&items).Error; err != nil {
		return nil, nil, nil, nil, err
	}
	if len(items) == 0 && strings.TrimSpace(order.ItemsJSON) != "" {
		var legacy []OrderItemInput
		if err := json.Unmarshal([]byte(order.ItemsJSON), &legacy); err == nil {
			for _, l := range legacy {
				items = append(items, database.TenantEcommerceOrderItem{
					OrderID: order.ID, ProductID: l.ProductID, Name: l.Name,
					Quantity: l.Quantity, UnitPrice: l.UnitPrice, Subtotal: money.RoundDisplay(l.Quantity * l.UnitPrice),
				})
			}
		}
	}
	var history []database.TenantEcommerceOrderStatusHistory
	if err := s.db.Where("order_id = ?", id).Order("created_at ASC, id ASC").Find(&history).Error; err != nil {
		return nil, nil, nil, nil, err
	}
	dispatch, err := s.GetDispatchByOrderID(id)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	return &order, items, history, dispatch, nil
}

// UpdateOrderStatusInput ver Contrato v2 §5/§6.2. BranchID: solo se aplica en la transición
// PENDIENTE→CONFIRMADO y solo si el pedido no tenía sucursal todavía (Contrato v2 §1.1).
type UpdateOrderStatusInput struct {
	NewStatus string
	UserID    uint
	Notes     string
	BranchID  *uint
	// CentralTenantID solo para la señal SSE post-commit (Fase 6), ver comentario en CreateOrderInput.
	CentralTenantID uint
}

// notificationForTransition notificación interna a crear para una transición concreta (Contrato v2
// §1.7, Fase 6) — solo las 3 transiciones "relevantes" pedidas: PENDIENTE→CONFIRMADO, →CANCELADO,
// →RECHAZADO. Cualquier otra transición (preparación, empaquetado, despacho...) no genera
// notificación: no tiene sentido operativo avisar de cada micro-cambio de picking.
func notificationForTransition(orderID uint, fromStatus, newStatus string) *database.TenantNotification {
	link := fmt.Sprintf("/sales/pedidos-web?id=%d", orderID)
	switch {
	case fromStatus == OrderStatusPendiente && newStatus == OrderStatusConfirmado:
		return &database.TenantNotification{
			Type: "ecommerce.order.confirmed", LinkPath: link,
			Title: fmt.Sprintf("Pedido #%d confirmado", orderID),
			Body:  "El pedido pasó a preparación.",
		}
	case newStatus == OrderStatusCancelado:
		return &database.TenantNotification{
			Type: "ecommerce.order.cancelled", LinkPath: link,
			Title: fmt.Sprintf("Pedido #%d cancelado", orderID),
			Body:  "El pedido fue cancelado.",
		}
	case newStatus == OrderStatusRechazado:
		return &database.TenantNotification{
			Type: "ecommerce.order.cancelled", LinkPath: link,
			Title: fmt.Sprintf("Pedido #%d rechazado", orderID),
			Body:  "El pedido fue rechazado.",
		}
	default:
		return nil
	}
}

// UpdateOrderStatus valida la transición contra orderTransitions (order_status.go) y la registra
// en TenantEcommerceOrderStatusHistory. La verificación de PERMISO específico de la transición
// (qué exige orderTransitions[i].Permission) es responsabilidad del handler, que conoce los claims
// del usuario autenticado — este método revalida solo la transición en sí (defensa en profundidad:
// nunca confía en que el caller ya la validó).
//
// Auditoría de Fase 11 (Parte H — idempotencia/concurrencia): antes de esta fase, el pedido se leía
// FUERA de la transacción y se actualizaba con un UPDATE ciego adentro — dos requests concurrentes
// sobre el MISMO pedido podían leer el mismo estado "viejo", pasar ambas la validación, y las dos
// escribir un UPDATE + una fila de historial (duplicando TenantEcommerceOrderStatusHistory con un
// FromStatus potencialmente obsoleto). Se corrigió con el mismo patrón SELECT...FOR UPDATE ya usado
// en CreateDispatch/MarkDispatchInTransit/MarkDispatchDelivered (Fase 1.5/8/9): el pedido se
// bloquea y se revalida DENTRO de la transacción, así que la segunda de dos transiciones
// concurrentes siempre relee el estado ya actualizado por la primera y se rechaza limpio.
func (s *EcommerceService) UpdateOrderStatus(id uint, input UpdateOrderStatusInput) error {
	newStatus := strings.ToUpper(strings.TrimSpace(input.NewStatus))
	if !validOrderStatuses[newStatus] {
		return fmt.Errorf("estado inválido: %s", newStatus)
	}
	if requiresReasonNotes(newStatus) && strings.TrimSpace(input.Notes) == "" {
		return fmt.Errorf("indica el motivo")
	}
	var fromStatus string
	if err := s.db.Transaction(func(tx *gorm.DB) error {
		var order database.TenantEcommerceOrder
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&order, id).Error; err != nil {
			return fmt.Errorf("pedido no encontrado")
		}
		fromStatus = order.Status
		if _, ok := FindOrderTransition(fromStatus, newStatus); !ok {
			return fmt.Errorf("no se puede pasar de %s a %s", fromStatus, newStatus)
		}

		updates := map[string]interface{}{"status": newStatus}
		if newStatus == OrderStatusConfirmado && order.BranchID == nil && input.BranchID != nil {
			updates["branch_id"] = *input.BranchID
		}
		if err := tx.Model(&database.TenantEcommerceOrder{}).Where("id = ?", id).Updates(updates).Error; err != nil {
			return err
		}
		var userID *uint
		if input.UserID > 0 {
			userID = &input.UserID
		}
		if err := tx.Create(&database.TenantEcommerceOrderStatusHistory{
			OrderID:    id,
			FromStatus: fromStatus,
			ToStatus:   newStatus,
			UserID:     userID,
			Notes:      strings.TrimSpace(input.Notes),
		}).Error; err != nil {
			return err
		}
		// Notificación interna (Contrato v2 §1.7, Fase 6) — misma transacción que el cambio de
		// estado: si algo de arriba falla, tampoco queda una notificación de un cambio que no
		// llegó a persistir.
		if notif := notificationForTransition(id, fromStatus, newStatus); notif != nil {
			if err := tx.Create(notif).Error; err != nil {
				return err
			}
		}
		// Deuda técnica #8 (Fase 9->11): cuando el pedido se marca DEVUELTO, sincronizar el
		// Dispatch asociado si existe y sigue ENTREGADO — misma transacción, para que
		// OrderStatusHistory y DispatchStatusHistory queden consistentes. Ver
		// syncDispatchOnOrderReturned.
		if newStatus == OrderStatusDevuelto {
			if err := syncDispatchOnOrderReturned(tx, id, userID, input.Notes); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return err
	}
	notificationevents.PublishChanged(context.Background(), input.CentralTenantID)
	return nil
}

// syncDispatchOnOrderReturned Deuda técnica #8 (auditada y corregida en Fase 11) — cuando
// UpdateOrderStatus mueve el pedido a DEVUELTO, el TenantEcommerceDispatch asociado (si existe y
// sigue en ENTREGADO) se sincroniza al mismo estado dentro de la MISMA transacción, escribiendo su
// propio TenantEcommerceDispatchStatusHistory — nunca se mezcla con el historial del pedido (los dos
// dominios siguen completamente separados, Contrato v2 §1.7/§8).
//
// No es una segunda máquina de estados: es la misma decisión que ya rige CreateDispatch/
// MarkDispatchDelivered (un cambio en un dominio que, cuando corresponde, empuja el cambio
// correspondiente en el otro dentro de la misma transacción).
//
// Casos cubiertos:
//   - Pedido sin Dispatch (legacy, o recojo en tienda sin despacho registrado): gorm.ErrRecordNotFound,
//     no hay nada que sincronizar, no es un error.
//   - Dispatch ya no está en ENTREGADO: no se fuerza el salto — el pedido es la fuente de verdad de
//     la devolución, nunca se inventa una transición de Dispatch que no corresponde. En la práctica
//     esto no debería pasar (ENTREGADO del pedido solo se alcanza vía MarkDispatchDelivered, que
//     siempre deja también el Dispatch en ENTREGADO), pero se revalida igual (defensa en profundidad
//     ante datos inconsistentes, mismo criterio que MarkDispatchDelivered revalida Order.Status).
//   - Doble click / retry / concurrencia: idempotente por construcción — el pedido solo puede llegar
//     a DEVUELTO una vez (no existe DEVUELTO->DEVUELTO en orderTransitions), así que este bloque
//     nunca vuelve a ejecutarse para el mismo pedido; un segundo intento se rechaza limpio antes de
//     llegar acá (fromStatus ya no sería ENTREGADO). El SELECT...FOR UPDATE sobre el Dispatch cierra
//     además la ventana de carrera con cualquier otra operación concurrente sobre el mismo Dispatch.
func syncDispatchOnOrderReturned(tx *gorm.DB, orderID uint, userID *uint, notes string) error {
	var dispatch database.TenantEcommerceDispatch
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("order_id = ?", orderID).First(&dispatch).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if dispatch.Status != DispatchStatusEntregado {
		return nil
	}
	if err := tx.Model(&database.TenantEcommerceDispatch{}).Where("id = ?", dispatch.ID).
		Update("status", DispatchStatusDevuelto).Error; err != nil {
		return err
	}
	return tx.Create(&database.TenantEcommerceDispatchStatusHistory{
		DispatchID: dispatch.ID,
		FromStatus: DispatchStatusEntregado,
		ToStatus:   DispatchStatusDevuelto,
		UserID:     userID,
		Notes:      strings.TrimSpace(notes),
	}).Error
}
