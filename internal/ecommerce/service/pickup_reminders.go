package service

import (
	"fmt"
	"math"
	"time"

	"tukifac/pkg/database"
)

const (
	notifPickupSoon    = "ecommerce.order.pickup_soon"
	notifPickupExpired = "ecommerce.order.pickup_expired"
)

// PickupNotificationTypes tipos de aviso de recojo, para declararlos en el mapa de permisos de notificaciones.
var PickupNotificationTypes = []string{notifPickupSoon, notifPickupExpired}

// NotifyPickupDeadlines avisa en el panel de los pedidos que siguen en la agencia sin recoger: uno cuando faltan
// pocos días (los que indica la empresa de envío, 5 por defecto) y otro cuando el plazo ya venció. Cada aviso se
// crea una sola vez por pedido, así que se puede ejecutar las veces que haga falta. Devuelve cuántos creó.
func (s *EcommerceService) NotifyPickupDeadlines(now time.Time) (int, error) {
	var dispatches []database.TenantEcommerceDispatch
	if err := s.db.Where("status = ? AND pickup_deadline IS NOT NULL", DispatchStatusEnAgencia).Find(&dispatches).Error; err != nil {
		return 0, err
	}
	if len(dispatches) == 0 {
		return 0, nil
	}
	warnByCarrier := map[uint]int{}
	var carriers []database.TenantCarrier
	s.db.Find(&carriers)
	for _, c := range carriers {
		warnByCarrier[c.ID] = c.WarnDaysBefore
	}

	created := 0
	for _, d := range dispatches {
		warn := 5
		if d.CarrierID != nil {
			if w, ok := warnByCarrier[*d.CarrierID]; ok && w > 0 {
				warn = w
			}
		}
		left := int(math.Ceil(d.PickupDeadline.Sub(now).Hours() / 24))
		var typ string
		switch {
		case left <= 0:
			typ = notifPickupExpired
		case left <= warn:
			typ = notifPickupSoon
		default:
			continue
		}
		link := fmt.Sprintf("/sales/pedidos-web?id=%d", d.OrderID)
		var n int64
		if err := s.db.Model(&database.TenantNotification{}).Where("type = ? AND link_path = ?", typ, link).Count(&n).Error; err != nil {
			return created, err
		}
		if n > 0 {
			continue
		}
		var order database.TenantEcommerceOrder
		if err := s.db.Select("id", "customer_name", "recipient_name").First(&order, d.OrderID).Error; err != nil {
			continue
		}
		who := order.RecipientName
		if who == "" {
			who = order.CustomerName
		}
		title, body := "", ""
		if typ == notifPickupExpired {
			title = fmt.Sprintf("Venció el plazo de recojo de PW-%d", d.OrderID)
			body = fmt.Sprintf("%s no recogió el paquete a tiempo. Revisa si lo devuelves o le das más plazo.", who)
		} else {
			title = fmt.Sprintf("PW-%d vence en %d día%s", d.OrderID, left, plural(left))
			body = fmt.Sprintf("%s aún no recoge el paquete en la agencia. Avísale por WhatsApp.", who)
		}
		if err := s.db.Create(&database.TenantNotification{Type: typ, Title: title, Body: body, LinkPath: link}).Error; err != nil {
			return created, err
		}
		created++
	}
	return created, nil
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
