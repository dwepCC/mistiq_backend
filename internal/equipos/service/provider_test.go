package service

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"tukifac/internal/shalom"
	"tukifac/pkg/database"
)

const testMasterKey = "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"

type shalomFake struct {
	srv   *httptest.Server
	calls atomic.Int32
}

// providerFixture: fixture de Equipos + tablas del proveedor + servidor falso de Shalom (sin red real).
func providerFixture(t *testing.T, handler http.HandlerFunc) (*fixture, *shalomFake) {
	t.Helper()
	t.Setenv("SECRETS_ENCRYPTION_KEY", testMasterKey)
	f := newFixture(t)
	if err := f.s.db.AutoMigrate(&database.EquipCarrierAgency{}, &database.EquipProviderSettings{}, &database.EquipProviderLocation{}, &database.EquipProviderEvent{}, &database.EquipNotification{}); err != nil {
		t.Fatal(err)
	}
	fake := &shalomFake{}
	fake.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fake.calls.Add(1)
		if r.Header.Get("X-API-Key") != "key-abcd1234" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"code":"unauthorized","message":"API key inválida"}}`))
			return
		}
		handler(w, r)
	}))
	t.Cleanup(fake.srv.Close)
	prev := newShalomClient
	newShalomClient = func(baseURL, apiKey string) *shalom.Client {
		return shalom.New(fake.srv.URL, apiKey, fake.srv.Client())
	}
	t.Cleanup(func() { newShalomClient = prev })
	return f, fake
}

func enableProvider(t *testing.T, f *fixture) {
	t.Helper()
	key, on := "key-abcd1234", true
	if _, err := f.s.SaveProviderSettings(ProviderSettingsInput{APIKey: &key, Enabled: &on}); err != nil {
		t.Fatal(err)
	}
}

func TestProviderSettingsEncryptAPIKey(t *testing.T) {
	f, _ := providerFixture(t, func(w http.ResponseWriter, r *http.Request) {})
	on := true
	if _, err := f.s.SaveProviderSettings(ProviderSettingsInput{Enabled: &on}); err == nil {
		t.Fatal("habilitar sin API key debe rechazarse")
	}
	key := "key-abcd1234"
	v, err := f.s.SaveProviderSettings(ProviderSettingsInput{APIKey: &key, Enabled: &on})
	if err != nil || !v.APIKeySet || v.APIKeyHint != "1234" || !v.Enabled || !v.EncryptionReady {
		t.Fatalf("vista: %+v %v", v, err)
	}
	var st database.EquipProviderSettings
	f.s.db.First(&st)
	if st.APIKeyEnc == "" || strings.Contains(st.APIKeyEnc, "abcd1234") {
		t.Fatalf("la API key debe guardarse cifrada: %q", st.APIKeyEnc)
	}
	empty := ""
	if v, _ := f.s.SaveProviderSettings(ProviderSettingsInput{APIKey: &empty}); !v.APIKeySet {
		t.Error("API key vacía no debe borrar la existente")
	}
	if v, _ := f.s.SaveProviderSettings(ProviderSettingsInput{ClearAPIKey: true}); v.APIKeySet || v.Enabled {
		t.Errorf("clear_api_key: %+v", v)
	}
	http1 := "http://x.com/hook"
	if _, err := f.s.SaveProviderSettings(ProviderSettingsInput{WebhookURL: &http1}); err == nil {
		t.Error("el webhook debe ser https")
	}
}

func TestProviderNotEnabledNeverCallsAPI(t *testing.T) {
	f, fake := providerFixture(t, func(w http.ResponseWriter, r *http.Request) {})
	if _, err := f.s.SyncAgencies(t.Context(), true); err == nil || !IsValidation(err) {
		t.Fatalf("sin activar debe ser error de validación: %v", err)
	}
	if fake.calls.Load() != 0 {
		t.Error("no debe llamar a la API sin integración activa")
	}
}

func TestSyncAgenciesMergesWithoutOverwriting(t *testing.T) {
	f, fake := providerFixture(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"page":1,"per_page":1000,"total":4,"items":[
			{"id":190,"abrebiatura":"MIRAF","nombre":"LIMA / LIMA / MIRAFLORES / AV. JOSE PARDO","departamento":"LIMA","provincia":"LIMA","direccion":"AV. JOSE PARDO 533","ubi_id":150122,"aereo":true,"habilitado":true},
			{"id":7,"abrebiatura":"AQP","nombre":"AREQUIPA / AREQUIPA / AREQUIPA / AV PARRA","departamento":"AREQUIPA","provincia":"AREQUIPA","ubi_id":40101,"habilitado":false},
			{"id":404,"abrebiatura":"SLS","nombre":"ICA / ICA / SALAS / PANAMERICANA","departamento":"ICA","provincia":"ICA","habilitado":true},
			{"id":900,"abrebiatura":"CLS","nombre":"CUSCO / CUSCO / CUSCO / CERRADA","departamento":"CUSCO","provincia":"CUSCO","habilitado":false}]}`))
	})
	enableProvider(t, f)
	// Agencia ya cargada a mano (por código): se enlaza, conserva su nombre y no pisa lo que ya tiene.
	f.s.db.Create(&database.EquipCarrierAgency{CarrierID: f.carrier.ID, Code: "MIRAF", Name: "Miraflores (manual)", Department: "Lima", Address: "Dirección corregida", Active: true, LocationStatus: database.AgencyLocConfirmed})
	// Agencia del Excel: su código es el ID de Shalom (404), no la abreviatura; debe enlazarse y no duplicarse.
	f.s.db.Create(&database.EquipCarrierAgency{CarrierID: f.carrier.ID, Code: "404", Name: "SALAS (Excel)", Active: true})
	// Agencia manual que Shalom deshabilitó: se desactiva.
	f.s.db.Create(&database.EquipCarrierAgency{CarrierID: f.carrier.ID, Code: "AQP", Name: "Arequipa Parra", Active: true})

	res, err := f.s.SyncAgencies(t.Context(), true)
	if err != nil || res.Total != 4 || res.Created != 1 || res.Updated != 3 {
		t.Fatalf("resultado: %+v %v", res, err)
	}
	var m database.EquipCarrierAgency
	f.s.db.Where("code = ?", "MIRAF").First(&m)
	if m.Name != "Miraflores (manual)" || m.Address != "Dirección corregida" || m.ProviderRef != 190 || m.Ubigeo != "150122" || m.District != "MIRAFLORES" {
		t.Errorf("agencia enlazada: %+v", m)
	}
	var aqp database.EquipCarrierAgency
	f.s.db.Where("code = ?", "AQP").First(&aqp)
	if aqp.Active || aqp.ProviderEnabled == nil || *aqp.ProviderEnabled {
		t.Errorf("deshabilitada por Shalom: %+v", aqp)
	}
	var n int64
	f.s.db.Model(&database.EquipCarrierAgency{}).Where("provider_ref = 404 AND name = ?", "SALAS (Excel)").Count(&n)
	if n != 1 {
		t.Error("la agencia del Excel (código = id de Shalom) debe enlazarse conservando su nombre")
	}
	f.s.db.Model(&database.EquipCarrierAgency{}).Where("carrier_id = ?", f.carrier.ID).Count(&n)
	if n != 4 {
		t.Errorf("no debe haber duplicados: %d agencias, quería 4", n)
	}
	var closed database.EquipCarrierAgency
	f.s.db.Where("provider_ref = 900").First(&closed)
	if closed.ID == 0 || closed.Active {
		t.Errorf("una agencia nueva deshabilitada por Shalom debe quedar inactiva: %+v", closed)
	}
	// Job automático: hay una sincronización reciente → no vuelve a llamar a la API.
	before := fake.calls.Load()
	if r, err := f.s.SyncAgencies(t.Context(), false); err != nil || !r.Skipped || fake.calls.Load() != before {
		t.Errorf("el job debe omitir si hay una sincronización de menos de 24 h: %+v %v", r, err)
	}
	// Idempotente: repetir con force no duplica ni cambia nada.
	if r, err := f.s.SyncAgencies(t.Context(), true); err != nil || r.Created != 0 || r.Updated != 0 {
		t.Errorf("segunda sincronización: %+v %v", r, err)
	}
}

