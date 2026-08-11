package service

import (
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"tukifac/pkg/database"
	"tukifac/pkg/tax"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	OrderTypeDineIn    = "dine_in"
	OrderTypeTakeaway  = "takeaway"
	OrderTypeDelivery  = "delivery"
	OrderTypeQuickSale = "quick_sale"
)

const (
	OrderStatusDraft         = "draft"
	OrderStatusPending       = "pending"
	OrderStatusSentToKitchen = "sent_to_kitchen"
	OrderStatusPreparing     = "preparing"
	OrderStatusReady         = "ready"
	OrderStatusOnTheWay      = "on_the_way"
	OrderStatusDelivered     = "delivered"
	OrderStatusPaid          = "paid"
	OrderStatusCancelled     = "cancelled"
)

// OpenSessionInput datos al abrir un pedido (mesa, POS, delivery, llevar).
type OpenSessionInput struct {
	TableID            *uint
	StaffID            *uint
	BranchID           uint
	UserID             uint
	Guests             int
	Notes              string
	OrderType          string
	ContactID          *uint
	CustomerName       string
	CustomerPhone      string
	DeliveryDriverID   *uint
	DeliveryAddress    string
	DeliveryReference  string
	EstimatedMinutes   int
	SaveAsDraft        bool
}

// UpdateSessionInput actualiza metadatos del pedido sin tocar ítems.
type UpdateSessionInput struct {
	ContactID         *uint
	CustomerName      string
	CustomerPhone     string
	DeliveryDriverID  *uint
	DeliveryAddress   string
	DeliveryReference string
	EstimatedMinutes  int
	Notes             string
	OrderStatus       string
}

// OrderSummary vista agrupada para comandas / POS.
type OrderSummary struct {
	database.TenantTableSession
	TableName      string `json:"table_name"`
	FloorName      string `json:"floor_name"`
	WaiterName     string `json:"waiter_name"`
	DriverName     string `json:"driver_name"`
	ContactName    string `json:"contact_name"`
	ItemCount      int    `json:"item_count"`
	ActiveComandas int    `json:"active_comandas"`
}

// PrecuentaPayload resumen imprimible sin venta.
type PrecuentaPayload struct {
	OrderCode          string              `json:"order_code"`
	OrderType          string              `json:"order_type"`
	TableName          string              `json:"table_name"`
	CustomerName       string              `json:"customer_name"`
	CustomerPhone      string              `json:"customer_phone"`
	DeliveryAddress    string              `json:"delivery_address"`
	DeliveryReference  string              `json:"delivery_reference"`
	DriverName         string              `json:"driver_name"`
	OpenedAt           time.Time           `json:"opened_at"`
	Notes              string              `json:"notes"`
	Subtotal      float64             `json:"subtotal"`
	TaxAmount     float64             `json:"tax_amount"`
	Total         float64             `json:"total"`
	Lines         []PrecuentaLine     `json:"lines"`
}

type PrecuentaLine struct {
	ProductName   string  `json:"product_name"`
	Quantity      float64 `json:"quantity"`
	UnitPrice     float64 `json:"unit_price"`
	LineTotal     float64 `json:"line_total"`
	Notes         string  `json:"notes"`
	ModifiersJSON string  `json:"modifiers_json,omitempty"`
}

func normalizeOrderType(t string, tableID *uint) string {
	switch t {
	case OrderTypeDineIn, OrderTypeTakeaway, OrderTypeDelivery, OrderTypeQuickSale:
		return t
	}
	if tableID != nil {
		return OrderTypeDineIn
	}
	return OrderTypeQuickSale
}

func initialOrderStatus(orderType string, saveDraft bool) string {
	if saveDraft {
		return OrderStatusDraft
	}
	switch orderType {
	case OrderTypeDineIn:
		return OrderStatusPending
	case OrderTypeTakeaway, OrderTypeDelivery:
		return OrderStatusDraft
	default:
		return OrderStatusDraft
	}
}

