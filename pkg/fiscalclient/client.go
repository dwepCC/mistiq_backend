// Package fiscalclient envía documentos fiscales al facturador (SSOT).
package fiscalclient

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// EmitRequest payload mínimo para POST /api/v1/fiscal/emit.
// El facturador resuelve modo, proveedor y credenciales desde empresa (SSOT).
type EmitRequest struct {
	TenantID       uint                   `json:"tenant_id"`
	TenantSlug     string                 `json:"tenant_slug"`
	SaleID         uint                   `json:"sale_id"`
	RUC            string                 `json:"ruc"`
	Document       map[string]interface{} `json:"document"`
	IdempotencyKey string                 `json:"idempotency_key,omitempty"`
	// Reissue autoriza al facturador a sobrescribir el snapshot de un documento
	// ya aceptado y volver a generarlo. Solo lo activa la corrección de soporte
	// (fecha de emisión nueva sobre un comprobante emitido contra beta).
	Reissue bool `json:"reissue,omitempty"`
}

// EmitResponse respuesta 202 del facturador.
type EmitResponse struct {
	DocumentUUID string `json:"document_uuid"`
	Status       string `json:"status"`
	QueuedAt     string `json:"queued_at,omitempty"`
	Error        string `json:"error,omitempty"`
}

// Client HTTP al facturador fiscal async.
type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

var shared *Client

// Init configura cliente global.
func Init(baseURL, token string) {
	baseURL = strings.TrimSuffix(strings.TrimSpace(baseURL), "/")
	shared = &Client{
		BaseURL: baseURL,
		Token:   token,
		HTTP:    &http.Client{Timeout: 8 * time.Second},
	}
}

// Enabled indica si el cliente fiscal está configurado.
func Enabled() bool {
	return shared != nil && shared.BaseURL != "" && shared.Token != ""
}

func addToken(base, path, token string) string {
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	return base + path + sep + "token=" + url.QueryEscape(token)
}

// Emit encola documento fiscal (no espera SUNAT).
func Emit(req *EmitRequest) (*EmitResponse, error) {
	if !Enabled() {
		return nil, fmt.Errorf("fiscalclient no configurado")
	}
	if req == nil || req.TenantID == 0 || req.TenantSlug == "" || req.SaleID == 0 {
		return nil, fmt.Errorf("tenant_id, tenant_slug y sale_id requeridos")
	}
	if strings.TrimSpace(req.RUC) == "" {
		return nil, fmt.Errorf("ruc requerido")
	}
	if req.Document == nil {
		return nil, fmt.Errorf("document requerido")
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	endpoint := addToken(shared.BaseURL, "/api/v1/fiscal/emit", shared.Token)
	httpReq, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	if req.IdempotencyKey != "" {
		httpReq.Header.Set("X-Idempotency-Key", req.IdempotencyKey)
	}

	resp, err := shared.HTTP.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("fiscal emit: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)

	var out EmitResponse
	_ = json.Unmarshal(raw, &out)
	if resp.StatusCode >= 400 {
		if out.Error == "" {
			out.Error = string(raw)
		}
		return &out, fmt.Errorf("fiscal emit HTTP %d: %s", resp.StatusCode, out.Error)
	}
	return &out, nil
}
