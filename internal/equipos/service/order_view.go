package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"tukifac/pkg/database"

	"gorm.io/gorm"
)

// OrderItemView línea del pedido con datos del catálogo y la marca de precio distinto al de lista.
type OrderItemView struct {
	database.EquipOrderItem
	ProductName    string      `json:"product_name"`
	ReferencePrice float64     `json:"reference_price"`
	PriceDeviation float64     `json:"price_deviation"` // fracción respecto al precio de lista (0.35 = 35 % distinto); 0 si no aplica
	Components     []comboSnap `json:"components,omitempty"`
}

// PaymentLine cobro aplicado a un pedido.
type PaymentLine struct {
	PaymentID uint      `json:"payment_id"`
	PaidAt    time.Time `json:"paid_at"`
	Method    string    `json:"method"`
	Moment    string    `json:"moment"`
	Reference string    `json:"reference"`
	Invoice   string    `json:"invoice"`
	Amount    float64   `json:"amount"` // lo aplicado a ESTE pedido
	Total     float64   `json:"payment_total"`
	Status    string    `json:"status"`
	Notes     string    `json:"notes"`
	// VoucherMime no vacío = el cobro tiene voucher adjunto.
	VoucherMime string `json:"voucher_mime"`
}

// TimelineEvent hito del pedido (cada fecha es un dato distinto: ver §5.5 de la propuesta).
type TimelineEvent struct {
	At     time.Time `json:"at"`
	Kind   string    `json:"kind"`
	Label  string    `json:"label"`
	Future bool      `json:"future,omitempty"`
}

// OrderView pedido completo para la pantalla.
type OrderView struct {
	database.EquipOrder
	Items         []OrderItemView         `json:"items"`
	Customer      *database.EquipCustomer `json:"customer"`
	CreatedByName string                  `json:"created_by_name"`
	Payments      []PaymentLine           `json:"payments"`
	Shipment      *ShipmentView           `json:"shipment"`
	Packing       []PackingLine           `json:"packing"`
	Timeline      []TimelineEvent         `json:"timeline"`
}

func invoiceText(p database.EquipPayment) string {
	if p.InvoiceSeries == "" && p.InvoiceNumber == "" {
		return ""
	}
	return strings.TrimSpace(p.InvoiceSeries + "-" + p.InvoiceNumber)
}