func (s *RestaurantService) generateOrderCode(tx *gorm.DB, branchID uint, at time.Time) (string, error) {
	day := at.Format("20060102")
	prefix := "P-" + day + "-"
	var count int64
	if err := tx.Model(&database.TenantTableSession{}).
		Where("branch_id = ? AND order_code LIKE ?", branchID, prefix+"%").
		Count(&count).Error; err != nil {
		return "", err
	}
	return fmt.Sprintf("%s%04d", prefix, count+1), nil
}

func (s *RestaurantService) OpenSession(in OpenSessionInput) (*database.TenantTableSession, error) {
	return s.OpenTableExtended(in)
}

func (s *RestaurantService) OpenTableExtended(in OpenSessionInput) (*database.TenantTableSession, error) {
	tableID := in.TableID
	orderType := normalizeOrderType(in.OrderType, tableID)
	now := time.Now()
	var result *database.TenantTableSession

	err := s.db.Transaction(func(tx *gorm.DB) error {
		if tableID != nil {
			var table database.TenantRestaurantTable
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&table, *tableID).Error; err != nil {
				return errors.New("mesa no encontrada")
			}
			if table.BranchID != in.BranchID {
				return errors.New("la mesa no pertenece a esta sucursal")
			}

			existing, err := s.findOpenSessionForTableLocked(tx, *tableID)
			if err != nil {
				return err
			}
			if existing != nil {
				log.Printf("[restaurant] mesa id=%d ya tiene sesión open id=%d — reutilizando", *tableID, existing.ID)
				if err := s.syncTableStatusFromOpenSession(tx, *tableID); err != nil {
					return err
				}
				result = existing
				return nil
			}
		}

		sess := &database.TenantTableSession{
			TableID:           tableID,
			StaffID:           in.StaffID,
			UserID:            in.UserID,
			BranchID:          in.BranchID,
			Guests:            in.Guests,
			OpenedAt:          now,
			Status:            sessionStatusOpen,
			OrderType:         orderType,
			OrderStatus:       initialOrderStatus(orderType, in.SaveAsDraft),
			ContactID:         in.ContactID,
			CustomerName:      in.CustomerName,
			CustomerPhone:     in.CustomerPhone,
			DeliveryDriverID:  in.DeliveryDriverID,
			DeliveryAddress:   in.DeliveryAddress,
			DeliveryReference: in.DeliveryReference,
			EstimatedMinutes:  in.EstimatedMinutes,
			Notes:             in.Notes,
		}
		code, err := s.generateOrderCode(tx, in.BranchID, now)
		if err != nil {
			return err
		}
		sess.OrderCode = code
		if err := tx.Create(sess).Error; err != nil {
			if tableID != nil && isDuplicateOpenSessionError(err) {
				existing, err2 := s.findOpenSessionForTableLocked(tx, *tableID)
				if err2 != nil {
					return err2
				}
				if existing != nil {
					log.Printf("[restaurant] carrera al abrir mesa id=%d — sesión existente id=%d", *tableID, existing.ID)
					result = existing
					return s.syncTableStatusFromOpenSession(tx, *tableID)
				}
			}
			return err
		}
		result = sess
		if tableID != nil {
			return s.syncTableStatusFromOpenSession(tx, *tableID)
		}
		return nil
	})
	return result, err
}

func (s *RestaurantService) syncSessionOrderStatus(tx *gorm.DB, sessionID uint) error {
	var comandas []database.TenantComanda
	tx.Where("session_id = ? AND cancelled_at IS NULL", sessionID).Find(&comandas)
	if len(comandas) == 0 {
		return nil
	}

	var sess database.TenantTableSession
	if tx.First(&sess, sessionID).Error != nil {
		return nil
	}
	if sess.Status != "open" {
		return nil
	}

	status := OrderStatusSentToKitchen
	hasPreparing := false
	allReady := true
	for _, c := range comandas {
		switch c.Status {
		case "preparacion":
			hasPreparing = true
			allReady = false
		case "pendiente":
			allReady = false
		case "lista", "entregada":
			// ok
		default:
			allReady = false
		}
	}
	if hasPreparing {
		status = OrderStatusPreparing
	} else if allReady {
		status = OrderStatusReady
		now := time.Now()
		tx.Model(&sess).Updates(map[string]interface{}{
			"order_status": status,
			"ready_at":     now,
		})
		return nil
	}

	updates := map[string]interface{}{"order_status": status}
	if sess.SentToKitchenAt == nil && status != OrderStatusDraft && status != OrderStatusPending {
		now := time.Now()
		updates["sent_to_kitchen_at"] = now
	}
	return tx.Model(&sess).Updates(updates).Error
}

