package service

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"tukifac/pkg/database"
)

// openReturn deja un pedido despachado y un retorno solicitado sobre él.
func openReturn(t *testing.T, f *fixture) *ReturnView {
	t.Helper()
	o := f.shipped(t, 2, 100)
	r, err := f.s.CreateReturn(ReturnInput{OrderID: o.ID}, 1)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestReturnGuide_linkTracksStageAndMovesToEnCamino(t *testing.T) {
	f, _ := providerFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/tracking" {
			if r.URL.Query().Get("numero") != "101567718" || r.URL.Query().Get("codigo") != "MKWD" {
				t.Errorf("consulta: %s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"detailed":false,"order":{"ose_id":77},"status":{"transito":{"fecha":"2026-11-10 09:00:00","completo":true}}}`))
		}
	})
	enableProvider(t, f)
	r := openReturn(t, f)
	if r.TrackStage != "" || r.Status != "solicitado" {
		t.Fatalf("un retorno nuevo no tiene seguimiento: %+v", r)
	}
	v, warns, err := f.s.SetReturnGuide(t.Context(), r.ID, "101567718 - MKWD", "")
	if err != nil {
		t.Fatal(err)
	}
	if v.GuideNumber != "101567718 - MKWD" || v.ProviderCode != "MKWD" || v.TrackStage != "transito" || v.TrackLabel != "En tránsito" {
		t.Fatalf("vincular la guía del retorno: %+v", v)
	}
	if v.Status != "en_camino" {
		t.Errorf("en movimiento en Shalom: el retorno pasa a «en camino»: %s", v.Status)
	}
	if len(warns) != 1 || !strings.Contains(warns[0], "no está verificado") {
		t.Errorf("sin webhook verificado avisa, no falla: %v", warns)
	}
	// Cambiar de guía descarta el seguimiento anterior.
	v, _, err = f.s.SetReturnGuide(t.Context(), r.ID, "texto libre", "")
	if err != nil || v.TrackStage != "" || v.ProviderCode != "" {
		t.Fatalf("otra guía reinicia el seguimiento: %v %+v", err, v)
	}
}

func TestReturnGuide_withoutShalomFormatIsSavedWithWarning(t *testing.T) {
	f, fake := providerFixture(t, func(w http.ResponseWriter, r *http.Request) {})
	enableProvider(t, f)
	r := openReturn(t, f)
	v, warns, err := f.s.SetReturnGuide(t.Context(), r.ID, "GUIA-ABC", "")
	if err != nil || v.GuideNumber != "GUIA-ABC" || len(warns) != 1 || !strings.Contains(warns[0], "formato de Shalom") {
		t.Fatalf("guía libre: %v %v %+v", err, warns, v)
	}
	// Con número válido pero sin código: se guarda y pide el código.
	_, warns, err = f.s.SetReturnGuide(t.Context(), r.ID, "101567718", "")
	if err != nil || len(warns) != 1 || !strings.Contains(warns[0], "código") {
		t.Fatalf("sin código: %v %v", err, warns)
	}
	if fake.calls.Load() != 0 {
		t.Error("sin código no debe consultar a Shalom")
	}
	if _, _, err := f.s.SetReturnGuide(t.Context(), r.ID, "101567718", "ab"); err == nil || !IsValidation(err) {
		t.Errorf("el código debe tener 4 caracteres: %v", err)
	}
	if _, _, err := f.s.SetReturnGuide(t.Context(), 99999, "x", ""); err == nil {
		t.Error("retorno inexistente")
	}
}

