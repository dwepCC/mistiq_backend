// Package shalom es el cliente HTTP de «Shalom API Perú» (https://shalom-api-peru.com/docs). Solo cubre lo
// que usa el panel: ubicaciones, agencias, webhook/suscripciones y tracking. No hay sandbox: nada de aquí
// crea guías reales (eso queda fuera de esta integración).
package shalom

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const DefaultBaseURL = "https://api.shalom-api-peru.com"

// Client llama a la API con la X-API-Key del panel. Las llamadas que tocan a Shalom pueden tardar hasta
// 2 minutos la primera vez (lo advierte la documentación), por eso el timeout por defecto es de 150 s.
type Client struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

func New(baseURL, apiKey string, httpClient *http.Client) *Client {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 150 * time.Second}
	}
	return &Client{baseURL: baseURL, apiKey: strings.TrimSpace(apiKey), http: httpClient}
}

// APIError error devuelto por la API con su envelope estándar {"error":{code,message,request_id}}.
type APIError struct {
	Status     int
	Code       string
	Message    string
	RequestID  string
	RetryAfter time.Duration
}

func (e *APIError) Error() string {
	msg := e.Message
	if msg == "" {
		msg = http.StatusText(e.Status)
	}
	return fmt.Sprintf("shalom API %d %s: %s", e.Status, e.Code, msg)
}

// IsAuth indica que la API key es inválida, está revocada, vencida o deshabilitada.
func (e *APIError) IsAuth() bool {
	return e.Status == http.StatusUnauthorized || e.Status == http.StatusForbidden
}

func (e *APIError) IsNotFound() bool { return e.Status == http.StatusNotFound }

// AsAPIError extrae un *APIError de una cadena de errores.
func AsAPIError(err error) (*APIError, bool) {
	var ae *APIError
	if errors.As(err, &ae) {
		return ae, true
	}
	return nil, false
}

func (c *Client) do(ctx context.Context, method, path string, query url.Values, body, out any) error {
	return c.doH(ctx, method, path, query, body, out, nil)
}

// doH igual que do, con cabeceras extra (p. ej. X-Shalom-Session).
func (c *Client) doH(ctx context.Context, method, path string, query url.Values, body, out any, headers map[string]string) error {
	if c.apiKey == "" {
		return errors.New("falta la API key de Shalom")
	}
	u := c.baseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return err
	}
	req.Header.Set("X-API-Key", c.apiKey)
	req.Header.Set("Accept", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 300 {
		ae := &APIError{Status: resp.StatusCode}
		var env struct {
			Error struct {
				Code      string `json:"code"`
				Message   string `json:"message"`
				RequestID string `json:"request_id"`
			} `json:"error"`
		}
		if json.Unmarshal(raw, &env) == nil {
			ae.Code, ae.Message, ae.RequestID = env.Error.Code, env.Error.Message, env.Error.RequestID
		}
		if ae.Message == "" {
			ae.Message = strings.TrimSpace(string(raw))
			if len(ae.Message) > 200 {
				ae.Message = ae.Message[:200]
			}
		}
		if s, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && s > 0 {
			ae.RetryAfter = time.Duration(s) * time.Second
		}
		return ae
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("respuesta de Shalom no interpretable: %w", err)
	}
	return nil
}

// ── Ubicaciones ────────────────────────────────────────────────────────────

type Location struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	UbiID int    `json:"ubi_id"` // solo en distritos (ubigeo INEI de 6 dígitos)
}

type locationList struct {
	Items []Location `json:"items"`
}

func (c *Client) Departments(ctx context.Context) ([]Location, error) {
	var out locationList
	err := c.do(ctx, http.MethodGet, "/v1/locations/departments", nil, nil, &out)
	return out.Items, err
}

func (c *Client) Provinces(ctx context.Context, depID int) ([]Location, error) {
	var out locationList
	err := c.do(ctx, http.MethodGet, fmt.Sprintf("/v1/locations/departments/%d/provinces", depID), nil, nil, &out)
	return out.Items, err
}

func (c *Client) Districts(ctx context.Context, depID, provID int) ([]Location, error) {
	var out locationList
	err := c.do(ctx, http.MethodGet, fmt.Sprintf("/v1/locations/departments/%d/provinces/%d/districts", depID, provID), nil, nil, &out)
	return out.Items, err
}