func (s *Service) GetOrder(id uint) (*OrderView, error) {
	var o database.EquipOrder
	if found, err := findOne(s.db, &o, "id = ?", id); err != nil {
		return nil, err
	} else if !found {
		return nil, invalid("pedido no encontrado")
	}
	v := &OrderView{EquipOrder: o, Payments: []PaymentLine{}}
	if o.CreatedBy != nil {
		var name string
		if err := s.db.Table("super_admin_users").Select("name").Where("id = ?", *o.CreatedBy).Scan(&name).Error; err != nil {
			return nil, err
		}
		v.CreatedByName = name
	}

	var items []database.EquipOrderItem
	if err := s.db.Where("order_id = ?", id).Order("line_no ASC, id ASC").Find(&items).Error; err != nil {
		return nil, err
	}
	prodIDs := []uint{}
	for _, it := range items {
		if it.ProductID != nil {
			prodIDs = append(prodIDs, *it.ProductID)
		}
	}
	prods := map[uint]database.EquipProduct{}
	if len(prodIDs) > 0 {
		var ps []database.EquipProduct
		if err := s.db.Where("id IN ?", prodIDs).Find(&ps).Error; err != nil {
			return nil, err
		}
		for _, p := range ps {
			prods[p.ID] = p
		}
	}
	for _, it := range items {
		iv := OrderItemView{EquipOrderItem: it}
		if it.ProductID != nil {
			p := prods[*it.ProductID]
			iv.ProductName, iv.ReferencePrice = p.Name, p.ReferencePrice
			if p.ReferencePrice > 0 && !it.IsCourtesy {
				if dev := math.Abs(it.UnitPrice-p.ReferencePrice) / p.ReferencePrice; dev > 0.3 {
					iv.PriceDeviation = math.Round(dev*100) / 100
				}
			}
		}
		if it.LineType == "combo" {
			_ = json.Unmarshal([]byte(it.ComboSnapshotJSON), &iv.Components)
		}
		v.Items = append(v.Items, iv)
	}
	v.Packing = s.packingFor(s.db, items)

	if o.CustomerID != nil {
		var c database.EquipCustomer
		if found, _ := findOne(s.db, &c, "id = ?", *o.CustomerID); found {
			v.Customer = &c
		}
	}

	type allocRow struct {
		PaymentID uint
		Amount    float64
	}
	var allocs []allocRow
	if err := s.db.Table("equip_payment_allocations").Select("payment_id, amount").Where("order_id = ?", id).Scan(&allocs).Error; err != nil {
		return nil, err
	}
	if len(allocs) > 0 {
		pids := make([]uint, 0, len(allocs))
		amt := map[uint]float64{}
		for _, a := range allocs {
			pids = append(pids, a.PaymentID)
			amt[a.PaymentID] += a.Amount
		}
		var pays []database.EquipPayment
		if err := s.db.Where("id IN ?", pids).Order("paid_at ASC, id ASC").Find(&pays).Error; err != nil {
			return nil, err
		}
		for _, p := range pays {
			v.Payments = append(v.Payments, PaymentLine{PaymentID: p.ID, PaidAt: p.PaidAt, Method: p.Method, Moment: p.Moment, Reference: p.Reference,
				Invoice: invoiceText(p), Amount: amt[p.ID], Total: p.Amount, Status: p.Status, Notes: p.Notes, VoucherMime: p.VoucherMime})
		}
	}

	st, err := s.GetSettings()
	if err != nil {
		return nil, err
	}
	var sh database.EquipShipment
	if found, err := findOne(s.db, &sh, "order_id = ? AND is_current = ?", id, true); err != nil {
		return nil, err
	} else if found {
		sv := s.shipmentView(s.db, sh, st)
		v.Shipment = &sv
	}
	v.Timeline = buildTimeline(v)
	return v, nil
}

func buildTimeline(v *OrderView) []TimelineEvent {
	var ev []TimelineEvent
	add := func(at *time.Time, kind, label string) {
		if at != nil && !at.IsZero() {
			ev = append(ev, TimelineEvent{At: *at, Kind: kind, Label: label, Future: at.After(time.Now().Add(24 * time.Hour))})
		}
	}
	od := v.OrderDate
	add(&od, "pedido", "Fecha del pedido")
	reg := v.RegisteredAt
	add(&reg, "registro", "Registrado en el sistema")
	add(v.ConfirmedAt, "confirmacion", "Confirmado (stock descontado)")
	if v.ValidationStatus == "validado" {
		add(v.ValidatedAt, "validacion", "Validado")
	}
	for _, p := range v.Payments {
		if p.Status == "vigente" {
			pa := p.PaidAt
			add(&pa, "cobro", fmt.Sprintf("Cobro S/ %.2f (%s)", p.Amount, methodLabel(p.Method)))
		}
	}
	if sh := v.Shipment; sh != nil {
		add(sh.ScheduledDispatchDate, "despacho_programado", "Despacho programado")
		add(sh.DispatchedAt, "despacho", "Despachado")
		add(sh.ArrivedAt, "llegada", "Llegó a la agencia")
		if sh.Status == "en_agencia" {
			add(sh.PickupDeadline, "vencimiento", "Vence el plazo de recojo")
		}
		add(sh.PickedUpAt, "recojo", "Recogido por el cliente")
	}
	sort.SliceStable(ev, func(i, j int) bool { return ev[i].At.Before(ev[j].At) })
	return ev
}

var paymentMethodLabels = map[string]string{
	"yape": "Yape", "plin": "Plin", "efectivo": "Efectivo", "transferencia": "Transferencia", "deposito": "Depósito", "otro": "Otro",
}

