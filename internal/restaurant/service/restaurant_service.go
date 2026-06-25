package service

import (
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"tukifac/internal/restaurant/staff"
	"tukifac/pkg/database"
	"tukifac/pkg/docseries"
	"tukifac/pkg/gormutil"
	"tukifac/pkg/money"
	"tukifac/pkg/saas/docusage"
	"tukifac/pkg/tax"
	cashbanksvc "tukifac/internal/cashbank/service"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type RestaurantService struct {
	db *gorm.DB
}

func New(db *gorm.DB) *RestaurantService {
	return &RestaurantService{db: db}
}

// restaurantLinePayableTotal es el importe a cobrar por una línea (cantidad × precio unitario)
// según tipo de afectación SUNAT y si el precio incluye o no IGV.
func restaurantLinePayableTotal(
	taxCfg tax.Config,
	affType string,
	priceIncludesIgv bool,
	unitPrice, quantity float64,
) float64 {
	if strings.TrimSpace(affType) == "" {
		affType = "10"
	}
	_, _, total := tax.CalcItem(unitPrice, quantity, 0, affType, priceIncludesIgv, taxCfg)
	return money.RoundSunat(total)
}

// ============================= PISOS / SALAS =============================

func (s *RestaurantService) GetRestaurantSettings() (*database.TenantRestaurantSetting, error) {
	var cfg database.TenantRestaurantSetting
	if err := s.db.First(&cfg).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return &database.TenantRestaurantSetting{}, nil
		}
		return nil, err
	}
	return &cfg, nil
}

func isDeletionPinConfigured(stored string) bool {
	stored = strings.TrimSpace(stored)
	if stored == "" {
		return false
	}
	if strings.HasPrefix(stored, "$2a$") || strings.HasPrefix(stored, "$2b$") {
		return len(stored) > 20
	}
	return len(stored) >= 4
}

func (s *RestaurantService) HasDeletionPin() (bool, error) {
	cfg, err := s.GetRestaurantSettings()
	if err != nil {
		return false, err
	}
	return isDeletionPinConfigured(cfg.DeletionPin), nil
}

func (s *RestaurantService) SaveRestaurantSettings(deletionPin string) error {
	deletionPin = strings.TrimSpace(deletionPin)
	if len(deletionPin) < 4 {
		return errors.New("el PIN debe tener al menos 4 dígitos")
	}
	if len(deletionPin) > 6 {
		return errors.New("el PIN no puede tener más de 6 dígitos")
	}
	for _, r := range deletionPin {
		if r < '0' || r > '9' {
			return errors.New("el PIN solo puede contener dígitos")
		}
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(deletionPin), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	stored := string(hash)

	var cfg database.TenantRestaurantSetting
	if err := s.db.First(&cfg).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			cfg.DeletionPin = stored
			return s.db.Create(&cfg).Error
		}
		return err
	}
	return s.db.Model(&cfg).Update("deletion_pin", stored).Error
}

// VerifyDeletionPin verifica que el PIN coincida con el configurado. Si no hay PIN configurado, retorna error pidiendo configurarlo.
func (s *RestaurantService) VerifyDeletionPin(pin string) error {
	pin = strings.TrimSpace(pin)
	if pin == "" {
		return errors.New("se requiere el PIN de seguridad")
	}
	cfg, err := s.GetRestaurantSettings()
	if err != nil {
		return err
	}
	stored := strings.TrimSpace(cfg.DeletionPin)
	if !isDeletionPinConfigured(stored) {
		return errors.New("configure el PIN de seguridad en Ajustes del Restaurante (panel tenant) antes de anular pedidos o comandas")
	}
	if strings.HasPrefix(stored, "$2a$") || strings.HasPrefix(stored, "$2b$") {
		if bcrypt.CompareHashAndPassword([]byte(stored), []byte(pin)) != nil {
			return errors.New("PIN incorrecto")
		}
		return nil
	}
	// Compatibilidad con PIN en texto plano guardado antes del hash.
	if stored != pin {
		return errors.New("PIN incorrecto")
	}
	return nil
}

// ============================= PISOS / SALAS =============================

func (s *RestaurantService) ListFloors(branchID uint) ([]database.TenantRestaurantFloor, error) {
	var floors []database.TenantRestaurantFloor
	q := s.db.Where("active = ?", true)
	if branchID > 0 {
		q = q.Where("branch_id = ?", branchID)
	}
	err := q.Order("sort_order ASC, name ASC").Find(&floors).Error
	return floors, err
}

func (s *RestaurantService) CreateFloor(branchID uint, name string, order int) (*database.TenantRestaurantFloor, error) {
	if name == "" {
		return nil, errors.New("el nombre del piso es requerido")
	}
	if branchID == 0 {
		return nil, errors.New("sucursal requerida")
	}
	f := &database.TenantRestaurantFloor{BranchID: branchID, Name: name, SortOrder: order, Active: true}
	err := s.db.Create(f).Error
	return f, err
}

func (s *RestaurantService) UpdateFloor(id uint, name string, order int, active bool) error {
	return s.db.Model(&database.TenantRestaurantFloor{}).Where("id = ?", id).Updates(map[string]interface{}{
		"name": name, "sort_order": order, "active": active,
	}).Error
}

func (s *RestaurantService) DeleteFloor(id uint) error {
	var count int64
	s.db.Model(&database.TenantRestaurantTable{}).Where("floor_id = ? AND active = ?", id, true).Count(&count)
	if count > 0 {
		return errors.New("no se puede eliminar un piso con mesas activas")
	}
	return s.db.Delete(&database.TenantRestaurantFloor{}, id).Error
}

// ============================= MESAS =============================

type TableWithSession struct {
	database.TenantRestaurantTable
	FloorName   string  `json:"floor_name"`
	SessionID   *uint   `json:"session_id"`
	TotalAmount float64 `json:"total_amount"`
	WaiterName  string  `json:"waiter_name"`
}