// ── Agencias ───────────────────────────────────────────────────────────────

// Agency campos de /v1/agencies que usa el panel. «abrebiatura» (sic) es el nombre real del campo.
type Agency struct {
	ID           int    `json:"id"`
	Abbreviation string `json:"abrebiatura"`
	Name         string `json:"nombre"`
	LocalName    string `json:"lugar_over"`
	Department   string `json:"departamento"`
	Province     string `json:"provincia"`
	Address      string `json:"direccion"`
	UbiID        int    `json:"ubi_id"`
	Aereo        bool   `json:"aereo"`
	Enabled      bool   `json:"habilitado"`
	HomeDelivery bool   `json:"reparto_habilitado"`
}

type AgencyPage struct {
	Page    int      `json:"page"`
	PerPage int      `json:"per_page"`
	Total   int      `json:"total"`
	Items   []Agency `json:"items"`
}

// Agencies devuelve el catálogo completo paginando de a 1000 (≈546 agencias: normalmente una sola llamada).
func (c *Client) Agencies(ctx context.Context) ([]Agency, error) {
	var all []Agency
	for page := 1; page <= 50; page++ {
		var p AgencyPage
		q := url.Values{"page": {strconv.Itoa(page)}, "per_page": {"1000"}, "incluir_deshabilitadas": {"true"}}
		if err := c.do(ctx, http.MethodGet, "/v1/agencies", q, nil, &p); err != nil {
			return nil, err
		}
		all = append(all, p.Items...)
		if len(p.Items) == 0 || len(all) >= p.Total {
			break
		}
	}
	return all, nil
}

// ── Webhook y suscripciones ────────────────────────────────────────────────

type WebhookConfig struct {
	URL           string `json:"url"`
	Enabled       bool   `json:"enabled"`
	Verified      bool   `json:"verified"`
	Created       bool   `json:"created"`
	SigningSecret string `json:"signing_secret"` // solo viene al crear o rotar
	Configured    bool   `json:"configured"`
	LastDelivery  string `json:"last_delivery_at"`
	Usage         struct {
		Used int `json:"used"`
		Max  int `json:"max"`
	} `json:"usage"`
}

// SetWebhook registra la URL. Shalom envía un ping de verificación y hasta que se responda no entrega eventos.
func (c *Client) SetWebhook(ctx context.Context, webhookURL string) (*WebhookConfig, error) {
	var out WebhookConfig
	err := c.do(ctx, http.MethodPut, "/v1/webhooks", nil, map[string]string{"url": webhookURL}, &out)
	return &out, err
}

func (c *Client) GetWebhook(ctx context.Context) (*WebhookConfig, error) {
	var out WebhookConfig
	err := c.do(ctx, http.MethodGet, "/v1/webhooks", nil, nil, &out)
	return &out, err
}

func (c *Client) RotateWebhookSecret(ctx context.Context) (*WebhookConfig, error) {
	var out WebhookConfig
	err := c.do(ctx, http.MethodPost, "/v1/webhooks/rotate", nil, nil, &out)
	return &out, err
}

func (c *Client) DeleteWebhook(ctx context.Context) error {
	return c.do(ctx, http.MethodDelete, "/v1/webhooks", nil, nil, nil)
}

type Subscription struct {
	ID         int    `json:"id"`
	Numero     string `json:"numero"`
	Codigo     string `json:"codigo"`
	Status     string `json:"status"`
	NextPollAt string `json:"next_poll_at"`
}

type subscribeResponse struct {
	Subscription Subscription `json:"subscription"`
	Outcome      string       `json:"outcome"`
}

// Subscribe suscribe una guía. Shalom envía enseguida un primer evento con el estado actual.
func (c *Client) Subscribe(ctx context.Context, numero, codigo string) (*Subscription, error) {
	var out subscribeResponse
	err := c.do(ctx, http.MethodPost, "/v1/tracking/subscriptions", nil, map[string]string{"numero": numero, "codigo": codigo}, &out)
	if err != nil {
		return nil, err
	}
	return &out.Subscription, nil
}

func (c *Client) Unsubscribe(ctx context.Context, id int) error {
	return c.do(ctx, http.MethodDelete, fmt.Sprintf("/v1/tracking/subscriptions/%d", id), nil, nil, nil)
}