func methodLabel(m string) string {
	if l, ok := paymentMethodLabels[m]; ok {
		return l
	}
	return m
}

// ── Listado ────────────────────────────────────────────────────────────────

type OrderFilter struct {
	Q                string
	From, To         string // AAAA-MM-DD sobre la fecha del pedido
	SaleType         string
	PaymentStatus    string
	ValidationStatus string
	Status           string
	ShipmentStatus   string
	CarrierID        uint
	Agency           string // "con_llegada": en agencia y con fecha de llegada
	Sort             string // "agency_days": más días en agencia primero
	Department       string
	CreatedBy        uint // usuario del panel que registró el pedido
	NoCreator        bool // solo pedidos sin usuario registrado (antiguos o importados sin dato)
	Page, PerPage    int
}

// OrderUserOption usuario que ha registrado al menos un pedido (opciones del filtro «Usuario»).
type OrderUserOption struct {
	ID   uint   `json:"id"`
	Name string `json:"name"`
}

// OrderUsers lista, sin datos sensibles, a los usuarios que registraron pedidos.
func (s *Service) OrderUsers() ([]OrderUserOption, error) {
	out := []OrderUserOption{}
	err := s.db.Table("super_admin_users u").
		Select("u.id, u.name").
		Where("u.id IN (?)", s.db.Table("equip_orders").Select("DISTINCT created_by").Where("created_by IS NOT NULL")).
		Order("u.name ASC, u.id ASC").Scan(&out).Error
	return out, err
}

type OrderRow struct {
	ID                    uint       `json:"id"`
	OrderNumber           int        `json:"order_number"`
	SaleType              string     `json:"sale_type"`
	OrderDate             time.Time  `json:"order_date"`
	CustomerName          string     `json:"customer_name"`
	CustomerDocType       string     `json:"customer_doc_type"`
	CustomerDoc           string     `json:"customer_doc_number"`
	TotalAmount           float64    `json:"total_amount"`
	PaidAmount            float64    `json:"paid_amount"`
	BalanceAmount         float64    `json:"balance_amount"`
	PaymentStatus         string     `json:"payment_status"`
	ValidationStatus      string     `json:"validation_status"`
	Status                string     `json:"status"`
	IsGift                bool       `json:"is_gift"`
	ShipmentStatus        string     `json:"shipment_status"`
	CarrierName           string     `json:"carrier_name"`
	GuideNumber           string     `json:"guide_number"`
	Department            string     `json:"department"`
	Summary               string     `json:"summary"`
	ArrivedAt             *time.Time `json:"arrived_at"`
	DaysInAgency          *int       `json:"days_in_agency"`
	DaysLeft              *int       `json:"days_left"`
	AgencyLevel           string     `json:"agency_level"` // verde | amarillo | rojo | critico | sin_fecha | ""
	AgencyLabel           string     `json:"agency_label"`
	DestinationAgency     string     `json:"destination_agency"`
	DeliveryMode          string     `json:"delivery_mode"`
	FreightCost           float64    `json:"freight_cost"`
	ScheduledDispatchDate *time.Time `json:"scheduled_dispatch_date"`
	CreatedBy             *uint      `json:"created_by"`
	CreatedByName         string     `json:"created_by_name"`
	CustomerID            *uint      `json:"customer_id"`
}

type OrderListResult struct {
	Rows       []OrderRow `json:"rows"`
	Total      int64      `json:"total"`
	Page       int        `json:"page"`
	PerPage    int        `json:"per_page"`
	SumTotal   float64    `json:"sum_total"`
	SumBalance float64    `json:"sum_balance"`
}

