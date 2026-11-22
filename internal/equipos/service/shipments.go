package service

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	"tukifac/pkg/database"

	"gorm.io/gorm"
)

func shipmentStatusLabel(s string) string {
	switch s {
	case "pendiente_envio":
		return "por despachar"
	case "en_transito":
		return "en tránsito"
	case "en_agencia":
		return "en agencia"
	case "entregado":
		return "recogido"
	case "retorno":
		return "en retorno"
	}
	return s
}

var weekdayNames = []string{"domingo", "lunes", "martes", "miércoles", "jueves", "viernes", "sábado"}

// ── Armado del envío ───────────────────────────────────────────────────────

func (s *Service) defaultCarrierTx(tx *gorm.DB) (*database.EquipCarrier, error) {
	var st database.EquipSettings
	if found, err := findOne(tx, &st, "id = ?", 1); err != nil {
		return nil, err
	} else if found && st.DefaultCarrierID != nil {
		var c database.EquipCarrier
		if found, err := findOne(tx, &c, "id = ? AND active = ?", *st.DefaultCarrierID, true); err != nil {
			return nil, err
		} else if found {
			return &c, nil
		}
	}
	var c database.EquipCarrier
	if found, err := findOne(tx.Order("is_default DESC, sort_order ASC, id ASC"), &c, "active = ?", true); err != nil {
		return nil, err
	} else if found {
		return &c, nil
	}
	return nil, nil
}

func (s *Service) carrierByIDTx(tx *gorm.DB, id *uint) (*database.EquipCarrier, error) {
	if id == nil {
		return nil, nil
	}
	var c database.EquipCarrier
	if found, err := findOne(tx, &c, "id = ?", *id); err != nil {
		return nil, err
	} else if !found {
		return nil, nil
	}
	return &c, nil
}

// isOfficeMode indica si la modalidad es de recojo en oficina (sin transportista).
func isOfficeMode(mode string) bool {
	m := strings.TrimSpace(mode)
	return m == "oficina" || m == "pendiente_recojo"
}

// applyShipmentInput copia los datos editables del envío validándolos contra su transportista.
func (s *Service) applyShipmentInput(tx *gorm.DB, sh *database.EquipShipment, in *ShipmentInput) error {
	if in == nil {
		if sh.CarrierID == nil {
			if c, err := s.defaultCarrierTx(tx); err != nil {
				return err
			} else if c != nil {
				id := c.ID
				sh.CarrierID = &id
			}
		}
		return nil
	}
	// Recojo en oficina: no interviene ningún transportista ni agencia, así que esos datos se descartan.
	office := isOfficeMode(in.DeliveryMode)
	if office {
		cp := *in
		cp.CarrierID, cp.GuideNumber, cp.DestinationAgency = nil, "", ""
		cp.OriginAgencyID, cp.DestinationAgencyID = nil, nil
		in = &cp
		sh.CarrierID = nil
	}
	if in.CarrierID != nil {
		c, err := s.carrierByIDTx(tx, in.CarrierID)
		if err != nil {
			return err
		}
		if c == nil || !c.Active {
			return invalid("el transportista no existe o está inactivo")
		}
		id := c.ID
		sh.CarrierID = &id
	} else if sh.CarrierID == nil && !office {
		if c, err := s.defaultCarrierTx(tx); err != nil {
			return err
		} else if c != nil {
			id := c.ID
			sh.CarrierID = &id
		}
	}
	guide := strings.TrimSpace(in.GuideNumber)
	if guide != "" {
		c, err := s.carrierByIDTx(tx, sh.CarrierID)
		if err != nil {
			return err
		}
		if c != nil && c.GuideFormat != "" {
			re, err := regexp.Compile(c.GuideFormat)
			if err == nil && !re.MatchString(guide) {
				return invalid("el %s no tiene el formato de %s", strings.ToLower(c.GuideLabel), c.Name)
			}
		}
	}
	sh.GuideNumber = guide
	sh.DestinationAgency = collapse(in.DestinationAgency)
	dep := collapse(in.DestinationDepartment)
	if name, ok := peruDepartments[foldText(dep)]; ok {
		dep = name
	}
	sh.DestinationDepartment, sh.DestinationProvince, sh.DestinationDistrict = dep, collapse(in.DestinationProvince), collapse(in.DestinationDistrict)
	if err := s.applyAgencies(tx, sh, in); err != nil {
		return err
	}
	switch strings.TrimSpace(in.DeliveryMode) {
	case "", "agencia":
		sh.DeliveryMode = "agencia"
	case "oficina", "pendiente_recojo":
		sh.DeliveryMode = strings.TrimSpace(in.DeliveryMode)
	default:
		return invalid("modalidad de entrega inválida")
	}
	d, err := parseDateOnly(in.ScheduledDispatchDate)
	if err != nil {
		return err
	}
	sh.ScheduledDispatchDate = d
	sh.Notes = strings.TrimSpace(in.Notes)
	if in.FreightCost != nil {
		if *in.FreightCost < 0 {
			return invalid("el costo del flete no puede ser negativo")
		}
		sh.FreightCost = *in.FreightCost
	}
	return nil
}