func TestLocationsAreDBFirstAndLazyPerDepartment(t *testing.T) {
	f, fake := providerFixture(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/locations/departments":
			_, _ = w.Write([]byte(`{"items":[{"id":15,"name":"LIMA"},{"id":4,"name":"AREQUIPA"}]}`))
		case "/v1/locations/departments/15/provinces":
			_, _ = w.Write([]byte(`{"items":[{"id":1,"name":"LIMA"},{"id":2,"name":"BARRANCA"}]}`))
		case "/v1/locations/departments/15/provinces/1/districts":
			_, _ = w.Write([]byte(`{"items":[{"id":10,"name":"MIRAFLORES","ubi_id":150122}]}`))
		case "/v1/locations/departments/15/provinces/2/districts":
			_, _ = w.Write([]byte(`{"items":[{"id":1,"name":"BARRANCA","ubi_id":150201}]}`))
		default:
			t.Errorf("ruta inesperada: %s", r.URL.Path)
		}
	})
	enableProvider(t, f)
	deps, err := f.s.ProviderDepartments(t.Context(), false)
	if err != nil || len(deps) != 2 || fake.calls.Load() != 1 {
		t.Fatalf("departamentos: %v %v calls=%d", deps, err, fake.calls.Load())
	}
	if _, err := f.s.ProviderDepartments(t.Context(), false); err != nil || fake.calls.Load() != 1 {
		t.Errorf("la segunda lectura debe salir de la BD (calls=%d)", fake.calls.Load())
	}
	// Elegir Lima: 1 llamada de provincias + 1 de distritos por provincia (2) = 3 más.
	provs, err := f.s.ProviderProvinces(t.Context(), 15, false)
	if err != nil || len(provs) != 2 || fake.calls.Load() != 4 {
		t.Fatalf("provincias: %v %v calls=%d", provs, err, fake.calls.Load())
	}
	if _, err := f.s.ProviderProvinces(t.Context(), 15, false); err != nil || fake.calls.Load() != 4 {
		t.Errorf("provincias ya sincronizadas no deben volver a la API (calls=%d)", fake.calls.Load())
	}
	dists, err := f.s.ProviderDistricts(15, 2)
	if err != nil || len(dists) != 1 || dists[0].Ubigeo != "150201" || fake.calls.Load() != 4 {
		t.Errorf("distritos desde la BD: %v %v calls=%d", dists, err, fake.calls.Load())
	}
	// Arequipa nunca se eligió: no se sincronizó nada de ese departamento.
	var n int64
	f.s.db.Model(&database.EquipProviderLocation{}).Where("dep_id = 4").Count(&n)
	if n != 0 {
		t.Errorf("no debe sincronizar departamentos no elegidos: %d filas", n)
	}
}

