package service

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"tukifac/pkg/database"
)

func eventBody(id, event, numero, status string, milestones ...string) []byte {
	var tl []string
	for _, m := range milestones { // "destino|2026-10-31 10:00:00"
		p := strings.SplitN(m, "|", 2)
		tl = append(tl, fmt.Sprintf(`{"milestone":%q,"fecha":%q,"completo":true}`, p[0], p[1]))
	}
	return []byte(fmt.Sprintf(`{"id":%q,"event":%q,"data":{"numero":%q,"status":%q,"timeline":[%s]}}`, id, event, numero, status, strings.Join(tl, ",")))
}

func TestWebhookCreatesPanelNotificationsOncePerMilestone(t *testing.T) {
	f, _ := providerFixture(t, func(w http.ResponseWriter, r *http.Request) {})
	orderID := shalomShipment(t, f, "80574902")
	enableProvider(t, f)
	secret := strings.Repeat("ef", 32)
	setSecret(t, f, secret)
	now := time.Now()
	send := func(body []byte) WebhookResult {
		return f.s.HandleWebhook(body, hookSign(secret, now.Unix(), body), now)
	}

	if r := send(eventBody("e1", "tracking.updated", "80574902", "destino", "destino|2026-10-31 10:00:00")); r.Status != 200 {
		t.Fatalf("llegada: %+v", r)
	}
	list, err := f.s.ListNotifications(30)
	if err != nil || len(list.Items) != 1 || list.Unread != 1 {
		t.Fatalf("avisos: %+v %v", list, err)
	}
	n := list.Items[0]
	if n.Kind != NotifArrival || n.OrderID != orderID || !strings.Contains(n.Title, "llegó a la agencia") || n.Severity != "success" {
		t.Errorf("aviso de llegada: %+v", n)
	}

	// Mismo hito por otro evento (o entrega duplicada): no vuelve a avisar.
	send(eventBody("e2", "tracking.updated", "80574902", "destino", "destino|2026-10-31 10:00:00"))
	send(eventBody("e1", "tracking.updated", "80574902", "destino", "destino|2026-10-31 10:00:00"))
	if list, _ := f.s.ListNotifications(30); len(list.Items) != 1 {
		t.Errorf("no debe duplicar el aviso de llegada: %d avisos", len(list.Items))
	}

	// Entregado: segundo aviso, de otro tipo.
	send(eventBody("e3", "tracking.delivered", "80574902", "entregado", "destino|2026-10-31 10:00:00", "entregado|2026-11-02 16:00:00"))
	list, _ = f.s.ListNotifications(30)
	if len(list.Items) != 2 || list.Items[0].Kind != NotifDelivered || list.Unread != 2 {
		t.Fatalf("aviso de entrega: %+v", list)
	}

	// Marcar leídos: uno y luego todos.
	if err := f.s.MarkNotificationsRead([]uint{list.Items[0].ID}, false); err != nil {
		t.Fatal(err)
	}
	if l, _ := f.s.ListNotifications(30); l.Unread != 1 {
		t.Errorf("tras leer uno quedan %d sin leer", l.Unread)
	}
	if err := f.s.MarkNotificationsRead(nil, true); err != nil {
		t.Fatal(err)
	}
	if l, _ := f.s.ListNotifications(30); l.Unread != 0 || len(l.Items) != 2 {
		t.Errorf("tras leer todos: %+v", l)
	}
}

func TestDelayNotificationOnlyOnTransition(t *testing.T) {
	f, _ := providerFixture(t, func(w http.ResponseWriter, r *http.Request) {})
	shalomShipment(t, f, "80574902")
	enableProvider(t, f)
	secret := strings.Repeat("ab", 32)
	setSecret(t, f, secret)
	now := time.Now()
	send := func(body []byte) { f.s.HandleWebhook(body, hookSign(secret, now.Unix(), body), now) }

	send(eventBody("d1", "tracking.updated", "80574902", "demora", "transito|2026-10-30 09:00:00", "demora|2026-10-31 09:00:00"))
	send(eventBody("d2", "tracking.updated", "80574902", "demora", "transito|2026-10-30 09:00:00", "demora|2026-10-31 09:00:00"))
	list, _ := f.s.ListNotifications(30)
	if len(list.Items) != 1 || list.Items[0].Kind != NotifDelay || list.Items[0].Severity != "warning" {
		t.Errorf("demora: %+v", list.Items)
	}
}

func TestProviderEventsHistoryFromWebhookAndPolls(t *testing.T) {
	f, _ := providerFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/tracking" {
			_, _ = w.Write([]byte(`{"detailed":false,"order":{"ose_id":5},"status":{"registrado":{"fecha":"2026-10-28 09:00:00"},"transito":{"fecha":"2026-10-29 09:00:00","completo":true}}}`))
		}
	})
	orderID := shalomShipment(t, f, "80574902")
	enableProvider(t, f)
	secret := strings.Repeat("12", 32)
	setSecret(t, f, secret)
	now := time.Now()
	body := eventBody("h1", "tracking.updated", "80574902", "destino", "transito|2026-10-29 09:00:00", "destino|2026-10-31 10:00:00")
	f.s.HandleWebhook(body, hookSign(secret, now.Unix(), body), now)

	if _, err := f.s.RefreshShipmentFromProvider(t.Context(), orderID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.RefreshShipmentFromProvider(t.Context(), orderID); err != nil { // sin cambios: no se duplica la consulta
		t.Fatal(err)
	}
	evs, err := f.s.ShipmentProviderEvents(orderID)
	if err != nil || len(evs) != 2 {
		t.Fatalf("historial: %d eventos %v", len(evs), err)
	}
	var hook, poll *ProviderEventView
	for i := range evs {
		if evs[i].Source == "webhook" {
			hook = &evs[i]
		} else {
			poll = &evs[i]
		}
	}
	if hook == nil || hook.Status != "destino" || len(hook.Milestone) != 2 || hook.Milestone[1].Milestone != "destino" {
		t.Errorf("evento del webhook: %+v", hook)
	}
	if poll == nil || poll.Status != "transito" || len(poll.Milestone) != 2 || poll.Source != "consulta" {
		t.Errorf("consulta manual: %+v", poll)
	}

	// Un envío sin guía con formato Shalom devuelve historial vacío, sin error.
	o2 := f.confirmed(t, 1, 100)
	if _, err := f.s.UpdateShipment(o2.ID, ShipmentInput{CarrierID: &f.carrier.ID, GuideNumber: "G1", DeliveryMode: "agencia"}); err != nil {
		t.Fatal(err)
	}
	if evs, err := f.s.ShipmentProviderEvents(o2.ID); err != nil || len(evs) != 0 {
		t.Errorf("sin guía Shalom: %v %v", evs, err)
	}
	var cnt int64
	f.s.db.Model(&database.EquipProviderEvent{}).Count(&cnt)
	if cnt != 2 {
		t.Errorf("eventos guardados: %d", cnt)
	}
}