// requireDestinationAgency: un pedido con envío por agencia no se guarda ni se confirma sin agencia de destino
// (elegida de la lista o escrita a mano). Se exige al armar o editar el pedido desde el formulario; la importación
// del Excel, los datos históricos y la edición posterior de datos de envío no pasan por aquí.
func requireDestinationAgency(in *ShipmentInput) error {
	if in == nil {
		return nil
	}
	if m := strings.TrimSpace(in.DeliveryMode); m != "" && m != "agencia" {
		return nil
	}
	if (in.DestinationAgencyID == nil || *in.DestinationAgencyID == 0) && strings.TrimSpace(in.DestinationAgency) == "" {
		return invalid("elige la agencia de destino para guardar el pedido con envío por agencia")
	}
	return nil
}

func (s *Service) saveShipmentTx(tx *gorm.DB, orderID uint, in *ShipmentInput) error {
	sh := database.EquipShipment{OrderID: orderID, Status: "pendiente_envio", IsCurrent: true, DeliveryMode: "agencia"}
	if err := s.applyShipmentInput(tx, &sh, in); err != nil {
		return err
	}
	return tx.Create(&sh).Error
}

// shipmentUpdateOnOrderTx actualiza el envío vigente desde el formulario del pedido (no cambia su estado).
func (s *Service) shipmentUpdateOnOrderTx(tx *gorm.DB, orderID uint, in *ShipmentInput) error {
	if in == nil {
		return nil
	}
	var sh database.EquipShipment
	found, err := findOne(tx, &sh, "order_id = ? AND is_current = ?", orderID, true)
	if err != nil {
		return err
	}
	if !found {
		return s.saveShipmentTx(tx, orderID, in)
	}
	if sh.Status == "entregado" || sh.Status == "retorno" {
		return nil // un envío cerrado no se edita desde el pedido
	}
	if err := s.applyShipmentInput(tx, &sh, in); err != nil {
		return err
	}
	return tx.Save(&sh).Error
}

// dispatchDayWarning avisa (sin bloquear) si la fecha programada cae en un día en que el transportista no despacha.
func (s *Service) dispatchDayWarning(tx *gorm.DB, sh *database.EquipShipment) string {
	if sh.ScheduledDispatchDate == nil || sh.CarrierID == nil {
		return ""
	}
	c, err := s.carrierByIDTx(tx, sh.CarrierID)
	if err != nil || c == nil || strings.TrimSpace(c.DispatchDays) == "" {
		return ""
	}
	allowed := map[int]bool{}
	for _, p := range strings.Split(c.DispatchDays, ",") {
		if n, err := strconv.Atoi(strings.TrimSpace(p)); err == nil {
			allowed[n] = true
		}
	}
	d := *sh.ScheduledDispatchDate
	if allowed[int(d.Weekday())] {
		return ""
	}
	next := d
	for i := 0; i < 7; i++ {
		next = next.AddDate(0, 0, 1)
		if allowed[int(next.Weekday())] {
			break
		}
	}
	return fmt.Sprintf("%s despacha %s: el %s (%s) no es día de despacho. El siguiente es el %s (%s).",
		c.Name, formatDaysEs(c.DispatchDays), d.Format("02/01"), weekdayNames[d.Weekday()], next.Format("02/01"), weekdayNames[next.Weekday()])
}