func (s *Service) ListOrders(f OrderFilter) (*OrderListResult, error) {
	if f.Page < 1 {
		f.Page = 1
	}
	if f.PerPage < 1 || f.PerPage > 200 {
		f.PerPage = 25
	}
	base := s.db.Table("equip_orders o").
		Joins("LEFT JOIN equip_shipments sh ON sh.order_id = o.id AND sh.is_current = ?", true).
		Joins("LEFT JOIN equip_carriers c ON c.id = sh.carrier_id").
		Joins("LEFT JOIN super_admin_users cu ON cu.id = o.created_by")
	if f.CreatedBy > 0 {
		base = base.Where("o.created_by = ?", f.CreatedBy)
	} else if f.NoCreator {
		base = base.Where("o.created_by IS NULL")
	}
	if q := strings.TrimSpace(f.Q); q != "" {
		like := "%" + strings.ToLower(q) + "%"
		// También por quien recoge (nombre y documento): el destinatario del rótulo puede no ser el cliente.
		base = base.Where("LOWER(o.customer_name) LIKE ? OR LOWER(o.contact_name) LIKE ? OR o.customer_doc_number LIKE ? OR o.contact_dni LIKE ? OR o.customer_phone LIKE ? OR sh.guide_number LIKE ? OR CAST(o.order_number AS CHAR) = ?",
			like, like, "%"+q+"%", "%"+q+"%", "%"+q+"%", "%"+q+"%", q)
	}
	if d, err := parseDateOnly(f.From); err != nil {
		return nil, err
	} else if d != nil {
		base = base.Where("o.order_date >= ?", d.Format("2006-01-02"))
	}
	if d, err := parseDateOnly(f.To); err != nil {
		return nil, err
	} else if d != nil {
		base = base.Where("o.order_date <= ?", d.Format("2006-01-02"))
	}
	for col, val := range map[string]string{"o.sale_type": f.SaleType, "o.payment_status": f.PaymentStatus, "o.validation_status": f.ValidationStatus, "o.status": f.Status, "sh.status": f.ShipmentStatus} {
		if strings.TrimSpace(val) != "" {
			base = base.Where(col+" = ?", strings.TrimSpace(val))
		}
	}
	if f.CarrierID > 0 {
		base = base.Where("sh.carrier_id = ?", f.CarrierID)
	}
	if f.Agency == "con_llegada" {
		base = base.Where("sh.status = ? AND sh.arrived_at IS NOT NULL", "en_agencia")
	}
	if dep := strings.TrimSpace(f.Department); dep != "" {
		base = base.Where("LOWER(sh.destination_department) = ?", strings.ToLower(dep))
	}

	// Rows vacío (no nil): un nil se serializa como null y el panel fallaba al leer rows.length.
	res := &OrderListResult{Page: f.Page, PerPage: f.PerPage, Rows: []OrderRow{}}
	if err := base.Session(&gorm.Session{}).Count(&res.Total).Error; err != nil {
		return nil, err
	}
	type sums struct{ T, B float64 }
	var sm sums
	if err := base.Session(&gorm.Session{}).Select("COALESCE(SUM(CASE WHEN o.status <> 'anulado' THEN o.total_amount ELSE 0 END), 0) AS t, COALESCE(SUM(CASE WHEN o.status <> 'anulado' THEN o.balance_amount ELSE 0 END), 0) AS b").Scan(&sm).Error; err != nil {
		return nil, err
	}
	res.SumTotal, res.SumBalance = round2(sm.T), round2(sm.B)

	orderBy := "o.order_number DESC"
	if f.Sort == "agency_days" { // en agencia con llegada (más días primero), en agencia sin fecha, pendientes/en tránsito, entregados y retornos
		orderBy = "CASE WHEN sh.status = 'en_agencia' AND sh.arrived_at IS NOT NULL THEN 0 WHEN sh.status = 'en_agencia' THEN 1 WHEN sh.status IN ('entregado', 'retorno') THEN 3 ELSE 2 END, CASE WHEN sh.status = 'en_agencia' THEN sh.arrived_at END ASC, o.order_number DESC"
	}
	if err := base.Session(&gorm.Session{}).
		Select("o.id, o.order_number, o.sale_type, o.order_date, o.customer_name, o.customer_doc_type, o.customer_doc_number AS customer_doc, o.total_amount, o.paid_amount, o.balance_amount, o.payment_status, o.validation_status, o.status, o.is_gift, COALESCE(sh.status, '') AS shipment_status, COALESCE(c.name, '') AS carrier_name, COALESCE(sh.guide_number, '') AS guide_number, COALESCE(sh.destination_department, '') AS department, sh.arrived_at, COALESCE(sh.destination_agency, '') AS destination_agency, COALESCE(sh.delivery_mode, '') AS delivery_mode, COALESCE(sh.freight_cost, 0) AS freight_cost, sh.scheduled_dispatch_date, o.created_by, o.customer_id, COALESCE(cu.name, '') AS created_by_name").
		Order(orderBy).
		Limit(f.PerPage).Offset((f.Page - 1) * f.PerPage).Scan(&res.Rows).Error; err != nil {
		return nil, err
	}
	today := todayNoon()
	for i := range res.Rows {
		r := &res.Rows[i]
		if r.ShipmentStatus != "en_agencia" {
			continue
		}
		if r.ArrivedAt == nil {
			r.AgencyLevel, r.AgencyLabel = "sin_fecha", "Sin fecha de llegada"
			continue
		}
		d := daysInAgency(*r.ArrivedAt, today)
		level, label, left := agencyReturnStatus(d)
		r.DaysInAgency, r.DaysLeft, r.AgencyLevel, r.AgencyLabel = &d, &left, level, label
	}
	if len(res.Rows) > 0 {
		ids := make([]uint, len(res.Rows))
		for i, r := range res.Rows {
			ids[i] = r.ID
		}
		var items []database.EquipOrderItem
		if err := s.db.Where("order_id IN ?", ids).Order("line_no").Find(&items).Error; err != nil {
			return nil, err
		}
		parts := map[uint][]string{}
		for _, it := range items {
			parts[it.OrderID] = append(parts[it.OrderID], fmt.Sprintf("%gx %s", it.Quantity, it.Description))
		}
		for i := range res.Rows {
			res.Rows[i].Summary = strings.Join(parts[res.Rows[i].ID], " | ")
		}
	}
	return res, nil
}