func (s *RestaurantService) ListOpenOrders(branchID uint, orderType string) ([]OrderSummary, error) {
	q := s.db.Where("branch_id = ? AND status = ?", branchID, "open")
	if orderType != "" && orderType != "all" {
		q = q.Where("order_type = ?", orderType)
	}
	var sessions []database.TenantTableSession
	if err := q.Order("opened_at DESC").Find(&sessions).Error; err != nil {
		return nil, err
	}
	out := make([]OrderSummary, 0, len(sessions))
	for _, sess := range sessions {
		sum := OrderSummary{TenantTableSession: sess}
		if sess.TableID != nil {
			var table database.TenantRestaurantTable
			if s.db.First(&table, *sess.TableID).Error == nil {
				sum.TableName = table.Name
				var floor database.TenantRestaurantFloor
				if s.db.First(&floor, table.FloorID).Error == nil {
					sum.FloorName = floor.Name
				}
			}
		}
		if sess.StaffID != nil {
			sum.WaiterName = s.staffDisplayName(sess.StaffID)
		}
		if sess.DeliveryDriverID != nil {
			var d database.TenantDeliveryDriver
			if s.db.First(&d, *sess.DeliveryDriverID).Error == nil {
				sum.DriverName = d.Name
			}
		}
		if sess.ContactID != nil {
			var c database.TenantContact
			if s.db.First(&c, *sess.ContactID).Error == nil {
				sum.ContactName = c.BusinessName
			}
		}
		if sum.CustomerName == "" && sum.ContactName != "" {
			sum.CustomerName = sum.ContactName
		}
		var cnt int64
		s.db.Model(&database.TenantComanda{}).
			Where("session_id = ? AND cancelled_at IS NULL", sess.ID).
			Count(&cnt)
		sum.ItemCount = int(cnt)
		s.db.Model(&database.TenantComanda{}).
			Where("session_id = ? AND cancelled_at IS NULL AND status NOT IN ('entregada')", sess.ID).
			Count(&cnt)
		sum.ActiveComandas = int(cnt)
		out = append(out, sum)
	}
	return out, nil
}

func (s *RestaurantService) UpdateSession(sessionID uint, in UpdateSessionInput) error {
	var sess database.TenantTableSession
	if err := s.db.First(&sess, sessionID).Error; err != nil {
		return errors.New("pedido no encontrado")
	}
	if sess.Status != "open" {
		return errors.New("el pedido ya está cerrado")
	}
	updates := map[string]interface{}{
		"customer_name":      in.CustomerName,
		"customer_phone":     in.CustomerPhone,
		"delivery_address":   in.DeliveryAddress,
		"delivery_reference": in.DeliveryReference,
		"estimated_minutes":  in.EstimatedMinutes,
		"notes":              in.Notes,
	}
	if in.ContactID != nil {
		updates["contact_id"] = in.ContactID
	}
	if in.DeliveryDriverID != nil {
		updates["delivery_driver_id"] = in.DeliveryDriverID
	}
	if in.OrderStatus != "" {
		valid := map[string]bool{
			OrderStatusDraft: true, OrderStatusPending: true, OrderStatusOnTheWay: true,
			OrderStatusDelivered: true,
		}
		if !valid[in.OrderStatus] {
			return errors.New("estado de pedido no permitido en actualización manual")
		}
		updates["order_status"] = in.OrderStatus
	}
	return s.db.Model(&sess).Updates(updates).Error
}