func formatDaysEs(csv string) string {
	var names []string
	for _, p := range strings.Split(csv, ",") {
		if n, err := strconv.Atoi(strings.TrimSpace(p)); err == nil && n >= 0 && n <= 6 {
			names = append(names, weekdayNames[n])
		}
	}
	return strings.Join(names, ", ")
}

// ── Acciones sobre el envío ────────────────────────────────────────────────

func (s *Service) shipmentActionTx(orderID uint, fn func(tx *gorm.DB, o *database.EquipOrder, sh *database.EquipShipment, c *database.EquipCarrier) ([]string, error)) (*OrderResult, error) {
	var warnings []string
	err := s.db.Transaction(func(tx *gorm.DB) error {
		o, err := s.loadOrderForUpdate(tx, orderID)
		if err != nil {
			return err
		}
		if o.Status != "registrado" {
			return invalid("el pedido debe estar confirmado para gestionar su envío")
		}
		var sh database.EquipShipment
		if found, err := findOne(tx, &sh, "order_id = ? AND is_current = ?", o.ID, true); err != nil {
			return err
		} else if !found {
			return invalid("el pedido no tiene envío")
		}
		c, err := s.carrierByIDTx(tx, sh.CarrierID)
		if err != nil {
			return err
		}
		w, err := fn(tx, o, &sh, c)
		if err != nil {
			return err
		}
		warnings = w
		return tx.Save(&sh).Error
	})
	if err != nil {
		return nil, err
	}
	v, err := s.GetOrder(orderID)
	if err != nil {
		return nil, err
	}
	return &OrderResult{Order: v, Warnings: warnings}, nil
}

func actionDate(raw string) (time.Time, error) {
	d, err := parseDateOnly(raw)
	if err != nil {
		return time.Time{}, err
	}
	if d == nil {
		return todayNoon(), nil
	}
	if d.After(todayNoon().AddDate(0, 0, 1)) {
		return time.Time{}, invalid("la fecha no puede ser futura")
	}
	return *d, nil
}

// DispatchOrder marca el envío como despachado (en tránsito). Exige pedido validado; el N° de guía NO es
// obligatorio (la agencia lo entrega después): si falta, se despacha igual con un aviso para registrarlo luego.
func (s *Service) DispatchOrder(orderID uint, date string) (*OrderResult, error) {
	res, err := s.dispatchOrder(orderID, date)
	if err == nil {
		s.maybeSubscribeAsync(orderID)
	}
	return res, err
}

func (s *Service) dispatchOrder(orderID uint, date string) (*OrderResult, error) {
	return s.shipmentActionTx(orderID, func(tx *gorm.DB, o *database.EquipOrder, sh *database.EquipShipment, c *database.EquipCarrier) ([]string, error) {
		if sh.Status != "pendiente_envio" {
			return nil, invalid("el envío ya está %s", shipmentStatusLabel(sh.Status))
		}
		d, err := actionDate(date)
		if err != nil {
			return nil, err
		}
		sh.DispatchedAt, sh.Status = &d, "en_transito"
		if sh.ScheduledDispatchDate == nil {
			sh.ScheduledDispatchDate = &d
		}
		if sh.DeliveryMode != "agencia" { // entrega en oficina/recojo: no pasa por una agencia
			return nil, nil
		}
		var warns []string
		if strings.TrimSpace(sh.GuideNumber) == "" {
			warns = append(warns, "Se despachó sin N° de guía: regístralo cuando la agencia te lo entregue.")
		}
		if msg := s.dispatchDayWarning(tx, &database.EquipShipment{ScheduledDispatchDate: &d, CarrierID: sh.CarrierID}); msg != "" {
			warns = append(warns, msg)
		}
		return warns, nil
	})
}

