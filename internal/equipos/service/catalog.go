package service

import (
	"errors"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"tukifac/pkg/database"

	"gorm.io/gorm"
)

var validKinds = map[string]bool{
	database.EquipKindEquipo: true, database.EquipKindConsumible: true,
	database.EquipKindEtiqueta: true, database.EquipKindAccesorio: true,
}

// ── Productos ───────────────────────────────────────────────────────────────

// ProductInput datos editables de un producto.
type ProductInput struct {
	Code            string  `json:"code"`
	Name            string  `json:"name"`
	Kind            string  `json:"kind"`
	Notes           string  `json:"notes"`
	ReferencePrice  float64 `json:"reference_price"`
	YellowThreshold int     `json:"yellow_threshold"`
	GreenThreshold  int     `json:"green_threshold"`
	Active          *bool   `json:"active"`
}

// ProductWithStock producto con su stock actual (suma de todo el kardex).
type ProductWithStock struct {
	database.EquipProduct
	Stock int `json:"stock"` // Almacén 1 (el que vende)
	// StockDepot lo que hay en los demás almacenes (depósitos): no se puede vender hasta transferirlo al Almacén 1.
	StockDepot int    `json:"stock_depot"`
	Semaphore  string `json:"semaphore"`
	// LastPrice último precio unitario cobrado en un pedido vigente (0 si nunca se vendió): sugerencia
	// de precio cuando el producto no tiene precio de referencia.
	LastPrice float64 `json:"last_price"`
}

// lastSalePrices devuelve, por producto, el precio unitario de su línea más reciente (sin obsequios ni anulados).
func (s *Service) lastSalePrices() (map[uint]float64, error) {
	var rows []struct {
		ProductID uint
		UnitPrice float64
	}
	err := s.db.Raw(`SELECT i.product_id, i.unit_price FROM equip_order_items i
		JOIN (SELECT MAX(i2.id) AS mid FROM equip_order_items i2
			JOIN equip_orders o ON o.id = i2.order_id
			WHERE o.status <> 'anulado' AND i2.is_courtesy = 0 AND i2.unit_price > 0 AND i2.product_id IS NOT NULL
			GROUP BY i2.product_id) m ON m.mid = i.id`).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make(map[uint]float64, len(rows))
	for _, r := range rows {
		out[r.ProductID] = r.UnitPrice
	}
	return out, nil
}

func (in *ProductInput) normalize() error {
	in.Code = strings.TrimSpace(in.Code)
	in.Name = strings.TrimSpace(in.Name)
	in.Kind = strings.TrimSpace(in.Kind)
	in.Notes = strings.TrimSpace(in.Notes)
	if in.Code == "" {
		return invalid("el código es obligatorio")
	}
	if len(in.Code) > 60 {
		return invalid("el código no puede superar 60 caracteres")
	}
	if in.Name == "" {
		in.Name = in.Code
	}
	if in.Kind == "" {
		in.Kind = database.EquipKindEquipo
	}
	if !validKinds[in.Kind] {
		return invalid("tipo de producto inválido (equipo, consumible, etiqueta o accesorio)")
	}
	if in.ReferencePrice < 0 {
		return invalid("el precio de referencia no puede ser negativo")
	}
	if in.YellowThreshold < 0 || in.GreenThreshold < 0 {
		return invalid("los umbrales no pueden ser negativos")
	}
	if in.YellowThreshold > in.GreenThreshold {
		return invalid("el umbral amarillo no puede ser mayor que el verde")
	}
	return nil
}