func (s *RestaurantService) UpdateOrderStatus(sessionID uint, orderStatus string) error {
	var sess database.TenantTableSession
	if err := s.db.First(&sess, sessionID).Error; err != nil {
		return errors.New("pedido no encontrado")
	}
	if sess.Status != "open" {
		return errors.New("el pedido ya está cerrado")
	}
	allowed := map[string]bool{
		OrderStatusOnTheWay: true, OrderStatusDelivered: true, OrderStatusReady: true,
		OrderStatusPending: true, OrderStatusDraft: true,
	}
	if !allowed[orderStatus] {
		return errors.New("estado no válido")
	}
	updates := map[string]interface{}{"order_status": orderStatus}
	if orderStatus == OrderStatusReady {
		now := time.Now()
		updates["ready_at"] = now
	}
	return s.db.Model(&sess).Updates(updates).Error
}

func (s *RestaurantService) GetPrecuenta(sessionID uint) (*PrecuentaPayload, error) {
	detail, err := s.GetSessionDetail(sessionID)
	if err != nil {
		return nil, err
	}
	taxCfg := tax.LoadFromDB(s.db)
	lines := make([]PrecuentaLine, 0)
	var subtotal, taxAmount, total float64
	for _, ord := range detail.Orders {
		active := make([]database.TenantComanda, 0, len(ord.Comandas))
		for _, c := range ord.Comandas {
			if c.CancelledAt == nil {
				active = append(active, c)
			}
		}
		// Mismo colapso que al facturar: un combo es una línea con su precio fijo, no sus
		// componentes a 0. La precuenta debe cuadrar con lo que el cliente terminará pagando.
		for _, line := range comandasToBillLines(active) {
			affType, priceIncludes := line.IgvAffectationType, line.PriceIncludesIgv
			if !line.IsCombo {
				affType, priceIncludes = comandaIgvForCalc(s.db, line.Comanda)
			}
			if strings.TrimSpace(affType) == "" {
				affType = "10"
			}
			payable := tax.CalcItemPayableTotal(line.UnitPrice, line.Quantity, 0, affType, priceIncludes, taxCfg)
			if !tax.IsBonificacionGravada(affType) {
				lineSub, lineTax, _ := tax.CalcItem(line.UnitPrice, line.Quantity, 0, affType, priceIncludes, taxCfg)
				subtotal += lineSub
				taxAmount += lineTax
			}
			total += payable
			lines = append(lines, PrecuentaLine{
				ProductName:   line.Name,
				Quantity:      line.Quantity,
				UnitPrice:     line.UnitPrice,
				LineTotal:     payable,
				Notes:         line.Notes,
				ModifiersJSON: line.ModifiersJSON,
			})
		}
	}
	if len(lines) == 0 {
		total = detail.TotalAmount
	}
	customer := detail.CustomerName
	if customer == "" {
		customer = detail.ContactName
	}
	if total == 0 && detail.TotalAmount > 0 {
		total = detail.TotalAmount
		subtotal = detail.TotalAmount
	}
	return &PrecuentaPayload{
		OrderCode:         detail.OrderCode,
		OrderType:         detail.OrderType,
		TableName:         detail.TableName,
		CustomerName:      customer,
		CustomerPhone:     detail.CustomerPhone,
		DeliveryAddress:   detail.DeliveryAddress,
		DeliveryReference: detail.DeliveryReference,
		DriverName:        detail.DriverName,
		OpenedAt:          detail.OpenedAt,
		Notes:             detail.Notes,
		Subtotal:      subtotal,
		TaxAmount:     taxAmount,
		Total:         total,
		Lines:         lines,
	}, nil
}

// Delivery drivers & companies CRUD

func (s *RestaurantService) ListDeliveryCompanies(activeOnly bool) ([]database.TenantDeliveryCompany, error) {
	var list []database.TenantDeliveryCompany
	q := s.db.Order("sort_order ASC, name ASC")
	if activeOnly {
		q = q.Where("active = ?", true)
	}
	return list, q.Find(&list).Error
}