func (s *RestaurantService) ListTables(branchID, floorID uint) ([]TableWithSession, error) {
	type raw struct {
		database.TenantRestaurantTable
		FloorName   string  `gorm:"column:floor_name"`
		SessionID   *uint   `gorm:"column:session_id"`
		TotalAmount float64 `gorm:"column:total_amount"`
		WaiterName  string  `gorm:"column:waiter_name"`
	}
	var rows []raw
	q := s.db.Table("tenant_restaurant_tables t").
		Select("t.*, f.name AS floor_name, ts.id AS session_id, COALESCE(ts.total_amount,0) AS total_amount, COALESCE(NULLIF(st.display_name,''), u.name, '') AS waiter_name").
		Joins("JOIN tenant_restaurant_floors f ON f.id = t.floor_id").
		Joins(`LEFT JOIN tenant_table_sessions ts ON ts.id = (
			SELECT s2.id FROM tenant_table_sessions s2
			WHERE s2.table_id = t.id AND s2.status = 'open'
			ORDER BY s2.opened_at DESC, s2.id DESC
			LIMIT 1
		)`).
		Joins("LEFT JOIN tenant_restaurant_staff st ON st.id = ts.staff_id").
		Joins("LEFT JOIN tenant_users u ON u.id = st.user_id").
		Where("t.active = ? AND t.deleted_at IS NULL", true)
	if branchID > 0 {
		q = q.Where("t.branch_id = ?", branchID)
	}
	if floorID > 0 {
		q = q.Where("t.floor_id = ?", floorID)
	}
	q.Order("f.sort_order ASC, t.name ASC").Scan(&rows)

	result := make([]TableWithSession, len(rows))
	for i, r := range rows {
		displayStatus := resolveTableDisplayStatus(r.Status, r.SessionID)
		tbl := r.TenantRestaurantTable
		tbl.Status = displayStatus
		result[i] = TableWithSession{
			TenantRestaurantTable: tbl,
			FloorName:             r.FloorName,
			SessionID:             r.SessionID,
			TotalAmount:           r.TotalAmount,
			WaiterName:            r.WaiterName,
		}
	}
	return result, nil
}

func (s *RestaurantService) CreateTable(branchID, floorID uint, name string, capacity int) (*database.TenantRestaurantTable, error) {
	if branchID == 0 || floorID == 0 || name == "" {
		return nil, errors.New("sucursal, piso y nombre son requeridos")
	}
	var floor database.TenantRestaurantFloor
	if err := s.db.Where("id = ? AND branch_id = ?", floorID, branchID).First(&floor).Error; err != nil {
		return nil, errors.New("piso no pertenece a esta sucursal")
	}
	t := &database.TenantRestaurantTable{BranchID: branchID, FloorID: floorID, Name: name, Capacity: capacity, Status: "libre", Active: true}
	err := s.db.Create(t).Error
	return t, err
}

func (s *RestaurantService) UpdateTable(id uint, floorID *uint, name string, capacity int, active bool) error {
	var table database.TenantRestaurantTable
	if err := s.db.First(&table, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errors.New("mesa no encontrada")
		}
		return err
	}

	updates := map[string]interface{}{
		"name":     name,
		"capacity": capacity,
		"active":   active,
	}

	if floorID != nil && *floorID != table.FloorID {
		if reason := s.tableDeleteBlockReason(&table); reason != "" {
			return fmt.Errorf("no se puede cambiar de piso: %s", reason)
		}
		var floor database.TenantRestaurantFloor
		if err := s.db.Where("id = ? AND branch_id = ?", *floorID, table.BranchID).First(&floor).Error; err != nil {
			return errors.New("piso no pertenece a esta sucursal")
		}
		updates["floor_id"] = *floorID
	}

	return s.db.Model(&table).Updates(updates).Error
}

// tableDeleteBlockReason devuelve el motivo por el que no se puede eliminar la mesa, o "" si está permitido.
func (s *RestaurantService) tableDeleteBlockReason(table *database.TenantRestaurantTable) string {
	if table == nil {
		return "mesa no encontrada"
	}
	if !table.Active {
		return "la mesa ya fue eliminada"
	}
	if table.Status != "libre" {
		switch table.Status {
		case "ocupada":
			return "la mesa está ocupada: cierre o libere la mesa antes de eliminarla"
		case "en_consumo":
			return "la mesa está en consumo: finalice la operación antes de eliminarla"
		default:
			return fmt.Sprintf("la mesa está en estado «%s»; debe estar libre para eliminarla", table.Status)
		}
	}

	var openSess database.TenantTableSession
	if err := s.db.Where("table_id = ? AND status = ?", table.ID, "open").First(&openSess).Error; err == nil {
		if strings.TrimSpace(openSess.OrderCode) != "" {
			return fmt.Sprintf("la mesa tiene el pedido %s abierto; anúlelo o ciérrelo antes de eliminar la mesa", openSess.OrderCode)
		}
		return "la mesa tiene un pedido abierto; anúlelo o ciérrelo antes de eliminar la mesa"
	}

	var sessionIDs []uint
	if err := s.db.Model(&database.TenantTableSession{}).
		Where("table_id = ? AND status IN ?", table.ID, []string{"open", "billed"}).
		Pluck("id", &sessionIDs).Error; err != nil {
		return "no se pudo verificar operaciones vinculadas a la mesa"
	}
	if len(sessionIDs) == 0 {
		return ""
	}

	var activeOrders int64
	s.db.Model(&database.TenantTableOrder{}).
		Where("session_id IN ? AND status = ?", sessionIDs, "active").
		Count(&activeOrders)
	if activeOrders > 0 {
		return fmt.Sprintf("la mesa tiene %d pedido(s) activo(s); finalice o anule la operación antes de eliminarla", activeOrders)
	}

	var pendingComandas int64
	s.db.Model(&database.TenantComanda{}).
		Where("session_id IN ? AND cancelled_at IS NULL", sessionIDs).
		Where("status IN ?", []string{"pendiente", "preparacion", "lista"}).
		Count(&pendingComandas)
	if pendingComandas > 0 {
		return fmt.Sprintf("la mesa tiene %d comanda(s) en cocina; espere la entrega o anule el pedido", pendingComandas)
	}

	return ""
}

func (s *RestaurantService) DeleteTable(id uint) error {
	var table database.TenantRestaurantTable
	if err := s.db.First(&table, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errors.New("mesa no encontrada")
		}
		return err
	}
	if reason := s.tableDeleteBlockReason(&table); reason != "" {
		return errors.New(reason)
	}
	// Borrado físico: ListTables usa Table() sin el scope de soft-delete de GORM.
	if err := s.db.Unscoped().Delete(&database.TenantRestaurantTable{}, id).Error; err != nil {
		return err
	}
	return nil
}

// ============================= SESIONES DE MESA =============================

type SessionDetail struct {
	database.TenantTableSession
	TableName   string        `json:"table_name"`
	FloorName   string        `json:"floor_name"`
	WaiterName  string        `json:"waiter_name"`
	DriverName  string        `json:"driver_name"`
	ContactName string        `json:"contact_name"`
	Orders      []OrderDetail `json:"orders"`
}