func (s *Service) ListProducts(q, kind string, includeInactive bool) ([]ProductWithStock, error) {
	var rows []database.EquipProduct
	tx := s.db.Model(&database.EquipProduct{})
	if !includeInactive {
		tx = tx.Where("active = ?", true)
	}
	if kind = strings.TrimSpace(kind); kind != "" {
		tx = tx.Where("kind = ?", kind)
	}
	if q = strings.TrimSpace(q); q != "" {
		like := "%" + strings.ToLower(q) + "%"
		tx = tx.Where("LOWER(code) LIKE ? OR LOWER(name) LIKE ?", like, like)
	}
	if err := tx.Order("kind ASC, code ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	stock, err := s.stockTotals()
	if err != nil {
		return nil, err
	}
	depot, err := s.stockDepot()
	if err != nil {
		return nil, err
	}
	last, err := s.lastSalePrices()
	if err != nil {
		return nil, err
	}
	out := make([]ProductWithStock, 0, len(rows))
	for _, p := range rows {
		cur := stock[p.ID]
		out = append(out, ProductWithStock{EquipProduct: p, Stock: cur, StockDepot: depot[p.ID], Semaphore: semaphore(cur, p.YellowThreshold, p.GreenThreshold), LastPrice: last[p.ID]})
	}
	return out, nil
}

func (s *Service) codeTaken(code string, exceptProductID uint) (bool, error) {
	var n int64
	tx := s.db.Model(&database.EquipProduct{}).Where("LOWER(code) = ?", strings.ToLower(code))
	if exceptProductID > 0 {
		tx = tx.Where("id <> ?", exceptProductID)
	}
	if err := tx.Count(&n).Error; err != nil {
		return false, err
	}
	if n > 0 {
		return true, nil
	}
	// El código de un producto tampoco puede coincidir con el de un combo: el detalle de un pedido
	// los resuelve por nombre.
	if err := s.db.Model(&database.EquipCombo{}).Where("LOWER(code) = ?", strings.ToLower(code)).Count(&n).Error; err != nil {
		return false, err
	}
	return n > 0, nil
}

func (s *Service) CreateProduct(in ProductInput) (*database.EquipProduct, error) {
	if err := in.normalize(); err != nil {
		return nil, err
	}
	if taken, err := s.codeTaken(in.Code, 0); err != nil {
		return nil, err
	} else if taken {
		return nil, invalid("ya existe un producto o combo con el código %q", in.Code)
	}
	p := &database.EquipProduct{
		Code: in.Code, Name: in.Name, Kind: in.Kind, Notes: in.Notes, ReferencePrice: in.ReferencePrice,
		YellowThreshold: in.YellowThreshold, GreenThreshold: in.GreenThreshold, Active: true,
	}
	if in.Active != nil {
		p.Active = *in.Active
	}
	inactive := !p.Active
	if err := s.db.Create(p).Error; err != nil {
		return nil, err
	}
	if inactive {
		// gorm trata false como "sin valor" y aplica el default:true de la columna al insertar.
		if err := s.db.Model(p).Update("active", false).Error; err != nil {
			return nil, err
		}
	}
	return p, nil
}

func (s *Service) UpdateProduct(id uint, in ProductInput) (*database.EquipProduct, error) {
	var p database.EquipProduct
	if err := s.db.First(&p, id).Error; err != nil {
		return nil, invalid("producto no encontrado")
	}
	if err := in.normalize(); err != nil {
		return nil, err
	}
	if !strings.EqualFold(in.Code, p.Code) {
		if taken, err := s.codeTaken(in.Code, id); err != nil {
			return nil, err
		} else if taken {
			return nil, invalid("ya existe un producto o combo con el código %q", in.Code)
		}
	}
	p.Code, p.Name, p.Kind, p.Notes = in.Code, in.Name, in.Kind, in.Notes
	p.ReferencePrice, p.YellowThreshold, p.GreenThreshold = in.ReferencePrice, in.YellowThreshold, in.GreenThreshold
	if in.Active != nil {
		p.Active = *in.Active
	}
	if err := s.db.Save(&p).Error; err != nil {
		return nil, err
	}
	return &p, nil
}

// ── Combos ──────────────────────────────────────────────────────────────────

type ComboItemInput struct {
	ProductID uint `json:"product_id"`
	Quantity  int  `json:"quantity"`
}

type ComboInput struct {
	Code           string           `json:"code"`
	Name           string           `json:"name"`
	ReferencePrice float64          `json:"reference_price"`
	Active         *bool            `json:"active"`
	Items          []ComboItemInput `json:"items"`
}

// ComboView combo con sus componentes ya resueltos.
type ComboView struct {
	database.EquipCombo
	Components []ComboComponentView `json:"components"`
}

type ComboComponentView struct {
	ProductID   uint   `json:"product_id"`
	ProductCode string `json:"product_code"`
	ProductName string `json:"product_name"`
	Quantity    int    `json:"quantity"`
}

func (s *Service) ListCombos(includeInactive bool) ([]ComboView, error) {
	var combos []database.EquipCombo
	tx := s.db.Preload("Items")
	if !includeInactive {
		tx = tx.Where("active = ?", true)
	}
	if err := tx.Order("code ASC").Find(&combos).Error; err != nil {
		return nil, err
	}
	var products []database.EquipProduct
	if err := s.db.Find(&products).Error; err != nil {
		return nil, err
	}
	byID := make(map[uint]database.EquipProduct, len(products))
	for _, p := range products {
		byID[p.ID] = p
	}
	out := make([]ComboView, 0, len(combos))
	for _, c := range combos {
		v := ComboView{EquipCombo: c}
		for _, it := range c.Items {
			p := byID[it.ProductID]
			v.Components = append(v.Components, ComboComponentView{ProductID: it.ProductID, ProductCode: p.Code, ProductName: p.Name, Quantity: it.Quantity})
		}
		out = append(out, v)
	}
	return out, nil
}

func (s *Service) validateCombo(in *ComboInput, exceptID uint) error {
	in.Code = strings.TrimSpace(in.Code)
	in.Name = strings.TrimSpace(in.Name)
	if in.Code == "" {
		return invalid("el código del combo es obligatorio")
	}
	if in.Name == "" {
		in.Name = in.Code
	}
	if in.ReferencePrice < 0 {
		return invalid("el precio de referencia no puede ser negativo")
	}
	if len(in.Items) == 0 {
		return invalid("el combo necesita al menos un componente")
	}
	seen := map[uint]bool{}
	for _, it := range in.Items {
		if it.Quantity < 1 {
			return invalid("la cantidad de cada componente debe ser al menos 1")
		}
		if seen[it.ProductID] {
			return invalid("un componente aparece repetido en el combo")
		}
		seen[it.ProductID] = true
		var n int64
		if err := s.db.Model(&database.EquipProduct{}).Where("id = ? AND active = ?", it.ProductID, true).Count(&n).Error; err != nil {
			return err
		}
		if n == 0 {
			return invalid("un componente del combo no existe o está inactivo")
		}
	}
	var n int64
	tx := s.db.Model(&database.EquipCombo{}).Where("LOWER(code) = ?", strings.ToLower(in.Code))
	if exceptID > 0 {
		tx = tx.Where("id <> ?", exceptID)
	}
	if err := tx.Count(&n).Error; err != nil {
		return err
	}
	if n > 0 {
		return invalid("ya existe un combo con el código %q", in.Code)
	}
	if err := s.db.Model(&database.EquipProduct{}).Where("LOWER(code) = ?", strings.ToLower(in.Code)).Count(&n).Error; err != nil {
		return err
	}
	if n > 0 {
		return invalid("el código %q ya lo usa un producto", in.Code)
	}
	return nil
}

func (s *Service) CreateCombo(in ComboInput) (*ComboView, error) {
	if err := s.validateCombo(&in, 0); err != nil {
		return nil, err
	}
	var id uint
	err := s.db.Transaction(func(tx *gorm.DB) error {
		c := database.EquipCombo{Code: in.Code, Name: in.Name, ReferencePrice: in.ReferencePrice, Active: true}
		if in.Active != nil {
			c.Active = *in.Active
		}
		inactive := !c.Active
		if err := tx.Create(&c).Error; err != nil {
			return err
		}
		if inactive {
			// gorm trata false como "sin valor" y aplica el default:true de la columna al insertar.
			if err := tx.Model(&c).Update("active", false).Error; err != nil {
				return err
			}
		}
		id = c.ID
		for _, it := range in.Items {
			if err := tx.Create(&database.EquipComboItem{ComboID: c.ID, ProductID: it.ProductID, Quantity: it.Quantity}).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.getCombo(id)
}

func (s *Service) UpdateCombo(id uint, in ComboInput) (*ComboView, error) {
	var c database.EquipCombo
	if err := s.db.First(&c, id).Error; err != nil {
		return nil, invalid("combo no encontrado")
	}
	if err := s.validateCombo(&in, id); err != nil {
		return nil, err
	}
	err := s.db.Transaction(func(tx *gorm.DB) error {
		c.Code, c.Name, c.ReferencePrice = in.Code, in.Name, in.ReferencePrice
		if in.Active != nil {
			c.Active = *in.Active
		}
		if err := tx.Save(&c).Error; err != nil {
			return err
		}
		if err := tx.Where("combo_id = ?", id).Delete(&database.EquipComboItem{}).Error; err != nil {
			return err
		}
		for _, it := range in.Items {
			if err := tx.Create(&database.EquipComboItem{ComboID: id, ProductID: it.ProductID, Quantity: it.Quantity}).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.getCombo(id)
}

func (s *Service) getCombo(id uint) (*ComboView, error) {
	all, err := s.ListCombos(true)
	if err != nil {
		return nil, err
	}
	for i := range all {
		if all[i].ID == id {
			return &all[i], nil
		}
	}
	return nil, errors.New("combo no encontrado")
}

// ── Transportistas ──────────────────────────────────────────────────────────

type CarrierInput struct {
	Code                  string `json:"code"`
	Name                  string `json:"name"`
	DispatchDays          string `json:"dispatch_days"`
	PickupDays            int    `json:"pickup_days"`
	GuideLabel            string `json:"guide_label"`
	GuideFormat           string `json:"guide_format"`
	TrackingURLTpl        string `json:"tracking_url_template"`
	LabelTemplate         string `json:"label_template"`
	IsDefault             bool   `json:"is_default"`
	Active                *bool  `json:"active"`
	SortOrder             int    `json:"sort_order"`
	DefaultOriginAgencyID *uint  `json:"default_origin_agency_id"`
}

var carrierCodeRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,28}$`)

// NormalizeDispatchDays valida y ordena los días de despacho («1,3,5»; 0 = domingo).
func NormalizeDispatchDays(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", nil
	}
	seen := map[int]bool{}
	for _, part := range strings.Split(raw, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil || n < 0 || n > 6 {
			return "", invalid("los días de despacho deben ser números de 0 (domingo) a 6 (sábado) separados por coma")
		}
		seen[n] = true
	}
	days := make([]int, 0, len(seen))
	for d := range seen {
		days = append(days, d)
	}
	sort.Ints(days)
	parts := make([]string, len(days))
	for i, d := range days {
		parts[i] = strconv.Itoa(d)
	}
	return strings.Join(parts, ","), nil
}

func (in *CarrierInput) normalize() error {
	in.Code = strings.ToLower(strings.TrimSpace(in.Code))
	in.Name = strings.TrimSpace(in.Name)
	if !carrierCodeRe.MatchString(in.Code) {
		return invalid("el código del transportista debe ser corto, en minúsculas, sin espacios (ej. shalom, olva)")
	}
	if in.Name == "" {
		return invalid("el nombre del transportista es obligatorio")
	}
	days, err := NormalizeDispatchDays(in.DispatchDays)
	if err != nil {
		return err
	}
	in.DispatchDays = days
	if in.PickupDays == 0 {
		in.PickupDays = 15
	}
	if in.PickupDays < 1 || in.PickupDays > 90 {
		return invalid("el plazo de recojo debe estar entre 1 y 90 días")
	}
	in.GuideLabel = strings.TrimSpace(in.GuideLabel)
	if in.GuideLabel == "" {
		in.GuideLabel = "N° de guía"
	}
	in.GuideFormat = strings.TrimSpace(in.GuideFormat)
	if in.GuideFormat != "" {
		if _, err := regexp.Compile(in.GuideFormat); err != nil {
			return invalid("el formato de guía no es una expresión regular válida")
		}
	}
	in.TrackingURLTpl = strings.TrimSpace(in.TrackingURLTpl)
	if in.TrackingURLTpl != "" && !strings.Contains(in.TrackingURLTpl, "{guia}") {
		return invalid("el enlace de seguimiento debe incluir {guia}")
	}
	return nil
}

func (s *Service) ListCarriers(includeInactive bool) ([]database.EquipCarrier, error) {
	var rows []database.EquipCarrier
	tx := s.db.Model(&database.EquipCarrier{})
	if !includeInactive {
		tx = tx.Where("active = ?", true)
	}
	if err := tx.Order("sort_order ASC, name ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	var counts []struct {
		CarrierID uint
		N         int
	}
	if err := s.db.Model(&database.EquipCarrierAgency{}).Select("carrier_id, COUNT(*) AS n").Where("active = ?", true).Group("carrier_id").Scan(&counts).Error; err != nil {
		return nil, err
	}
	byCarrier := make(map[uint]int, len(counts))
	for _, c := range counts {
		byCarrier[c.CarrierID] = c.N
	}
	for i := range rows {
		rows[i].AgencyCount = byCarrier[rows[i].ID]
	}
	return rows, nil
}

func (s *Service) CreateCarrier(in CarrierInput) (*database.EquipCarrier, error) {
	if err := in.normalize(); err != nil {
		return nil, err
	}
	var n int64
	if err := s.db.Model(&database.EquipCarrier{}).Where("code = ?", in.Code).Count(&n).Error; err != nil {
		return nil, err
	}
	if n > 0 {
		return nil, invalid("ya existe un transportista con el código %q", in.Code)
	}
	c := &database.EquipCarrier{
		Code: in.Code, Name: in.Name, DispatchDays: in.DispatchDays, PickupDays: in.PickupDays,
		GuideLabel: in.GuideLabel, GuideFormat: in.GuideFormat, TrackingURLTpl: in.TrackingURLTpl,
		LabelTemplate: in.LabelTemplate, SortOrder: in.SortOrder, Active: true,
	}
	if in.Active != nil {
		c.Active = *in.Active
	}
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if in.IsDefault {
			if err := tx.Model(&database.EquipCarrier{}).Where("is_default = ?", true).Update("is_default", false).Error; err != nil {
				return err
			}
			c.IsDefault = true
		}
		inactive := !c.Active
		if err := tx.Create(c).Error; err != nil {
			return err
		}
		if inactive {
			// gorm trata false como "sin valor" y aplica el default:true de la columna al insertar.
			return tx.Model(c).Update("active", false).Error
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return c, nil
}

func (s *Service) UpdateCarrier(id uint, in CarrierInput) (*database.EquipCarrier, error) {
	var c database.EquipCarrier
	if err := s.db.First(&c, id).Error; err != nil {
		return nil, invalid("transportista no encontrado")
	}
	if err := in.normalize(); err != nil {
		return nil, err
	}
	if in.Code != c.Code {
		var n int64
		if err := s.db.Model(&database.EquipCarrier{}).Where("code = ? AND id <> ?", in.Code, id).Count(&n).Error; err != nil {
			return nil, err
		}
		if n > 0 {
			return nil, invalid("ya existe un transportista con el código %q", in.Code)
		}
	}
	err := s.db.Transaction(func(tx *gorm.DB) error {
		if in.IsDefault && !c.IsDefault {
			if err := tx.Model(&database.EquipCarrier{}).Where("is_default = ?", true).Update("is_default", false).Error; err != nil {
				return err
			}
		}
		c.Code, c.Name, c.DispatchDays, c.PickupDays = in.Code, in.Name, in.DispatchDays, in.PickupDays
		c.GuideLabel, c.GuideFormat, c.TrackingURLTpl, c.LabelTemplate = in.GuideLabel, in.GuideFormat, in.TrackingURLTpl, in.LabelTemplate
		c.SortOrder, c.IsDefault = in.SortOrder, in.IsDefault
		if err := s.checkOriginAgency(tx, c.ID, in.DefaultOriginAgencyID); err != nil {
			return err
		}
		c.DefaultOriginAgencyID = in.DefaultOriginAgencyID
		if in.Active != nil {
			c.Active = *in.Active
		}
		if !c.Active && c.IsDefault {
			return invalid("el transportista por defecto no puede quedar inactivo")
		}
		return tx.Save(&c).Error
	})
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// ── Configuración ───────────────────────────────────────────────────────────

type SettingsInput struct {
	DefaultCarrierID *uint  `json:"default_carrier_id"`
	StockManager     string `json:"stock_manager"`
	AlertYellowDays  int    `json:"alert_yellow_days"`
	AlertRedDays     int    `json:"alert_red_days"`
	NextOrderNumber  int    `json:"next_order_number"`
}

func (s *Service) GetSettings() (*database.EquipSettings, error) {
	var st database.EquipSettings
	err := s.db.First(&st, 1).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		st = database.EquipSettings{ID: 1, AlertYellowDays: 5, AlertRedDays: 12, NextOrderNumber: 1}
		if err := s.db.Create(&st).Error; err != nil {
			return nil, err
		}
		return &st, nil
	}
	return &st, err
}

func (s *Service) UpdateSettings(in SettingsInput) (*database.EquipSettings, error) {
	st, err := s.GetSettings()
	if err != nil {
		return nil, err
	}
	if in.AlertYellowDays < 1 || in.AlertRedDays <= in.AlertYellowDays {
		return nil, invalid("los días de alerta deben cumplir 1 ≤ amarillo < rojo")
	}
	if in.NextOrderNumber < 1 {
		return nil, invalid("el siguiente número de pedido debe ser al menos 1")
	}
	var maxNum int
	if err := s.db.Model(&database.EquipOrder{}).Select("COALESCE(MAX(order_number), 0)").Scan(&maxNum).Error; err != nil {
		return nil, err
	}
	if in.NextOrderNumber <= maxNum {
		return nil, invalid("el siguiente número de pedido debe ser mayor que el último existente (%d)", maxNum)
	}
	if in.DefaultCarrierID != nil {
		var n int64
		if err := s.db.Model(&database.EquipCarrier{}).Where("id = ? AND active = ?", *in.DefaultCarrierID, true).Count(&n).Error; err != nil {
			return nil, err
		}
		if n == 0 {
			return nil, invalid("el transportista por defecto no existe o está inactivo")
		}
	}
	st.DefaultCarrierID = in.DefaultCarrierID
	st.StockManager = strings.TrimSpace(in.StockManager)
	st.AlertYellowDays, st.AlertRedDays, st.NextOrderNumber = in.AlertYellowDays, in.AlertRedDays, in.NextOrderNumber
	if err := s.db.Save(st).Error; err != nil {
		return nil, err
	}
	return st, nil
}