func TestReturnWebhook_notifiesArrivalAndDeliveryOnceAndNeverMovesStock(t *testing.T) {
	f, _ := providerFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/tracking" {
			_, _ = w.Write([]byte(`{"detailed":false,"order":{"ose_id":9},"status":{"transito":{"fecha":"2026-11-10 09:00:00","completo":true}}}`))
		}
	})
	enableProvider(t, f)
	secret := strings.Repeat("9a", 32)
	setSecret(t, f, secret)
	r := openReturn(t, f)
	stockBefore := f.stock(t, f.printer.ID)
	if _, _, err := f.s.SetReturnGuide(t.Context(), r.ID, "101567718 - MKWD", ""); err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	send := func(id, status string, ms ...string) WebhookResult {
		body := eventBody(id, "tracking.updated", "101567718", status, ms...)
		return f.s.HandleWebhook(body, hookSign(secret, now.Unix(), body), now)
	}
	if res := send("e1", "destino", "transito|2026-11-10 09:00:00", "destino|2026-11-12 10:00:00"); res.Status != 200 {
		t.Fatalf("webhook destino: %+v", res)
	}
	v, _ := f.s.getReturnView(r.ID)
	if v.TrackStage != "destino" || v.TrackLabel != "En destino" {
		t.Fatalf("etapa tras el aviso: %+v", v)
	}
	list, _ := f.s.ListNotifications(30)
	if len(list.Items) != 1 || list.Items[0].Kind != NotifReturnArrival {
		t.Fatalf("aviso de llegada a destino: %+v", list.Items)
	}
	// El mismo hito por otra vía no duplica el aviso.
	send("e2", "destino", "transito|2026-11-10 09:00:00", "destino|2026-11-12 10:00:00")
	if l, _ := f.s.ListNotifications(30); len(l.Items) != 1 {
		t.Errorf("no debe duplicarse: %d", len(l.Items))
	}
	send("e3", "entregado", "destino|2026-11-12 10:00:00", "entregado|2026-11-13 11:00:00")
	if l, _ := f.s.ListNotifications(30); len(l.Items) != 2 {
		t.Errorf("aviso de entregado: %d", len(l.Items))
	}
	v, _ = f.s.getReturnView(r.ID)
	if v.TrackStage != "entregado" || v.Status != "en_camino" {
		t.Errorf("entregado en Shalom NO recibe el retorno solo: %+v", v)
	}
	if got := f.stock(t, f.printer.ID); got != stockBefore {
		t.Fatalf("Shalom nunca mueve el stock: %d → %d", stockBefore, got)
	}
}

func TestReceiveReturn_goodReentersStockDamagedDoesNot(t *testing.T) {
	f := newFixture(t)
	r := openReturn(t, f)
	before := f.stock(t, f.printer.ID)
	v, err := f.s.ReceiveReturn(r.ID, "", 1)
	if err != nil || v.Status != "recibido" || v.Condition != "buen_estado" || v.ReceivedAt == nil {
		t.Fatalf("recibir en buen estado: %v %+v", err, v)
	}
	if got := f.stock(t, f.printer.ID); got != before+2 {
		t.Fatalf("buen estado reingresa 2 unidades: %d → %d", before, got)
	}
	if _, err := f.s.ReceiveReturn(r.ID, "", 1); err == nil || !IsValidation(err) {
		t.Errorf("no se recibe dos veces: %v", err)
	}
	if got := f.stock(t, f.printer.ID); got != before+2 {
		t.Errorf("recibir dos veces no duplica el reingreso: %d", got)
	}

	r2 := openReturn(t, f)
	b2 := f.stock(t, f.printer.ID)
	v2, err := f.s.ReceiveReturn(r2.ID, "danado", 1)
	if err != nil || v2.Status != "recibido" || v2.Condition != "danado" {
		t.Fatalf("recibir dañado: %v %+v", err, v2)
	}
	if got := f.stock(t, f.printer.ID); got != b2 {
		t.Fatalf("dañado no reingresa: %d → %d", b2, got)
	}
	if _, err := f.s.ReceiveReturn(r2.ID, "roto", 1); err == nil {
		t.Error("condición inválida (ya recibido)")
	}
}

func TestReturnBatchSync_includesOpenReturnsAndSkipsClosed(t *testing.T) {
	f, _ := providerFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/tracking/batch" {
			_, _ = w.Write([]byte(`{"results":[{"custom_id":"1","numero":"101567718","ok":true,"tracking":{"detailed":false,"status":{"transito":{"fecha":"2026-11-10 09:00:00"},"destino":{"fecha":"2026-11-12 10:00:00"}}}}]}`))
		}
	})
	enableProvider(t, f)
	r := openReturn(t, f)
	f.s.db.Model(&database.EquipReturn{}).Where("id = ?", r.ID).Update("guide_number", "101567718 - MKWD")
	res, err := f.s.SyncShalomShipments(t.Context())
	if err != nil || res.Checked != 1 || res.Changed != 1 {
		t.Fatalf("el lote incluye retornos abiertos: %+v %v", res, err)
	}
	v, _ := f.s.getReturnView(r.ID)
	if v.TrackStage != "destino" {
		t.Fatalf("etapa tras el lote: %+v", v)
	}
	if l, _ := f.s.ListNotifications(30); len(l.Items) != 1 || l.Items[0].Kind != NotifReturnArrival {
		t.Errorf("el lote avisa la llegada a destino: %+v", l.Items)
	}
}
