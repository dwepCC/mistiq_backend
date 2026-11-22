package service

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"

	"tukifac/pkg/database"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ── Entradas ───────────────────────────────────────────────────────────────

type OrderItemInput struct {
	LineType    string  `json:"line_type"` // producto | combo | plan | otro
	ProductID   *uint   `json:"product_id"`
	ComboID     *uint   `json:"combo_id"`
	SaasPlanID  *uint   `json:"saas_plan_id"`
	Description string  `json:"description"`
	PlanMonths  int     `json:"plan_months"`
	Quantity    float64 `json:"quantity"`
	UnitPrice   float64 `json:"unit_price"`
	IsCourtesy  bool    `json:"is_courtesy"`
	Notes       string  `json:"notes"`
}

// ShipmentInput datos del envío que se capturan al armar el pedido (o después, desde Envíos).
type ShipmentInput struct {
	CarrierID             *uint    `json:"carrier_id"`
	GuideNumber           string   `json:"guide_number"`
	DestinationAgency     string   `json:"destination_agency"`
	OriginAgencyID        *uint    `json:"origin_agency_id"`
	DestinationAgencyID   *uint    `json:"destination_agency_id"`
	DestinationDepartment string   `json:"destination_department"`
	DestinationProvince   string   `json:"destination_province"`
	DestinationDistrict   string   `json:"destination_district"`
	DeliveryMode          string   `json:"delivery_mode"`           // agencia | oficina | pendiente_recojo
	ScheduledDispatchDate string   `json:"scheduled_dispatch_date"` // AAAA-MM-DD
	Notes                 string   `json:"notes"`
	FreightCost           *float64 `json:"freight_cost"` // nil = no cambia
}

type OrderInput struct {
	CustomerID        *uint            `json:"customer_id"`
	CustomerName      string           `json:"customer_name"`
	CustomerDocType   string           `json:"customer_doc_type"`
	CustomerDocNumber string           `json:"customer_doc_number"`
	ContactDocType    string           `json:"contact_doc_type"` // DNI (por defecto) | CE
	ContactDNI        string           `json:"contact_dni"`
	ContactName       string           `json:"contact_name"`
	CustomerPhone     string           `json:"customer_phone"`
	SaveCustomer     bool             `json:"save_customer"` // crea el cliente si el documento no existe
	SaleType          string           `json:"sale_type"`
	OrderDate         string           `json:"order_date"` // AAAA-MM-DD; vacío = hoy
	BillingDocType    string           `json:"billing_doc_type"`
	Notes             string           `json:"notes"`
	Items             []OrderItemInput `json:"items"`
	Shipment          *ShipmentInput   `json:"shipment"`
}

// NegativeStockItem producto que quedaría con stock negativo al confirmar.
type NegativeStockItem struct {
	ProductID uint   `json:"product_id"`
	Code      string `json:"code"`
	Current   int    `json:"current"`
	Needed    int    `json:"needed"`
	Resulting int    `json:"resulting"`
}

// NegativeStockError se devuelve cuando confirmar dejaría productos en negativo: el usuario debe confirmarlo con una nota.
type NegativeStockError struct{ Items []NegativeStockItem }

func (e *NegativeStockError) Error() string {
	parts := make([]string, 0, len(e.Items))
	for _, it := range e.Items {
		parts = append(parts, fmt.Sprintf("%s quedaría en %d", it.Code, it.Resulting))
	}
	return "El stock quedaría en negativo: " + strings.Join(parts, ", ")
}

// ConfirmInput opciones al confirmar el pedido.
type ConfirmInput struct {
	AllowNegative bool   `json:"allow_negative"`
	NegativeNote  string `json:"negative_note"`
}

// ── Utilidades ─────────────────────────────────────────────────────────────

func parseDateOnly(raw string) (*time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	t, err := time.ParseInLocation("2006-01-02", raw[:min(len(raw), 10)], limaLoc())
	if err != nil {
		return nil, invalid("fecha inválida (use AAAA-MM-DD)")
	}
	d := noonLima(t)
	return &d, nil
}

func todayNoon() time.Time { return noonLima(time.Now().In(limaLoc())) }

// outflow salida de stock de un pedido: un producto (directo o por combo).
type outflow struct {
	ProductID uint
	Quantity  int
	ComboID   *uint
	ItemID    uint
}

type comboSnap struct {
	ProductID uint   `json:"product_id"`
	Code      string `json:"code"`
	Quantity  int    `json:"quantity"`
}