func (s *RestaurantService) CreateDeliveryCompany(name string) (*database.TenantDeliveryCompany, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errors.New("nombre requerido")
	}
	var existing database.TenantDeliveryCompany
	if err := s.db.Where("name = ?", name).First(&existing).Error; err == nil {
		return nil, errors.New("ya existe una empresa con ese nombre")
	} else if !errors.Is(err, gorm.ErrRecordNotFound) && err != nil {
		return nil, err
	}
	c := &database.TenantDeliveryCompany{Name: name, Active: true}
	return c, s.db.Create(c).Error
}

func (s *RestaurantService) UpdateDeliveryCompany(id uint, name string, active bool, sortOrder int) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("nombre requerido")
	}
	var company database.TenantDeliveryCompany
	if err := s.db.First(&company, id).Error; err != nil {
		return errors.New("empresa no encontrada")
	}
	var dup database.TenantDeliveryCompany
	if err := s.db.Where("name = ? AND id <> ?", name, id).First(&dup).Error; err == nil {
		return errors.New("ya existe una empresa con ese nombre")
	}
	return s.db.Model(&company).Updates(map[string]interface{}{
		"name": name, "active": active, "sort_order": sortOrder,
	}).Error
}

func (s *RestaurantService) DeleteDeliveryCompany(id uint) error {
	var company database.TenantDeliveryCompany
	if err := s.db.First(&company, id).Error; err != nil {
		return errors.New("empresa no encontrada")
	}
	var drivers int64
	s.db.Model(&database.TenantDeliveryDriver{}).Where("delivery_company_id = ?", id).Count(&drivers)
	if drivers > 0 {
		return fmt.Errorf("no se puede eliminar: %d repartidor(es) vinculado(s) a esta empresa", drivers)
	}
	return s.db.Unscoped().Delete(&database.TenantDeliveryCompany{}, id).Error
}

func (s *RestaurantService) ListDeliveryDrivers(activeOnly bool) ([]database.TenantDeliveryDriver, error) {
	var list []database.TenantDeliveryDriver
	q := s.db.Preload("DeliveryCompany").Order("name ASC")
	if activeOnly {
		q = q.Where("active = ?", true)
	}
	return list, q.Find(&list).Error
}

func (s *RestaurantService) CreateDeliveryDriver(name, phone, vehicleType, plate, notes string, deliveryCompanyID *uint) (*database.TenantDeliveryDriver, error) {
	if strings.TrimSpace(name) == "" {
		return nil, errors.New("nombre requerido")
	}
	if deliveryCompanyID != nil && *deliveryCompanyID > 0 {
		var company database.TenantDeliveryCompany
		if err := s.db.First(&company, *deliveryCompanyID).Error; err != nil {
			return nil, errors.New("empresa de delivery no encontrada")
		}
	} else {
		deliveryCompanyID = nil
	}
	d := &database.TenantDeliveryDriver{
		Name: name, Phone: phone, VehicleType: vehicleType, Plate: plate, Notes: notes,
		DeliveryCompanyID: deliveryCompanyID, Active: true,
	}
	return d, s.db.Create(d).Error
}

func (s *RestaurantService) UpdateDeliveryDriver(id uint, name, phone, vehicleType, plate, notes string, active bool, deliveryCompanyID *uint) error {
	if deliveryCompanyID != nil && *deliveryCompanyID > 0 {
		var company database.TenantDeliveryCompany
		if err := s.db.First(&company, *deliveryCompanyID).Error; err != nil {
			return errors.New("empresa de delivery no encontrada")
		}
	} else {
		deliveryCompanyID = nil
	}
	return s.db.Model(&database.TenantDeliveryDriver{}).Where("id = ?", id).Updates(map[string]interface{}{
		"name": name, "phone": phone, "vehicle_type": vehicleType, "plate": plate, "notes": notes, "active": active,
		"delivery_company_id": deliveryCompanyID,
	}).Error
}

func (s *RestaurantService) DeleteDeliveryDriver(id uint) error {
	return s.db.Delete(&database.TenantDeliveryDriver{}, id).Error
}
