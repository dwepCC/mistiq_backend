package service

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Caso real de producción: al vincular, la consulta manual mueve el envío ANTES de que llegue el primer evento del
// webhook; el aviso debe nacer igual y el webhook posterior («sin cambios») no debe duplicarlo.
func TestLinkNotifiesEvenThoughWebhookArrivesAfterThePoll(t *testing.T) {
	f, _ := providerFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/tracking" {
			_, _ = w.Write([]byte(`{"detailed":false,"order":{"ose_id":3},"status":{"transito":{"fecha":"2026-10-30 09:00:00","completo":true},"destino":{"fecha":"2026-11-03 10:00:00","completo":true}}}`))
		}
	})
	orderID := guidedShipment(t, f, "101567718 - MKWD")
	enableProvider(t, f)
	secret := strings.Repeat("9a", 32)
	setSecret(t, f, secret)

	if _, err := f.s.LinkShipmentTracking(t.Context(), orderID, ""); err != nil {
		t.Fatal(err)
	}
	list, _ := f.s.ListNotifications(30)
	if len(list.Items) != 1 || list.Items[0].Kind != NotifArrival || list.Items[0].OrderID != orderID {
		t.Fatalf("la consulta al vincular debe crear el aviso de llegada: %+v", list.Items)
	}
	now := time.Now()
	body := eventBody("w1", "tracking.updated", "101567718", "destino", "transito|2026-10-30 09:00:00", "destino|2026-11-03 10:00:00")
	if r := f.s.HandleWebhook(body, hookSign(secret, now.Unix(), body), now); r.Status != 200 || r.Note != "sin cambios" {
		t.Fatalf("webhook posterior: %+v", r)
	}
	if list, _ := f.s.ListNotifications(30); len(list.Items) != 1 {
		t.Errorf("el webhook posterior no debe duplicar el aviso: %d", len(list.Items))
	}
}

func TestBatchSyncNotifiesArrivalsButNotOldDeliveries(t *testing.T) {
	// El servidor falso contesta según el N° de guía que recibe (el custom_id se devuelve tal cual).
	f, _ := providerFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/tracking/batch" {
			return
		}
		var req struct {
			Items []struct {
				CustomID string `json:"custom_id"`
				Numero   string `json:"numero"`
			} `json:"items"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		var rs []string
		for _, it := range req.Items {
			status := `"destino":{"fecha":"2026-11-03 10:00:00","completo":true}`
			if it.Numero == "101567568" { // entregada hace días
				status = `"destino":{"fecha":"2026-10-25 10:00:00"},"entregado":{"fecha":"2026-10-26 10:00:00"}`
			}
			rs = append(rs, fmt.Sprintf(`{"custom_id":%q,"numero":%q,"ok":true,"tracking":{"detailed":false,"status":{%s}}}`, it.CustomID, it.Numero, status))
		}
		_, _ = w.Write([]byte(`{"results":[` + strings.Join(rs, ",") + `]}`))
	})
	arrived := guidedShipment(t, f, "101732118 - 9TJW")
	guidedShipment(t, f, "101567568 - D77K")
	enableProvider(t, f)

	if r, err := f.s.SyncShalomShipments(t.Context()); err != nil || r.Changed != 2 {
		t.Fatalf("lote: %+v %v", r, err)
	}
	list, _ := f.s.ListNotifications(30)
	if len(list.Items) != 1 || list.Items[0].Kind != NotifArrival || list.Items[0].OrderID != arrived {
		t.Errorf("en lote solo se avisa la llegada, no la entrega antigua: %+v", list.Items)
	}
}
