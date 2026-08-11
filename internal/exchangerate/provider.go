package exchangerate

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// TokenProvider obtiene el token apiperu desde ajustes centrales.
type TokenProvider interface {
	GetTokenConsulta() (string, error)
}

type apiPeruProvider struct {
	tokenProvider TokenProvider
	client        *http.Client
}

func NewApiPeruProvider(tokenProvider TokenProvider) *apiPeruProvider {
	return &apiPeruProvider{
		tokenProvider: tokenProvider,
		client:        &http.Client{Timeout: 15 * time.Second},
	}
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

const apiPeruTipoCambioURL = "https://apiperu.dev/api/tipo-de-cambio"

// Fetch consulta apiperu.dev para una fecha (yyyy-mm-dd).
func (p *apiPeruProvider) Fetch(ctx context.Context, fecha string) (*ProviderResult, error) {
	token, err := p.tokenProvider.GetTokenConsulta()
	if err != nil || strings.TrimSpace(token) == "" {
		return nil, fmt.Errorf("no está configurado el token de consulta en ajustes del sistema central")
	}
	body, _ := json.Marshal(map[string]string{"fecha": fecha})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiPeruTipoCambioURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := p.client.Do(req)
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
		return &ProviderResult{OK: false, Fecha: fecha, Error: msg}, nil
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
	if venta <= 0 {
		return &ProviderResult{
			OK:    false,
			Fecha: fecha,
			Error: "tipo de cambio venta inválido en respuesta del proveedor",
		}, nil
	}
	return &ProviderResult{
		OK:     true,
		Fecha:  outFecha,
		Moneda: moneda,
		Venta:  venta,
		Compra: compra,
	}, nil
}