// shalomShipment deja un envío despachado con guía y código vinculados, ANTES de activar la integración.
func shalomShipment(t *testing.T, f *fixture, guide string) uint {
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
	f.s.db.Model(&database.EquipShipment{}).Where("order_id = ?", o.ID).Update("provider_code", "W79H")
	return o.ID
}

func hookSign(secret string, ts int64, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(fmt.Sprintf("%d.", ts)))
	mac.Write(body)
	return fmt.Sprintf("t=%d,v1=%s", ts, hex.EncodeToString(mac.Sum(nil)))
}

func TestWebhookPingSignatureDedupAndStateApplication(t *testing.T) {
	f, _ := providerFixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/webhooks" && r.Method == http.MethodPut {
			_, _ = w.Write([]byte(`{"url":"https://api.example.com/hook","enabled":true,"verified":false,"created":true,"signing_secret":"` + strings.Repeat("ab", 32) + `"}`))
		}
	})
	orderID := shalomShipment(t, f, "80574902")
	enableProvider(t, f)
	hook := "https://api.example.com/hook"
	if _, err := f.s.SaveProviderSettings(ProviderSettingsInput{WebhookURL: &hook}); err != nil {
		t.Fatal(err)
	}
	v, err := f.s.SetupWebhook(t.Context())
	if err != nil || !v.WebhookSecretSet || v.WebhookVerified {
		t.Fatalf("registro del webhook: %+v %v", v, err)
	}
	secret := strings.Repeat("ab", 32)
	now := time.Now()

	// El ping se contesta SIN firma y devuelve el challenge.
	if r := f.s.HandleWebhook([]byte(`{"id":"p1","event":"webhook.ping","data":{"challenge":"xyz-123"}}`), "", now); r.Status != 200 || r.Body != "xyz-123" {
		t.Fatalf("ping: %+v", r)
	}
	if v, _ := f.s.ProviderSettingsView(); !v.WebhookVerified {
		t.Error("tras el ping el webhook queda verificado")
	}

	arrived := now.In(limaLoc()).AddDate(0, 0, -3)
	body := []byte(fmt.Sprintf(`{"id":"evt_1","event":"tracking.updated","data":{"numero":"80574902","ose_id":584210,"status":"destino","timeline":[{"milestone":"transito","fecha":"%s 09:00:00","completo":true},{"milestone":"destino","fecha":"%s 10:05:03","completo":true}]}}`,
		arrived.AddDate(0, 0, -1).Format("2006-01-02"), arrived.Format("2006-01-02")))

	if r := f.s.HandleWebhook(body, "t=1,v1=bad", now); r.Status != 401 {
		t.Errorf("firma inválida debe ser 401: %+v", r)
	}
	if r := f.s.HandleWebhook(body, "", now); r.Status != 401 {
		t.Errorf("sin firma debe ser 401: %+v", r)
	}
	sig := hookSign(secret, now.Unix(), body)
	if r := f.s.HandleWebhook(body, sig, now); r.Status != 200 {
		t.Fatalf("evento válido: %+v", r)
	}
	var sh database.EquipShipment
	f.s.db.Where("order_id = ?", orderID).First(&sh)
	if sh.Status != "en_agencia" || sh.ArrivedAt == nil || sh.ArrivedAt.In(limaLoc()).Format("2006-01-02") != arrived.Format("2006-01-02") {
		t.Fatalf("envío: %+v", sh)
	}
	// El despacho se registró hoy pero la salida real fue antes: se retrocede (no bloquea).
	if sh.DispatchedAt == nil || sh.DispatchedAt.After(*sh.ArrivedAt) || sh.ProviderOseID != "584210" || sh.ProviderStatus != "destino" {
		t.Errorf("fechas/seguimiento: dispatched=%v ose=%q status=%q", sh.DispatchedAt, sh.ProviderOseID, sh.ProviderStatus)
	}
	if sh.PickupDeadline == nil {
		t.Error("debe correr el plazo de recojo desde la llegada")
	}

	// Entrega duplicada (mismo id): se acepta pero no se reprocesa.
	if r := f.s.HandleWebhook(body, sig, now); r.Status != 200 || r.Note != "evento duplicado" {
		t.Errorf("duplicado: %+v", r)
	}
	var n int64
	f.s.db.Model(&database.EquipProviderEvent{}).Where("event_id = ?", "evt_1").Count(&n)
	if n != 1 {
		t.Errorf("el evento debe guardarse una sola vez: %d", n)
	}

	// Una llegada ya registrada a mano NO se pisa con otra fecha de Shalom.
	manual := *sh.ArrivedAt
	body2 := []byte(fmt.Sprintf(`{"id":"evt_2","event":"tracking.updated","data":{"numero":"80574902","status":"destino","timeline":[{"milestone":"destino","fecha":"%s 08:00:00","completo":true}]}}`, arrived.AddDate(0, 0, 1).Format("2006-01-02")))
	f.s.HandleWebhook(body2, hookSign(secret, now.Unix(), body2), now)
	f.s.db.Where("order_id = ?", orderID).First(&sh)
	if !sh.ArrivedAt.Equal(manual) {
		t.Errorf("la fecha de llegada existente no se pisa: %v → %v", manual, sh.ArrivedAt)
	}

	// Entregado: cierra el envío con la fecha de Shalom.
	body3 := []byte(fmt.Sprintf(`{"id":"evt_3","event":"tracking.delivered","data":{"numero":"80574902","status":"entregado","delivered":true,"timeline":[{"milestone":"entregado","fecha":"%s 16:00:00","completo":true}]}}`, now.In(limaLoc()).Format("2006-01-02")))
	f.s.HandleWebhook(body3, hookSign(secret, now.Unix(), body3), now)
	f.s.db.Where("order_id = ?", orderID).First(&sh)
	if sh.Status != "entregado" || sh.PickedUpAt == nil {
		t.Errorf("entregado: %+v", sh)
	}
	// Un evento tardío no revierte un envío ya entregado.
	body4 := []byte(`{"id":"evt_4","event":"tracking.updated","data":{"numero":"80574902","status":"transito","timeline":[{"milestone":"transito","fecha":"2026-01-01 09:00:00","completo":true}]}}`)
	f.s.HandleWebhook(body4, hookSign(secret, now.Unix(), body4), now)
	f.s.db.Where("order_id = ?", orderID).First(&sh)
	if sh.Status != "entregado" {
		t.Errorf("no debe retroceder: %s", sh.Status)
	}
}

