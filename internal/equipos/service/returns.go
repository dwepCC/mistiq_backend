package service

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"tukifac/pkg/database"

	"gorm.io/gorm"
)

var (
	returnStatuses   = map[string]bool{"solicitado": true, "en_camino": true, "recibido": true, "desechado_por_agencia": true, "anulado": true}
	returnConditions = map[string]bool{"buen_estado": true, "danado": true}
	returnActions    = map[string]bool{"retorno": true, "reenvio": true, "reembolso": true, "perdida": true}
)

type ReturnItem struct {
	ProductID uint   `json:"product_id"`
	Code      string `json:"code"`
	Quantity  int    `json:"quantity"`
}

type ReturnItemInput struct {
	ProductID uint `json:"product_id"`
	Quantity  int  `json:"quantity"`
}

// ReturnInput alta de un retorno desde un envío (paquete no recogido que vuelve).
type ReturnInput struct {
	OrderID       uint              `json:"order_id"`
	RequestedAt   string            `json:"requested_at"`
	Items         []ReturnItemInput `json:"items"` // vacío = todo lo del pedido
	UnpaidBalance *float64          `json:"unpaid_balance"`
	ReturnCost    float64           `json:"return_cost"`
	Condition     string            `json:"condition"`
	Notes         string            `json:"notes"`
}

// ReturnUpdate cambios sobre un retorno existente.
type ReturnUpdate struct {
	Status        string  `json:"status"`
	Condition     string  `json:"condition"`
	ActionTaken   string  `json:"action_taken"`
	ReturnCost    float64 `json:"return_cost"`
	UnpaidBalance float64 `json:"unpaid_balance"`
	ReceivedAt    string  `json:"received_at"`
	Notes         string  `json:"notes"`
}

type ReturnView struct {
	database.EquipReturn
	OrderNumber int          `json:"order_number"`
	Items       []ReturnItem `json:"items"`
	Reshipped   bool         `json:"reshipped"`
	// TrackStage / TrackLabel etapa del retorno en Shalom: origen | transito | destino | entregado ("" = sin seguimiento).
	TrackStage string `json:"track_stage"`
	TrackLabel string `json:"track_label"`
}

func (s *Service) returnView(tx *gorm.DB, r database.EquipReturn) ReturnView {
	v := ReturnView{EquipReturn: r, Items: []ReturnItem{}}
	_ = json.Unmarshal([]byte(r.ItemsJSON), &v.Items)
	if r.OrderID != nil {
		var n struct{ OrderNumber int }
		_ = tx.Model(&database.EquipOrder{}).Select("order_number").Where("id = ?", *r.OrderID).Scan(&n).Error
		v.OrderNumber = n.OrderNumber
	}
	var c int64
	_ = tx.Model(&database.EquipStockMovement{}).Where("return_id = ? AND movement_type = ?", r.ID, database.EquipMoveReenvio).Count(&c).Error
	v.Reshipped = c > 0
	v.TrackStage, v.TrackLabel = returnStage(r.ProviderStatus)
	return v
}

func itemsText(items []ReturnItem) string {
	parts := make([]string, 0, len(items))
	for _, it := range items {
		parts = append(parts, fmt.Sprintf("%d x %s", it.Quantity, it.Code))
	}
	t := strings.Join(parts, ", ")
	if len(t) > 250 {
		t = t[:250]
	}
	return t
}

