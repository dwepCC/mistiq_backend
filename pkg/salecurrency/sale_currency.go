package salecurrency

import (
	"fmt"
	"strings"

	sunatpre "tukifac/pkg/sunat/prepayment"
)

const (
	CurrencyPEN    = "PEN"
	CurrencyUSD    = "USD"
	OpVentaInterna = "0101"
	OpDetraccion   = "1001"
)

// NormalizeCurrency valida PEN/USD.
func NormalizeCurrency(raw string) (string, error) {
	c := strings.ToUpper(strings.TrimSpace(raw))
	if c == "" {
		return CurrencyPEN, nil
	}
	if c != CurrencyPEN && c != CurrencyUSD {
		return "", fmt.Errorf("moneda no válida: use PEN o USD")
	}
	return c, nil
}

// NormalizeOperationType permite venta interna (0101), emisión de anticipos (configurable) y detracción (1001).
func NormalizeOperationType(raw string) (string, error) {
	code := strings.TrimSpace(raw)
	if code == "" {
		return OpVentaInterna, nil
	}
	switch code {
	case OpVentaInterna, OpDetraccion:
		return code, nil
	default:
		if sunatpre.IsAllowedEmitOperationType(code) {
			return code, nil
		}
		return "", fmt.Errorf("tipo de operación %s no está habilitado; use %s, %s o %s",
			code, OpVentaInterna, sunatpre.EmitOperationTypeCode(), OpDetraccion)
	}
}

// NormalizeExchangeRate normaliza TC; no es obligatorio (ingreso manual o consulta fallida).
func NormalizeExchangeRate(currency string, rate *float64) (*float64, error) {
	if currency != CurrencyUSD {
		return nil, nil
	}
	if rate == nil {
		return nil, nil
	}
	if *rate <= 0 {
		return nil, fmt.Errorf("el tipo de cambio debe ser mayor a cero")
	}
	r := *rate
	return &r, nil
}

// RequireExchangeRateForUSD exige TC cuando la moneda es USD (p. ej. emisión electrónica desde NV).
func RequireExchangeRateForUSD(currency string, rate *float64) error {
	c, err := NormalizeCurrency(currency)
	if err != nil {
		return err
	}
	if c != CurrencyUSD {
		return nil
	}
	if rate == nil || *rate <= 0 {
		return fmt.Errorf("el tipo de cambio es obligatorio para ventas en dólares (USD)")
	}
	return nil
}

// TotalInPEN convierte el total de venta a soles para reglas fiscales (umbral retención).
func TotalInPEN(currency string, total float64, exchangeRate *float64) float64 {
	if strings.ToUpper(strings.TrimSpace(currency)) != CurrencyUSD {
		return total
	}
	rate := 0.0
	if exchangeRate != nil {
		rate = *exchangeRate
	}
	if rate <= 0 {
		return total
	}
	return total * rate
}