type OrderDetail struct {
	database.TenantTableOrder
	Comandas []database.TenantComanda `json:"comandas"`
}

func (s *RestaurantService) staffDisplayName(staffID *uint) string {
	if staffID == nil || *staffID == 0 {
		return ""
	}
	st, err := staff.New(s.db).GetStaffByID(*staffID)
	if err != nil {
		return ""
	}
	return staff.New(s.db).StaffDisplayName(st)
}

func (s *RestaurantService) OpenTable(tableID *uint, staffID *uint, branchID, userID uint, guests int, notes string) (*database.TenantTableSession, error) {
	return s.OpenTableExtended(OpenSessionInput{
		TableID: tableID, StaffID: staffID, BranchID: branchID, UserID: userID,
		Guests: guests, Notes: notes,
	})
}

func (s *RestaurantService) GetSessionDetail(sessionID uint) (*SessionDetail, error) {
	var sess database.TenantTableSession
	if err := s.db.First(&sess, sessionID).Error; err != nil {
		return nil, errors.New("sesión no encontrada")
	}

	detail := &SessionDetail{TenantTableSession: sess}

	// Datos de mesa y mozo
	if sess.TableID != nil {
		var table database.TenantRestaurantTable
		if s.db.First(&table, *sess.TableID).Error == nil {
			detail.TableName = table.Name
			// piso
			var floor database.TenantRestaurantFloor
			if s.db.First(&floor, table.FloorID).Error == nil {
				detail.FloorName = floor.Name
			}
		}
	}
	if sess.StaffID != nil {
		detail.WaiterName = s.staffDisplayName(sess.StaffID)
	}
	if sess.DeliveryDriverID != nil {
		var d database.TenantDeliveryDriver
		if s.db.First(&d, *sess.DeliveryDriverID).Error == nil {
			detail.DriverName = d.Name
		}
	}
	if sess.ContactID != nil {
		var c database.TenantContact
		if s.db.First(&c, *sess.ContactID).Error == nil {
			detail.ContactName = c.BusinessName
			if detail.CustomerName == "" {
				detail.CustomerName = c.BusinessName
			}
		}
	}

	// Pedidos con comandas
	var orders []database.TenantTableOrder
	s.db.Where("session_id = ? AND status = 'active'", sessionID).Order("order_number ASC").Find(&orders)
	detail.Orders = make([]OrderDetail, 0, len(orders))
	for _, o := range orders {
		var comandas []database.TenantComanda
		s.db.Where("order_id = ? AND cancelled_at IS NULL", o.ID).Order("created_at ASC").Find(&comandas)
		if len(comandas) == 0 {
			continue
		}
		detail.Orders = append(detail.Orders, OrderDetail{TenantTableOrder: o, Comandas: comandas})
	}

	return detail, nil
}

func (s *RestaurantService) GetActiveSessionByTable(tableID uint) (*database.TenantTableSession, error) {
	var count int64
	if err := s.db.Model(&database.TenantTableSession{}).
		Where("table_id = ? AND status = ?", tableID, sessionStatusOpen).
		Count(&count).Error; err != nil {
		return nil, err
	}
	if count > 1 {
		log.Printf("[restaurant] ADVERTENCIA: mesa id=%d tiene %d sesiones open; usando la más reciente", tableID, count)
	}

	var sess database.TenantTableSession
	err := s.db.Where("table_id = ? AND status = ?", tableID, sessionStatusOpen).
		Order("opened_at DESC, id DESC").
		First(&sess).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &sess, err
}

// ============================= PEDIDOS Y COMANDAS =============================

type NewOrderItem struct {
	ProductID          *uint   `json:"product_id"`
	ProductCode        string  `json:"product_code"`
	ProductName        string  `json:"product_name"`
	Quantity           float64 `json:"quantity"`
	UnitPrice          float64 `json:"unit_price"`
	Notes              string  `json:"notes"`
	ModifiersJSON      string  `json:"modifiers_json"`
	IgvAffectationType string  `json:"igv_affectation_type"`
	PriceIncludesIgv   bool    `json:"price_includes_igv"`
}

// comandaIgvForCalc devuelve afectación e «incluye IGV» de la línea (snapshot en comanda).
func comandaIgvForCalc(db *gorm.DB, c *database.TenantComanda) (affType string, priceIncludes bool) {
	affType = strings.TrimSpace(c.IgvAffectationType)
	if affType == "" {
		affType = "10"
	}
	priceIncludes = c.PriceIncludesIgv
	if c.ProductID != nil && strings.TrimSpace(c.IgvAffectationType) == "" {
		var p database.TenantProduct
		if db.First(&p, *c.ProductID).Error == nil {
			if p.IgvAffectationType != "" {
				affType = p.IgvAffectationType
			}
			priceIncludes = p.PriceIncludesIgv
		}
	}
	return affType, priceIncludes
}

