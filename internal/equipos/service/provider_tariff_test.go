package service

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"tukifac/pkg/database"
)

// tariffFixture: envío de Shalom con agencias de origen y destino sincronizadas y un Shalom Pro simulado.
type tariffFake struct {
	logins, tariffs atomic.Int32
	badPassword     atomic.Bool
	expireSession   atomic.Bool // la próxima tarifa responde shalom_auth_failed (sesión vencida)
}

func tariffFixture(t *testing.T) (*fixture, *tariffFake, uint) {
	t.Helper()
	fake := &tariffFake{}
	clearProSession()
	t.Cleanup(clearProSession)
	f, _ := providerFixture(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/shalom/sessions":
			fake.logins.Add(1)
			var b map[string]string
			_ = json.NewDecoder(r.Body).Decode(&b)
			if fake.badPassword.Load() || b["email"] != "pro@empresa.com" || b["password"] != "clave-pro" {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":{"code":"shalom_auth_failed","message":"rechazado"}}`))
				return
			}
			_, _ = w.Write([]byte(`{"session_token":"ssk_tok` + string(rune('0'+fake.logins.Load())) + `","expires_at":"2099-01-01T00:00:00Z"}`))
		case "/v1/tariff/calculate":
			fake.tariffs.Add(1)
			if fake.expireSession.CompareAndSwap(true, false) || !strings.HasPrefix(r.Header.Get("X-Shalom-Session"), "ssk_tok") {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte(`{"error":{"code":"shalom_auth_failed","message":"sesión inválida"}}`))
				return
			}
			var b struct {
				Origin int `json:"origin_terminal_id"`
				Dest   int `json:"destiny_terminal_id"`
			}
			_ = json.NewDecoder(r.Body).Decode(&b)
			if b.Origin != 66 || b.Dest != 7 {
				t.Errorf("ids de terminal: %+v", b)
			}
			_, _ = w.Write([]byte(`{"currency":"PEN","aereo":false,"breakdown":{"sobre":8,"caja_paquete_s":12,"otra_medida":25}}`))
		}
	})
	enableProvider(t, f)
	pro, pass := "pro@empresa.com", "clave-pro"
	if _, err := f.s.SaveProviderSettings(ProviderSettingsInput{ProEmail: &pro, ProPassword: &pass}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(clearProSession)

	t66 := true
	origin := database.EquipCarrierAgency{CarrierID: f.carrier.ID, Code: "66", Name: "LIMA ORIGEN", Active: true, ProviderRef: 66, ProviderAereo: &t66}
	dest := database.EquipCarrierAgency{CarrierID: f.carrier.ID, Code: "7", Name: "AREQUIPA DESTINO", Active: true, ProviderRef: 7}
	f.s.db.Create(&origin)
	f.s.db.Create(&dest)
	o := f.confirmed(t, 1, 100)
	if _, err := f.s.UpdateShipment(o.ID, ShipmentInput{CarrierID: &f.carrier.ID, GuideNumber: "G9", OriginAgencyID: &origin.ID, DestinationAgencyID: &dest.ID, DestinationDepartment: "Arequipa", DeliveryMode: "agencia"}); err != nil {
		t.Fatal(err)
	}
	return f, fake, o.ID
}

func TestProCredentialsAreEncryptedAndNeverReturned(t *testing.T) {
	f, _, _ := tariffFixture(t)
	v, err := f.s.ProviderSettingsView()
	if err != nil || v.ProEmail != "pro@empresa.com" || !v.ProPasswordSet {
		t.Fatalf("vista: %+v %v", v, err)
	}
	var st database.EquipProviderSettings
	f.s.db.First(&st)
	if st.ProPasswordEnc == "" || strings.Contains(st.ProPasswordEnc, "clave-pro") {
		t.Fatalf("la clave de Shalom Pro debe guardarse cifrada: %q", st.ProPasswordEnc)
	}
	if raw, _ := json.Marshal(v); strings.Contains(string(raw), "clave-pro") || strings.Contains(string(raw), st.ProPasswordEnc) {
		t.Error("la clave no debe salir en el JSON de la vista")
	}
	empty := ""
	if v, _ := f.s.SaveProviderSettings(ProviderSettingsInput{ProPassword: &empty}); !v.ProPasswordSet {
		t.Error("clave vacía no debe borrar la existente")
	}
	if v, _ := f.s.SaveProviderSettings(ProviderSettingsInput{ClearPro: true}); v.ProPasswordSet || v.ProEmail != "" {
		t.Errorf("clear_pro: %+v", v)
	}
}