// CreateReturn registra un retorno sobre el envío vigente del pedido (en tránsito o en agencia) y lo marca «en retorno».
func (s *Service) CreateReturn(in ReturnInput, userID uint) (*ReturnView, error) {
	cond := in.Condition
	if cond == "" {
		cond = "buen_estado"
	}
	if !returnConditions[cond] {
		return nil, invalid("condición inválida (buen_estado o danado)")
	}
	if in.ReturnCost < 0 {
		return nil, invalid("el costo del retorno no puede ser negativo")
	}
	req, err := parseDateOnly(in.RequestedAt)
	if err != nil {
		return nil, err
	}
	if req == nil {
		t := todayNoon()
		req = &t
	}
	var out ReturnView
	err = s.db.Transaction(func(tx *gorm.DB) error {
		o, err := s.loadOrderForUpdate(tx, in.OrderID)
		if err != nil {
			return err
		}
		if o.Status != "registrado" {
			return invalid("solo un pedido confirmado puede tener retorno")
		}
		var sh database.EquipShipment
		if found, err := findOne(tx, &sh, "order_id = ? AND is_current = ?", o.ID, true); err != nil {
			return err
		} else if !found || (sh.Status != "en_transito" && sh.Status != "en_agencia") {
			return invalid("el retorno se registra sobre un envío en tránsito o en agencia")
		}
		var its []database.EquipOrderItem
		if err := tx.Where("order_id = ?", o.ID).Find(&its).Error; err != nil {
			return err
		}
		outs, err := outflows(its)
		if err != nil {
			return err
		}
		sold := map[uint]int{}
		for _, ou := range outs {
			sold[ou.ProductID] += ou.Quantity
		}
		want := map[uint]int{}
		if len(in.Items) == 0 {
			want = sold
		} else {
			for _, it := range in.Items {
				if it.Quantity <= 0 {
					return invalid("las cantidades del retorno deben ser mayores a 0")
				}
				want[it.ProductID] += it.Quantity
			}
		}
		var items []ReturnItem
		for pid, q := range want {
			if q > sold[pid] {
				return invalid("no puedes devolver más de lo enviado (producto %d)", pid)
			}
			var p database.EquipProduct
			if found, _ := findOne(tx, &p, "id = ?", pid); !found {
				return invalid("producto del retorno no encontrado")
			}
			items = append(items, ReturnItem{ProductID: pid, Code: p.Code, Quantity: q})
		}
		for pid, q := range sold { // sin retornos parciales por ahora: se devuelve el pedido completo
			if want[pid] != q {
				return invalid("el retorno parcial aún no está disponible: se devuelve el pedido completo")
			}
		}
		if len(items) == 0 {
			return invalid("el retorno no tiene productos")
		}
		sortReturnItems(items)
		raw, _ := json.Marshal(items)
		var max struct{ N int }
		_ = tx.Model(&database.EquipReturn{}).Select("COALESCE(MAX(return_number), 0) AS n").Scan(&max).Error
		unpaid := o.BalanceAmount
		if in.UnpaidBalance != nil {
			unpaid = *in.UnpaidBalance
		}
		if unpaid < 0 {
			return invalid("el saldo no cobrado no puede ser negativo")
		}
		oid, sid := o.ID, sh.ID
		r := database.EquipReturn{
			ReturnNumber: max.N + 1, OrderID: &oid, ShipmentID: &sid, CustomerName: o.CustomerName, GuideNumber: sh.GuideNumber,
			RequestedAt: req, ItemsJSON: string(raw), ItemsText: itemsText(items), UnpaidBalance: unpaid, ReturnCost: in.ReturnCost,
			Status: "solicitado", Condition: cond, ActionTaken: "retorno", Notes: strings.TrimSpace(in.Notes),
		}
		if err := tx.Create(&r).Error; err != nil {
			return err
		}
		sh.Status = "retorno"
		if err := tx.Save(&sh).Error; err != nil {
			return err
		}
		out = s.returnView(tx, r)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func sortReturnItems(items []ReturnItem) {
	for i := 1; i < len(items); i++ {
		for j := i; j > 0 && items[j].Code < items[j-1].Code; j-- {
			items[j], items[j-1] = items[j-1], items[j]
		}
	}
}

// UpdateReturn avanza el estado del retorno. Al recibirlo en buen estado, los equipos reingresan al stock; dañados
// quedan solo registrados (no generan stock). Un retorno cerrado ya no cambia de estado.
func (s *Service) UpdateReturn(id uint, in ReturnUpdate, userID uint) (*ReturnView, error) {
	if in.Status != "" && !returnStatuses[in.Status] {
		return nil, invalid("estado de retorno inválido")
	}
	if in.Condition != "" && !returnConditions[in.Condition] {
		return nil, invalid("condición inválida")
	}
	if in.ActionTaken != "" && !returnActions[in.ActionTaken] {
		return nil, invalid("acción inválida")
	}
	if in.ReturnCost < 0 || in.UnpaidBalance < 0 {
		return nil, invalid("los montos no pueden ser negativos")
	}
	var out ReturnView
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var r database.EquipReturn
		if found, err := findOne(tx, &r, "id = ?", id); err != nil {
			return err
		} else if !found {
			return invalid("retorno no encontrado")
		}
		if r.Status == "anulado" {
			return invalid("el retorno está anulado y no se puede modificar")
		}
		closed := r.Status == "recibido" || r.Status == "desechado_por_agencia"
		if closed && in.Status != "" && in.Status != r.Status {
			return invalid("el retorno ya está cerrado (%s)", r.Status)
		}
		if closed && in.Condition != "" && in.Condition != r.Condition {
			return invalid("no se puede cambiar la condición de un retorno cerrado: usa un ajuste de stock")
		}
		if in.Condition != "" {
			r.Condition = in.Condition
		}
		if in.ActionTaken != "" && !closed {
			r.ActionTaken = in.ActionTaken
		}
		r.ReturnCost, r.UnpaidBalance = in.ReturnCost, in.UnpaidBalance
		if n := strings.TrimSpace(in.Notes); n != "" {
			r.Notes = n
		}
		becameReceived := in.Status == "recibido" && r.Status != "recibido"
		if in.Status != "" {
			r.Status = in.Status
		}
		if r.Status == "recibido" || r.Status == "desechado_por_agencia" {
			d, err := parseDateOnly(in.ReceivedAt)
			if err != nil {
				return err
			}
			if d == nil {
				if r.ReceivedAt != nil {
					d = r.ReceivedAt
				} else {
					t := todayNoon()
					d = &t
				}
			}
			r.ReceivedAt = d
		}
		if becameReceived && r.Condition == "buen_estado" {
			var items []ReturnItem
			_ = json.Unmarshal([]byte(r.ItemsJSON), &items)
			var uid *uint
			if userID > 0 {
				u := userID
				uid = &u
			}
			rid := r.ID
			rows := make([]database.EquipStockMovement, 0, len(items))
			for _, it := range items {
				rows = append(rows, database.EquipStockMovement{
					ProductID: it.ProductID, OccurredAt: time.Now(), MovementType: database.EquipMoveReingreso, Quantity: it.Quantity,
					OrderID: r.OrderID, ReturnID: &rid, Note: fmt.Sprintf("Retorno N° %d", r.ReturnNumber), CreatedBy: uid,
				})
			}
			if len(rows) > 0 {
				if err := tx.Create(&rows).Error; err != nil {
					return err
				}
			}
		}
		if err := tx.Save(&r).Error; err != nil {
			return err
		}
		out = s.returnView(tx, r)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// Reship crea un segundo envío para el mismo pedido a partir de un retorno recibido en buen estado: los equipos
// vuelven a salir del stock (movimiento «salida_reenvio») y el envío anterior queda en el historial.
func (s *Service) Reship(returnID uint, userID uint) (*OrderResult, error) {
	var orderID uint
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var r database.EquipReturn
		if found, err := findOne(tx, &r, "id = ?", returnID); err != nil {
			return err
		} else if !found {
			return invalid("retorno no encontrado")
		}
		if r.Status != "recibido" || r.Condition != "buen_estado" {
			return invalid("solo se reenvía un retorno recibido en buen estado")
		}
		if r.OrderID == nil {
			return invalid("el retorno no tiene pedido asociado")
		}
		var done int64
		if err := tx.Model(&database.EquipStockMovement{}).Where("return_id = ? AND movement_type = ?", r.ID, database.EquipMoveReenvio).Count(&done).Error; err != nil {
			return err
		}
		if done > 0 {
			return invalid("este retorno ya fue reenviado")
		}
		o, err := s.loadOrderForUpdate(tx, *r.OrderID)
		if err != nil {
			return err
		}
		orderID = o.ID
		var old database.EquipShipment
		if found, err := findOne(tx, &old, "order_id = ? AND is_current = ?", o.ID, true); err != nil {
			return err
		} else if !found || old.Status != "retorno" {
			return invalid("el envío vigente del pedido no está en retorno")
		}
		old.IsCurrent = false
		if err := tx.Save(&old).Error; err != nil {
			return err
		}
		next := database.EquipShipment{
			OrderID: o.ID, CarrierID: old.CarrierID, DestinationAgency: old.DestinationAgency, DestinationDepartment: old.DestinationDepartment,
			DestinationProvince: old.DestinationProvince, DestinationDistrict: old.DestinationDistrict, Ubigeo: old.Ubigeo,
			DeliveryMode: old.DeliveryMode, Status: "pendiente_envio", IsCurrent: true, Notes: fmt.Sprintf("Reenvío del retorno N° %d", r.ReturnNumber),
		}
		if err := tx.Create(&next).Error; err != nil {
			return err
		}
		var items []ReturnItem
		_ = json.Unmarshal([]byte(r.ItemsJSON), &items)
		var uid *uint
		if userID > 0 {
			u := userID
			uid = &u
		}
		rid, oid := r.ID, o.ID
		rows := make([]database.EquipStockMovement, 0, len(items))
		for _, it := range items {
			rows = append(rows, database.EquipStockMovement{
				ProductID: it.ProductID, OccurredAt: time.Now(), MovementType: database.EquipMoveReenvio, Quantity: -it.Quantity,
				OrderID: &oid, ReturnID: &rid, SaleTypeSnapshot: o.SaleType, Note: fmt.Sprintf("Reenvío tras retorno N° %d", r.ReturnNumber), CreatedBy: uid,
			})
		}
		if len(rows) > 0 {
			if err := tx.Create(&rows).Error; err != nil {
				return err
			}
		}
		r.ActionTaken = "reenvio"
		return tx.Save(&r).Error
	})
	if err != nil {
		return nil, err
	}
	v, err := s.GetOrder(orderID)
	if err != nil {
		return nil, err
	}
	return &OrderResult{Order: v}, nil
}

// ListReturns lista los retornos (más recientes primero); status vacío = todos.
func (s *Service) ListReturns(status string) ([]ReturnView, error) {
	tx := s.db.Model(&database.EquipReturn{})
	switch status {
	case "":
	case "abiertos":
		tx = tx.Where("status IN ?", []string{"solicitado", "en_camino"})
	default:
		tx = tx.Where("status = ?", status)
	}
	var rows []database.EquipReturn
	if err := tx.Order("return_number DESC, id DESC").Limit(500).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]ReturnView, 0, len(rows))
	for _, r := range rows {
		out = append(out, s.returnView(s.db, r))
	}
	return out, nil
}

