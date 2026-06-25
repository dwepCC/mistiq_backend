package service

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"tukifac/pkg/database"

	ajustesvc "tukifac/internal/ajustes/service"
)

const (
	apiPeruBase        = "https://apiperu.dev"
	apiPeruDNI         = apiPeruBase + "/api/dni"
	apiPeruRUC         = apiPeruBase + "/api/ruc"
	apiPeruTipoCambio  = apiPeruBase + "/api/tipo-de-cambio"
)

// ConsultaService consulta DNI/RUC vía apiperu.dev usando token_consulta de ajustes centrales.
type ConsultaService struct {
	ajusteSvc *ajustesvc.AjusteService
	client    *http.Client
}

func NewConsultaService() *ConsultaService {
	return &ConsultaService{
		ajusteSvc: ajustesvc.NewAjusteService(),
		client:    &http.Client{Timeout: 15 * time.Second},
	}
}

// ValidateTenantByRUC verifica que exista un tenant activo en la central con el RUC dado.
// Se usa en el endpoint público para que solo empresas registradas puedan usar la consulta.
func (s *ConsultaService) ValidateTenantByRUC(tenantRUC string) error {
	tenantRUC = strings.TrimSpace(strings.ReplaceAll(tenantRUC, "-", ""))
	if len(tenantRUC) != 11 {
		return fmt.Errorf("el RUC del tenant debe tener 11 dígitos")
	}
	var count int64
	err := database.CentralDB.Model(&database.Tenant{}).
		Where("ruc = ? AND status = ?", tenantRUC, "active").
		Count(&count).Error
	if err != nil {
		return fmt.Errorf("error al validar empresa: %w", err)
	}
	if count == 0 {
		return fmt.Errorf("la empresa no está registrada o no está activa en el sistema central")
	}
	return nil
}

// flexString acepta en JSON tanto string como number (apiperu.dev a veces devuelve codigo_verificacion como número).
type flexString string

func (s *flexString) UnmarshalJSON(data []byte) error {
	if len(data) == 0 {
		*s = ""
		return nil
	}
	if data[0] == '"' {
		var str string
		if err := json.Unmarshal(data, &str); err != nil {
			return err
		}
		*s = flexString(str)
		return nil
	}
	var n json.Number
	if err := json.Unmarshal(data, &n); err != nil {
		return err
	}
	*s = flexString(n.String())
	return nil
}

// Respuesta DNI apiperu.dev (campos que usamos).
type DNIResponse struct {
	Success bool `json:"success"`
	Data    struct {
		Numero             string     `json:"numero"`
		NombreCompleto    string     `json:"nombre_completo"`
		Nombres           string     `json:"nombres"`
		ApellidoPaterno   string     `json:"apellido_paterno"`
		ApellidoMaterno   string     `json:"apellido_materno"`
		CodigoVerificacion flexString `json:"codigo_verificacion"`
	} `json:"data"`
	Message string `json:"message"`
}

// DNIResult datos normalizados para el formulario (cliente/proveedor con DNI).
type DNIResult struct {
	Success       bool   `json:"success"`
	NombreCompleto string `json:"nombre_completo"`
	Nombres       string `json:"nombres"`
	ApellidoPaterno string `json:"apellido_paterno"`
	ApellidoMaterno string `json:"apellido_materno"`
	DocNumber     string `json:"doc_number"`
}

// Respuesta RUC apiperu.dev.
type RUCResponse struct {
	Success bool `json:"success"`
	Data    struct {
		RUC                  string   `json:"ruc"`
		NombreORazonSocial    string   `json:"nombre_o_razon_social"`
		Direccion             string   `json:"direccion"`
		DireccionCompleta     string   `json:"direccion_completa"`
		Estado                string   `json:"estado"`
		Condicion             string   `json:"condicion"`
		Departamento          string   `json:"departamento"`
		Provincia             string   `json:"provincia"`
		Distrito              string   `json:"distrito"`
		UbigeoSunat                    string   `json:"ubigeo_sunat"`
		Ubigeo                         []string `json:"ubigeo"`
		EsAgenteDeRetencion            string   `json:"es_agente_de_retencion"`
		EsAgenteDePercepcion           string   `json:"es_agente_de_percepcion"`
		EsAgenteDePercepcionCombustible string  `json:"es_agente_de_percepcion_combustible"`
		EsBuenContribuyente            string   `json:"es_buen_contribuyente"`
	} `json:"data"`
	Message string `json:"message"`
}