// MarkArrived registra la llegada a la agencia: arranca el plazo de recojo del transportista.
func (s *Service) MarkArrived(orderID uint, date string) (*OrderResult, error) {
	return s.shipmentActionTx(orderID, func(tx *gorm.DB, o *database.EquipOrder, sh *database.EquipShipment, c *database.EquipCarrier) ([]string, error) {
		if sh.Status != "en_transito" {
			return nil, invalid("solo un envío en tránsito puede llegar a la agencia (estado: %s)", shipmentStatusLabel(sh.Status))
		}
		d, err := arrivalDate(date)
		if err != nil {
			return nil, err
		}
		// El despacho suele registrarse tarde (el paquete ya salió y llegó antes de marcarlo en el
		// sistema). La llegada real manda: se retrocede el despacho a esa fecha en vez de bloquear.
		var warns []string
		if sh.DispatchedAt != nil && d.Before(noonLima(*sh.DispatchedAt)) {
			warns = append(warns, fmt.Sprintf("El despacho estaba registrado el %s; se ajustó al %s porque el paquete no puede llegar antes de salir.", sh.DispatchedAt.Format("02/01/2006"), d.Format("02/01/2006")))
			sh.DispatchedAt = &d
		}
		days := 15
		if c != nil && c.PickupDays > 0 {
			days = c.PickupDays
		}
		deadline := d.AddDate(0, 0, days)
		sh.ArrivedAt, sh.PickupDeadline, sh.Status = &d, &deadline, "en_agencia"
		return warns, nil
	})
}

// MarkPickedUp cierra el envío: el cliente recogió el paquete. Si queda saldo, solo avisa (el cobro es independiente).
func (s *Service) MarkPickedUp(orderID uint, date string) (*OrderResult, error) {
	return s.shipmentActionTx(orderID, func(tx *gorm.DB, o *database.EquipOrder, sh *database.EquipShipment, c *database.EquipCarrier) ([]string, error) {
		direct := sh.DeliveryMode != "agencia" // oficina o recojo: no hay llegada a agencia
		switch {
		case sh.Status == "en_agencia":
		case direct && (sh.Status == "pendiente_envio" || sh.Status == "en_transito"):
		default:
			return nil, invalid("el envío está %s: no se puede registrar el recojo", shipmentStatusLabel(sh.Status))
		}
		d, err := actionDate(date)
		if err != nil {
			return nil, err
		}
		// Igual que en la llegada: fechas anteriores a las ya registradas se retroceden (registro tardío).
		var warns []string
		if sh.ArrivedAt != nil && d.Before(noonLima(*sh.ArrivedAt)) {
			warns = append(warns, fmt.Sprintf("La llegada estaba registrada el %s; se ajustó al %s porque el recojo no puede ser anterior.", sh.ArrivedAt.Format("02/01/2006"), d.Format("02/01/2006")))
			sh.ArrivedAt = &d
		}
		if sh.DispatchedAt != nil && d.Before(noonLima(*sh.DispatchedAt)) {
			warns = append(warns, fmt.Sprintf("El despacho estaba registrado el %s; se ajustó al %s.", sh.DispatchedAt.Format("02/01/2006"), d.Format("02/01/2006")))
			sh.DispatchedAt = &d
		}
		if sh.DispatchedAt == nil {
			sh.DispatchedAt = &d
		}
		sh.PickedUpAt, sh.Status = &d, "entregado"
		if o.BalanceAmount > 0.004 {
			warns = append(warns, fmt.Sprintf("El pedido %d fue recogido con un saldo pendiente de S/ %.2f: queda en cobranza hasta registrar el cobro.", o.OrderNumber, o.BalanceAmount))
		}
		return warns, nil
	})
}

// UndoPickup deshace un «Cliente recogió» marcado por error: el envío vuelve al estado que tenía antes (en agencia si ya
// había llegado; si no, en tránsito o por despachar) y se borra la fecha de recojo. Después el pedido admite un retorno u
// otro recojo con normalidad. Exige motivo. Si Shalom ya informó «entregado», el siguiente seguimiento lo volvería a
// marcar: el aviso lo advierte, porque en ese caso la entrega es real según la agencia.
func (s *Service) UndoPickup(orderID uint, reason string) (*OrderResult, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return nil, invalid("indica el motivo para deshacer el recojo")
	}
	return s.shipmentActionTx(orderID, func(tx *gorm.DB, o *database.EquipOrder, sh *database.EquipShipment, c *database.EquipCarrier) ([]string, error) {
		if sh.Status != "entregado" {
			return nil, invalid("solo se puede deshacer el recojo de un envío entregado (estado: %s)", shipmentStatusLabel(sh.Status))
		}
		switch {
		case sh.DeliveryMode == "agencia" && sh.ArrivedAt != nil:
			sh.Status = "en_agencia"
		case sh.DispatchedAt != nil:
			sh.Status = "en_transito"
		default:
			sh.Status = "pendiente_envio"
		}
		sh.PickedUpAt = nil
		sh.Notes = strings.TrimSpace(sh.Notes + " | Recojo deshecho: " + reason)
		var warns []string
		if sh.ProviderStatus == "entregado" {
			warns = append(warns, "Shalom informó este envío como entregado: al actualizar el seguimiento volverá a marcarse como recogido.")
		}
		return warns, nil
	})
}