func TestLinkRefreshAndBatchSync(t *testing.T) {
	f, fake := providerFixture(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/tracking":
			if r.URL.Query().Get("numero") != "80574902" || r.URL.Query().Get("codigo") != "W79H" {
				t.Errorf("consulta: %s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"detailed":false,"order":{"ose_id":584210},"status":{"registrado":{"fecha":"2026-10-28 09:00:00"},"transito":{"fecha":"2026-10-29 09:00:00","completo":true},"destino":{"fecha":"2026-10-31 10:00:00","completo":true}}}`))
		case "/v1/tracking/batch":
			var req struct {
				Items []struct {
					CustomID string `json:"custom_id"`
				} `json:"items"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			_, _ = w.Write([]byte(`{"results":[{"custom_id":"` + req.Items[0].CustomID + `","numero":"80574902","ok":true,"tracking":{"detailed":false,"status":{"entregado":{"fecha":"2026-11-02 12:00:00"},"destino":{"fecha":"2026-10-31 10:00:00"}}}}]}`))
		}
	})
	o := f.confirmed(t, 1, 100)
	f.s.ValidateOrder(o.ID, "", 1)
	if _, err := f.s.UpdateShipment(o.ID, ShipmentInput{CarrierID: &f.carrier.ID, GuideNumber: "80574902", DestinationDepartment: "Cusco", DeliveryMode: "agencia"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.DispatchOrder(o.ID, ""); err != nil {
		t.Fatal(err)
	}
	enableProvider(t, f)

	if _, err := f.s.LinkShipmentTracking(t.Context(), o.ID, "ab"); err == nil {
		t.Error("el código debe tener 4 caracteres")
	}
	res, err := f.s.LinkShipmentTracking(t.Context(), o.ID, "w79h")
	if err != nil {
		t.Fatal(err)
	}
	sh := res.Order.Shipment
	if sh.ProviderCode != "W79H" || sh.Status != "en_agencia" || sh.ArrivedAt.In(limaLoc()).Format("2006-01-02") != "2026-10-31" {
		t.Fatalf("vincular + estado: %+v", sh)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "no está verificado") {
		t.Errorf("sin webhook verificado debe avisar, no fallar: %v", res.Warnings)
	}

	before := fake.calls.Load()
	r, err := f.s.SyncShalomShipments(t.Context())
	if err != nil || r.Checked != 1 || r.Changed != 1 || fake.calls.Load() != before+1 {
		t.Fatalf("lote: %+v %v (llamadas %d→%d)", r, err, before, fake.calls.Load())
	}
	var after database.EquipShipment
	f.s.db.Where("order_id = ?", o.ID).First(&after)
	if after.Status != "entregado" || after.PickedUpAt == nil {
		t.Errorf("el lote debe cerrar la entrega: %+v", after)
	}
}