// outflows expande los ítems del pedido a salidas por producto (los combos descuentan sus componentes con la composición
// con la que se vendieron).
func outflows(items []database.EquipOrderItem) ([]outflow, error) {
	var out []outflow
	for _, it := range items {
		switch it.LineType {
		case "producto":
			if it.ProductID == nil {
				return nil, invalid("una línea de producto no tiene producto")
			}
			out = append(out, outflow{ProductID: *it.ProductID, Quantity: int(math.Round(it.Quantity)), ItemID: it.ID})
		case "combo":
			var snaps []comboSnap
			if err := json.Unmarshal([]byte(it.ComboSnapshotJSON), &snaps); err != nil || len(snaps) == 0 {
				return nil, invalid("el combo %q no tiene composición", it.Description)
			}
			for _, sn := range snaps {
				out = append(out, outflow{ProductID: sn.ProductID, Quantity: sn.Quantity * int(math.Round(it.Quantity)), ComboID: it.ComboID, ItemID: it.ID})
			}
		}
	}
	return out, nil
}

// ── Armado de ítems ────────────────────────────────────────────────────────

func (s *Service) buildItems(tx *gorm.DB, in []OrderItemInput) ([]database.EquipOrderItem, error) {
	if len(in) == 0 {
		return nil, nil
	}
	items := make([]database.EquipOrderItem, 0, len(in))
	for i, it := range in {
		row := database.EquipOrderItem{LineNo: i + 1, LineType: it.LineType, Notes: strings.TrimSpace(it.Notes), PlanMonths: it.PlanMonths}
		if it.Quantity <= 0 {
			return nil, invalid("línea %d: la cantidad debe ser mayor a cero", i+1)
		}
		if it.UnitPrice < 0 {
			return nil, invalid("línea %d: el precio no puede ser negativo", i+1)
		}
		row.Quantity = it.Quantity
		switch it.LineType {
		case "producto":
			if it.ProductID == nil {
				return nil, invalid("línea %d: elige el producto", i+1)
			}
			var p database.EquipProduct
			if found, err := findOne(tx, &p, "id = ?", *it.ProductID); err != nil {
				return nil, err
			} else if !found || !p.Active {
				return nil, invalid("línea %d: el producto no existe o está inactivo", i+1)
			}
			if it.Quantity != math.Trunc(it.Quantity) {
				return nil, invalid("línea %d: la cantidad de %s debe ser un número entero", i+1, p.Code)
			}
			id := p.ID
			row.ProductID, row.Description = &id, p.Code
		case "combo":
			if it.ComboID == nil {
				return nil, invalid("línea %d: elige el combo", i+1)
			}
			var c database.EquipCombo
			if found, err := findOne(tx.Preload("Items"), &c, "id = ?", *it.ComboID); err != nil {
				return nil, err
			} else if !found || !c.Active || len(c.Items) == 0 {
				return nil, invalid("línea %d: el combo no existe, está inactivo o no tiene componentes", i+1)
			}
			if it.Quantity != math.Trunc(it.Quantity) {
				return nil, invalid("línea %d: la cantidad de combos debe ser un número entero", i+1)
			}
			var snaps []comboSnap
			for _, ci := range c.Items {
				var p database.EquipProduct
				if _, err := findOne(tx, &p, "id = ?", ci.ProductID); err != nil {
					return nil, err
				}
				snaps = append(snaps, comboSnap{ProductID: ci.ProductID, Code: p.Code, Quantity: ci.Quantity})
			}
			b, _ := json.Marshal(snaps)
			id := c.ID
			row.ComboID, row.Description, row.ComboSnapshotJSON = &id, c.Code, string(b)
		case "plan":
			desc := collapse(it.Description)
			if desc == "" && it.SaasPlanID != nil {
				var pl database.SaasPlan
				if found, err := findOne(tx, &pl, "id = ?", *it.SaasPlanID); err == nil && found {
					desc = pl.Name
				}
			}
			if desc == "" {
				return nil, invalid("línea %d: indica el plan (ej. «Plan semestral»)", i+1)
			}
			if it.PlanMonths < 0 || it.PlanMonths > 120 {
				return nil, invalid("línea %d: los meses del plan no son válidos", i+1)
			}
			row.Description, row.SaasPlanID = desc, it.SaasPlanID
			if row.PlanMonths == 0 {
				if _, m := planMonths(desc); m > 0 {
					row.PlanMonths = m
				}
			}
		case "otro":
			desc := collapse(it.Description)
			if desc == "" {
				return nil, invalid("línea %d: describe el concepto", i+1)
			}
			row.Description = desc
		default:
			return nil, invalid("línea %d: tipo de línea inválido", i+1)
		}
		price := round2(it.UnitPrice)
		if it.IsCourtesy {
			price = 0
		}
		row.UnitPrice, row.IsCourtesy = price, price == 0
		row.Subtotal = round2(row.Quantity * price)
		items = append(items, row)
	}
	return items, nil
}

