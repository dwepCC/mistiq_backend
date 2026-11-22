package shalom

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func newTestClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return New(srv.URL, "key-123", srv.Client())
}

func TestClientSendsAPIKeyAndParsesLocations(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Key") != "key-123" {
			t.Errorf("falta X-API-Key: %q", r.Header.Get("X-API-Key"))
		}
		if r.URL.Path != "/v1/locations/departments/15/provinces/2/districts" {
			t.Errorf("ruta: %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"items":[{"id":1,"name":"BARRANCA","ubi_id":150201}]}`))
	})
	got, err := c.Districts(context.Background(), 15, 2)
	if err != nil || len(got) != 1 || got[0].UbiID != 150201 || got[0].Name != "BARRANCA" {
		t.Fatalf("Districts = %+v, %v", got, err)
	}
}

func TestClientAPIError(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "7")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"code":"rate_limited","message":"demasiadas","request_id":"r1"}}`))
	})
	_, err := c.Departments(context.Background())
	ae, ok := AsAPIError(err)
	if !ok || ae.Status != 429 || ae.Code != "rate_limited" || ae.RetryAfter != 7*time.Second {
		t.Fatalf("error: %+v %v", ae, err)
	}
	c2 := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"code":"key_expired","message":"vencida"}}`))
	})
	if _, err := c2.Departments(context.Background()); err == nil {
		t.Fatal("quería error")
	} else if ae, _ := AsAPIError(err); !ae.IsAuth() {
		t.Errorf("IsAuth: %+v", ae)
	}
}

func TestClientWithoutKeyDoesNotCallAPI(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true }))
	defer srv.Close()
	if _, err := New(srv.URL, "  ", srv.Client()).Departments(context.Background()); err == nil || called {
		t.Fatalf("sin API key no debe llamar: err=%v called=%v", err, called)
	}
}

func TestAgenciesPaginates(t *testing.T) {
	calls := 0
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Query().Get("per_page") != "1000" {
			t.Errorf("per_page: %s", r.URL.Query().Get("per_page"))
		}
		page := r.URL.Query().Get("page")
		items := `[{"id":1,"abrebiatura":"AAA","nombre":"A / B / C / D","habilitado":true}]`
		if page == "2" {
			items = `[{"id":2,"abrebiatura":"BBB","nombre":"E / F / G / H","habilitado":false}]`
		}
		fmt.Fprintf(w, `{"page":%s,"per_page":1000,"total":2,"items":%s}`, page, items)
	})
	got, err := c.Agencies(context.Background())
	if err != nil || len(got) != 2 || calls != 2 || got[1].Enabled || got[0].Abbreviation != "AAA" {
		t.Fatalf("Agencies = %+v calls=%d err=%v", got, calls, err)
	}
}

func TestSubscribeAndBatch(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/tracking/subscriptions":
			var b map[string]string
			_ = json.NewDecoder(r.Body).Decode(&b)
			if b["numero"] != "80574902" || b["codigo"] != "CJTW" {
				t.Errorf("body: %v", b)
			}
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"subscription":{"id":812,"numero":"80574902","codigo":"CJTW","status":"pending_resolve"},"outcome":"created"}`))
		case "/v1/tracking/batch":
			_, _ = w.Write([]byte(`{"results":[{"custom_id":"p1","numero":"1","ok":true,"tracking":{"detailed":false,"status":{"destino":{"fecha":"2026-11-02 10:05:03","completo":true}}}},{"custom_id":"p2","numero":"2","ok":false,"error":{"code":"not_found","message":"orden no encontrada"}}]}`))
		}
	})
	sub, err := c.Subscribe(context.Background(), "80574902", "CJTW")
	if err != nil || sub.ID != 812 || sub.Status != "pending_resolve" {
		t.Fatalf("Subscribe = %+v %v", sub, err)
	}
	res, err := c.TrackBatch(context.Background(), []BatchItem{{CustomID: "p1", Numero: "1", Codigo: "AAAA"}, {CustomID: "p2", Numero: "2", Codigo: "BBBB"}})
	if err != nil || len(res) != 2 || !res[0].OK || res[1].OK || res[1].Error.Code != "not_found" {
		t.Fatalf("TrackBatch = %+v %v", res, err)
	}
	d := res[0].Tracking.Status.Destino.Date()
	if d == nil || d.Format("2006-01-02 15:04") != "2026-11-02 10:05" {
		t.Errorf("fecha de destino: %v", d)
	}
	if _, err := c.TrackBatch(context.Background(), make([]BatchItem, 51)); err == nil {
		t.Error("más de 50 guías debe rechazarse")
	}
}

func sign(secret string, ts int64, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(fmt.Sprintf("%d.", ts)))
	mac.Write(body)
	return fmt.Sprintf("t=%d,v1=%s", ts, hex.EncodeToString(mac.Sum(nil)))
}

func TestVerifySignature(t *testing.T) {
	now := time.Now()
	body := []byte(`{"id":"evt_1"}`)
	good := sign("sec", now.Unix(), body)
	if err := VerifySignature("sec", good, body, now); err != nil {
		t.Fatalf("firma válida: %v", err)
	}
	if err := VerifySignature("otro", good, body, now); err == nil {
		t.Error("secreto distinto debe fallar")
	}
	if err := VerifySignature("sec", good, []byte(`{"id":"evt_2"}`), now); err == nil {
		t.Error("cuerpo alterado debe fallar")
	}
	if err := VerifySignature("sec", sign("sec", now.Add(-10*time.Minute).Unix(), body), body, now); err == nil {
		t.Error("firma vieja (replay) debe fallar")
	}
	if err := VerifySignature("sec", "t=1", body, now); err == nil {
		t.Error("firma incompleta debe fallar")
	}
}

func TestTrackingDataMilestoneDate(t *testing.T) {
	var d TrackingData
	_ = json.Unmarshal([]byte(`{"numero":"1","status":"destino","timeline":[{"milestone":"transito","fecha":"2026-07-29 14:08:21","completo":true},{"milestone":"destino","fecha":"2026-07-30 10:05:03","completo":true}]}`), &d)
	if got := d.MilestoneDate("destino"); got == nil || got.Format("2006-01-02") != "2026-07-30" {
		t.Errorf("destino: %v", got)
	}
	if d.MilestoneDate("entregado") != nil {
		t.Error("hito ausente debe ser nil")
	}
}
