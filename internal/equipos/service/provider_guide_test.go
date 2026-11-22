package service

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"tukifac/pkg/database"
	"tukifac/pkg/secrets"
)

// Formatos reales de producción: la guía de Shalom se registra como «NÚMERO - CÓDIGO».
func TestShalomRefParsesRealGuideFormats(t *testing.T) {
	cases := []struct{ in, numero, code string }{
		{"100439617 - M7JK", "100439617", "M7JK"},
		{"99526318-kHHT", "99526318", "KHHT"},
		{"101732548 -3WJC", "101732548", "3WJC"},
		{"1015674484 - 3NH9", "1015674484", "3NH9"},
		{"  98647248 - DMHH  ", "98647248", "DMHH"},
		{"80574902", "80574902", ""}, // solo número: el código se pide aparte
		{"G1", "", ""},
		{"12345", "", ""},
		{"", "", ""},
		{"100439617 - M7J", "", ""}, // código incompleto
	}
	for _, c := range cases {
		if n, code := shalomRef(c.in); n != c.numero || code != c.code {
			t.Errorf("shalomRef(%q) = (%q, %q), quería (%q, %q)", c.in, n, code, c.numero, c.code)
		}
	}
}

func guidedShipment(t *testing.T, f *fixture, guide string) uint {
	t.Helper()
	o := f.confirmed(t, 1, 100)
	if _, err := f.s.ValidateOrder(o.ID, "", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.UpdateShipment(o.ID, ShipmentInput{CarrierID: &f.carrier.ID, GuideNumber: guide, DestinationDepartment: "Cusco", DeliveryMode: "agencia"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.DispatchOrder(o.ID, ""); err != nil {
		t.Fatal(err)
	}
	return o.ID
}

func TestLinkTakesCodeFromGuideAndWebhookMatchesCombinedGuide(t *testing.T) {
	var tracked []string
	f, _ := providerFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/tracking" {
			tracked = append(tracked, r.URL.Query().Get("numero")+"/"+r.URL.Query().Get("codigo"))
			_, _ = w.Write([]byte(`{"detailed":false,"order":{"ose_id":7},"status":{"transito":{"fecha":"2026-10-30 09:00:00","completo":true}}}`))
		}
	})
	orderID := guidedShipment(t, f, "100439617 - M7JK")
	enableProvider(t, f)

	// Sin escribir el código: sale de la propia guía.
	res, err := f.s.LinkShipmentTracking(t.Context(), orderID, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Order.Shipment.ProviderCode != "M7JK" || len(tracked) != 1 || tracked[0] != "100439617/M7JK" {
		t.Fatalf("código/consulta: %q %v", res.Order.Shipment.ProviderCode, tracked)
	}

	// El webhook informa solo el número y debe encontrar el envío aunque la guía guardada incluya el código.
	f.s.db.Model(&database.EquipProviderSettings{}).Where("1=1").Updates(map[string]any{"webhook_verified": true})
	secret := strings.Repeat("cd", 32)
	setSecret(t, f, secret)
	now := time.Now()
	body := []byte(`{"id":"evt_g1","event":"tracking.updated","data":{"numero":"100439617","status":"destino","timeline":[{"milestone":"destino","fecha":"2026-10-31 10:00:00","completo":true}]}}`)
	if r := f.s.HandleWebhook(body, hookSign(secret, now.Unix(), body), now); r.Status != 200 || !strings.Contains(r.Note, "llegó a agencia") {
		t.Fatalf("webhook: %+v", r)
	}
	var sh database.EquipShipment
	f.s.db.Where("order_id = ?", orderID).First(&sh)
	if sh.Status != "en_agencia" || sh.ArrivedAt == nil || sh.ArrivedAt.In(limaLoc()).Format("2006-01-02") != "2026-10-31" {
		t.Errorf("envío: %+v", sh)
	}
	// Otra guía que solo comparte el prefijo numérico no debe confundirse.
	body2 := []byte(`{"id":"evt_g2","event":"tracking.updated","data":{"numero":"10043961","status":"destino","timeline":[{"milestone":"destino","fecha":"2026-11-01 10:00:00","completo":true}]}}`)
	if r := f.s.HandleWebhook(body2, hookSign(secret, now.Unix(), body2), now); !strings.Contains(r.Note, "sin envío vigente") {
		t.Errorf("un número que solo es prefijo no debe coincidir: %+v", r)
	}
}

func setSecret(t *testing.T, f *fixture, secret string) {
	t.Helper()
	enc, err := encryptForTest(secret)
	if err != nil {
		t.Fatal(err)
	}
	f.s.db.Model(&database.EquipProviderSettings{}).Where("1=1").Update("webhook_secret_enc", enc)
}

func TestBatchSyncDerivesCodesFromGuidesAndSkipsInvalid(t *testing.T) {
	var got []map[string]string
	f, _ := providerFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/tracking/batch" {
			return
		}
		var req struct {
			Items []map[string]string `json:"items"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		got = req.Items
		var rs []string
		for _, it := range req.Items {
			rs = append(rs, fmt.Sprintf(`{"custom_id":%q,"numero":%q,"ok":true,"tracking":{"detailed":false,"status":{"destino":{"fecha":"2026-10-31 10:00:00","completo":true}}}}`, it["custom_id"], it["numero"]))
		}
		_, _ = w.Write([]byte(`{"results":[` + strings.Join(rs, ",") + `]}`))
	})
	good := guidedShipment(t, f, "101732548 -3WJC")
	bad := guidedShipment(t, f, "SIN-FORMATO")
	enableProvider(t, f)

	r, err := f.s.SyncShalomShipments(t.Context())
	if err != nil || r.Checked != 1 || r.Changed != 1 || r.Skipped != 1 {
		t.Fatalf("lote: %+v %v", r, err)
	}
	if len(got) != 1 || got[0]["numero"] != "101732548" || got[0]["codigo"] != "3WJC" {
		t.Errorf("debe consultar número y código separados: %v", got)
	}
	var a, b database.EquipShipment
	f.s.db.Where("order_id = ?", good).First(&a)
	f.s.db.Where("order_id = ?", bad).First(&b)
	if a.ProviderCode != "3WJC" || a.Status != "en_agencia" {
		t.Errorf("el código derivado debe quedar guardado: %+v", a)
	}
	if b.Status != "en_transito" || b.ProviderCode != "" {
		t.Errorf("la guía sin formato no se toca: %+v", b)
	}
}

func encryptForTest(plain string) (string, error) { return secrets.Encrypt(plain) }

// «registrado» en Shalom solo significa que la guía existe; el paquete no salió: el envío sigue por despachar.
func TestRegisteredOnlyDoesNotDispatchShipment(t *testing.T) {
	f, _ := providerFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/tracking" {
			_, _ = w.Write([]byte(`{"detailed":false,"order":{"ose_id":9},"status":{"registrado":{"fecha":"2026-11-02 09:00:00","completo":true}}}`))
		}
	})
	o := f.confirmed(t, 1, 100)
	f.s.ValidateOrder(o.ID, "", 1)
	if _, err := f.s.UpdateShipment(o.ID, ShipmentInput{CarrierID: &f.carrier.ID, GuideNumber: "100439617 - M7JK", DestinationDepartment: "Cusco", DeliveryMode: "agencia"}); err != nil {
		t.Fatal(err)
	}
	enableProvider(t, f)
	res, err := f.s.LinkShipmentTracking(t.Context(), o.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	sh := res.Order.Shipment
	if sh.Status != "pendiente_envio" || sh.DispatchedAt != nil || sh.ProviderStatus != "registrado" {
		t.Errorf("no debe marcarse como despachado solo por estar registrado: %+v", sh)
	}
}
