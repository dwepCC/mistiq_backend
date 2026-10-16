package service

import (
	"errors"
	"math"
	"net/url"
	"strconv"
	"strings"
	"time"

	"tukifac/pkg/database"
)

// ErrTrackNotFound se devuelve igual para "no existe" y "el celular no coincide": quien consulta no debe
// poder deducir qué pedidos existen.
var ErrTrackNotFound = errors.New("no encontramos un pedido con esos datos: revisa el número y el celular")

// TrackEvent un paso del recorrido que el cliente puede ver.
type TrackEvent struct {
	Key   string    `json:"key"`
	Label string    `json:"label"`
	At    time.Time `json:"at"`
}

// TrackItem producto del pedido tal como lo ve el cliente.
type TrackItem struct {
	Name     string  `json:"name"`
	Quantity float64 `json:"quantity"`
	Subtotal float64 `json:"subtotal"`
}

// TrackView lo que el cliente final puede ver de su pedido: nunca notas internas, usuarios, ni nada que no
// sea suyo.
type TrackView struct {
	OrderNumber string `json:"order_number"`
	Stage       string `json:"stage"`
	StageLabel  string `json:"stage_label"`
	// Headline frase principal ("Ya llegó a la agencia de Cusco").
	Headline       string       `json:"headline"`
	Message        string       `json:"message"`
	DeliveryMethod string       `json:"delivery_method"`
	CarrierName    string       `json:"carrier_name"`
	TrackingCode   string       `json:"tracking_code"`
	TrackingURL    string       `json:"tracking_url"`
	AgencyName     string       `json:"agency_name"`
	AgencyAddress  string       `json:"agency_address"`
	Department     string       `json:"department"`
	PickupDeadline *time.Time   `json:"pickup_deadline"`
	PickupDaysLeft *int         `json:"pickup_days_left"`
	Total          float64      `json:"total"`
	Paid           float64      `json:"paid"`
	Balance        float64      `json:"balance"`
	Items          []TrackItem  `json:"items"`
	Events         []TrackEvent `json:"events"`
}

var stageLabels = map[string]string{
	StagePorConfirmar: "Recibimos tu pedido",
	StagePorPreparar:  "Preparando tu pedido",
	StageListo:        "Listo para salir",
	StageEnCamino:     "En camino",
	StageEnAgencia:    "Ya está en la agencia",
	StageEntregado:    "Entregado",
	StageRetorno:      "Regresó a la tienda",
	StageCancelado:    "Cancelado",
}

// stageOf misma regla que stageSQL, para un pedido ya cargado.
func stageOf(orderStatus, dispatchStatus string) string {
	switch orderStatus {
	case OrderStatusPendiente:
		return StagePorConfirmar
	case OrderStatusConfirmado, OrderStatusEnPreparacion, OrderStatusEmpaquetado:
		return StagePorPreparar
	case OrderStatusListoParaDespacho:
		return StageListo
	case OrderStatusDespachado:
		if dispatchStatus == DispatchStatusEnAgencia {
			return StageEnAgencia
		}
		return StageEnCamino
	case OrderStatusEntregado:
		return StageEntregado
	case OrderStatusDevuelto:
		return StageRetorno
	}
	return StageCancelado
}

func digitsTail(s string, n int) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	d := b.String()
	if len(d) > n {
		d = d[len(d)-n:]
	}
	return d
}

func parseOrderRef(ref string) (uint, bool) {
	ref = strings.ToUpper(strings.TrimSpace(ref))
	ref = strings.TrimPrefix(ref, "PW-")
	ref = strings.TrimPrefix(ref, "#")
	n, err := strconv.ParseUint(strings.TrimSpace(ref), 10, 32)
	if err != nil || n == 0 {
		return 0, false
	}
	return uint(n), true
}