// MarkLabelPrinted deja constancia de que se imprimió el rótulo.
func (s *Service) MarkLabelPrinted(orderID uint) error {
	now := time.Now()
	return s.db.Model(&database.EquipShipment{}).Where("order_id = ? AND is_current = ?", orderID, true).Update("label_printed_at", now).Error
}

// ── Vista de envíos ────────────────────────────────────────────────────────

// alertFor semáforo del plazo de recojo de un paquete en agencia.
func alertFor(sh *database.EquipShipment, yellow, red int, today time.Time) (since, left *int, level string) {
	if sh.Status != "en_agencia" || sh.ArrivedAt == nil {
		return nil, nil, ""
	}
	d := daysInAgency(*sh.ArrivedAt, today)
	since = &d
	if sh.PickupDeadline != nil {
		l := int(noonLima(*sh.PickupDeadline).Sub(today).Hours() / 24)
		left = &l
		if l < 0 {
			return since, left, "vencido"
		}
	}
	switch {
	case d >= red:
		level = "rojo"
	case d >= yellow:
		level = "amarillo"
	default:
		level = "verde"
	}
	return since, left, level
}

// ShipmentView envío con los datos calculados que usa la pantalla.
type ShipmentView struct {
	database.EquipShipment
	CarrierName       string `json:"carrier_name"`
	OriginAgencyName  string `json:"origin_agency_name"`
	CarrierCode       string `json:"carrier_code"`
	GuideLabel        string `json:"guide_label"`
	TrackingURL       string `json:"tracking_url"`
	DispatchDays      string `json:"dispatch_days"`
	DaysSinceArrival  *int   `json:"days_since_arrival"`
	DaysLeft          *int   `json:"days_left"`
	Alert             string `json:"alert"` // verde | amarillo | rojo | vencido | ""
	DispatchDayNotice string `json:"dispatch_day_notice"`
}

func (s *Service) shipmentView(tx *gorm.DB, sh database.EquipShipment, st *database.EquipSettings) ShipmentView {
	v := ShipmentView{EquipShipment: sh}
	v.OriginAgencyName = s.agencyName(tx, sh.OriginAgencyID)
	if c, _ := s.carrierByIDTx(tx, sh.CarrierID); c != nil {
		v.CarrierName, v.CarrierCode, v.GuideLabel, v.DispatchDays = c.Name, c.Code, c.GuideLabel, c.DispatchDays
		if c.TrackingURLTpl != "" && sh.GuideNumber != "" {
			v.TrackingURL = strings.ReplaceAll(c.TrackingURLTpl, "{guia}", sh.GuideNumber)
		}
	}
	v.DaysSinceArrival, v.DaysLeft, v.Alert = alertFor(&sh, st.AlertYellowDays, st.AlertRedDays, todayNoon())
	if sh.Status == "pendiente_envio" {
		v.DispatchDayNotice = s.dispatchDayWarning(tx, &sh)
	}
	return v
}

// PackingLine producto a armar para un pedido (los combos ya vienen expandidos y sumados).
type PackingLine struct {
	ProductID uint   `json:"product_id"`
	Code      string `json:"code"`
	Quantity  int    `json:"quantity"`
}