func TestQuoteTariffReusesSessionAndCachesRoute(t *testing.T) {
	f, fake, orderID := tariffFixture(t)
	q, err := f.s.QuoteShipmentTariff(t.Context(), orderID, false)
	if err != nil || q.Breakdown["sobre"] != 8 || q.Breakdown["caja_paquete_s"] != 12 || q.Currency != "PEN" || q.FromCache {
		t.Fatalf("cotización: %+v %v", q, err)
	}
	if fake.logins.Load() != 1 || fake.tariffs.Load() != 1 {
		t.Fatalf("una cotización = 1 login + 1 tarifa, hubo %d/%d", fake.logins.Load(), fake.tariffs.Load())
	}
	// Misma ruta: sale de la cache (sin tocar la API).
	if q2, err := f.s.QuoteShipmentTariff(t.Context(), orderID, false); err != nil || !q2.FromCache || fake.tariffs.Load() != 1 {
		t.Errorf("segunda cotización debe salir de cache: %+v %v tarifas=%d", q2, err, fake.tariffs.Load())
	}
}

func TestQuoteTariffRenewsExpiredSessionOnce(t *testing.T) {
	f, fake, orderID := tariffFixture(t)
	if _, err := f.s.QuoteShipmentTariff(t.Context(), orderID, false); err != nil {
		t.Fatal(err)
	}
	clearTariffMemo()
	fake.expireSession.Store(true) // Shalom invalida la sesión: debe reloguear y reintentar una vez
	q, err := f.s.QuoteShipmentTariff(t.Context(), orderID, false)
	if err != nil || q.Breakdown["sobre"] != 8 {
		t.Fatalf("debe recuperarse de una sesión vencida: %+v %v", q, err)
	}
	if fake.logins.Load() != 2 {
		t.Errorf("debe haber iniciado sesión 2 veces en total: %d", fake.logins.Load())
	}
}

func clearTariffMemo() {
	tariffMu.Lock()
	tariffMemo = map[string]tariffMemoEntry{}
	tariffMu.Unlock()
}

func TestQuoteTariffValidations(t *testing.T) {
	f, fake, orderID := tariffFixture(t)
	// Sin cuenta de Shalom Pro.
	f.s.SaveProviderSettings(ProviderSettingsInput{ClearPro: true})
	if _, err := f.s.QuoteShipmentTariff(t.Context(), orderID, false); err == nil || !strings.Contains(err.Error(), "Shalom Pro") || !IsValidation(err) {
		t.Errorf("sin cuenta Pro: %v", err)
	}
	// Credenciales incorrectas: error claro y sin reintentos en bucle.
	pro, pass := "pro@empresa.com", "otra-clave"
	f.s.SaveProviderSettings(ProviderSettingsInput{ProEmail: &pro, ProPassword: &pass})
	fake.badPassword.Store(true)
	before := fake.logins.Load()
	if _, err := f.s.QuoteShipmentTariff(t.Context(), orderID, false); err == nil || !strings.Contains(err.Error(), "rechazó el correo o la clave") {
		t.Errorf("credenciales inválidas: %v", err)
	}
	if fake.logins.Load() != before+1 {
		t.Errorf("no debe insistir con credenciales inválidas: %d intentos", fake.logins.Load()-before)
	}
	// Vía aérea: destino sin vuelo declarado.
	var dest database.EquipCarrierAgency
	f.s.db.Where("provider_ref = 7").First(&dest)
	no := false
	f.s.db.Model(&dest).Update("provider_aereo", &no)
	if _, err := f.s.QuoteShipmentTariff(t.Context(), orderID, true); err == nil || !strings.Contains(err.Error(), "aérea") {
		t.Errorf("aéreo sin soporte: %v", err)
	}
	// Agencias sin sincronizar con Shalom.
	f.s.db.Model(&database.EquipCarrierAgency{}).Where("provider_ref = 66").Update("provider_ref", 0)
	if _, err := f.s.QuoteShipmentTariff(t.Context(), orderID, false); err == nil || !strings.Contains(err.Error(), "sincronizadas") {
		t.Errorf("agencia sin enlace: %v", err)
	}
}
