package shalom

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
)

// SignatureTolerance ventana anti-replay de la firma (la documentación recomienda 5 minutos).
const SignatureTolerance = 5 * time.Minute

// VerifySignature valida la cabecera X-Shalom-Signature: «t=<unix>,v1=<hex>» con
// v1 = HMAC-SHA256(t + "." + cuerpo_crudo, signing_secret). Se calcula sobre los bytes crudos del cuerpo,
// nunca sobre un JSON reserializado.
func VerifySignature(secret, header string, body []byte, now time.Time) error {
	var ts, sig string
	for _, part := range strings.Split(header, ",") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) != 2 {
			continue
		}
		switch kv[0] {
		case "t":
			ts = kv[1]
		case "v1":
			sig = kv[1]
		}
	}
	if ts == "" || sig == "" {
		return errors.New("firma incompleta")
	}
	sec, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return errors.New("timestamp de firma inválido")
	}
	if d := now.Sub(time.Unix(sec, 0)); d > SignatureTolerance || d < -SignatureTolerance {
		return errors.New("firma fuera de la ventana de tolerancia")
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts + "."))
	mac.Write(body)
	want := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(want), []byte(sig)) {
		return errors.New("firma inválida")
	}
	return nil
}

// Event sobre del webhook.
type Event struct {
	ID         string          `json:"id"`
	Event      string          `json:"event"`
	OccurredAt string          `json:"occurred_at"`
	Data       json.RawMessage `json:"data"`
}

const (
	EventPing      = "webhook.ping"
	EventUpdated   = "tracking.updated"
	EventDelivered = "tracking.delivered"
	EventExpired   = "tracking.expired"
)

// TrackingData «data» de tracking.updated / delivered / expired.
type TrackingData struct {
	Numero         string `json:"numero"`
	OseID          int    `json:"ose_id"`
	Status         string `json:"status"`
	PreviousStatus string `json:"previous_status"`
	Delivered      bool   `json:"delivered"`
	Timeline       []struct {
		Milestone string `json:"milestone"`
		Fecha     string `json:"fecha"`
		Completo  bool   `json:"completo"`
	} `json:"timeline"`
}

// MilestoneDate fecha de un hito del timeline («YYYY-MM-DD HH:MM:SS» en hora de Lima); nil si no ocurrió.
func (d *TrackingData) MilestoneDate(name string) *time.Time {
	for _, m := range d.Timeline {
		if m.Milestone == name {
			return parseFecha(m.Fecha)
		}
	}
	return nil
}

// FechaOf convierte la fecha de un hito de GET /v1/tracking.
func (m *Milestone) Date() *time.Time {
	if m == nil {
		return nil
	}
	return parseFecha(m.Fecha)
}

func parseFecha(s string) *time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	loc, err := time.LoadLocation("America/Lima")
	if err != nil {
		loc = time.FixedZone("America/Lima", -5*3600)
	}
	for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, s, loc); err == nil {
			return &t
		}
	}
	return nil
}