func (s *Service) packingFor(tx *gorm.DB, items []database.EquipOrderItem) []PackingLine {
	outs, err := outflows(items)
	if err != nil || len(outs) == 0 {
		return nil
	}
	sum := map[uint]int{}
	var order []uint
	for _, o := range outs {
		if _, ok := sum[o.ProductID]; !ok {
			order = append(order, o.ProductID)
		}
		sum[o.ProductID] += o.Quantity
	}
	var prods []database.EquipProduct
	_ = tx.Where("id IN ?", order).Find(&prods).Error
	code := map[uint]string{}
	for _, p := range prods {
		code[p.ID] = p.Code
	}
	lines := make([]PackingLine, 0, len(order))
	for _, id := range order {
		lines = append(lines, PackingLine{ProductID: id, Code: code[id], Quantity: sum[id]})
	}
	return lines
}

// ShipmentRow fila del tablero de envíos.
type ShipmentRow struct {
	ShipmentView
	OrderNumber      int           `json:"order_number"`
	CustomerName     string        `json:"customer_name"`
	CustomerPhone    string        `json:"customer_phone"`
	ValidationStatus string        `json:"validation_status"`
	PaymentStatus    string        `json:"payment_status"`
	TotalAmount      float64       `json:"total_amount"`
	BalanceAmount    float64       `json:"balance_amount"`
	ReadyToDispatch  bool          `json:"ready_to_dispatch"`
	Packing          []PackingLine `json:"packing"`
}

type ShipmentFilter struct {
	Status    string // "" = abiertos (todo menos recogidos)
	CarrierID uint
	Q         string
}

// ListShipments tablero de envíos (por despachar, en tránsito, en agencia, recogidos).
func (s *Service) ListShipments(f ShipmentFilter) ([]ShipmentRow, error) {
	tx := s.db.Table("equip_shipments sh").
		Select("sh.*").
		Joins("JOIN equip_orders o ON o.id = sh.order_id").
		Where("sh.is_current = ? AND o.status = ?", true, "registrado")
	switch f.Status {
	case "":
		tx = tx.Where("sh.status <> ?", "entregado")
	default:
		tx = tx.Where("sh.status = ?", f.Status)
	}
	if f.CarrierID > 0 {
		tx = tx.Where("sh.carrier_id = ?", f.CarrierID)
	}
	if q := strings.TrimSpace(f.Q); q != "" {
		like := "%" + strings.ToLower(q) + "%"
		tx = tx.Where("LOWER(o.customer_name) LIKE ? OR sh.guide_number LIKE ? OR CAST(o.order_number AS CHAR) = ?", like, "%"+q+"%", q)
	}
	var shs []database.EquipShipment
	if err := tx.Order("COALESCE(sh.scheduled_dispatch_date, sh.created_at) ASC, sh.id ASC").Limit(500).Scan(&shs).Error; err != nil {
		return nil, err
	}
	st, err := s.GetSettings()
	if err != nil {
		return nil, err
	}
	ids := make([]uint, 0, len(shs))
	for _, sh := range shs {
		ids = append(ids, sh.OrderID)
	}
	orders := map[uint]database.EquipOrder{}
	items := map[uint][]database.EquipOrderItem{}
	if len(ids) > 0 {
		var os []database.EquipOrder
		if err := s.db.Where("id IN ?", ids).Find(&os).Error; err != nil {
			return nil, err
		}
		for _, o := range os {
			orders[o.ID] = o
		}
		var its []database.EquipOrderItem
		if err := s.db.Where("order_id IN ?", ids).Order("line_no").Find(&its).Error; err != nil {
			return nil, err
		}
		for _, it := range its {
			items[it.OrderID] = append(items[it.OrderID], it)
		}
	}
	rows := make([]ShipmentRow, 0, len(shs))
	for _, sh := range shs {
		o := orders[sh.OrderID]
		rows = append(rows, ShipmentRow{
			ShipmentView: s.shipmentView(s.db, sh, st), OrderNumber: o.OrderNumber, CustomerName: o.CustomerName, CustomerPhone: o.CustomerPhone,
			ValidationStatus: o.ValidationStatus, PaymentStatus: o.PaymentStatus, TotalAmount: o.TotalAmount, BalanceAmount: o.BalanceAmount,
			ReadyToDispatch: sh.Status == "pendiente_envio",
			Packing:         s.packingFor(s.db, items[sh.OrderID]),
		})
	}
	return rows, nil
}