func sumItems(items []database.EquipOrderItem) float64 {
	t := 0.0
	for _, it := range items {
		t += it.Subtotal
	}
	return round2(t)
}

// ── Cliente ────────────────────────────────────────────────────────────────

// resolveCustomer fija el cliente y la copia (snapshot) del pedido.
func (s *Service) resolveCustomer(tx *gorm.DB, in OrderInput, o *database.EquipOrder) error {
	// Quien recoge es dato del pedido (no del cliente): vacío = recoge el mismo cliente.
	in.ContactDNI = strings.TrimSpace(in.ContactDNI)
	in.ContactName = collapse(in.ContactName)
	contactType := strings.ToUpper(strings.TrimSpace(in.ContactDocType))
	switch contactType {
	case "", "DNI":
		contactType = "DNI"
		if in.ContactDNI != "" && (!onlyDigits(in.ContactDNI) || len(in.ContactDNI) != 8) {
			return invalid("el DNI de quien recoge debe tener 8 dígitos")
		}
	case "CE":
		// Carné de extranjería: 9 a 12 caracteres alfanuméricos.
		in.ContactDNI = strings.ToUpper(in.ContactDNI)
		if in.ContactDNI != "" && !ceDocRe.MatchString(in.ContactDNI) {
			return invalid("el carné de extranjería de quien recoge debe tener entre 9 y 12 letras o dígitos")
		}
	default:
		return invalid("tipo de documento de quien recoge inválido (DNI o CE)")
	}
	if in.ContactDNI == "" && in.ContactName == "" {
		o.ContactDocType, o.ContactDNI, o.ContactName = "DNI", "", ""
	} else {
		o.ContactDocType, o.ContactDNI, o.ContactName = contactType, in.ContactDNI, in.ContactName
	}
	if in.CustomerID != nil {
		var c database.EquipCustomer
		if found, err := findOne(tx, &c, "id = ?", *in.CustomerID); err != nil {
			return err
		} else if !found {
			return invalid("el cliente no existe")
		}
		id := c.ID
		o.CustomerID, o.CustomerName, o.CustomerDocType, o.CustomerPhone = &id, c.Name, c.DocType, c.Phone
		// Nombre y celular del pedido son una copia editable: lo que el usuario corrige en el
		// pedido prevalece sobre la ficha del cliente (el documento sí sale de la ficha).
		if n := collapse(in.CustomerName); n != "" {
			o.CustomerName = n
		}
		if p := strings.TrimSpace(in.CustomerPhone); p != "" {
			o.CustomerPhone, _, _ = classifyPhone(p)
		}
		o.CustomerDocNumber = ""
		if c.DocNumber != nil {
			o.CustomerDocNumber = *c.DocNumber
		}
		return nil
	}
	name := collapse(in.CustomerName)
	docType := strings.ToUpper(strings.TrimSpace(in.CustomerDocType))
	if strings.TrimSpace(in.CustomerDocNumber) == "" || docType == "" || docType == "SIN_DOC" {
		o.CustomerID, o.CustomerName, o.CustomerDocType, o.CustomerDocNumber = nil, name, "SIN_DOC", ""
		if o.CustomerName == "" {
			o.CustomerName = "Clientes varios"
		}
		phone, _, _ := classifyPhone(in.CustomerPhone)
		o.CustomerPhone = phone
		return nil
	}
	num, err := validateDoc(docType, in.CustomerDocNumber)
	if err != nil {
		return err
	}
	if name == "" {
		return invalid("el nombre del cliente es obligatorio")
	}
	phone, kind, _ := classifyPhone(in.CustomerPhone)
	o.CustomerName, o.CustomerDocType, o.CustomerDocNumber, o.CustomerPhone = name, docType, num, phone
	if !in.SaveCustomer {
		o.CustomerID = nil
		return nil
	}
	var existing database.EquipCustomer
	found, err := findOne(tx, &existing, "doc_type = ? AND doc_number = ?", docType, num)
	if err != nil {
		return err
	}
	if !found {
		n := num
		existing = database.EquipCustomer{Name: name, DocType: docType, DocNumber: &n, ContactDNI: in.ContactDNI, Phone: phone, PhoneKind: kind}
		if err := tx.Create(&existing).Error; err != nil {
			return err
		}
	}
	id := existing.ID
	o.CustomerID = &id
	return nil
}

