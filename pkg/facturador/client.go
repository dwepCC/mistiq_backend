// Package facturador implementa el cliente HTTP para facturador_lycet (API fiscal SUNAT/PSE).
// Ver API-facturacion.md para la documentación del API.
package facturador

import (
	"bytes"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

)

// Client es el cliente para el API del facturador Lycet.
type Client struct {
	BaseURL string // ej. https://tu-dominio.com/api/v1
	Token   string
	HTTP    *http.Client
}

// NewClient crea un cliente con timeout por defecto.
func NewClient(baseURL, token string) *Client {
	baseURL = strings.TrimSuffix(baseURL, "/")
	if baseURL != "" && !strings.HasSuffix(baseURL, "/api/v1") {
		baseURL = baseURL + "/api/v1"
	}
	return &Client{
		BaseURL: baseURL,
		Token:   token,
		HTTP:    &http.Client{Timeout: 45 * time.Second},
	}
}

// addToken añade el token a la URL (query ?token=...).
func (c *Client) addToken(path string) string {
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	return c.BaseURL + path + sep + "token=" + url.QueryEscape(c.Token)
}

// ListEmpresas obtiene todas las empresas registradas en Lycet (GET /api/v1/empresas).
// La respuesta puede ser un mapa RUC -> datos o un array; se normaliza a mapa por RUC.
func (c *Client) ListEmpresas() (map[string]EmpresaEntry, error) {
	req, err := http.NewRequest("GET", c.addToken("/empresas"), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("facturador rechazó el token (403): verifica FACTURADOR_TOKEN")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("facturador respondió %d: %s", resp.StatusCode, string(body))
	}
	// Aceptar tanto mapa como array de empresas (según implementación del facturador).
	var empresas map[string]EmpresaEntry
	if err := json.Unmarshal(body, &empresas); err != nil {
		var list []struct {
			RUC      string `json:"ruc"`
			Ambiente string `json:"ambiente"`
		}
		if e2 := json.Unmarshal(body, &list); e2 == nil {
			empresas = make(map[string]EmpresaEntry)
			for _, e := range list {
				if e.RUC != "" {
					empresas[e.RUC] = EmpresaEntry{Ambiente: e.Ambiente}
				}
			}
			return empresas, nil
		}
		// Array de objetos con estructura EmpresaEntry
		var listFull []struct {
			RUC      string `json:"ruc"`
			Ambiente string `json:"ambiente"`
			SOLUser  string `json:"SOL_USER"`
		}
		if e3 := json.Unmarshal(body, &listFull); e3 == nil {
			empresas = make(map[string]EmpresaEntry)
			for _, e := range listFull {
				if e.RUC != "" {
					empresas[e.RUC] = EmpresaEntry{Ambiente: e.Ambiente, SOLUser: e.SOLUser}
				}
			}
			return empresas, nil
		}
		return nil, fmt.Errorf("respuesta empresas: %w", err)
	}
	if empresas == nil {
		empresas = make(map[string]EmpresaEntry)
	}
	return empresas, nil
}

// GetEmpresa obtiene una empresa por RUC (GET /api/v1/empresas/{ruc}). 404 si no está registrada.
func (c *Client) GetEmpresa(ruc string) (*EmpresaEntry, error) {
	if ruc == "" {
		return nil, fmt.Errorf("ruc requerido")
	}
	path := "/empresas/" + url.PathEscape(ruc)
	req, err := http.NewRequest("GET", c.addToken(path), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil // no registrada en Lycet
	}
	if resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Errorf("facturador rechazó el token (403): verifica FACTURADOR_TOKEN")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("facturador respondió %d: %s", resp.StatusCode, string(body))
	}
	var entry EmpresaEntry
	if err := json.Unmarshal(body, &entry); err != nil {
		return nil, fmt.Errorf("respuesta empresa: %w", err)
	}
	return &entry, nil
}
type EmpresaEntry struct {
	SOLUser        string `json:"SOL_USER"`
	SOLPass        string `json:"SOL_PASS"`
	Certificate    string `json:"certificate"`
	Logo           string `json:"logo,omitempty"`
	Ambiente       string `json:"ambiente,omitempty"` // "pruebas" | "produccion"
	FEURL          string `json:"FE_URL,omitempty"`
	REURL          string `json:"RE_URL,omitempty"`
	GuiaURL        string `json:"GUIA_URL,omitempty"`
	TenantID       int    `json:"tenant_id,omitempty"`
	TenantSlug     string `json:"tenant_slug,omitempty"`
	SendMode         string `json:"send_mode,omitempty"`
	Provider         string `json:"provider,omitempty"`
	PSEUser        string `json:"pse_user,omitempty"`
	ConnectionType string `json:"connection_type,omitempty"`
		ConnectionStatus string `json:"connection_status,omitempty"`
		PSEBaseURL       string `json:"pse_base_url,omitempty"`
	AutomaticSend  bool   `json:"automatic_send,omitempty"`
	EmailEnabled   bool   `json:"email_enabled,omitempty"`
	RetryEnabled   bool   `json:"retry_enabled,omitempty"`
	Enabled        bool   `json:"enabled,omitempty"`
}

// ConnectionType devuelve "PSE" o "SUNAT" según configuración en facturador.
func ConnectionType(entry EmpresaEntry) string {
	sm := strings.ToLower(strings.TrimSpace(entry.SendMode))
	pr := strings.ToLower(strings.TrimSpace(entry.Provider))
	if sm == "pse" || strings.Contains(sm, "pse") ||
		strings.Contains(pr, "pse") || strings.Contains(pr, "validapse") {
		return "PSE"
	}
	return "SUNAT"
}

