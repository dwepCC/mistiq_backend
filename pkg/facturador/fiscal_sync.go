package facturador

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// FiscalCompanySyncPayload body para POST /api/v1/fiscal/company-sync
type FiscalCompanySyncPayload struct {
	RUC            string                 `json:"ruc"`
	TenantID       uint                   `json:"tenant_id,omitempty"`
	TenantSlug     string                 `json:"tenant_slug,omitempty"`
	SendMode       string                 `json:"send_mode"`
	Provider       string                 `json:"provider,omitempty"`
	ConnectionType string                 `json:"connection_type,omitempty"`
	Ambiente       string                 `json:"ambiente,omitempty"`
	SOLUser        string                 `json:"SOL_USER,omitempty"`
	SOLPass        string                 `json:"SOL_PASS,omitempty"`
	CertificateB64 string                 `json:"certificate_base64,omitempty"`
	CertPassword   string                 `json:"certificate_password,omitempty"`
	LogoB64        string                 `json:"logo_base64,omitempty"`
	PSEBaseURL     string                 `json:"pse_base_url,omitempty"`
	PSEUser        string                 `json:"pse_user,omitempty"`
	PSEPassword    string                 `json:"pse_password,omitempty"`
	PSEToken       string                 `json:"pse_token,omitempty"`
	PSESecondary   string                 `json:"pse_secondary_user,omitempty"`
	GreClientID    string                 `json:"gre_client_id,omitempty"`
	GreClientSecret string                `json:"gre_client_secret,omitempty"`
	PSEMetadata    map[string]interface{} `json:"pse_metadata_json,omitempty"`
	AutomaticSend  *bool                  `json:"automatic_send,omitempty"`
	EmailEnabled   *bool                  `json:"email_enabled,omitempty"`
	RetryEnabled   *bool                  `json:"retry_enabled,omitempty"`
	Enabled        *bool                  `json:"enabled,omitempty"`
	Sunat          map[string]interface{} `json:"sunat,omitempty"`
	PSE            map[string]interface{} `json:"pse,omitempty"`
}

// FiscalCompanyStatus respuesta GET /api/v1/empresas/{ruc}/status
type FiscalCompanyStatus struct {
	RUC                     string  `json:"ruc"`
	TenantID                int     `json:"tenant_id"`
	TenantSlug              string  `json:"tenant_slug"`
	SendMode                string  `json:"send_mode"`
	Provider                string  `json:"provider"`
	ConnectionType          string  `json:"connection_type"`
	Ambiente                string  `json:"ambiente"`
	ConnectionStatus        string  `json:"connection_status"`
	ConnectionError         *string `json:"connection_error"`
	LastConnectionCheck     *string `json:"last_connection_check"`
	PSEBaseURLConfigured    bool    `json:"pse_base_url_configured"`
	PSETokenConfigured      bool    `json:"pse_token_configured"`
	SOLConfigured           bool    `json:"sol_configured"`
	CertificateConfigured   bool    `json:"certificate_configured"`
	GreClientConfigured     bool    `json:"gre_client_configured"`
	GreClientID             string  `json:"gre_client_id,omitempty"`
	Enabled                 bool    `json:"enabled"`
}

// CompanySync sincroniza configuración fiscal completa (SSOT facturador).
func (c *Client) CompanySync(payload FiscalCompanySyncPayload) (*FiscalCompanyStatus, error) {
	if payload.RUC == "" {
		return nil, fmt.Errorf("ruc es obligatorio")
	}
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest("POST", c.addToken("/fiscal/company-sync"), bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("company-sync: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		var errBody struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(raw, &errBody)
		if errBody.Error == "" {
			errBody.Error = string(raw)
		}
		return nil, fmt.Errorf("facturador company-sync %d: %s", resp.StatusCode, errBody.Error)
	}
	var out struct {
		OK     bool                `json:"ok"`
		Status FiscalCompanyStatus `json:"status"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("respuesta company-sync: %w", err)
	}
	return &out.Status, nil
}

// TestFiscalConnection POST /api/v1/fiscal/test-connection
func (c *Client) TestFiscalConnection(ruc string) (*FiscalCompanyStatus, error) {
	if ruc == "" {
		return nil, fmt.Errorf("ruc requerido")
	}
	body, _ := json.Marshal(map[string]string{"ruc": ruc})
	req, err := http.NewRequest("POST", c.addToken("/fiscal/test-connection"), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("test-connection %d: %s", resp.StatusCode, string(raw))
	}
	var status FiscalCompanyStatus
	if err := json.Unmarshal(raw, &status); err != nil {
		return nil, err
	}
	return &status, nil
}

// GetEmpresaFiscalStatus GET /api/v1/empresas/{ruc}/status
func (c *Client) GetEmpresaFiscalStatus(ruc string) (*FiscalCompanyStatus, error) {
	if ruc == "" {
		return nil, fmt.Errorf("ruc requerido")
	}
	req, err := http.NewRequest("GET", c.addToken("/empresas/"+ruc+"/status"), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("empresa status %d: %s", resp.StatusCode, string(raw))
	}
	var status FiscalCompanyStatus
	if err := json.Unmarshal(raw, &status); err != nil {
		return nil, err
	}
	return &status, nil
}