// ── Crear / editar ─────────────────────────────────────────────────────────

func normalizeSaleType(v string) (string, error) {
	switch strings.TrimSpace(v) {
	case "", database.EquipSaleIndependiente:
		return database.EquipSaleIndependiente, nil
	case database.EquipSalePromoTK:
		return database.EquipSalePromoTK, nil
	}
	return "", invalid("tipo de salida inválido (independiente o promo_tk)")
}

var ceDocRe = regexp.MustCompile(`^[A-Z0-9]{9,12}$`)

func normalizeBilling(v string) (string, error) {
	switch strings.TrimSpace(v) {
	case "", "ninguno":
		return "ninguno", nil
	case "boleta", "factura":
		return strings.TrimSpace(v), nil
	}
	return "", invalid("tipo de comprobante inválido (boleta, factura o ninguno)")
}

// OrderResult pedido guardado y avisos que no lo bloquean.
type OrderResult struct {
	Order    *OrderView `json:"order"`
	Warnings []string   `json:"warnings"`
}

func (s *Service) CreateOrder(in OrderInput, userID uint) (*OrderResult, error) {
	var created uint
	var warnings []string
	err := s.db.Transaction(func(tx *gorm.DB) error {
		saleType, err := normalizeSaleType(in.SaleType)
		if err != nil {
			return err
		}
		billing, err := normalizeBilling(in.BillingDocType)
		if err != nil {
			return err
		}
		date, err := parseDateOnly(in.OrderDate)
		if err != nil {
			return err
		}
		if date == nil {
			d := todayNoon()
			date = &d
		}
		items, err := s.buildItems(tx, in.Items)
		if err != nil {
			return err
		}
		o := database.EquipOrder{
			SaleType: saleType, OrderDate: *date, RegisteredAt: time.Now(), BillingDocType: billing,
			Notes: strings.TrimSpace(in.Notes), Status: "borrador", ValidationStatus: "pendiente_validacion", PaymentStatus: "pendiente",
		}
		if userID > 0 {
			u := userID
			o.CreatedBy = &u
		}
		if err := s.resolveCustomer(tx, in, &o); err != nil {
			return err
		}
		// Número continuo: se toma de la configuración dentro de la transacción.
		var st database.EquipSettings
		if found, err := findOne(tx.Clauses(clause.Locking{Strength: "UPDATE"}), &st, "id = ?", 1); err != nil {
			return err
		} else if !found {
			st = database.EquipSettings{ID: 1, AlertYellowDays: 5, AlertRedDays: 12, NextOrderNumber: 1}
			if err := tx.Create(&st).Error; err != nil {
				return err
			}
		}
		num := st.NextOrderNumber
		for {
			var n int64
			if err := tx.Model(&database.EquipOrder{}).Where("order_number = ?", num).Count(&n).Error; err != nil {
				return err
			}
			if n == 0 {
				break
			}
			num++
		}
		o.OrderNumber = num
		o.TotalAmount = sumItems(items)
		o.BalanceAmount = o.TotalAmount
		o.IsGift = o.TotalAmount == 0 && len(items) > 0
		if err := tx.Create(&o).Error; err != nil {
			return err
		}
		if err := tx.Model(&database.EquipSettings{}).Where("id = ?", 1).Update("next_order_number", num+1).Error; err != nil {
			return err
		}
		for i := range items {
			items[i].OrderID = o.ID
			if err := tx.Create(&items[i]).Error; err != nil {
				return err
			}
		}
		if err := requireDestinationAgency(in.Shipment); err != nil {
			return err
		}
		if err := s.saveShipmentTx(tx, o.ID, in.Shipment); err != nil {
			return err
		}
		created = o.ID
		warnings = s.orderWarnings(tx, &o, items, in.Shipment)
		return nil
	})
	if err != nil {
		return nil, err
	}
	v, err := s.GetOrder(created)
	if err != nil {
		return nil, err
	}
	return &OrderResult{Order: v, Warnings: warnings}, nil
}