func (s *RestaurantService) AddOrder(sessionID uint, staffID *uint, userID uint, items []NewOrderItem, notes string) (*OrderDetail, error) {
	if len(items) == 0 {
		return nil, errors.New("el pedido debe tener al menos un ítem")
	}

	var order database.TenantTableOrder
	var comandas []database.TenantComanda

	err := s.db.Transaction(func(tx *gorm.DB) error {
		var sess database.TenantTableSession
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&sess, sessionID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errors.New("sesión no encontrada")
			}
			return err
		}
		if sess.Status != sessionStatusOpen {
			return errors.New("la sesión ya está cerrada")
		}

		resolvedStaff := staffID
		if resolvedStaff == nil && sess.StaffID != nil {
			resolvedStaff = sess.StaffID
		}

		var lastOrder database.TenantTableOrder
		nextNum := 1
		if tx.Where("session_id = ?", sessionID).Order("order_number DESC").First(&lastOrder).Error == nil {
			nextNum = lastOrder.OrderNumber + 1
		}

		order = database.TenantTableOrder{
			SessionID:   sessionID,
			StaffID:     resolvedStaff,
			UserID:      userID,
			OrderNumber: nextNum,
			Notes:       notes,
			Status:      "active",
		}
		if err := tx.Create(&order).Error; err != nil {
			return err
		}

		var sessionTotal float64
		taxCfg := tax.LoadFromDB(tx)
		for i := range items {
			item := &items[i]
			if err := resolveRestaurantOrderItem(tx, item); err != nil {
				return err
			}
			prepArea := resolveProductPreparationArea(tx, item.ProductID)
			affType := strings.TrimSpace(item.IgvAffectationType)
			if affType == "" {
				affType = "10"
			}
			c := database.TenantComanda{
				OrderID:            order.ID,
				SessionID:          sessionID,
				ProductID:          item.ProductID,
				ProductCode:        item.ProductCode,
				ProductName:        item.ProductName,
				PreparationArea:    prepArea,
				Quantity:           item.Quantity,
				UnitPrice:          item.UnitPrice,
				Notes:              item.Notes,
				ModifiersJSON:      strings.TrimSpace(item.ModifiersJSON),
				IgvAffectationType:   affType,
				PriceIncludesIgv:   item.PriceIncludesIgv,
				Status:               "pendiente",
			}
			if err := tx.Create(&c).Error; err != nil {
				return err
			}
			if err := gormutil.PersistBoolWithDefault(tx, &c, "price_includes_igv", item.PriceIncludesIgv); err != nil {
				return err
			}
			c.PriceIncludesIgv = item.PriceIncludesIgv
			comandas = append(comandas, c)
			_, _, lineTotal := tax.CalcItem(item.UnitPrice, item.Quantity, 0, affType, item.PriceIncludesIgv, taxCfg)
			sessionTotal += money.RoundSunat(lineTotal)
		}

		tx.Model(&database.TenantTableSession{}).Where("id = ?", sessionID).
			UpdateColumn("total_amount", gorm.Expr("total_amount + ?", sessionTotal))

		now := time.Now()
		sessUpdates := map[string]interface{}{
			"order_status": OrderStatusSentToKitchen,
		}
		if sess.SentToKitchenAt == nil {
			sessUpdates["sent_to_kitchen_at"] = now
		}
		if sess.OrderStatus == OrderStatusDraft || sess.OrderStatus == OrderStatusPending || sess.OrderStatus == "" {
			sessUpdates["order_status"] = OrderStatusSentToKitchen
		}
		tx.Model(&database.TenantTableSession{}).Where("id = ?", sessionID).Updates(sessUpdates)
		return s.syncSessionOrderStatus(tx, sessionID)
	})

	if err != nil {
		return nil, err
	}
	return &OrderDetail{TenantTableOrder: order, Comandas: comandas}, nil
}

func comandaStatusRank(status string) int {
	switch status {
	case "preparacion":
		return 1
	case "lista":
		return 2
	case "entregada":
		return 3
	default:
		return 0 // pendiente u otro
	}
}

// UpdateComandaNotes actualiza la nota de una línea de comanda (instrucciones de cocina).
func (s *RestaurantService) UpdateComandaNotes(id uint, notes string) error {
	var c database.TenantComanda
	if err := s.db.First(&c, id).Error; err != nil {
		return errors.New("comanda no encontrada")
	}
	if c.CancelledAt != nil {
		return errors.New("la comanda está anulada")
	}
	trimmed := strings.TrimSpace(notes)
	if len(trimmed) > 500 {
		return errors.New("la nota no puede superar 500 caracteres")
	}
	return s.db.Model(&c).Update("notes", trimmed).Error
}

// UpdateComandaStatus cambia el estado de una comanda (solo avance, sin retroceder).
func (s *RestaurantService) UpdateComandaStatus(id uint, status string, userID uint) error {
	validStatuses := map[string]bool{
		"pendiente": true, "preparacion": true, "lista": true, "entregada": true,
	}
	if !validStatuses[status] {
		return errors.New("estado inválido: usa pendiente, preparacion, lista o entregada")
	}
	var c database.TenantComanda
	if err := s.db.First(&c, id).Error; err != nil {
		return errors.New("comanda no encontrada")
	}
	if c.CancelledAt != nil {
		return errors.New("la comanda está anulada")
	}
	if comandaStatusRank(status) <= comandaStatusRank(c.Status) {
		return errors.New("no se puede retroceder el estado de la comanda")
	}
	err := s.db.Model(&database.TenantComanda{}).Where("id = ?", id).Update("status", status).Error
	if err != nil {
		return err
	}
	return s.syncSessionOrderStatus(s.db, c.SessionID)
}

// CancelComanda anula una comanda (permiso o.cx / s.m + PIN de operaciones).
func (s *RestaurantService) CancelComanda(id uint, reason string, cancelledByID uint) error {
	var c database.TenantComanda
	if err := s.db.First(&c, id).Error; err != nil {
		return errors.New("comanda no encontrada")
	}
	return s.db.Transaction(func(tx *gorm.DB) error {
		return s.cancelComandaTx(tx, &c, reason, cancelledByID)
	})
}

func (s *RestaurantService) cancelComandaTx(tx *gorm.DB, c *database.TenantComanda, reason string, cancelledByID uint) error {
	if c.CancelledAt != nil {
		return errors.New("la comanda ya fue anulada")
	}
	if c.Status == "entregada" {
		return errors.New("no se puede anular una comanda ya entregada")
	}
	now := time.Now()
	if err := tx.Model(c).Updates(map[string]interface{}{
		"cancelled_at":    now,
		"cancelled_by_id": cancelledByID,
		"cancel_reason":   reason,
		"status":          "entregada",
	}).Error; err != nil {
		return err
	}
	taxCfg := tax.LoadFromDB(tx)
	affType, priceIncludes := comandaIgvForCalc(tx, c)
	_, _, deduct := tax.CalcItem(c.UnitPrice, c.Quantity, 0, affType, priceIncludes, taxCfg)
	deduct = money.RoundSunat(deduct)
	if err := tx.Model(&database.TenantTableSession{}).Where("id = ?", c.SessionID).
		UpdateColumn("total_amount", gorm.Expr("GREATEST(0, total_amount - ?)", deduct)).Error; err != nil {
		return err
	}
	return s.syncSessionOrderStatus(tx, c.SessionID)
}

// CancelAllComandasResult resultado de anulación masiva de comandas.
type CancelAllComandasResult struct {
	CancelledCount int `json:"cancelled_count"`
}