// CancelReturn anula un retorno generado por error. Revierte sus efectos:
//   - si ya se había recibido en buen estado, descuenta de nuevo el stock que reingresó (movimiento de ajuste);
//   - el envío del pedido vuelve al estado que tenía antes del retorno (en agencia si ya había llegado, si no en tránsito).
//
// No se puede anular un retorno ya reenviado (habría que deshacer primero el reenvío) ni uno ya anulado.
func (s *Service) CancelReturn(id uint, reason string, userID uint) (*ReturnView, error) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return nil, invalid("indica el motivo de la anulación")
	}
	if len(reason) > 255 {
		reason = reason[:255]
	}
	var out ReturnView
	err := s.db.Transaction(func(tx *gorm.DB) error {
		var r database.EquipReturn
		if found, err := findOne(tx, &r, "id = ?", id); err != nil {
			return err
		} else if !found {
			return invalid("retorno no encontrado")
		}
		if r.Status == "anulado" {
			return invalid("el retorno ya está anulado")
		}
		var reshipped int64
		if err := tx.Model(&database.EquipStockMovement{}).Where("return_id = ? AND movement_type = ?", r.ID, database.EquipMoveReenvio).Count(&reshipped).Error; err != nil {
			return err
		}
		if reshipped > 0 {
			return invalid("el retorno ya fue reenviado: no se puede anular")
		}
		now := time.Now()
		var uid *uint
		if userID > 0 {
			u := userID
			uid = &u
		}
		// Reingreso ya hecho (recibido en buen estado): se compensa con el movimiento contrario.
		var back []database.EquipStockMovement
		if err := tx.Where("return_id = ? AND movement_type = ?", r.ID, database.EquipMoveReingreso).Find(&back).Error; err != nil {
			return err
		}
		if len(back) > 0 {
			rid := r.ID
			rows := make([]database.EquipStockMovement, 0, len(back))
			for _, m := range back {
				rows = append(rows, database.EquipStockMovement{
					ProductID: m.ProductID, OccurredAt: now, MovementType: database.EquipMoveAjuste, Quantity: -m.Quantity,
					OrderID: r.OrderID, ReturnID: &rid, Note: fmt.Sprintf("Anulación del retorno N° %d", r.ReturnNumber), CreatedBy: uid,
				})
			}
			if err := tx.Create(&rows).Error; err != nil {
				return err
			}
		}
		// El envío del pedido deja de estar «en retorno».
		if r.ShipmentID != nil {
			var sh database.EquipShipment
			if found, err := findOne(tx, &sh, "id = ?", *r.ShipmentID); err != nil {
				return err
			} else if found && sh.IsCurrent && sh.Status == "retorno" {
				switch {
				case sh.ArrivedAt != nil:
					sh.Status = "en_agencia"
				case sh.DispatchedAt != nil:
					sh.Status = "en_transito"
				default:
					sh.Status = "pendiente_envio"
				}
				if err := tx.Save(&sh).Error; err != nil {
					return err
				}
			}
		}
		r.Status, r.CancelledAt, r.CancelReason = "anulado", &now, reason
		if err := tx.Save(&r).Error; err != nil {
			return err
		}
		out = s.returnView(tx, r)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}