// orderWarnings avisos que no bloquean: pedido abierto reciente del mismo cliente, precio atípico, día de despacho.
func (s *Service) orderWarnings(tx *gorm.DB, o *database.EquipOrder, items []database.EquipOrderItem, sh *ShipmentInput) []string {
	var w []string
	if o.CustomerID != nil {
		var n int64
		tx.Model(&database.EquipOrder{}).
			Where("customer_id = ? AND id <> ? AND status <> ? AND order_date >= ?", *o.CustomerID, o.ID, "anulado", o.OrderDate.AddDate(0, 0, -7)).
			Where("id IN (?)", tx.Model(&database.EquipShipment{}).Select("order_id").Where("is_current = ? AND status <> ?", true, "entregado")).
			Count(&n)
		if n > 0 {
			w = append(w, fmt.Sprintf("Este cliente ya tiene %d pedido(s) abiertos de los últimos 7 días: revisa que no sea un pedido partido.", n))
		}
	}
	for _, it := range items {
		if it.LineType == "producto" && it.ProductID != nil && !it.IsCourtesy {
			var p database.EquipProduct
			if found, _ := findOne(tx, &p, "id = ?", *it.ProductID); found && p.ReferencePrice > 0 {
				if dev := math.Abs(it.UnitPrice-p.ReferencePrice) / p.ReferencePrice; dev > 0.3 {
					w = append(w, fmt.Sprintf("%s: precio S/ %.2f distinto al de lista (S/ %.2f).", p.Code, it.UnitPrice, p.ReferencePrice))
				}
			}
		}
	}
	if sh != nil {
		var cur database.EquipShipment
		if found, _ := findOne(tx, &cur, "order_id = ? AND is_current = ?", o.ID, true); found {
			if msg := s.dispatchDayWarning(tx, &cur); msg != "" {
				w = append(w, msg)
			}
		}
	}
	return w
}

func (s *Service) loadOrderForUpdate(tx *gorm.DB, id uint) (*database.EquipOrder, error) {
	var o database.EquipOrder
	if found, err := findOne(tx.Clauses(clause.Locking{Strength: "UPDATE"}), &o, "id = ?", id); err != nil {
		return nil, err
	} else if !found {
		return nil, invalid("pedido no encontrado")
	}
	return &o, nil
}

func (s *Service) UpdateOrder(id uint, in OrderInput, confirm ConfirmInput, userID uint) (*OrderResult, error) {
	var warnings []string
	err := s.db.Transaction(func(tx *gorm.DB) error {
		o, err := s.loadOrderForUpdate(tx, id)
		if err != nil {
			return err
		}
		if o.Status == "anulado" {
			return invalid("un pedido anulado no se puede editar")
		}
		saleType, err := normalizeSaleType(in.SaleType)
		if err != nil {
			return err
		}
		billing, err := normalizeBilling(in.BillingDocType)
		if err != nil {
			return err
		}
		date, err := parseDateOnly(in.OrderDate)
		if err != nil {
			return err
		}
		items, err := s.buildItems(tx, in.Items)
		if err != nil {
			return err
		}
		if o.Status == "registrado" && len(items) == 0 {
			return invalid("un pedido confirmado necesita al menos un ítem")
		}
		// El cliente y quien recoge se pueden corregir aunque el pedido ya tenga cobros aplicados.
		if err := s.resolveCustomer(tx, in, o); err != nil {
			return err
		}
		o.SaleType, o.BillingDocType, o.Notes = saleType, billing, strings.TrimSpace(in.Notes)
		if date != nil {
			o.OrderDate = *date
		}
		o.TotalAmount = sumItems(items)
		o.IsGift = o.TotalAmount == 0 && len(items) > 0

		old := []database.EquipOrderItem{}
		if err := tx.Where("order_id = ?", o.ID).Find(&old).Error; err != nil {
			return err
		}
		if err := tx.Where("order_id = ?", o.ID).Delete(&database.EquipOrderItem{}).Error; err != nil {
			return err
		}
		for i := range items {
			items[i].OrderID = o.ID
			if err := tx.Create(&items[i]).Error; err != nil {
				return err
			}
		}
		if o.Status == "registrado" {
			// El stock se regenera con los ítems nuevos (queda registrado en la auditoría del cambio).
			if err := s.regenerateMovementsTx(tx, o, items, confirm, userID); err != nil {
				return err
			}
		}
		var cur database.EquipShipment
		if found, _ := findOne(tx, &cur, "order_id = ? AND is_current = ?", o.ID, true); !found || (cur.Status != "entregado" && cur.Status != "retorno") {
			if err := requireDestinationAgency(in.Shipment); err != nil {
				return err
			}
		}
		if err := s.shipmentUpdateOnOrderTx(tx, o.ID, in.Shipment); err != nil {
			return err
		}
		if err := s.recalcOrderPaymentsTx(tx, o); err != nil {
			return err
		}
		warnings = s.orderWarnings(tx, o, items, in.Shipment)
		return tx.Save(o).Error
	})
	if err != nil {
		return nil, err
	}
	v, err := s.GetOrder(id)
	if err != nil {
		return nil, err
	}
	return &OrderResult{Order: v, Warnings: warnings}, nil
}

