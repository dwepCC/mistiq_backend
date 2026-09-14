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
	// OpVentasNoDomiciliados: cliente sin RUC peruano (extranjero, no domiciliado) que no
	// califica como exportación. Solo cambia qué tipo de documento de identidad se acepta en
	// factura (01) — sin campos ni cálculos propios. Ver sale_service.go: la factura exige RUC
	// salvo con esta operación.
	OpVentasNoDomiciliados = "0401"
	// OpDetraccionTransporte: detracción por servicio de transporte de carga por vía terrestre
	// (Resolución 073-2006-SUNAT), código de bien 027 exclusivo. Mismo valor que
	// sunatdet.OpDetraccionTransporte en pkg/sunat/detraccion — duplicado a propósito, igual que
	// OpDetraccion/OpDetraccionGeneral, porque ese paquete ya importa este y crear una dependencia
	// inversa sería un ciclo.
	OpDetraccionTransporte = "1004"
)

// IsDetraccion agrupa las dos variantes de detracción (1001 general, 1004 transporte de carga):
// comparten toda la validación de factura/moneda/cliente en sale_service.go, y solo difieren en
// el código de bien permitido, el umbral y los campos adicionales que exige 1004.
func IsDetraccion(opCode string) bool {
	return opCode == OpDetraccion || opCode == OpDetraccionTransporte
}

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

// NormalizeOperationType permite venta interna (0101), emisión de anticipos (configurable),
// detracción general (1001), ventas no domiciliados (0401) y detracción por transporte de carga
// (1004).
func NormalizeOperationType(raw string) (string, error) {
	code := strings.TrimSpace(raw)
	if code == "" {
		return OpVentaInterna, nil
	}
	switch code {
	case OpVentaInterna, OpDetraccion, OpVentasNoDomiciliados, OpDetraccionTransporte:
		return code, nil
	default:
		if sunatpre.IsAllowedEmitOperationType(code) {
			return code, nil
		}
		return "", fmt.Errorf("tipo de operación %s no está habilitado; use %s, %s, %s, %s o %s",
			code, OpVentaInterna, sunatpre.EmitOperationTypeCode(), OpDetraccion, OpVentasNoDomiciliados, OpDetraccionTransporte)
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