// TrackOrder seguimiento público de un pedido: se identifica con el número y el celular con el que se pidió.
func (s *EcommerceService) TrackOrder(orderRef, phone string) (*TrackView, error) {
	id, ok := parseOrderRef(orderRef)
	want := digitsTail(phone, 9)
	if !ok || len(want) < 7 {
		return nil, ErrTrackNotFound
	}
	var order database.TenantEcommerceOrder
	if err := s.db.First(&order, id).Error; err != nil {
		return nil, ErrTrackNotFound
	}
	if want != digitsTail(order.CustomerPhone, 9) && want != digitsTail(order.RecipientPhone, 9) {
		return nil, ErrTrackNotFound
	}

	dispatch, err := s.GetDispatchByOrderID(id)
	if err != nil {
		return nil, err
	}
	dispatchStatus := ""
	if dispatch != nil {
		dispatchStatus = dispatch.Status
	}
	stage := stageOf(order.Status, dispatchStatus)
	rows, _ := s.listPaymentsTx(s.db, id)
	sum := summarize(&order, rows)

	v := &TrackView{
		OrderNumber: orderNumber(order.ID), Stage: stage, StageLabel: stageLabels[stage],
		DeliveryMethod: order.DeliveryMethod, AgencyName: order.DestinationAgency, AgencyAddress: order.DestinationAddress,
		Total: order.Total, Paid: sum.Paid, Balance: sum.Balance,
	}
	dep, _, _ := s.UbigeoNames(order.DestinationUbigeo)
	v.Department = dep

	var carrier *database.TenantCarrier
	if cid := order.CarrierID; cid != nil {
		var c database.TenantCarrier
		if s.db.Unscoped().First(&c, *cid).Error == nil {
			carrier = &c
			v.CarrierName = c.Name
		}
	}
	if dispatch != nil {
		if dispatch.CarrierName != nil && *dispatch.CarrierName != "" {
			v.CarrierName = *dispatch.CarrierName
		}
		if dispatch.TrackingCode != nil {
			v.TrackingCode = *dispatch.TrackingCode
		}
		if carrier != nil && v.TrackingCode != "" && carrier.TrackingURL != "" {
			v.TrackingURL = strings.ReplaceAll(carrier.TrackingURL, "{guia}", url.QueryEscape(v.TrackingCode))
		}
		if stage == StageEnAgencia && dispatch.PickupDeadline != nil {
			v.PickupDeadline = dispatch.PickupDeadline
			left := int(math.Ceil(time.Until(*dispatch.PickupDeadline).Hours() / 24))
			v.PickupDaysLeft = &left
		}
	}

	var items []database.TenantEcommerceOrderItem
	s.db.Where("order_id = ?", id).Order("id ASC").Find(&items)
	for _, it := range items {
		v.Items = append(v.Items, TrackItem{Name: it.Name, Quantity: it.Quantity, Subtotal: it.Subtotal})
	}

	v.Headline, v.Message = trackMessage(stage, v, dispatch)
	v.Events = s.trackEvents(&order, dispatch)
	return v, nil
}

func trackMessage(stage string, v *TrackView, d *database.TenantEcommerceDispatch) (string, string) {
	where := strings.TrimSpace(v.Department)
	switch stage {
	case StagePorConfirmar:
		return "Recibimos tu pedido", "Te escribiremos por WhatsApp para confirmarlo y coordinar el envío."
	case StagePorPreparar:
		return "Estamos preparando tu pedido", "Cuando esté listo te avisamos con tu guía de envío."
	case StageListo:
		return "Tu pedido está listo", "Sale en el próximo despacho. Te avisamos con tu guía."
	case StageEnCamino:
		msg := "Te avisamos cuando llegue a la agencia."
		if v.TrackingCode != "" {
			msg = "Tu guía es " + v.TrackingCode + ". " + msg
		}
		return "Tu pedido va en camino", msg
	case StageEnAgencia:
		head := "Ya llegó a la agencia"
		if where != "" {
			head += " de " + where
		}
		msg := "Recógelo con tu documento de identidad."
		if v.PickupDaysLeft != nil {
			switch {
			case *v.PickupDaysLeft < 0:
				msg = "El plazo para recogerlo venció: escríbenos para ayudarte."
			case *v.PickupDaysLeft == 0:
				msg += " Hoy es el último día."
			default:
				msg += " Te quedan " + strconv.Itoa(*v.PickupDaysLeft) + " días."
			}
		}
		if v.Balance > 0 {
			msg += " Al recoger pagas el saldo de S/ " + strconv.FormatFloat(v.Balance, 'f', 2, 64) + "."
		}
		return head, msg
	case StageEntregado:
		return "Pedido entregado", "Gracias por tu compra."
	case StageRetorno:
		return "El paquete regresó a la tienda", "Escríbenos para coordinar de nuevo la entrega."
	}
	return "Pedido cancelado", "Si fue un error, escríbenos."
}

// trackEvents recorrido visible: solo hitos que el cliente entiende, ordenados por fecha.
func (s *EcommerceService) trackEvents(order *database.TenantEcommerceOrder, d *database.TenantEcommerceDispatch) []TrackEvent {
	var out []TrackEvent
	var hist []database.TenantEcommerceOrderStatusHistory
	s.db.Where("order_id = ?", order.ID).Order("created_at ASC, id ASC").Find(&hist)
	seen := map[string]bool{}
	add := func(key, label string, at time.Time) {
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, TrackEvent{Key: key, Label: label, At: at})
	}
	for _, h := range hist {
		switch h.ToStatus {
		case OrderStatusPendiente:
			add("recibido", "Pedido recibido", h.CreatedAt)
		case OrderStatusConfirmado:
			add("confirmado", "Pedido confirmado", h.CreatedAt)
		case OrderStatusEnPreparacion:
			add("preparando", "Preparando tu pedido", h.CreatedAt)
		case OrderStatusEmpaquetado:
			add("empaquetado", "Empaquetado y listo", h.CreatedAt)
		case OrderStatusDespachado:
			add("despachado", "En camino", h.CreatedAt)
		case OrderStatusEntregado:
			add("entregado", "Entregado", h.CreatedAt)
		case OrderStatusDevuelto:
			add("retorno", "El paquete regresó a la tienda", h.CreatedAt)
		case OrderStatusCancelado, OrderStatusRechazado:
			add("cancelado", "Pedido cancelado", h.CreatedAt)
		}
	}
	if d != nil && d.AgencyArrivedAt != nil {
		add("agencia", "Llegó a la agencia", *d.AgencyArrivedAt)
	}
	// Orden cronológico (la llegada a agencia se agrega al final del bucle).
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].At.Before(out[j-1].At); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}