// ── Stock ──────────────────────────────────────────────────────────────────

// stockNowTx stock actual por producto leído dentro de la transacción.
func stockNowTx(tx *gorm.DB, ids []uint) (map[uint]int, error) {
	type row struct {
		ProductID uint
		Total     int
	}
	var rows []row
	// Solo el Almacén 1 vende: el stock de los depósitos no cuenta para confirmar pedidos.
	if err := tx.Model(&database.EquipStockMovement{}).Select("product_id, COALESCE(SUM(quantity), 0) AS total").
		Where(inOperativeWarehouse).Where("product_id IN ?", ids).Group("product_id").Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := map[uint]int{}
	for _, r := range rows {
		out[r.ProductID] = r.Total
	}
	return out, nil
}

// checkNegative compara las salidas nuevas contra el stock disponible (descontando lo que el pedido ya tenía descontado).
func (s *Service) checkNegative(tx *gorm.DB, outs []outflow, alreadyOut map[uint]int, c ConfirmInput) error {
	need := map[uint]int{}
	for _, o := range outs {
		need[o.ProductID] += o.Quantity
	}
	ids := make([]uint, 0, len(need))
	for id := range need {
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil
	}
	cur, err := stockNowTx(tx, ids)
	if err != nil {
		return err
	}
	var prods []database.EquipProduct
	if err := tx.Where("id IN ?", ids).Find(&prods).Error; err != nil {
		return err
	}
	code := map[uint]string{}
	for _, p := range prods {
		code[p.ID] = p.Code
	}
	var bad []NegativeStockItem
	for _, id := range ids {
		available := cur[id] + alreadyOut[id]
		if res := available - need[id]; res < 0 {
			bad = append(bad, NegativeStockItem{ProductID: id, Code: code[id], Current: available, Needed: need[id], Resulting: res})
		}
	}
	if len(bad) == 0 {
		return nil
	}
	sort.Slice(bad, func(i, j int) bool { return bad[i].Code < bad[j].Code })
	if !c.AllowNegative {
		return &NegativeStockError{Items: bad}
	}
	if strings.TrimSpace(c.NegativeNote) == "" {
		return invalid("confirmar con stock negativo exige una nota que explique el motivo")
	}
	return nil
}

// regenerateMovementsTx reemplaza las salidas de stock del pedido por las de sus ítems actuales.
func (s *Service) regenerateMovementsTx(tx *gorm.DB, o *database.EquipOrder, items []database.EquipOrderItem, c ConfirmInput, userID uint) error {
	var oldMoves []database.EquipStockMovement
	if err := tx.Where("order_id = ? AND movement_type IN ?", o.ID, []string{database.EquipMoveSalida, database.EquipMoveReenvio}).Find(&oldMoves).Error; err != nil {
		return err
	}
	already := map[uint]int{}
	for _, m := range oldMoves {
		already[m.ProductID] += -m.Quantity
	}
	outs, err := outflows(items)
	if err != nil {
		return err
	}
	if err := s.checkNegative(tx, outs, already, c); err != nil {
		return err
	}
	if len(oldMoves) > 0 {
		if err := tx.Where("order_id = ? AND movement_type = ?", o.ID, database.EquipMoveSalida).Delete(&database.EquipStockMovement{}).Error; err != nil {
			return err
		}
	}
	at := time.Now()
	if o.ConfirmedAt != nil {
		at = *o.ConfirmedAt
	}
	return s.insertOutflowsTx(tx, o, outs, at, userID, c.NegativeNote)
}