// ShippingDataInput los únicos tres datos que edita el modal rápido de «Agencia» en Pedidos.
type ShippingDataInput struct {
	GuideNumber           string   `json:"guide_number"`            // vacío = quitar la guía
	FreightCost           *float64 `json:"freight_cost"`            // nil = no cambia; máx. 2 decimales
	ScheduledDispatchDate string   `json:"scheduled_dispatch_date"` // AAAA-MM-DD; vacío = quitar la fecha
}

// ShippingDataChange valores antes y después (para la auditoría).
type ShippingDataChange struct {
	GuideBefore, GuideAfter   string
	FreightBefore, FreightAft float64
	DateBefore, DateAfter     string
}

var guideCharsRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ./_-]*$`)

// UpdateShippingData actualiza solo guía, flete y despacho programado del envío vigente, sin tocar lo demás.
// Pedido anulado o envío entregado/en retorno no se edita. Si el pedido aún no tiene envío, se crea uno pendiente
// (igual que al guardar el pedido). Aplica la validación de formato de guía del transportista.
func (s *Service) UpdateShippingData(orderID uint, in ShippingDataInput) (*OrderResult, *ShippingDataChange, error) {
	var warnings []string
	ch := &ShippingDataChange{}
	err := s.db.Transaction(func(tx *gorm.DB) error {
		o, err := s.loadOrderForUpdate(tx, orderID)
		if err != nil {
			return err
		}
		if o.Status == "anulado" {
			return invalid("el pedido está anulado")
		}
		guide := strings.TrimSpace(in.GuideNumber)
		if len(guide) > 40 || (guide != "" && !guideCharsRe.MatchString(guide)) {
			return invalid("la guía solo admite letras, números y - . / _ (máximo 40 caracteres)")
		}
		if in.FreightCost != nil {
			f := *in.FreightCost
			if f < 0 || f > 100000 || math.Abs(f*100-math.Round(f*100)) > 1e-6 {
				return invalid("el costo del flete debe ser un monto entre 0 y 100000 con máximo 2 decimales")
			}
		}
		date, err := parseDateOnly(in.ScheduledDispatchDate)
		if err != nil {
			return err
		}
		var sh database.EquipShipment
		found, err := findOne(tx, &sh, "order_id = ? AND is_current = ?", orderID, true)
		if err != nil {
			return err
		}
		if !found {
			sh = database.EquipShipment{OrderID: orderID, Status: "pendiente_envio", IsCurrent: true, DeliveryMode: "agencia"}
			if err := s.applyShipmentInput(tx, &sh, nil); err != nil {
				return err
			}
		} else if sh.Status == "entregado" || sh.Status == "retorno" {
			return invalid("el envío ya está %s: no se puede modificar", shipmentStatusLabel(sh.Status))
		}
		if guide != "" {
			if c, err := s.carrierByIDTx(tx, sh.CarrierID); err != nil {
				return err
			} else if c != nil && c.GuideFormat != "" {
				if re, err := regexp.Compile(c.GuideFormat); err == nil && !re.MatchString(guide) {
					return invalid("el %s no tiene el formato de %s", strings.ToLower(c.GuideLabel), c.Name)
				}
			}
		}
		ch.GuideBefore, ch.FreightBefore = sh.GuideNumber, sh.FreightCost
		if sh.ScheduledDispatchDate != nil {
			ch.DateBefore = sh.ScheduledDispatchDate.Format("2006-01-02")
		}
		sh.GuideNumber, sh.ScheduledDispatchDate = guide, date
		if in.FreightCost != nil {
			sh.FreightCost = *in.FreightCost
		}
		ch.GuideAfter, ch.FreightAft = sh.GuideNumber, sh.FreightCost
		if date != nil {
			ch.DateAfter = date.Format("2006-01-02")
		}
		if err := tx.Save(&sh).Error; err != nil {
			return err
		}
		if sh.Status == "pendiente_envio" {
			if msg := s.dispatchDayWarning(tx, &sh); msg != "" {
				warnings = append(warnings, msg)
			}
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	v, err := s.GetOrder(orderID)
	if err != nil {
		return nil, nil, err
	}
	return &OrderResult{Order: v, Warnings: warnings}, ch, nil
}