// Errores tipados del reparto de pedidos sin usuario (el handler los traduce a 404 y 409).
var (
	ErrOrderNotFound = errors.New("pedido no encontrado")
	ErrOrderHasOwner = errors.New("el pedido ya tiene un usuario registrado; no se puede reasignar")
)

// ActiveUsers todos los usuarios activos del panel (solo id y nombre) para asignar pedidos sin usuario.
func (s *Service) ActiveUsers() ([]OrderUserOption, error) {
	out := []OrderUserOption{}
	err := s.db.Table("super_admin_users").Select("id, name").Where("active = ?", true).Order("name ASC, id ASC").Scan(&out).Error
	return out, err
}

// AssignOrderCreator asigna usuario a un pedido que no tiene. Solo funciona con created_by nulo;
// repetir la misma asignación es idempotente y con otro usuario devuelve ErrOrderHasOwner.
// Devuelve el nombre del usuario asignado.
func (s *Service) AssignOrderCreator(orderID, userID uint) (string, error) {
	var u struct {
		Name   string
		Active bool
	}
	res := s.db.Table("super_admin_users").Select("name, active").Where("id = ?", userID).Limit(1).Scan(&u)
	if res.Error != nil {
		return "", res.Error
	}
	if res.RowsAffected == 0 {
		return "", invalid("el usuario no existe")
	}
	if !u.Active {
		return "", invalid("el usuario está inactivo")
	}
	upd := s.db.Model(&database.EquipOrder{}).Where("id = ? AND created_by IS NULL", orderID).Update("created_by", userID)
	if upd.Error != nil {
		return "", upd.Error
	}
	if upd.RowsAffected == 1 {
		return u.Name, nil
	}
	var o database.EquipOrder
	if found, err := findOne(s.db, &o, "id = ?", orderID); err != nil {
		return "", err
	} else if !found {
		return "", ErrOrderNotFound
	}
	if o.CreatedBy != nil && *o.CreatedBy == userID {
		return u.Name, nil
	}
	return "", ErrOrderHasOwner
}
