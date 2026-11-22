package service

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm/clause"

	"tukifac/internal/shalom"
	"tukifac/pkg/database"
)

// ── Notificaciones del panel (campana del header) ──────────────────────────

const (
	NotifArrival   = "llegada"
	NotifDelivered = "entregado"
	NotifDelay     = "demora"
)

// notifyShipmentEvent crea el aviso del panel cuando Shalom informa un cambio relevante de un envío. Es idempotente
// por envío y tipo (una entrega repetida del webhook, o el mismo hito por otra vía, no duplica el aviso).
func (s *Service) notifyShipmentEvent(shipmentID uint, kind string) {
	var sh database.EquipShipment
	if found, _ := findOne(s.db, &sh, "id = ?", shipmentID); !found {
		return
	}
	var o database.EquipOrder
	if found, _ := findOne(s.db, &o, "id = ?", sh.OrderID); !found {
		return
	}
	who := strings.TrimSpace(o.CustomerName)
	if who == "" {
		who = "sin nombre"
	}
	n := database.EquipNotification{OrderID: o.ID, OrderNumber: o.OrderNumber, DedupKey: fmt.Sprintf("ship:%d:%s", shipmentID, kind)}
	switch kind {
	case NotifArrival:
		agency := strings.TrimSpace(sh.DestinationAgency)
		n.Kind, n.Severity, n.Title = kind, "success", fmt.Sprintf("Pedido N° %d llegó a la agencia", o.OrderNumber)
		n.Message = who
		if agency != "" {
			n.Message += " · " + agency
		}
		if sh.PickupDeadline != nil {
			n.Message += " · recoger antes del " + sh.PickupDeadline.In(limaLoc()).Format("02/01/2006")
		}
	case NotifDelivered:
		n.Kind, n.Severity, n.Title = kind, "info", fmt.Sprintf("Pedido N° %d fue entregado", o.OrderNumber)
		n.Message = who + " · Shalom informa que el cliente recogió el paquete"
	case NotifDelay:
		n.Kind, n.Severity, n.Title = kind, "warning", fmt.Sprintf("Pedido N° %d tiene una demora en Shalom", o.OrderNumber)
		n.Message = who + " · Shalom informó una incidencia: dar seguimiento a la guía"
	default:
		return
	}
	// El índice único de DedupKey frena duplicados; el error de «ya existe» se ignora a propósito.
	s.db.Clauses(clause.OnConflict{DoNothing: true}).Create(&n)
}

// NotificationList avisos recientes y cuántos siguen sin leer (para la insignia de la campana).
type NotificationList struct {
	Items  []database.EquipNotification `json:"items"`
	Unread int64                        `json:"unread"`
}

func (s *Service) ListNotifications(limit int) (*NotificationList, error) {
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	out := &NotificationList{Items: []database.EquipNotification{}}
	if err := s.db.Order("id DESC").Limit(limit).Find(&out.Items).Error; err != nil {
		return nil, err
	}
	if err := s.db.Model(&database.EquipNotification{}).Where("read_at IS NULL").Count(&out.Unread).Error; err != nil {
		return nil, err
	}
	return out, nil
}

// MarkNotificationsRead marca como leídos los avisos indicados (o todos).
func (s *Service) MarkNotificationsRead(ids []uint, all bool) error {
	q := s.db.Model(&database.EquipNotification{}).Where("read_at IS NULL")
	if !all {
		if len(ids) == 0 {
			return nil
		}
		q = q.Where("id IN ?", ids)
	}
	return q.Update("read_at", time.Now()).Error
}

// ── Historial de eventos de Shalom por envío ───────────────────────────────

// recordPollSnapshot guarda en la bitácora lo que Shalom informó en una consulta manual o en lote, para que el
// historial del envío no dependa de que haya llegado un webhook. Solo guarda si el estado cambió (hash del timeline).
func (s *Service) recordPollSnapshot(shipmentID uint, numero string, tr *shalom.Tracking) {
	type ms struct {
		Milestone string `json:"milestone"`
		Fecha     string `json:"fecha"`
		Completo  bool   `json:"completo"`
	}
	st := tr.Status
	var timeline []ms
	for _, p := range []struct {
		name string
		m    *shalom.Milestone
	}{{"registrado", st.Registrado}, {"origen", st.Origen}, {"transito", st.Transito}, {"demora", st.Demora}, {"destino", st.Destino}, {"reparto", st.Reparto}, {"entregado", st.Entregado}} {
		if p.m != nil {
			timeline = append(timeline, ms{p.name, p.m.Fecha, p.m.Completo})
		}
	}
	status := stateFromTracking(tr).Status
	raw, _ := json.Marshal(map[string]any{"numero": numero, "status": status, "timeline": timeline})
	sum := sha1.Sum(raw)
	row := database.EquipProviderEvent{
		Provider: database.ProviderShalom, EventID: fmt.Sprintf("poll:%d:%s", shipmentID, hex.EncodeToString(sum[:])[:16]),
		EventType: "consulta", Reference: numero, Payload: string(raw), Note: "consulta manual",
	}
	now := time.Now()
	row.ProcessedAt = &now
	s.db.Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
}

// ProviderEventView un movimiento del envío para la pantalla.
type ProviderEventView struct {
	ID        uint                `json:"id"`
	Source    string              `json:"source"` // webhook | consulta
	Type      string              `json:"type"`
	Status    string              `json:"status"`
	At        time.Time           `json:"at"`
	Note      string              `json:"note"`
	Milestone []ProviderMilestone `json:"timeline"`
}

type ProviderMilestone struct {
	Milestone string `json:"milestone"`
	Fecha     string `json:"fecha"`
	Completo  bool   `json:"completo"`
}

// ShipmentProviderEvents historial guardado del envío vigente de un pedido (lee de la BD, no llama a Shalom).
func (s *Service) ShipmentProviderEvents(orderID uint) ([]ProviderEventView, error) {
	var sh database.EquipShipment
	if found, err := findOne(s.db, &sh, "order_id = ? AND is_current = ?", orderID, true); err != nil {
		return nil, err
	} else if !found {
		return nil, invalid("el pedido no tiene envío")
	}
	numero, _ := shalomRef(sh.GuideNumber)
	if numero == "" {
		return []ProviderEventView{}, nil
	}
	var rows []database.EquipProviderEvent
	if err := s.db.Where("provider = ? AND reference = ?", database.ProviderShalom, numero).Order("id DESC").Limit(60).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]ProviderEventView, 0, len(rows))
	for _, r := range rows {
		v := ProviderEventView{ID: r.ID, Source: "webhook", Type: r.EventType, At: r.CreatedAt, Note: r.Note, Milestone: []ProviderMilestone{}}
		if r.EventType == "consulta" {
			v.Source = "consulta"
		}
		var body struct {
			Status string `json:"status"`
			Data   *struct {
				Status   string              `json:"status"`
				Timeline []ProviderMilestone `json:"timeline"`
			} `json:"data"`
			Timeline []ProviderMilestone `json:"timeline"`
		}
		if json.Unmarshal([]byte(r.Payload), &body) == nil {
			v.Status, v.Milestone = body.Status, body.Timeline
			if body.Data != nil { // sobre del webhook: {event, data:{status,timeline}}
				v.Status, v.Milestone = body.Data.Status, body.Data.Timeline
			}
		}
		if v.Milestone == nil {
			v.Milestone = []ProviderMilestone{}
		}
		out = append(out, v)
	}
	return out, nil
}