// RUCResult datos normalizados para formulario (tenant o contacto con RUC).
type RUCResult struct {
	Success        bool   `json:"success"`
	RUC            string `json:"ruc"`
	RazonSocial    string `json:"razon_social"`
	Direccion      string `json:"direccion"`
	DireccionCompleta string `json:"direccion_completa,omitempty"`
	Estado         string `json:"estado"`
	Condicion      string `json:"condicion"`
	Departamento   string `json:"departamento"`
	Provincia      string `json:"provincia"`
	Distrito       string `json:"distrito"`
	Ubigeo                          string `json:"ubigeo"` // 6 dígitos (distrito)
	EsAgenteDeRetencion             bool   `json:"es_agente_de_retencion"`
	EsAgenteDePercepcion            bool   `json:"es_agente_de_percepcion"`
	EsAgenteDePercepcionCombustible bool   `json:"es_agente_de_percepcion_combustible"`
	EsBuenContribuyente             bool   `json:"es_buen_contribuyente"`
}

func (s *ConsultaService) ConsultaDNI(dni string) (*DNIResult, error) {
	dni = strings.TrimSpace(strings.ReplaceAll(dni, "-", ""))
	if len(dni) != 8 {
		return nil, fmt.Errorf("el DNI debe tener 8 dígitos")
	}
	token, err := s.ajusteSvc.GetTokenConsulta()
	if err != nil || token == "" {
		return nil, fmt.Errorf("no está configurado el token de consulta en ajustes del sistema central")
	}
	body, _ := json.Marshal(map[string]string{"dni": dni})
	req, err := http.NewRequest(http.MethodPost, apiPeruDNI, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("error al conectar con el servicio de consulta: %w", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	var r DNIResponse
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("respuesta inválida del servicio: %w", err)
	}
	if !r.Success {
		return &DNIResult{Success: false}, nil
	}
	return &DNIResult{
		Success:         true,
		NombreCompleto:  r.Data.NombreCompleto,
		Nombres:         r.Data.Nombres,
		ApellidoPaterno: r.Data.ApellidoPaterno,
		ApellidoMaterno: r.Data.ApellidoMaterno,
		DocNumber:       dni,
	}, nil
}

func (s *ConsultaService) ConsultaRUC(ruc string) (*RUCResult, error) {
	ruc = strings.TrimSpace(strings.ReplaceAll(ruc, "-", ""))
	if len(ruc) != 11 {
		return nil, fmt.Errorf("el RUC debe tener 11 dígitos")
	}
	token, err := s.ajusteSvc.GetTokenConsulta()
	if err != nil || token == "" {
		return nil, fmt.Errorf("no está configurado el token de consulta en ajustes del sistema central")
	}
	body, _ := json.Marshal(map[string]string{"ruc": ruc})
	req, err := http.NewRequest(http.MethodPost, apiPeruRUC, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("error al conectar con el servicio de consulta: %w", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	var r RUCResponse
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("respuesta inválida del servicio: %w", err)
	}
	if !r.Success {
		return &RUCResult{Success: false}, nil
	}
	ubigeo := r.Data.UbigeoSunat
	if ubigeo == "" && len(r.Data.Ubigeo) >= 3 {
		ubigeo = r.Data.Ubigeo[2]
	}
	return &RUCResult{
		Success:                         true,
		RUC:                             r.Data.RUC,
		RazonSocial:                     r.Data.NombreORazonSocial,
		Direccion:                       r.Data.Direccion,
		DireccionCompleta:               r.Data.DireccionCompleta,
		Estado:                          r.Data.Estado,
		Condicion:                       r.Data.Condicion,
		Departamento:                    r.Data.Departamento,
		Provincia:                       r.Data.Provincia,
		Distrito:                        r.Data.Distrito,
		Ubigeo:                          ubigeo,
		EsAgenteDeRetencion:             sunatSiNoToBool(r.Data.EsAgenteDeRetencion),
		EsAgenteDePercepcion:            sunatSiNoToBool(r.Data.EsAgenteDePercepcion),
		EsAgenteDePercepcionCombustible: sunatSiNoToBool(r.Data.EsAgenteDePercepcionCombustible),
		EsBuenContribuyente:             sunatSiNoToBool(r.Data.EsBuenContribuyente),
	}, nil
}

// sunatSiNoToBool convierte "SI"/"NO" de apiperu.dev a bool.
func sunatSiNoToBool(v string) bool {
	return strings.EqualFold(strings.TrimSpace(v), "SI")
}

// TipoCambioResult tipo de cambio SUNAT (USD/PEN) para una fecha.
type TipoCambioResult struct {
	Success      bool    `json:"success"`
	Fecha        string  `json:"fecha"`
	Moneda       string  `json:"moneda"`
	Venta        float64 `json:"venta"`
	Compra       float64 `json:"compra"`
	Fuente       string  `json:"fuente"`
	ErrorMessage string  `json:"error_message,omitempty"`
}

type tipoCambioAPIResponse struct {
	Success bool `json:"success"`
	Data    struct {
		Moneda        string  `json:"moneda"`
		FechaBusqueda string  `json:"fecha_busqueda"`
		Date          string  `json:"date"`
		Venta         float64 `json:"venta"`
		Compra        float64 `json:"compra"`
		Sale          float64 `json:"sale"`
		Purchase      float64 `json:"purchase"`
	} `json:"data"`
	Message string `json:"message"`
}

// ConsultaTipoCambio obtiene TC SUNAT venta/compra para una fecha (yyyy-mm-dd).
func (s *ConsultaService) ConsultaTipoCambio(fecha string) (*TipoCambioResult, error) {
	fecha = strings.TrimSpace(fecha)
	if fecha == "" {
		return nil, fmt.Errorf("fecha requerida (formato yyyy-mm-dd)")
	}
	if _, err := time.Parse("2006-01-02", fecha); err != nil {
		return nil, fmt.Errorf("fecha inválida: use yyyy-mm-dd")
	}
	token, err := s.ajusteSvc.GetTokenConsulta()
	if err != nil || token == "" {
		return nil, fmt.Errorf("no está configurado el token de consulta en ajustes del sistema central")
	}
	body, _ := json.Marshal(map[string]string{"fecha": fecha})
	req, err := http.NewRequest(http.MethodPost, apiPeruTipoCambio, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("error al conectar con el servicio de tipo de cambio: %w", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	var r tipoCambioAPIResponse
	if err := json.Unmarshal(data, &r); err != nil {
		return nil, fmt.Errorf("respuesta inválida del servicio: %w", err)
	}
	if !r.Success {
		msg := strings.TrimSpace(r.Message)
		if msg == "" {
			msg = "tipo de cambio no disponible para la fecha indicada"
		}
		return &TipoCambioResult{Success: false, Fecha: fecha, ErrorMessage: msg}, nil
	}
	venta := r.Data.Venta
	if venta == 0 {
		venta = r.Data.Sale
	}
	compra := r.Data.Compra
	if compra == 0 {
		compra = r.Data.Purchase
	}
	outFecha := strings.TrimSpace(r.Data.FechaBusqueda)
	if outFecha == "" {
		outFecha = strings.TrimSpace(r.Data.Date)
	}
	if outFecha == "" {
		outFecha = fecha
	}
	moneda := strings.TrimSpace(r.Data.Moneda)
	if moneda == "" {
		moneda = "USD"
	}
	return &TipoCambioResult{
		Success: true,
		Fecha:   outFecha,
		Moneda:  moneda,
		Venta:   venta,
		Compra:  compra,
		Fuente:  "SUNAT",
	}, nil
}