// PatchAmbiente cambia solo el ambiente de la empresa en el facturador (PATCH /api/v1/empresas/{ruc}/ambiente).
// ambiente debe ser "pruebas" o "produccion". No modifica Clave SOL, certificado ni logo.
func (c *Client) PatchAmbiente(ruc, ambiente string) error {
	if ruc == "" {
		return fmt.Errorf("ruc es obligatorio")
	}
	if ambiente != "pruebas" && ambiente != "produccion" {
		return fmt.Errorf("ambiente debe ser pruebas o produccion")
	}
	body := map[string]string{"ambiente": ambiente}
	bodyBytes, _ := json.Marshal(body)
	path := "/empresas/" + url.PathEscape(ruc) + "/ambiente"
	req, err := http.NewRequest("PATCH", c.addToken(path), bytes.NewReader(bodyBytes))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("el RUC %s no está registrado en el facturador", ruc)
	}
	if resp.StatusCode == http.StatusForbidden {
		return fmt.Errorf("facturador rechazó el token (403): verifica FACTURADOR_TOKEN")
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("facturador respondió %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

// InvoicePayload es el body para POST /invoice/send (facturador). Estructura según PAYLOAD-FACTURA-BOLETA.md.
// Campos obligatorios: tipoOperacion (Cat.51), tipoDoc (Cat.01), serie, correlativo, fechaEmision,
// company (ruc, razonSocial, nombreComercial, address), client (tipoDoc Cat.06, numDoc, rznSocial, address),
// tipoMoneda, formaPago, details, legends, totales (mtoOperGravadas, mtoIGV, totalImpuestos, valorVenta, subTotal, mtoImpVenta).
type InvoicePayload struct {
	UBLVersion      string             `json:"ublVersion"`
	TipoOperacion   string             `json:"tipoOperacion"`   // Catálogo 51 SUNAT: 0101 Venta interna (Factura/Boleta), 0200 Exportación, etc.
	TipoDoc         string             `json:"tipoDoc"`         // 01 Factura, 03 Boleta
	Serie           string             `json:"serie"`
	Correlativo     string             `json:"correlativo"`
	FechaEmision    string             `json:"fechaEmision"`
	FecVencimiento  string             `json:"fecVencimiento,omitempty"` // Solo factura (01). ISO 8601 con zona (Y-m-d\TH:i:sP).
	FormaPago       *InvoiceFormaPago  `json:"formaPago,omitempty"`
	Company         InvoiceCompany     `json:"company"`
	Client          InvoiceClient      `json:"client"`
	TipoMoneda      string             `json:"tipoMoneda"`
	MtoOperGravadas float64            `json:"mtoOperGravadas"`
	MtoOperExoneradas float64         `json:"mtoOperExoneradas,omitempty"` // Total operaciones exoneradas (Cat.07 = 20). Obligatorio si hay líneas exoneradas.
	MtoOperInafectas float64           `json:"mtoOperInafectas,omitempty"`  // Total operaciones inafectas (Cat.07 = 30). Obligatorio si hay líneas inafectas.
	MtoIGV          float64            `json:"mtoIGV"`
	TotalImpuestos  float64            `json:"totalImpuestos"`
	ValorVenta      float64            `json:"valorVenta"`
	SubTotal        float64            `json:"subTotal"`
	MtoImpVenta     float64            `json:"mtoImpVenta"`
	Details         []InvoiceDetail    `json:"details"`
	Observacion     string             `json:"observacion,omitempty"` // Leyenda en letras sin languageLocaleID (ver SetSUNATLegendViaObservacion)
	Legends         []InvoiceLegend    `json:"legends,omitempty"`
	Compra          string             `json:"compra,omitempty"` // Orden de compra (O/C)
	Guias           []InvoiceRelatedDoc `json:"guias,omitempty"` // Guías relacionadas (tipoDoc + nroDoc)
	Detraccion      *InvoiceDetraction  `json:"detraccion,omitempty"`
	Parameters      *InvoicePDFParameters `json:"parameters,omitempty"` // Solo PDF Lycet; no afecta XML SUNAT.
}

// InvoiceDetraction bloque detracción SUNAT (cat. 54, 59, cuenta BN).
type InvoiceDetraction struct {
	Percent           float64 `json:"percent"`
	Mount             float64 `json:"mount"`
	CtaBanco          string  `json:"ctaBanco"`
	CodMedioPago      string  `json:"codMedioPago"`
	CodBienDetraccion string  `json:"codBienDetraccion"`
}

// InvoiceRelatedDoc documento relacionado en factura/boleta (guías, etc.).
type InvoiceRelatedDoc struct {
	TipoDoc string `json:"tipoDoc"`
	NroDoc  string `json:"nroDoc"`
}

// InvoiceFormaPago según doc: al menos "tipo" (ej. "Contado").
type InvoiceFormaPago struct {
	Tipo string `json:"tipo"`
}

type InvoiceCompany struct {
	RUC            string           `json:"ruc"`
	RazonSocial    string           `json:"razonSocial"`
	NombreComercial string          `json:"nombreComercial"`
	Address        InvoiceAddress   `json:"address"`
}

type InvoiceAddress struct {
	Ubigueo      string `json:"ubigueo"`
	CodigoPais   string `json:"codigoPais"`
	Departamento string `json:"departamento,omitempty"`
	Provincia    string `json:"provincia,omitempty"`
	Distrito     string `json:"distrito,omitempty"`
	Urbanizacion string `json:"urbanizacion,omitempty"`
	Direccion    string `json:"direccion"`
}

type InvoiceClient struct {
	TipoDoc  string         `json:"tipoDoc"`
	NumDoc   string         `json:"numDoc"`
	RznSocial string        `json:"rznSocial"`
	Address  InvoiceAddress `json:"address"`
}

type InvoiceDetail struct {
	Unidad          string  `json:"unidad"`
	Cantidad        float64 `json:"cantidad"`
	CodProducto     string  `json:"codProducto"`
	Descripcion     string  `json:"descripcion"`
	MtoValorUnitario float64 `json:"mtoValorUnitario"`
	MtoValorVenta   float64 `json:"mtoValorVenta"`
	TipAfeIgv       string  `json:"tipAfeIgv"`
	MtoBaseIgv      float64 `json:"mtoBaseIgv"`
	PorcentajeIgv   float64 `json:"porcentajeIgv"`
	Igv             float64 `json:"igv"`
	TotalImpuestos  float64 `json:"totalImpuestos"`
	MtoPrecioUnitario float64 `json:"mtoPrecioUnitario"`
}

type InvoiceLegend struct {
	Code  string `json:"code"`
	Value string `json:"value"`
}

// NoteRelDoc otros documentos relacionados (relDocs → AdditionalDocumentReference, catálogo SUNAT 12).
// No usar para la factura/boleta anulada: esa va en tipDocAfectado + numDocfectado (BillingReference, cat. 01).
type NoteRelDoc struct {
	TipoDoc string `json:"tipoDoc"`
	NroDoc  string `json:"nroDoc"`
}

// NotePayload es el body para POST /note/send (Lycet). Nota de crédito (07) o débito (08).
// Según PAYLOAD-NOTA-CREDITO-DEBITO.md: mismo esquema que factura/boleta más relDocs, codMotivo, desMotivo.
type NotePayload struct {
	UBLVersion      string             `json:"ublVersion"`
	TipoDoc         string             `json:"tipoDoc"`         // "07" Nota de crédito, "08" Nota de débito
	Serie           string             `json:"serie"`
	Correlativo     string             `json:"correlativo"`
	FechaEmision    string             `json:"fechaEmision"`
	FormaPago       *InvoiceFormaPago  `json:"formaPago,omitempty"`
	Company         InvoiceCompany     `json:"company"`
	Client          InvoiceClient     `json:"client"`
	TipoMoneda      string             `json:"tipoMoneda"`
	CodMotivo       string             `json:"codMotivo"`       // Catálogo SUNAT ej. "01" Anulación de la operación
	DesMotivo       string             `json:"desMotivo"`       // Descripción del motivo
	// Greenter/Lycet: BillingReference/InvoiceDocumentReference (obligatorio en XML SUNAT).
	TipDocAfectado string `json:"tipDocAfectado,omitempty"` // "01" factura, "03" boleta afectada
	NumDocfectado  string `json:"numDocfectado,omitempty"`  // serie-número afectado (typo histórico Greenter)
	RelDocs         []NoteRelDoc       `json:"relDocs,omitempty"` // solo otros docs (cat. 12); no duplicar el afectado
	MtoOperGravadas float64            `json:"mtoOperGravadas"`
	MtoOperExoneradas float64          `json:"mtoOperExoneradas,omitempty"`
	MtoOperInafectas float64           `json:"mtoOperInafectas,omitempty"`
	MtoIGV          float64            `json:"mtoIGV"`
	TotalImpuestos  float64            `json:"totalImpuestos"`
	ValorVenta      float64            `json:"valorVenta"`
	SubTotal        float64            `json:"subTotal"`
	MtoImpVenta     float64            `json:"mtoImpVenta"`
	Details         []InvoiceDetail    `json:"details"`
	Observacion     string             `json:"observacion,omitempty"`
	Legends         []InvoiceLegend    `json:"legends,omitempty"`
}

// SendNote envía la nota de crédito/débito al facturador (POST /note/send). Misma respuesta que invoice.
func (c *Client) SendNote(payload *NotePayload) (*SunatResponse, error) {
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("payload: %w", err)
	}
	req, err := http.NewRequest("POST", c.addToken("/note/send"), bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	var out SunatResponse
	if err := json.Unmarshal(respBody, &out); err != nil {
		if len(respBody) > 0 && respBody[0] == '<' {
			return nil, fmt.Errorf("el facturador respondió con un error (posiblemente HTML). Código HTTP: %d", resp.StatusCode)
		}
		return nil, fmt.Errorf("respuesta inválida del facturador: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return &out, fmt.Errorf("facturador respondió %d: %s", resp.StatusCode, string(respBody))
	}
	return &out, nil
}

// GetNotePDF obtiene el PDF de la nota (POST /note/pdf).
func (c *Client) GetNotePDF(payload *NotePayload) ([]byte, error) {
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("payload: %w", err)
	}
	req, err := http.NewRequest("POST", c.addToken("/note/pdf"), bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("facturador note PDF respondió %d: %s", resp.StatusCode, string(b))
	}
	return io.ReadAll(resp.Body)
}

// GetNoteXML obtiene el XML firmado de la nota sin enviar a SUNAT (POST /note/xml).
func (c *Client) GetNoteXML(payload *NotePayload) ([]byte, error) {
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("payload: %w", err)
	}
	req, err := http.NewRequest("POST", c.addToken("/note/xml"), bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("facturador note XML respondió %d: %s", resp.StatusCode, string(b))
	}
	return io.ReadAll(resp.Body)
}

// SunatResponse es la respuesta del facturador Lycet para POST /invoice/send. Estructura según RESPUESTA-SUNAT-BACKEND.md.
// HTTP 200 siempre que la petición sea válida; éxito/rechazo/error de conexión van en el cuerpo.
type SunatResponse struct {
	XML           string              `json:"xml"`
	Hash          string              `json:"hash"`
	SunatResponse *SunatResponseInner `json:"sunatResponse"`
}

// SunatResponseInner (BillResult): success, error, cdrZip, cdrResponse.
type SunatResponseInner struct {
	Success    bool   `json:"success"`
	CDRZip     string `json:"cdrZip"`
	Ticket     string `json:"ticket"`
	CDRResponse *SunatCDRResponse `json:"cdrResponse"`
	Error      *SunatError        `json:"error"`
}

// SunatCDRResponse: cuando SUNAT respondió (aceptado o rechazado). code "0" = aceptado.
type SunatCDRResponse struct {
	Accepted    bool     `json:"accepted"`
	ID          string   `json:"id"`
	Code        string   `json:"code"`
	Description string   `json:"description"`
	Notes       []string `json:"notes"`
}

// SunatError: cuando success=false por error de conexión/timeout (sin CDR).
type SunatError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// SendInvoice envía el comprobante al facturador y retorna la respuesta.
func (c *Client) SendInvoice(payload *InvoicePayload) (*SunatResponse, error) {
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("payload: %w", err)
	}
	req, err := http.NewRequest("POST", c.addToken("/invoice/send"), bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	var out SunatResponse
	if err := json.Unmarshal(respBody, &out); err != nil {
		// Si la respuesta es HTML (p. ej. error 500 del facturador), el usuario ve "invalid character '<'"
		if len(respBody) > 0 && respBody[0] == '<' {
			return nil, fmt.Errorf("el facturador respondió con un error (posiblemente HTML). Revisa el log del facturador: suele ser certificado o clave privada inválidos para el RUC (openssl_sign). Código HTTP: %d", resp.StatusCode)
		}
		return nil, fmt.Errorf("respuesta inválida del facturador: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return &out, fmt.Errorf("facturador respondió %d: %s", resp.StatusCode, string(respBody))
	}
	return &out, nil
}

// InvoicePDFExtra fila de información adicional en el PDF Lycet (parameters.user.extras).
type InvoicePDFExtra struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// InvoicePDFUserParameters bloque user dentro de parameters (POST /invoice/pdf).
type InvoicePDFUserParameters struct {
	Extras []InvoicePDFExtra `json:"extras,omitempty"`
}

// InvoicePDFParameters parámetros de representación impresa; Lycet los ignora en /send y /xml.
type InvoicePDFParameters struct {
	User InvoicePDFUserParameters `json:"user"`
}

// InvoicePDFOptions parámetros opcionales adicionales para POST /invoice/pdf.
type InvoicePDFOptions struct {
	Extras []InvoicePDFExtra `json:"extras,omitempty"`
}

// GetInvoicePDF obtiene el PDF del comprobante sin enviar a SUNAT (POST /invoice/pdf).
// Útil para guardar el PDF tras un send exitoso.
func (c *Client) GetInvoicePDF(payload *InvoicePayload, opts *InvoicePDFOptions) ([]byte, error) {
	bodyBytes, err := marshalInvoicePDFBody(payload, opts)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest("POST", c.addToken("/invoice/pdf"), bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("facturador PDF respondió %d: %s", resp.StatusCode, string(b))
	}
	return io.ReadAll(resp.Body)
}

func marshalInvoicePDFBody(payload *InvoicePayload, opts *InvoicePDFOptions) ([]byte, error) {
	if payload == nil {
		return nil, fmt.Errorf("payload: nil")
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("payload: %w", err)
	}
	extras := collectInvoicePDFExtras(payload, opts)
	if len(extras) == 0 {
		return raw, nil
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		return nil, fmt.Errorf("payload map: %w", err)
	}
	body["parameters"] = map[string]any{
		"user": map[string]any{
			"extras": extras,
		},
	}
	return json.Marshal(body)
}

func collectInvoicePDFExtras(payload *InvoicePayload, opts *InvoicePDFOptions) []map[string]string {
	seen := make(map[string]struct{})
	var out []map[string]string
	appendExtra := func(name, value string) {
		name = strings.TrimSpace(name)
		value = strings.TrimSpace(value)
		if name == "" || value == "" {
			return
		}
		if _, ok := seen[name]; ok {
			return
		}
		seen[name] = struct{}{}
		out = append(out, map[string]string{"name": name, "value": value})
	}
	if payload != nil && payload.Parameters != nil {
		for _, e := range payload.Parameters.User.Extras {
			appendExtra(e.Name, e.Value)
		}
	}
	if opts != nil {
		for _, e := range opts.Extras {
			appendExtra(e.Name, e.Value)
		}
	}
	return out
}

// GetInvoiceXML obtiene el XML firmado del comprobante sin enviarlo a SUNAT (POST /invoice/xml).
// Según RESPUESTA-SUNAT-BACKEND.md: mismo body que /send, respuesta es archivo XML (binario).
func (c *Client) GetInvoiceXML(payload *InvoicePayload) ([]byte, error) {
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("payload: %w", err)
	}
	req, err := http.NewRequest("POST", c.addToken("/invoice/xml"), bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("facturador XML respondió %d: %s", resp.StatusCode, string(b))
	}
	return io.ReadAll(resp.Body)
}

// Success retorna si la respuesta indica aceptación por SUNAT (sunatResponse.success === true y cdrResponse.code === "0").
func (r *SunatResponse) Success() bool {
	return r.SunatResponse != nil && r.SunatResponse.Success
}

// Message retorna el mensaje para guardar en BD: descripción del CDR o mensaje de error de conexión (según RESPUESTA-SUNAT-BACKEND.md).
func (r *SunatResponse) Message() string {
	if r.SunatResponse == nil {
		return ""
	}
	if r.SunatResponse.CDRResponse != nil {
		return r.SunatResponse.CDRResponse.Description
	}
	if r.SunatResponse.Error != nil {
		return r.SunatResponse.Error.Message
	}
	return ""
}

// CDRCode retorna el código de estado SUNAT ("0" = aceptado, "3205" etc. = rechazo). Vacío si no hay cdrResponse.
func (r *SunatResponse) CDRCode() string {
	if r.SunatResponse != nil && r.SunatResponse.CDRResponse != nil {
		return r.SunatResponse.CDRResponse.Code
	}
	return ""
}

// CDRNotes retorna los mensajes de detalle del CDR (notes). Nil si no hay cdrResponse.
func (r *SunatResponse) CDRNotes() []string {
	if r.SunatResponse != nil && r.SunatResponse.CDRResponse != nil {
		return r.SunatResponse.CDRResponse.Notes
	}
	return nil
}

// ConnectionError retorna el mensaje cuando falló la conexión con SUNAT (sunatResponse.error). Vacío si no aplica.
func (r *SunatResponse) ConnectionError() string {
	if r.SunatResponse != nil && r.SunatResponse.Error != nil {
		return r.SunatResponse.Error.Message
	}
	return ""
}

// CDRZipBase64 retorna el CDR en base64 si viene en la respuesta.
func (r *SunatResponse) CDRZipBase64() string {
	if r.SunatResponse != nil {
		return r.SunatResponse.CDRZip
	}
	return ""
}

// Ticket retorna el ticket cuando SUNAT devuelve ticket (resumen, comunicación de baja). Vacío si no aplica.
func (r *SunatResponse) Ticket() string {
	if r.SunatResponse != nil {
		return r.SunatResponse.Ticket
	}
	return ""
}

// --- Comunicación de baja (voided) - PAYLOAD-VOIDED-RESUMEN.md ---

// VoidedDetail es un comprobante a dar de baja.
type VoidedDetail struct {
	TipoDoc        string `json:"tipoDoc"`        // 01 Factura, 03 Boleta, 07 NC, 08 ND
	Serie         string `json:"serie"`
	Correlativo   string `json:"correlativo"`
	DesMotivoBaja string `json:"desMotivoBaja"`
}

// VoidedPayload es el body para POST /voided/send.
type VoidedPayload struct {
	Company         InvoiceCompany  `json:"company"`
	Correlativo     string          `json:"correlativo"`
	FecGeneracion   string          `json:"fecGeneracion"`   // ISO 8601
	FecComunicacion string          `json:"fecComunicacion"` // ISO 8601
	Details         []VoidedDetail  `json:"details"`
}

// SendVoided envía la comunicación de baja (POST /voided/send). Puede devolver ticket o CDR en la respuesta.
func (c *Client) SendVoided(payload *VoidedPayload) (*SunatResponse, error) {
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("payload: %w", err)
	}
	req, err := http.NewRequest("POST", c.addToken("/voided/send"), bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	var out SunatResponse
	if err := json.Unmarshal(respBody, &out); err != nil {
		return nil, fmt.Errorf("respuesta inválida: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return &out, fmt.Errorf("facturador respondió %d: %s", resp.StatusCode, string(respBody))
	}
	return &out, nil
}

// GetVoidedStatus consulta el estado del ticket (GET /voided/status). Según CONSULTA y PAYLOAD-VOIDED-RESUMEN.
func (c *Client) GetVoidedStatus(ticket, ruc string) (*StatusResult, error) {
	path := "/voided/status?ticket=" + url.QueryEscape(ticket)
	if ruc != "" {
		path += "&ruc=" + url.QueryEscape(ruc)
	}
	req, err := http.NewRequest("GET", c.addToken(path), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var out StatusResult
	_ = json.Unmarshal(body, &out)
	if resp.StatusCode != http.StatusOK {
		return &out, fmt.Errorf("facturador respondió %d: %s", resp.StatusCode, string(body))
	}
	return &out, nil
}

// --- Resumen diario (summary) - PAYLOAD-VOIDED-RESUMEN.md ---

// SummaryDetail es una línea del resumen diario (un comprobante).
type SummaryDetail struct {
	TipoDoc        string  `json:"tipoDoc"`        // 01, 03, 07, 08
	SerieNro       string  `json:"serieNro"`       // ej. B001-1
	ClienteTipo    string  `json:"clienteTipo"`   // 1 DNI, 6 RUC
	ClienteNro     string  `json:"clienteNro"`
	Total          float64 `json:"total"`
	MtoOperGravadas float64 `json:"mtoOperGravadas"`
	MtoIGV         float64 `json:"mtoIGV"`
}

// SummaryPayload es el body para POST /summary/send.
type SummaryPayload struct {
	Company       InvoiceCompany   `json:"company"`
	Correlativo   string           `json:"correlativo"`
	FecGeneracion string           `json:"fecGeneracion"` // ISO 8601
	FecResumen    string           `json:"fecResumen"`    // Fecha del día reportado (ISO 8601)
	Moneda        string           `json:"moneda"`        // PEN
	Details       []SummaryDetail  `json:"details"`
}

// SendSummary envía el resumen diario (POST /summary/send). SUNAT devuelve ticket; consultar con GetSummaryStatus.
func (c *Client) SendSummary(payload *SummaryPayload) (*SunatResponse, error) {
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("payload: %w", err)
	}
	req, err := http.NewRequest("POST", c.addToken("/summary/send"), bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	var out SunatResponse
	if err := json.Unmarshal(respBody, &out); err != nil {
		return nil, fmt.Errorf("respuesta inválida: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return &out, fmt.Errorf("facturador respondió %d: %s", resp.StatusCode, string(respBody))
	}
	return &out, nil
}

// GetSummaryStatus consulta el estado del ticket del resumen (GET /summary/status). Según PAYLOAD-VOIDED-RESUMEN.
func (c *Client) GetSummaryStatus(ticket, ruc string) (*StatusResult, error) {
	path := "/summary/status?ticket=" + url.QueryEscape(ticket)
	if ruc != "" {
		path += "&ruc=" + url.QueryEscape(ruc)
	}
	req, err := http.NewRequest("GET", c.addToken(path), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var out StatusResult
	_ = json.Unmarshal(body, &out)
	if resp.StatusCode != http.StatusOK {
		return &out, fmt.Errorf("facturador respondió %d: %s", resp.StatusCode, string(body))
	}
	return &out, nil
}

// StatusResult es la respuesta de voided/status, summary/status e invoice/status (consulta CDR).
type StatusResult struct {
	Success     bool                `json:"success"`
	Error       *SunatError         `json:"error"`
	Code        string              `json:"code"`
	CDRZip      string              `json:"cdrZip"`
	CDRResponse *SunatCDRResponse   `json:"cdrResponse"`
}

// GetInvoiceStatus consulta en SUNAT el estado/CDR de un comprobante (GET /invoice/status). Según CONSULTA-COMPROBANTE-CDR.
func (c *Client) GetInvoiceStatus(tipo, serie, numero, ruc string) (*StatusResult, error) {
	path := "/invoice/status?tipo=" + url.QueryEscape(tipo) + "&serie=" + url.QueryEscape(serie) + "&numero=" + url.QueryEscape(numero)
	if ruc != "" {
		path += "&ruc=" + url.QueryEscape(ruc)
	}
	req, err := http.NewRequest("GET", c.addToken(path), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var out StatusResult
	_ = json.Unmarshal(body, &out)
	if resp.StatusCode != http.StatusOK {
		return &out, fmt.Errorf("facturador respondió %d: %s", resp.StatusCode, string(body))
	}
	return &out, nil
}

// --- Guía de remisión (Despatch) - PAYLOAD-DESPATCH-RETENTION-PERCEPTION-REVERSION.md ---

// DespatchDirection lugar de partida o llegada.
type DespatchDirection struct {
	Ubigueo      string `json:"ubigueo"`
	CodigoPais   string `json:"codigoPais"`
	Departamento string `json:"departamento,omitempty"`
	Provincia    string `json:"provincia,omitempty"`
	Distrito     string `json:"distrito,omitempty"`
	Direccion    string `json:"direccion"`
}

// DespatchTransportist datos del transportista.
type DespatchTransportist struct {
	TipoDoc       string `json:"tipoDoc"`
	NumDoc        string `json:"numDoc"`
	RznSocial     string `json:"rznSocial"`
	NroMtc        string `json:"nroMtc,omitempty"`
	Placa         string `json:"placa"`
	ChoferTipoDoc string `json:"choferTipoDoc"`
	ChoferDoc     string `json:"choferDoc"`
}

// DespatchShipment datos del traslado (Greenter Shipment / GRE 2022).
type DespatchShipment struct {
	CodTraslado             string                `json:"codTraslado"`
	DesTraslado             string                `json:"desTraslado"`
	ModTraslado             string                `json:"modTraslado"`
	FecTraslado             string                `json:"fecTraslado"`
	FecEntregaBienes        string                `json:"fecEntregaBienes,omitempty"`
	FecEntregaTransportista string                `json:"fecEntregaTransportista,omitempty"`
	Partida                 DespatchDirection     `json:"partida"`
	Llegada                 DespatchDirection     `json:"llegada"`
	PesoTotal               float64               `json:"pesoTotal"`
	UndPesoTotal            string                `json:"undPesoTotal"`
	NumBultos               int                   `json:"numBultos"`
	Indicadores             []string              `json:"indicadores,omitempty"`
	Transportista           *DespatchTransportist `json:"transportista,omitempty"`
	Vehiculo                *DespatchVehicle      `json:"vehiculo,omitempty"`
	Choferes                []DespatchDriver      `json:"choferes,omitempty"`
}

// DespatchVehicle vehículo principal GRE (Greenter Vehicle).
type DespatchVehicle struct {
	Placa           string `json:"placa"`
	NroCirculacion  string `json:"nroCirculacion,omitempty"`
	NroAutorizacion string `json:"nroAutorizacion,omitempty"`
	CodEmisor       string `json:"codEmisor,omitempty"`
}

// DespatchDriver conductor GRE.
type DespatchDriver struct {
	Tipo      string `json:"tipo,omitempty"`
	TipoDoc   string `json:"tipoDoc"`
	NroDoc    string `json:"nroDoc"`
	Nombres   string `json:"nombres,omitempty"`
	Apellidos string `json:"apellidos,omitempty"`
	Licencia  string `json:"licencia,omitempty"`
}

// DespatchDetail ítem de la guía.
type DespatchDetail struct {
	Codigo        string  `json:"codigo"`
	Descripcion   string  `json:"descripcion"`
	Unidad        string  `json:"unidad"`
	Cantidad      float64 `json:"cantidad"`
	CodProdSunat  string  `json:"codProdSunat,omitempty"`
}

// DespatchAdditionalDoc documento relacionado con la guía (catálogo 61).
type DespatchAdditionalDoc struct {
	Tipo     string `json:"tipo,omitempty"`
	TipoDesc string `json:"tipoDesc,omitempty"`
	Nro      string `json:"nro"`
	Emisor   string `json:"emisor,omitempty"`
}

// DespatchPayload body para POST /despatch/send.
type DespatchPayload struct {
	Version      string                  `json:"version"`
	TipoDoc      string                  `json:"tipoDoc"`
	Serie        string                  `json:"serie"`
	Correlativo  string                  `json:"correlativo"`
	FechaEmision string                  `json:"fechaEmision"`
	Observacion  string                  `json:"observacion,omitempty"`
	Company      InvoiceCompany          `json:"company"`
	Destinatario InvoiceClient           `json:"destinatario"`
	Tercero      *InvoiceClient          `json:"tercero,omitempty"`
	Envio        DespatchShipment        `json:"envio"`
	Details      []DespatchDetail        `json:"details"`
	AddDocs      []DespatchAdditionalDoc `json:"addDocs,omitempty"`
}

// SendDespatch envía la guía de remisión (POST /despatch/send). Respuesta puede traer ticket o CDR directo (sin hash).
func (c *Client) SendDespatch(payload *DespatchPayload) (*SunatResponse, error) {
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("payload: %w", err)
	}
	req, err := http.NewRequest("POST", c.addToken("/despatch/send"), bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	var out SunatResponse
	if err := json.Unmarshal(respBody, &out); err != nil {
		return nil, fmt.Errorf("respuesta inválida: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return &out, fmt.Errorf("facturador respondió %d: %s", resp.StatusCode, string(respBody))
	}
	return &out, nil
}

// GetDespatchPDF obtiene PDF de guía (POST /despatch/pdf).
func (c *Client) GetDespatchPDF(payload *DespatchPayload) ([]byte, error) {
	return c.postDespatchDocument("/despatch/pdf", payload)
}

// GetDespatchXML obtiene XML firmado de guía (POST /despatch/xml).
func (c *Client) GetDespatchXML(payload *DespatchPayload) ([]byte, error) {
	return c.postDespatchDocument("/despatch/xml", payload)
}

func (c *Client) postDespatchDocument(path string, payload *DespatchPayload) ([]byte, error) {
	if payload == nil {
		return nil, fmt.Errorf("payload: nil")
	}
	normalizeDespatchPayloadDates(payload)
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("payload: %w", err)
	}
	req, err := http.NewRequest("POST", c.addToken(path), bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("facturador %s respondió %d: %s", path, resp.StatusCode, string(b))
	}
	return io.ReadAll(resp.Body)
}

// GetDespatchStatus consulta estado del ticket de guía (GET /despatch/status).
func (c *Client) GetDespatchStatus(ticket, ruc string) (*StatusResult, error) {
	path := "/despatch/status?ticket=" + url.QueryEscape(ticket)
	if ruc != "" {
		path += "&ruc=" + url.QueryEscape(ruc)
	}
	req, err := http.NewRequest("GET", c.addToken(path), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var out StatusResult
	_ = json.Unmarshal(body, &out)
	if resp.StatusCode != http.StatusOK {
		return &out, fmt.Errorf("facturador respondió %d: %s", resp.StatusCode, string(body))
	}
	return &out, nil
}

// --- Retención (Retention) ---

// RetentionDetail detalle de comprobante retenido.
type RetentionDetail struct {
	TipoDoc        string  `json:"tipoDoc"`
	NumDoc         string  `json:"numDoc"`
	FechaEmision   string  `json:"fechaEmision"`
	ImpTotal       float64 `json:"impTotal"`
	Moneda         string  `json:"moneda"`
	FechaRetencion string  `json:"fechaRetencion"`
	ImpRetenido    float64 `json:"impRetenido"`
	ImpPagar       float64 `json:"impPagar"`
	TipoCambio     float64 `json:"tipoCambio,omitempty"`
}

// RetentionPayload body para POST /retention/send.
type RetentionPayload struct {
	Serie       string             `json:"serie"`
	Correlativo string             `json:"correlativo"`
	FechaEmision string           `json:"fechaEmision"`
	Company     InvoiceCompany    `json:"company"`
	Proveedor   InvoiceClient     `json:"proveedor"`
	Regimen     string             `json:"regimen"`
	Tasa        float64            `json:"tasa"`
	ImpRetenido float64            `json:"impRetenido"`
	ImpPagado   float64            `json:"impPagado"`
	Observacion string             `json:"observacion,omitempty"`
	Details     []RetentionDetail  `json:"details"`
}

// SendRetention envía comprobante de retención (POST /retention/send).
func (c *Client) SendRetention(payload *RetentionPayload) (*SunatResponse, error) {
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("payload: %w", err)
	}
	req, err := http.NewRequest("POST", c.addToken("/retention/send"), bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	var out SunatResponse
	if err := json.Unmarshal(respBody, &out); err != nil {
		return nil, fmt.Errorf("respuesta inválida: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return &out, fmt.Errorf("facturador respondió %d: %s", resp.StatusCode, string(respBody))
	}
	return &out, nil
}

// --- Percepción (Perception) ---

// PerceptionDetail detalle de comprobante percibido.
type PerceptionDetail struct {
	TipoDoc         string  `json:"tipoDoc"`
	NumDoc          string  `json:"numDoc"`
	FechaEmision    string  `json:"fechaEmision"`
	ImpTotal        float64 `json:"impTotal"`
	Moneda          string  `json:"moneda"`
	FechaPercepcion string  `json:"fechaPercepcion"`
	ImpPercibido    float64 `json:"impPercibido"`
	ImpCobrar       float64 `json:"impCobrar"`
}

// PerceptionPayload body para POST /perception/send.
type PerceptionPayload struct {
	Serie         string                `json:"serie"`
	Correlativo   string                `json:"correlativo"`
	FechaEmision  string                `json:"fechaEmision"`
	Company       InvoiceCompany        `json:"company"`
	Proveedor     InvoiceClient         `json:"proveedor"`
	Regimen       string                `json:"regimen"`
	Tasa          float64               `json:"tasa"`
	ImpPercibido  float64               `json:"impPercibido"`
	ImpCobrado    float64               `json:"impCobrado"`
	Observacion   string                `json:"observacion,omitempty"`
	Details       []PerceptionDetail    `json:"details"`
}

// SendPerception envía comprobante de percepción (POST /perception/send).
func (c *Client) SendPerception(payload *PerceptionPayload) (*SunatResponse, error) {
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("payload: %w", err)
	}
	req, err := http.NewRequest("POST", c.addToken("/perception/send"), bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	var out SunatResponse
	if err := json.Unmarshal(respBody, &out); err != nil {
		return nil, fmt.Errorf("respuesta inválida: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return &out, fmt.Errorf("facturador respondió %d: %s", resp.StatusCode, string(respBody))
	}
	return &out, nil
}

// --- Reversión (Reversion) - mismo esquema que Voided ---

// SendReversion envía comunicación de reversión (POST /reversion/send). Mismo payload que voided.
func (c *Client) SendReversion(payload *VoidedPayload) (*SunatResponse, error) {
	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("payload: %w", err)
	}
	req, err := http.NewRequest("POST", c.addToken("/reversion/send"), bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	var out SunatResponse
	if err := json.Unmarshal(respBody, &out); err != nil {
		return nil, fmt.Errorf("respuesta inválida: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return &out, fmt.Errorf("facturador respondió %d: %s", resp.StatusCode, string(respBody))
	}
	return &out, nil
}

// GetReversionStatus consulta estado del ticket de reversión (GET /reversion/status).
func (c *Client) GetReversionStatus(ticket, ruc string) (*StatusResult, error) {
	path := "/reversion/status?ticket=" + url.QueryEscape(ticket)
	if ruc != "" {
		path += "&ruc=" + url.QueryEscape(ruc)
	}
	req, err := http.NewRequest("GET", c.addToken(path), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var out StatusResult
	_ = json.Unmarshal(body, &out)
	if resp.StatusCode != http.StatusOK {
		return &out, fmt.Errorf("facturador respondió %d: %s", resp.StatusCode, string(body))
	}
	return &out, nil
}

// PEMToBase64 codifica el contenido PEM del certificado en base64 para enviar al facturador.
func PEMToBase64(pemContent string) string {
	return base64.StdEncoding.EncodeToString([]byte(pemContent))
}

// PrepareGreenterCertificateBase64 convierte PFX o PEM al formato que Greenter setCertificate() espera:
// PEM con clave privada + certificado(s), normalizado para multi-tenant.
func PrepareGreenterCertificateBase64(pfxBase64, password, privateKeyBase64, certificateBase64 string) (string, error) {
	if strings.TrimSpace(pfxBase64) != "" {
		return PfxToCombinedPEMBase64(pfxBase64, password)
	}
	if strings.TrimSpace(privateKeyBase64) != "" || strings.TrimSpace(certificateBase64) != "" {
		return BuildCombinedPEMBase64(privateKeyBase64, certificateBase64)
	}
	return "", nil
}

// BuildCombinedPEMBase64 construye el PEM que Greenter/Lycet necesita: clave privada + certificado.
// Acepta: dos archivos PEM, un solo PEM combinado, o el mismo contenido en ambos campos.
func BuildCombinedPEMBase64(privateKeyBase64, certificateBase64 string) (string, error) {
	var chunks []string
	for _, b64 := range []string{privateKeyBase64, certificateBase64} {
		b64 = strings.TrimSpace(b64)
		if b64 == "" {
			continue
		}
		raw, err := base64.StdEncoding.DecodeString(b64)
		if err != nil {
			return "", fmt.Errorf("PEM inválido (base64): %w", err)
		}
		if s := strings.TrimSpace(string(raw)); s != "" {
			chunks = append(chunks, s)
		}
	}
	if len(chunks) == 0 {
		return "", fmt.Errorf("se requiere certificado PEM (PFX, archivo combinado o clave + certificado)")
	}
	full := normalizePEMWithBagAttributes([]byte(strings.Join(chunks, "\n")))
	return encodeGreenterCombinedPEM(full)
}

// PfxToCombinedPEMBase64 convierte certificado .pfx/.p12 (base64) a PEM combinado para Lycet.
func PfxToCombinedPEMBase64(pfxBase64, password string) (string, error) {
	raw, err := decodeBase64Flexible(pfxBase64)
	if err != nil {
		return "", err
	}
	if len(raw) == 0 {
		return "", fmt.Errorf("archivo PFX vacío")
	}
	blocks, err := pfxToPEMBlocks(raw, password)
	if err != nil {
		return "", err
	}
	var keyParts []string
	var certParts []string
	for _, block := range blocks {
		if block == nil {
			continue
		}
		part := strings.TrimSpace(string(pem.EncodeToMemory(block)))
		if part == "" {
			continue
		}
		switch block.Type {
		case "PRIVATE KEY", "RSA PRIVATE KEY", "EC PRIVATE KEY":
			if _, err := x509.ParsePKCS8PrivateKey(block.Bytes); err != nil {
				if pkcs1, err2 := x509.ParsePKCS1PrivateKey(block.Bytes); err2 == nil {
					block = &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(pkcs1)}
				}
			}
			part := strings.TrimSpace(string(pem.EncodeToMemory(block)))
			if part != "" {
				keyParts = append(keyParts, part)
			}
		case "ENCRYPTED PRIVATE KEY":
			dec, err := x509.DecryptPEMBlock(block, []byte(password))
			if err != nil {
				return "", fmt.Errorf("no se pudo desencriptar la clave del PFX (revise la contraseña): %w", err)
			}
			keyParts = append(keyParts, strings.TrimSpace(string(pem.EncodeToMemory(&pem.Block{
				Type:  "PRIVATE KEY",
				Bytes: dec,
			}))))
		case "CERTIFICATE":
			certParts = append(certParts, part)
		}
	}
	if len(keyParts) == 0 {
		return "", fmt.Errorf("el PFX no contiene clave privada")
	}
	if len(certParts) == 0 {
		return "", fmt.Errorf("el PFX no contiene certificado")
	}
	combined := strings.Join(keyParts, "\n") + "\n" + strings.Join(certParts, "\n")
	return encodeGreenterCombinedPEM([]byte(combined))
}

func encodeGreenterCombinedPEM(raw []byte) (string, error) {
	raw = normalizePEMWithBagAttributes(raw)
	var keyParts, certParts []string
	rest := raw
	for {
		block, rem := pem.Decode(rest)
		if block == nil {
			break
		}
		part := strings.TrimSpace(string(pem.EncodeToMemory(block)))
		switch block.Type {
		case "PRIVATE KEY", "RSA PRIVATE KEY", "EC PRIVATE KEY":
			if block.Type == "PRIVATE KEY" {
				if _, err := x509.ParsePKCS8PrivateKey(block.Bytes); err != nil {
					if pkcs1, err2 := x509.ParsePKCS1PrivateKey(block.Bytes); err2 == nil {
						part = strings.TrimSpace(string(pem.EncodeToMemory(&pem.Block{
							Type:  "RSA PRIVATE KEY",
							Bytes: x509.MarshalPKCS1PrivateKey(pkcs1),
						})))
					}
				}
			}
			if part != "" {
				keyParts = append(keyParts, part)
			}
		case "ENCRYPTED PRIVATE KEY":
			return "", fmt.Errorf("la clave privada está encriptada; use PFX con contraseña desde el panel central")
		case "CERTIFICATE":
			if part != "" {
				certParts = append(certParts, part)
			}
		}
		rest = rem
	}
	if len(keyParts) == 0 {
		return "", fmt.Errorf("el certificado no contiene clave privada usable para Greenter")
	}
	if len(certParts) == 0 {
		return "", fmt.Errorf("el certificado no contiene bloque CERTIFICATE")
	}
	combined := strings.Join(keyParts, "\n") + "\n" + strings.Join(certParts, "\n")
	if err := ValidateCombinedPEM([]byte(combined)); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString([]byte(combined)), nil
}

// ValidateCombinedPEMBase64 valida que el PEM incluya certificado y clave privada usable.
func ValidateCombinedPEMBase64(b64 string) error {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
	if err != nil {
		return fmt.Errorf("certificate_base64 inválido: %w", err)
	}
	return ValidateCombinedPEM(raw)
}

// ValidateCombinedPEM valida bloques PEM para firma SUNAT directa.
func ValidateCombinedPEM(raw []byte) error {
	raw = normalizePEMWithBagAttributes(raw)
	var hasKey, hasCert bool
	rest := raw
	for {
		block, rem := pem.Decode(rest)
		if block == nil {
			break
		}
		switch block.Type {
		case "PRIVATE KEY", "RSA PRIVATE KEY", "EC PRIVATE KEY":
			if _, err := x509.ParsePKCS8PrivateKey(block.Bytes); err != nil {
				if _, err2 := x509.ParsePKCS1PrivateKey(block.Bytes); err2 != nil {
					if _, err3 := x509.ParseECPrivateKey(block.Bytes); err3 != nil {
						return fmt.Errorf("clave privada no usable para firmar XML")
					}
				}
			}
			hasKey = true
		case "ENCRYPTED PRIVATE KEY":
			return fmt.Errorf("la clave privada está encriptada; suba el PFX con contraseña")
		case "CERTIFICATE":
			if _, err := x509.ParseCertificate(block.Bytes); err != nil {
				return fmt.Errorf("certificado inválido: %w", err)
			}
			hasCert = true
		}
		rest = rem
	}
	if !hasCert {
		return fmt.Errorf("falta bloque CERTIFICATE en el PEM")
	}
	if !hasKey {
		return fmt.Errorf("falta clave privada en el PEM")
	}
	return nil
}

var pemBeginRE = regexp.MustCompile(`-----BEGIN ([A-Z0-9 ]+)-----`)

// normalizePEMWithBagAttributes elimina friendlyName/localKeyId embebidos en bloques PEM.
func normalizePEMWithBagAttributes(raw []byte) []byte {
	pemText := strings.ReplaceAll(string(raw), "\r\n", "\n")
	var out []string
	offset := 0
	for {
		loc := pemBeginRE.FindStringSubmatchIndex(pemText[offset:])
		if loc == nil {
			break
		}
		absStart := offset + loc[0]
		typeName := pemText[offset+loc[2] : offset+loc[3]]
		begin := "-----BEGIN " + typeName + "-----"
		end := "-----END " + typeName + "-----"
		endIdx := strings.Index(pemText[absStart:], end)
		if endIdx < 0 {
			break
		}
		endIdx += absStart
		body := pemText[absStart+len(begin) : endIdx]
		var b64 strings.Builder
		for _, line := range strings.Split(body, "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.Contains(line, ":") {
				continue
			}
			b64.WriteString(line)
		}
		if b64.Len() > 0 {
			decoded, err := base64.StdEncoding.DecodeString(b64.String())
			if err == nil {
				blockType := typeName
				blockBytes := decoded
				if typeName == "PRIVATE KEY" {
					if _, err := x509.ParsePKCS8PrivateKey(decoded); err != nil {
						if pkcs1, err2 := x509.ParsePKCS1PrivateKey(decoded); err2 == nil {
							blockType = "RSA PRIVATE KEY"
							blockBytes = x509.MarshalPKCS1PrivateKey(pkcs1)
						}
					}
				}
				block := &pem.Block{Type: blockType, Bytes: blockBytes}
				out = append(out, strings.TrimSpace(string(pem.EncodeToMemory(block))))
			}
		}
		offset = endIdx + len(end)
	}
	if len(out) == 0 {
		return raw
	}
	return []byte(strings.Join(out, "\n"))
}

func extractPEMBlock(content, blockType string) string {
	begin := "-----BEGIN " + blockType + "-----"
	end := "-----END " + blockType + "-----"
	i := strings.Index(content, begin)
	if i < 0 {
		return ""
	}
	j := strings.Index(content[i:], end)
	if j < 0 {
		return ""
	}
	return content[i : i+j+len(end)]
}

func normalizeDespatchPayloadDates(payload *DespatchPayload) {
	if payload == nil {
		return
	}
	ref := payload.FechaEmision
	payload.FechaEmision = NormalizeFiscalDateTimeString(payload.FechaEmision, ref)
	ref = payload.FechaEmision
	payload.Envio.FecTraslado = NormalizeFiscalDateTimeString(payload.Envio.FecTraslado, ref)
	payload.Envio.FecEntregaBienes = NormalizeFiscalDateTimeString(payload.Envio.FecEntregaBienes, ref)
	payload.Envio.FecEntregaTransportista = NormalizeFiscalDateTimeString(payload.Envio.FecEntregaTransportista, ref)
}