// CancelAllComandas anula todas las comandas cancelables de una sesión (opcionalmente solo una ronda/order_id).
func (s *RestaurantService) CancelAllComandas(sessionID uint, orderID *uint, pin, reason string, userID uint) (*CancelAllComandasResult, error) {
	if err := s.VerifyDeletionPin(pin); err != nil {
		return nil, err
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return nil, errors.New("indique el motivo de anulación")
	}

	var sess database.TenantTableSession
	if err := s.db.First(&sess, sessionID).Error; err != nil {
		return nil, errors.New("pedido no encontrado")
	}
	if sess.Status != "open" {
		return nil, errors.New("solo se pueden anular comandas de pedidos abiertos")
	}
	if sess.SaleID != nil {
		return nil, errors.New("no se puede anular: el pedido ya fue facturado")
	}

	q := s.db.Where(
		"session_id = ? AND cancelled_at IS NULL AND status != ?",
		sessionID, "entregada",
	)
	if orderID != nil && *orderID > 0 {
		q = q.Where("order_id = ?", *orderID)
	}
	var comandas []database.TenantComanda
	if err := q.Order("id ASC").Find(&comandas).Error; err != nil {
		return nil, err
	}
	if len(comandas) == 0 {
		return nil, errors.New("no hay comandas que se puedan anular")
	}

	result := &CancelAllComandasResult{}
	err := s.db.Transaction(func(tx *gorm.DB) error {
		for i := range comandas {
			c := comandas[i]
			if err := s.cancelComandaTx(tx, &c, reason, userID); err != nil {
				return err
			}
			result.CancelledCount++
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func resolveProductPreparationArea(tx *gorm.DB, productID *uint) string {
	if productID == nil || *productID == 0 {
		return "cocina"
	}
	var p database.TenantProduct
	if err := tx.Select("preparation_area").First(&p, *productID).Error; err != nil {
		return "cocina"
	}
	area := strings.TrimSpace(strings.ToLower(p.PreparationArea))
	if area == "" {
		return "cocina"
	}
	return area
}

// comandaSaleLineKey agrupa líneas de venta solo si comparten producto, snapshot de modificadores y precio unitario.
func comandaSaleLineKey(c database.TenantComanda) string {
	pid := uint(0)
	if c.ProductID != nil {
		pid = *c.ProductID
	}
	return fmt.Sprintf("%d|%s|%s|%.4f", pid, strings.TrimSpace(c.ProductCode), strings.TrimSpace(c.ModifiersJSON), c.UnitPrice)
}

// MarkComandaPrinted marca una línea de comanda como impresa.
func (s *RestaurantService) MarkComandaPrinted(id uint, userID uint) error {
	now := time.Now()
	upd := map[string]interface{}{"printed": true, "printed_at": now}
	if userID > 0 {
		upd["printed_by_id"] = userID
	}
	return s.db.Model(&database.TenantComanda{}).Where("id = ?", id).Updates(upd).Error
}

// MarkTableOrderPrinted marca una ronda completa (ticket) y todas sus líneas como impresas.
func (s *RestaurantService) MarkTableOrderPrinted(tableOrderID uint, userID uint) error {
	var order database.TenantTableOrder
	if err := s.db.First(&order, tableOrderID).Error; err != nil {
		return errors.New("comanda no encontrada")
	}
	now := time.Now()
	return s.db.Transaction(func(tx *gorm.DB) error {
		orderUpd := map[string]interface{}{"printed_at": now}
		lineUpd := map[string]interface{}{"printed": true, "printed_at": now}
		if userID > 0 {
			orderUpd["printed_by_id"] = userID
			lineUpd["printed_by_id"] = userID
		}
		if err := tx.Model(&database.TenantTableOrder{}).Where("id = ?", tableOrderID).Updates(orderUpd).Error; err != nil {
			return err
		}
		return tx.Model(&database.TenantComanda{}).Where("order_id = ? AND cancelled_at IS NULL", tableOrderID).Updates(lineUpd).Error
	})
}

// KitchenSessionMeta contexto del pedido para la vista de cocina.
type KitchenSessionMeta struct {
	OrderCode       string     `json:"order_code"`
	OrderType       string     `json:"order_type"`
	OrderStatus     string     `json:"order_status"`
	TableID         *uint      `json:"table_id"`
	TableName       string     `json:"table_name"`
	FloorName       string     `json:"floor_name"`
	CustomerName    string     `json:"customer_name"`
	CustomerPhone   string     `json:"customer_phone"`
	DeliveryAddress string     `json:"delivery_address"`
	WaiterName      string     `json:"waiter_name"`
	DriverName      string     `json:"driver_name"`
	OpenedAt        time.Time  `json:"session_opened_at"`
}

// KitchenComandaView línea de cocina con datos del pedido y de la ronda.
type KitchenComandaView struct {
	database.TenantComanda
	OrderNumber int `json:"order_number"`
	KitchenSessionMeta
}

// GetKitchenComandas retorna comandas para la vista de cocina/comandas.
// Solo incluye comandas de sesiones ABIERTAS (mesas aún no cerradas), en los 4 estados.
func (s *RestaurantService) GetKitchenComandas(branchID uint) ([]KitchenComandaView, error) {
	var comandas []database.TenantComanda
	err := s.db.Joins("JOIN tenant_table_sessions ts ON ts.id = tenant_comandas.session_id").
		Where("ts.branch_id = ? AND ts.status = ? AND tenant_comandas.status IN ('pendiente','preparacion','lista','entregada') AND tenant_comandas.cancelled_at IS NULL", branchID, "open").
		Order("tenant_comandas.created_at ASC").
		Find(&comandas).Error
	if err != nil {
		return nil, err
	}
	if len(comandas) == 0 {
		return []KitchenComandaView{}, nil
	}

	sessionIDs := make([]uint, 0, len(comandas))
	seenSess := make(map[uint]struct{})
	orderIDs := make([]uint, 0, len(comandas))
	seenOrd := make(map[uint]struct{})
	for _, c := range comandas {
		if _, ok := seenSess[c.SessionID]; !ok {
			seenSess[c.SessionID] = struct{}{}
			sessionIDs = append(sessionIDs, c.SessionID)
		}
		if _, ok := seenOrd[c.OrderID]; !ok {
			seenOrd[c.OrderID] = struct{}{}
			orderIDs = append(orderIDs, c.OrderID)
		}
	}

	metaBySession := s.kitchenSessionMetaMap(sessionIDs)
	orderNum := make(map[uint]int)
	var orders []database.TenantTableOrder
	if s.db.Where("id IN ?", orderIDs).Find(&orders).Error == nil {
		for _, o := range orders {
			orderNum[o.ID] = o.OrderNumber
		}
	}

	out := make([]KitchenComandaView, 0, len(comandas))
	for _, c := range comandas {
		out = append(out, KitchenComandaView{
			TenantComanda:      c,
			OrderNumber:        orderNum[c.OrderID],
			KitchenSessionMeta: metaBySession[c.SessionID],
		})
	}
	return out, nil
}

func (s *RestaurantService) kitchenSessionMetaMap(sessionIDs []uint) map[uint]KitchenSessionMeta {
	out := make(map[uint]KitchenSessionMeta, len(sessionIDs))
	if len(sessionIDs) == 0 {
		return out
	}
	var sessions []database.TenantTableSession
	if err := s.db.Where("id IN ?", sessionIDs).Find(&sessions).Error; err != nil {
		return out
	}
	for _, sess := range sessions {
		meta := KitchenSessionMeta{
			OrderCode:       sess.OrderCode,
			OrderType:       sess.OrderType,
			OrderStatus:     sess.OrderStatus,
			TableID:         sess.TableID,
			CustomerName:    sess.CustomerName,
			CustomerPhone:   sess.CustomerPhone,
			DeliveryAddress: sess.DeliveryAddress,
			OpenedAt:        sess.OpenedAt,
		}
		if sess.TableID != nil {
			var table database.TenantRestaurantTable
			if s.db.First(&table, *sess.TableID).Error == nil {
				meta.TableName = table.Name
				var floor database.TenantRestaurantFloor
				if s.db.First(&floor, table.FloorID).Error == nil {
					meta.FloorName = floor.Name
				}
			}
		}
		if sess.StaffID != nil {
			meta.WaiterName = s.staffDisplayName(sess.StaffID)
		}
		if sess.DeliveryDriverID != nil {
			var d database.TenantDeliveryDriver
			if s.db.First(&d, *sess.DeliveryDriverID).Error == nil {
				meta.DriverName = d.Name
			}
		}
		if sess.ContactID != nil && meta.CustomerName == "" {
			var c database.TenantContact
			if s.db.First(&c, *sess.ContactID).Error == nil {
				meta.CustomerName = c.BusinessName
			}
		}
		out[sess.ID] = meta
	}
	return out
}

// ============================= COBRO Y CIERRE =============================

type BillInput struct {
	SessionID      uint
	UserID         uint
	EmployeeType   string // staff restaurante: bloquea efectivo a waiter
	SeriesID       uint
	DocType        string
	IssueDate      time.Time
	Currency       string
	ContactID      *uint
	Payments       []PaymentInput
	CashSessionID  *uint
	CloseSession      bool // si es false, genera la venta pero no cierra la mesa (cliente puede seguir consumiendo)
	DiscountAmount    float64 // descuento global en moneda sobre base imponible (subtotal)
	DiscountMode      string  // "percent" | "amount" (opcional; recalcula descuento en servidor)
	DiscountValue     float64 // valor del descuento (% o monto según DiscountMode)
	CentralTenantID   uint    // tenant SaaS (cupo documentos electrónicos)
}

type PaymentInput struct {
	Method    string  `json:"method"`
	Amount    float64 `json:"amount"`
	Reference string  `json:"reference"`
	Notes     string  `json:"notes"`
}

func resolveBillDiscountAmount(subtotalBase float64, input BillInput) float64 {
	subtotalBase = money.RoundSunat(subtotalBase)
	var amount float64
	if input.DiscountValue > 0 {
		mode := strings.TrimSpace(strings.ToLower(input.DiscountMode))
		if mode == "" {
			mode = "amount"
		}
		amount = money.CalcCheckoutDiscountAmount(subtotalBase, mode, input.DiscountValue)
	} else {
		amount = money.RoundSunat(input.DiscountAmount)
	}
	if amount < 0 {
		amount = 0
	}
	if amount > subtotalBase {
		amount = subtotalBase
	}
	return money.RoundSunat(amount)
}

// BillTable cierra la sesión, genera una venta formal y registra los pagos.
func (s *RestaurantService) BillTable(input BillInput, taxCfg tax.Config) (*database.TenantSale, error) {
	var sess database.TenantTableSession
	if err := s.db.First(&sess, input.SessionID).Error; err != nil {
		return nil, errors.New("sesión no encontrada")
	}
	if sess.Status != "open" {
		return nil, errors.New("la sesión ya está cerrada o facturada")
	}

	// Comandas a facturar.
	// Cierre total (POS / mesa): incluye todos los estados de cocina; "entregada" = servido, no facturado aún.
	// Cobro parcial (mesa sigue abierta): solo ítems aún no facturados en un cobro anterior.
	q := s.db.Where("session_id = ? AND cancelled_at IS NULL", input.SessionID)
	if !input.CloseSession {
		q = q.Where("status != ?", "entregada")
	}
	var comandas []database.TenantComanda
	if err := q.Find(&comandas).Error; err != nil {
		return nil, err
	}
	if len(comandas) == 0 {
		return nil, errors.New("no hay ítems para facturar en esta sesión")
	}

	resolvedCash, err := s.resolveCashSessionForSale(sess.BranchID, input.UserID, input.EmployeeType, input.CashSessionID, input.Payments)
	if err != nil {
		return nil, err
	}
	input.CashSessionID = resolvedCash

	seriesRow, err := docseries.ValidateForBranch(s.db, input.SeriesID, sess.BranchID)
	if err != nil {
		return nil, err
	}
	if err := docusage.GuardCountableSunatQuota(input.CentralTenantID, seriesRow.SunatCode); err != nil {
		return nil, err
	}

	// Construir ítems de venta desde las comandas (con tipo de afectación IGV para Lycet)
	type saleItemData struct {
		ProductID          *uint
		Code               string
		Description        string
		Unit               string
		Quantity           float64
		UnitPrice          float64
		TaxRate            float64
		IgvAffectationType string
		PriceIncludesIgv   bool
		ModifiersJSON      string
	}
	itemMap := make(map[string]*saleItemData)
	for _, c := range comandas {
		key := comandaSaleLineKey(c)
		if existing, ok := itemMap[key]; ok {
			existing.Quantity += c.Quantity
		} else {
			affType, priceIncludesIgv := comandaIgvForCalc(s.db, &c)
			itemMap[key] = &saleItemData{
				ProductID:          c.ProductID,
				Code:               c.ProductCode,
				Description:        c.ProductName,
				Unit:               "NIU",
				Quantity:           c.Quantity,
				UnitPrice:          c.UnitPrice,
				TaxRate:            taxCfg.EffectiveRate(affType),
				IgvAffectationType: affType,
				PriceIncludesIgv:   priceIncludesIgv,
				ModifiersJSON:      strings.TrimSpace(c.ModifiersJSON),
			}
		}
	}

	// Calcular totales (descuento global sobre base imponible / subtotal, luego IGV)
	type pricedLine struct {
		data    *saleItemData
		baseSub float64
		baseTax float64
	}
	var lines []pricedLine
	var subtotalBase float64
	for _, item := range itemMap {
		iSub, iTax, _ := tax.CalcItem(item.UnitPrice, item.Quantity, 0, item.IgvAffectationType, item.PriceIncludesIgv, taxCfg)
		iSub = money.RoundSunat(iSub)
		lines = append(lines, pricedLine{data: item, baseSub: iSub, baseTax: money.RoundSunat(iTax)})
		subtotalBase = money.RoundSunat(subtotalBase + iSub)
	}
	discountAmount := resolveBillDiscountAmount(subtotalBase, input)

	var subtotal, taxAmount, total float64
	var saleItems []database.TenantSaleItem
	remainingDisc := discountAmount
	for i, ln := range lines {
		itemDisc := 0.0
		if discountAmount > 0 && subtotalBase > 0 {
			if i == len(lines)-1 {
				itemDisc = remainingDisc
			} else {
				itemDisc = money.RoundSunat(discountAmount * (ln.baseSub / subtotalBase))
				remainingDisc = money.RoundSunat(remainingDisc - itemDisc)
			}
		}
		item := ln.data
		newSub := money.RoundSunat(ln.baseSub - itemDisc)
		if newSub < 0 {
			newSub = 0
		}
		var newTax, newTotal float64
		rate := taxCfg.EffectiveRate(item.IgvAffectationType)
		if rate == 0 || ln.baseSub <= 0 {
			newTax = 0
			newTotal = newSub
		} else {
			effRate := ln.baseTax / ln.baseSub
			newTax = money.RoundSunat(newSub * effRate)
			newTotal = money.RoundSunat(newSub + newTax)
		}
		subtotal = money.RoundSunat(subtotal + newSub)
		taxAmount = money.RoundSunat(taxAmount + newTax)
		total = money.RoundSunat(total + newTotal)
		itemDisc = money.RoundSunat(itemDisc)
		saleItems = append(saleItems, database.TenantSaleItem{
			ProductID:          item.ProductID,
			Code:               item.Code,
			Description:        item.Description,
			Unit:               item.Unit,
			Quantity:           item.Quantity,
			UnitPrice:          item.UnitPrice,
			Discount:           itemDisc,
			TaxRate:            item.TaxRate,
			IgvAffectationType: item.IgvAffectationType,
			Subtotal:           newSub,
			TaxAmount:          newTax,
			Total:              newTotal,
			ModifiersJSON:      item.ModifiersJSON,
		})
	}

	var totalPaid float64
	for _, p := range input.Payments {
		totalPaid += p.Amount
	}
	if !money.PaidCoversTotal(totalPaid, total) {
		return nil, fmt.Errorf("monto pagado (%.2f) es menor al total (%.2f)", money.RoundDisplay(totalPaid), money.RoundDisplay(total))
	}

	currency := input.Currency
	if currency == "" {
		currency = "PEN"
	}

	var sale *database.TenantSale
	var correlative uint
	var saleNumber string

	sale = &database.TenantSale{
		BranchID:            sess.BranchID,
		UserID:              input.UserID,
		ContactID:           input.ContactID,
		RestaurantSessionID: &input.SessionID,
		CashSessionID:       input.CashSessionID,
		SeriesID:            input.SeriesID,
		DocType:             input.DocType,
		IssueDate:     input.IssueDate,
		Subtotal:      money.RoundSunat(subtotal),
		TaxAmount:     money.RoundSunat(taxAmount),
		Total:         money.RoundSunat(total),
		Currency:      currency,
		PaymentMethod: input.Payments[0].Method, // método principal
		Status:        "paid",
		BillingStatus: "pending",
	}

	now := time.Now()
	return sale, s.db.Transaction(func(tx *gorm.DB) error {
		var lockedSess database.TenantTableSession
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&lockedSess, input.SessionID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errors.New("sesión no encontrada")
			}
			return err
		}
		if lockedSess.Status != sessionStatusOpen {
			return errors.New("la sesión ya está cerrada o facturada")
		}

		var err error
		correlative, seriesRow, err = docseries.ReserveNext(tx, input.SeriesID)
		if err != nil {
			return err
		}
		saleNumber = fmt.Sprintf("%s-%08d", seriesRow.Series, correlative)
		sale.Series = seriesRow.Series
		sale.Correlative = correlative
		sale.Number = saleNumber

		if err := tx.Create(sale).Error; err != nil {
			return err
		}
		for i := range saleItems {
			saleItems[i].SaleID = sale.ID
		}
		if err := tx.Create(&saleItems).Error; err != nil {
			return err
		}

		// Descontar stock y registrar kardex para productos con control de stock
		for _, item := range saleItems {
			if item.ProductID == nil {
				continue
			}
			var product database.TenantProduct
			if tx.First(&product, *item.ProductID).Error != nil {
				continue
			}
			if !product.ManageStock {
				continue
			}
			var stock database.TenantProductStock
			tx.Where("product_id = ? AND branch_id = ?", *item.ProductID, sess.BranchID).First(&stock)
			newQty := stock.Quantity - item.Quantity
			if stock.ID == 0 {
				tx.Create(&database.TenantProductStock{
					ProductID: *item.ProductID,
					BranchID:  sess.BranchID,
					Quantity:  newQty,
				})
			} else {
				tx.Model(&stock).Update("quantity", newQty)
			}
			tx.Create(&database.TenantStockMovement{
				ProductID: *item.ProductID,
				BranchID:  sess.BranchID,
				Type:      "out",
				Quantity:  item.Quantity,
				Balance:   newQty,
				Reference: "VENTA/" + sale.Number,
				UserID:    input.UserID,
				CreatedAt: now,
			})
		}

		// Registrar pagos múltiples: distribuir a caja o cuenta bancaria según método
		cbSvc := cashbanksvc.NewCashBankService(s.db)
		for _, p := range input.Payments {
			tx.Create(&database.TenantSalePayment{
				SaleID:    sale.ID,
				Method:    p.Method,
				Amount:    p.Amount,
				Reference: p.Reference,
				Notes:     p.Notes,
			})
			desc := "Venta " + sale.Number
			_ = cbSvc.RecordPayment(tx, p.Method, p.Amount, input.CashSessionID, sale.Number, desc, &sale.ID, input.UserID)
		}

		if input.CloseSession {
			// Cerrar sesión y liberar mesa
			tx.Model(&lockedSess).Updates(map[string]interface{}{
				"status": "billed", "closed_at": now, "sale_id": sale.ID,
				"order_status": OrderStatusPaid, "paid_at": now,
			})
			if lockedSess.TableID != nil {
				if err := s.syncTableStatusFromOpenSession(tx, *lockedSess.TableID); err != nil {
					return err
				}
			}
			// Eliminar comandas de la sesión cerrada (ya facturadas en la venta; no aparecen en cocina)
			tx.Where("session_id = ?", input.SessionID).Delete(&database.TenantComanda{})
		} else {
			// Generar venta pero mantener mesa abierta: descontar lo facturado del total de la sesión
			tx.Model(&lockedSess).UpdateColumn("total_amount", gorm.Expr("GREATEST(0, total_amount - ?)", total))
			// Marcar solo las comandas facturadas en este cobro (evita doble facturación en cobros parciales)
			billedIDs := make([]uint, 0, len(comandas))
			for _, c := range comandas {
				billedIDs = append(billedIDs, c.ID)
			}
			if len(billedIDs) > 0 {
				tx.Model(&database.TenantComanda{}).Where("id IN ?", billedIDs).
					Update("status", "entregada")
			}
		}

		return nil
	})
}

// CancelSession anula un pedido abierto: exige PIN, sin venta asociada, elimina registros (hard delete).
func (s *RestaurantService) CancelSession(sessionID uint, pin, reason string, userID uint) error {
	_ = userID
	if err := s.VerifyDeletionPin(pin); err != nil {
		return err
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return errors.New("indique el motivo de anulación")
	}

	var sess database.TenantTableSession
	if err := s.db.First(&sess, sessionID).Error; err != nil {
		return errors.New("pedido no encontrado")
	}
	if sess.Status != "open" {
		return errors.New("solo se pueden anular pedidos abiertos")
	}
	if sess.SaleID != nil {
		return errors.New("no se puede anular: el pedido ya fue facturado")
	}

	return s.db.Transaction(func(tx *gorm.DB) error {
		var orderIDs []uint
		if err := tx.Model(&database.TenantTableOrder{}).
			Where("session_id = ?", sessionID).
			Pluck("id", &orderIDs).Error; err != nil {
			return err
		}

		comandaQ := tx.Where("session_id = ?", sessionID)
		if len(orderIDs) > 0 {
			comandaQ = tx.Where("session_id = ? OR order_id IN ?", sessionID, orderIDs)
		}
		if err := comandaQ.Delete(&database.TenantComanda{}).Error; err != nil {
			return err
		}
		if err := tx.Where("session_id = ?", sessionID).Delete(&database.TenantTableOrder{}).Error; err != nil {
			return err
		}
		if err := tx.Delete(&database.TenantTableSession{}, sessionID).Error; err != nil {
			return err
		}
		if sess.TableID != nil {
			if err := s.syncTableStatusFromOpenSession(tx, *sess.TableID); err != nil {
				return err
			}
		}
		return nil
	})
}

// CloseSessionOnly cierra la sesión y libera la mesa sin generar venta (ej. mesa ya pagada, solo liberar).
func (s *RestaurantService) CloseSessionOnly(sessionID uint) error {
	var sess database.TenantTableSession
	if err := s.db.First(&sess, sessionID).Error; err != nil {
		return errors.New("sesión no encontrada")
	}
	if sess.Status != "open" {
		return errors.New("la sesión no está abierta")
	}
	now := time.Now()
	return s.db.Transaction(func(tx *gorm.DB) error {
		tx.Model(&sess).Updates(map[string]interface{}{
			"status": "closed", "closed_at": now,
		})
		if sess.TableID != nil {
			if err := s.syncTableStatusFromOpenSession(tx, *sess.TableID); err != nil {
				return err
			}
		}
		return nil
	})
}

// ============================= PAGOS MÚLTIPLES (ventas generales) =============================

// RegisterPayments registra uno o más pagos para una venta existente.
func (s *RestaurantService) RegisterPayments(saleID uint, payments []PaymentInput, userID uint) error {
	var sale database.TenantSale
	if err := s.db.First(&sale, saleID).Error; err != nil {
		return errors.New("venta no encontrada")
	}

	var totalPaid float64
	// Sumar pagos ya existentes
	var existing []database.TenantSalePayment
	s.db.Where("sale_id = ?", saleID).Find(&existing)
	for _, p := range existing {
		totalPaid += p.Amount
	}
	for _, p := range payments {
		totalPaid += p.Amount
	}

	if !money.PaidCoversTotal(totalPaid, sale.Total) {
		return fmt.Errorf("el total pagado (%.2f) es menor al total de la venta (%.2f)", totalPaid, sale.Total)
	}

	cbSvc := cashbanksvc.NewCashBankService(s.db)
	return s.db.Transaction(func(tx *gorm.DB) error {
		for _, p := range payments {
			tx.Create(&database.TenantSalePayment{
				SaleID:    saleID,
				Method:    p.Method,
				Amount:    p.Amount,
				Reference: p.Reference,
				Notes:     p.Notes,
			})
			desc := "Venta " + sale.Number
			_ = cbSvc.RecordPayment(tx, p.Method, p.Amount, sale.CashSessionID, sale.Number, desc, &sale.ID, userID)
		}
		// Actualizar método de pago principal en la venta
		if len(payments) > 0 {
			tx.Model(&sale).Update("payment_method", payments[0].Method)
		}
		return nil
	})
}

func (s *RestaurantService) GetSalePayments(saleID uint) ([]database.TenantSalePayment, error) {
	var payments []database.TenantSalePayment
	err := s.db.Where("sale_id = ?", saleID).Order("created_at ASC").Find(&payments).Error
	return payments, err
}

// ============================= HELPERS =============================

func (s *RestaurantService) resolveCashSessionForSale(
	branchID, userID uint,
	employeeType string,
	cashSessionID *uint,
	payments []PaymentInput,
) (*uint, error) {
	cbSvc := cashbanksvc.NewCashBankService(s.db)
	needsCash := false
	payLines := make([]cashbanksvc.PaymentLineInput, 0, len(payments))
	for _, p := range payments {
		if p.Amount <= 0 {
			continue
		}
		payLines = append(payLines, cashbanksvc.PaymentLineInput{Method: p.Method, Amount: p.Amount})
		pm, err := cbSvc.GetPaymentMethodByCode(p.Method)
		if err == nil && pm != nil && pm.DestinationType == "cash" {
			needsCash = true
		}
		if strings.EqualFold(strings.TrimSpace(p.Method), "cash") || strings.EqualFold(strings.TrimSpace(p.Method), "efectivo") {
			needsCash = true
		}
	}
	if needsCash {
		et := strings.ToLower(strings.TrimSpace(employeeType))
		if et == "waiter" || et == "mozo" {
			return nil, errors.New("los mozos no pueden cobrar en efectivo; use otro método de pago o un cajero")
		}
	}
	return cbSvc.ResolveCashSessionForSale(branchID, userID, cashSessionID, payLines)
}