// ── Tracking ───────────────────────────────────────────────────────────────

// Milestone un hito del envío; «Fecha» es "YYYY-MM-DD HH:MM:SS" (hora de Lima) y los hitos no ocurridos llegan null.
type Milestone struct {
	Fecha    string `json:"fecha"`
	Completo bool   `json:"completo"`
}

type TrackingStatus struct {
	Registrado *Milestone `json:"registrado"`
	Origen     *Milestone `json:"origen"`
	Transito   *Milestone `json:"transito"`
	Demora     *Milestone `json:"demora"`
	Destino    *Milestone `json:"destino"`
	Entregado  *Milestone `json:"entregado"`
	Reparto    *Milestone `json:"reparto"`
}

type Tracking struct {
	Detailed bool `json:"detailed"`
	Order    struct {
		OseID int `json:"ose_id"`
	} `json:"order"`
	Status TrackingStatus `json:"status"`
}

// Track consulta el estado por N° de guía + código (modo «estado»: solo X-API-Key, sin credenciales de Shalom Pro).
func (c *Client) Track(ctx context.Context, numero, codigo string) (*Tracking, error) {
	var out Tracking
	q := url.Values{"numero": {numero}, "codigo": {codigo}}
	if err := c.do(ctx, http.MethodGet, "/v1/tracking", q, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

type BatchItem struct {
	CustomID string `json:"custom_id,omitempty"`
	Numero   string `json:"numero"`
	Codigo   string `json:"codigo"`
}

type BatchResult struct {
	CustomID string    `json:"custom_id"`
	Numero   string    `json:"numero"`
	OK       bool      `json:"ok"`
	Tracking *Tracking `json:"tracking"`
	Error    *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// TrackBatch consulta hasta 50 guías en una llamada. Cada guía cuenta como una consulta del cupo de rastreo.
func (c *Client) TrackBatch(ctx context.Context, items []BatchItem) ([]BatchResult, error) {
	if len(items) == 0 {
		return nil, nil
	}
	if len(items) > 50 {
		return nil, errors.New("el lote admite como máximo 50 guías")
	}
	var out struct {
		Results []BatchResult `json:"results"`
	}
	if err := c.do(ctx, http.MethodPost, "/v1/tracking/batch", nil, map[string]any{"items": items}, &out); err != nil {
		return nil, err
	}
	return out.Results, nil
}

// ── Shalom Pro: sesión y tarifas ───────────────────────────────────────────

// Session token efímero (ssk_…, 2 horas). Solo sirve con la misma X-API-Key que lo emitió.
type Session struct {
	Token     string    `json:"session_token"`
	ExpiresAt time.Time `json:"expires_at"`
}

// CreateSession inicia sesión en Shalom Pro. La primera llamada de una cuenta puede tardar de 90 s a 2 min.
func (c *Client) CreateSession(ctx context.Context, email, password string) (*Session, error) {
	var out Session
	err := c.do(ctx, http.MethodPost, "/v1/shalom/sessions", nil, map[string]string{"email": email, "password": password}, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

type Dimensions struct {
	WeightKg float64 `json:"weight_kg"`
	HeightM  float64 `json:"height_m"`
	LengthM  float64 `json:"length_m"`
	WidthM   float64 `json:"width_m"`
}

type TariffRequest struct {
	OriginTerminalID  int         `json:"origin_terminal_id"`
	DestinyTerminalID int         `json:"destiny_terminal_id"`
	Dimensions        *Dimensions `json:"dimensions,omitempty"`
	Aereo             bool        `json:"aereo,omitempty"`
}

type TariffResult struct {
	Currency  string             `json:"currency"`
	Aereo     bool               `json:"aereo"`
	Breakdown map[string]float64 `json:"breakdown"`
}

// Tariff cotiza el envío entre dos agencias sin crear nada (no cobra ni genera guía).
func (c *Client) Tariff(ctx context.Context, sessionToken string, req TariffRequest) (*TariffResult, error) {
	var out TariffResult
	err := c.doH(ctx, http.MethodPost, "/v1/tariff/calculate", nil, req, &out, map[string]string{"X-Shalom-Session": sessionToken})
	if err != nil {
		return nil, err
	}
	return &out, nil
}