func (s *Service) insertOutflowsTx(tx *gorm.DB, o *database.EquipOrder, outs []outflow, at time.Time, userID uint, note string) error {
	var uid *uint
	if userID > 0 {
		u := userID
		uid = &u
	}
	oid := o.ID
	rows := make([]database.EquipStockMovement, 0, len(outs))
	for _, ou := range outs {
		itemID := ou.ItemID
		rows = append(rows, database.EquipStockMovement{
			ProductID: ou.ProductID, OccurredAt: at, MovementType: database.EquipMoveSalida, Quantity: -ou.Quantity,
			OrderID: &oid, OrderItemID: &itemID, ViaComboID: ou.ComboID, SaleTypeSnapshot: o.SaleType,
			Note: strings.TrimSpace(note), CreatedBy: uid,
		})
	}
	if len(rows) == 0 {
		return nil
	}
	return tx.CreateInBatches(&rows, 200).Error
}

// ConfirmOrder confirma el pedido: descuenta el stock (los combos descuentan sus componentes) y lo deja registrado.
func (s *Service) ConfirmOrder(id uint, c ConfirmInput, userID uint) (*OrderResult, error) {
	err := s.db.Transaction(func(tx *gorm.DB) error {
		o, err := s.loadOrderForUpdate(tx, id)
		if err != nil {
			return err
		}
		if o.Status != "borrador" {
			return invalid("solo se puede confirmar un pedido en borrador")
		}
		var items []database.EquipOrderItem
		if err := tx.Where("order_id = ?", o.ID).Order("line_no").Find(&items).Error; err != nil {
			return err
		}
		if len(items) == 0 {
			return invalid("agrega al menos un ítem antes de confirmar")
		}
		var sh database.EquipShipment
		if found, _ := findOne(tx, &sh, "order_id = ? AND is_current = ?", o.ID, true); found && sh.DeliveryMode == "agencia" &&
			sh.DestinationAgencyID == nil && strings.TrimSpace(sh.DestinationAgency) == "" {
			return invalid("elige la agencia de destino antes de confirmar el pedido con envío por agencia")
		}
		outs, err := outflows(items)
		if err != nil {
			return err
		}
		if err := s.checkNegative(tx, outs, nil, c); err != nil {
			return err
		}
		now := time.Now()
		if err := s.insertOutflowsTx(tx, o, outs, now, userID, c.NegativeNote); err != nil {
			return err
		}
		// La validación manual se retiró: confirmar el pedido lo deja validado (el estado se conserva por historial).
		o.Status, o.ConfirmedAt = "registrado", &now
		o.ValidationStatus, o.ValidatedAt = "validado", &now
		if c.AllowNegative && strings.TrimSpace(c.NegativeNote) != "" {
			o.Notes = strings.TrimSpace(o.Notes + " | Confirmado con stock negativo: " + strings.TrimSpace(c.NegativeNote))
		}
		return tx.Save(o).Error
	})
	if err != nil {
		return nil, err
	}
	v, err := s.GetOrder(id)
	if err != nil {
		return nil, err
	}
	return &OrderResult{Order: v}, nil
}

// CancelOrder anula el pedido y devuelve su stock. No se anula si ya salió (en tránsito, en agencia o recogido): eso se gestiona
// con un retorno o un ajuste; tampoco si tiene cobros vigentes aplicados (primero se anulan o reasignan).
func (s *Service) CancelOrder(id uint, reason string, userID uint) (*OrderView, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return nil, invalid("indica el motivo de la anulación")
	}
	err := s.db.Transaction(func(tx *gorm.DB) error {
		o, err := s.loadOrderForUpdate(tx, id)
		if err != nil {
			return err
		}
		if o.Status == "anulado" {
			return invalid("el pedido ya está anulado")
		}
		var sh database.EquipShipment
		if found, err := findOne(tx, &sh, "order_id = ? AND is_current = ?", o.ID, true); err != nil {
			return err
		} else if found && (sh.Status == "en_transito" || sh.Status == "en_agencia" || sh.Status == "entregado" || sh.Status == "retorno") {
			return invalid("el pedido ya salió (%s): no se anula directamente. Gestiona un retorno o un ajuste de stock", shipmentStatusLabel(sh.Status))
		}
		var n int64
		if err := tx.Table("equip_payment_allocations a").Joins("JOIN equip_payments p ON p.id = a.payment_id").
			Where("a.order_id = ? AND p.status = ?", o.ID, "vigente").Count(&n).Error; err != nil {
			return err
		}
		if n > 0 {
			return invalid("el pedido tiene cobros aplicados: anúlalos o reasígnalos primero")
		}
		if err := tx.Where("order_id = ? AND movement_type IN ?", o.ID, []string{database.EquipMoveSalida, database.EquipMoveReenvio}).Delete(&database.EquipStockMovement{}).Error; err != nil {
			return err
		}
		o.Status = "anulado"
		o.Notes = strings.TrimSpace(o.Notes + " | ANULADO: " + reason)
		return tx.Save(o).Error
	})
	if err != nil {
		return nil, err
	}
	return s.GetOrder(id)
}

// ── Validación (encargada) ─────────────────────────────────────────────────

func (s *Service) ValidateOrder(id uint, notes string, userID uint) (*OrderView, error) {
	err := s.db.Transaction(func(tx *gorm.DB) error {
		o, err := s.loadOrderForUpdate(tx, id)
		if err != nil {
			return err
		}
		if o.Status != "registrado" {
			return invalid("solo se valida un pedido confirmado")
		}
		now := time.Now()
		o.ValidationStatus, o.ValidatedAt, o.ValidationNotes = "validado", &now, strings.TrimSpace(notes)
		if userID > 0 {
			u := userID
			o.ValidatedBy = &u
		}
		return tx.Save(o).Error
	})
	if err != nil {
		return nil, err
	}
	return s.GetOrder(id)
}

func (s *Service) ObserveOrder(id uint, notes string, userID uint) (*OrderView, error) {
	notes = strings.TrimSpace(notes)
	if notes == "" {
		return nil, invalid("explica qué hay que corregir")
	}
	err := s.db.Transaction(func(tx *gorm.DB) error {
		o, err := s.loadOrderForUpdate(tx, id)
		if err != nil {
			return err
		}
		if o.Status != "registrado" {
			return invalid("solo se observa un pedido confirmado")
		}
		o.ValidationStatus, o.ValidatedAt, o.ValidationNotes = "observado", nil, notes
		if userID > 0 {
			u := userID
			o.ValidatedBy = &u
		}
		return tx.Save(o).Error
	})
	if err != nil {
		return nil, err
	}
	return s.GetOrder(id)
}

// SetNoPayment marca (o desmarca) el pedido como «no pagó» cuando el cobro ya no se espera.
func (s *Service) SetNoPayment(id uint, on bool) (*OrderView, error) {
	err := s.db.Transaction(func(tx *gorm.DB) error {
		o, err := s.loadOrderForUpdate(tx, id)
		if err != nil {
			return err
		}
		if on && o.BalanceAmount <= 0.004 {
			return invalid("el pedido no tiene saldo pendiente")
		}
		if on {
			o.PaymentStatus = "no_pago"
		} else if o.PaymentStatus == "no_pago" {
			o.PaymentStatus = "pendiente"
		}
		if err := tx.Save(o).Error; err != nil {
			return err
		}
		return s.recalcOrderPaymentsTx(tx, o)
	})
	if err != nil {
		return nil, err
	}
	return s.GetOrder(id)
}

// recalcOrderPaymentsTx recalcula lo cobrado, el saldo y el estado de pago desde los cobros VIGENTES aplicados.
func (s *Service) recalcOrderPaymentsTx(tx *gorm.DB, o *database.EquipOrder) error {
	var paid float64
	if err := tx.Table("equip_payment_allocations a").Select("COALESCE(SUM(a.amount), 0)").
		Joins("JOIN equip_payments p ON p.id = a.payment_id").
		Where("a.order_id = ? AND p.status = ?", o.ID, "vigente").Scan(&paid).Error; err != nil {
		return err
	}
	paid = round2(paid)
	o.PaidAmount = paid
	o.BalanceAmount = math.Max(0, round2(o.TotalAmount-paid))
	switch {
	case o.TotalAmount <= 0 || paid >= o.TotalAmount-0.005:
		o.PaymentStatus = "pagado"
	case paid > 0:
		o.PaymentStatus = "parcial"
	case o.PaymentStatus != "no_pago":
		o.PaymentStatus = "pendiente"
	}
	return tx.Model(&database.EquipOrder{}).Where("id = ?", o.ID).Updates(map[string]any{
		"paid_amount": o.PaidAmount, "balance_amount": o.BalanceAmount, "payment_status": o.PaymentStatus,
		"total_amount": o.TotalAmount, "is_gift": o.IsGift,
	}).Error
}

func sameUint(a, b *uint) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
