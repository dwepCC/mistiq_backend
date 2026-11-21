package salecurrency

import (
	"fmt"
	"strings"
	"time"

	"tukifac/pkg/dailyfx"
	"tukifac/pkg/datespe"
)

// ResolveDocumentRate normaliza moneda y TC de un documento y **completa el TC desde el guardado del día** cuando el
// cliente no lo envía: toda operación en USD queda registrada con el tipo de cambio de su fecha de emisión.
//   - PEN: sin TC.
//   - USD con TC válido: se respeta (el usuario puede editarlo).
//   - USD sin TC: se toma el TC venta guardado para la fecha de emisión (hora de Lima); si no hay ninguno, error.
//   - Sin proveedor de TC configurado (pruebas): se conserva el comportamiento anterior (TC opcional).
func ResolveDocumentRate(rawCurrency string, rate *float64, issueDate time.Time) (currency string, resolved *float64, estimated bool, err error) {
	currency, err = NormalizeCurrency(rawCurrency)
	if err != nil {
		return "", nil, false, err
	}
	if currency != CurrencyUSD {
		return currency, nil, false, nil
	}
	resolved, err = NormalizeExchangeRate(currency, rate)
	if err != nil {
		return "", nil, false, err
	}
	if resolved != nil {
		return currency, resolved, false, nil
	}
	if !dailyfx.Configured() {
		return currency, nil, false, nil
	}
	day := issueDate.In(datespe.Location()).Format("2006-01-02")
	r, est, ok := dailyfx.SaleRate(day)
	if !ok || r <= 0 {
		return "", nil, false, fmt.Errorf("no hay tipo de cambio guardado para el %s: ingréselo manualmente", day)
	}
	return currency, &r, est, nil
}

// DayOf formatea una fecha de emisión como día de Lima (yyyy-mm-dd).
func DayOf(t time.Time) string { return strings.TrimSpace(t.In(datespe.Location()).Format("2006-01-02")) }

// MaxRateDeviation es cuánto puede apartarse el TC de un documento del TC oficial guardado del día para un usuario SIN permiso de
// editar precios. Un TC libre permitiría fijar un precio distinto al del catálogo (precio catálogo ÷ TC cuadra siempre con el TC
// que se escribió), así que el TC editable queda acotado.
const MaxRateDeviation = 0.05

// EnsureRateNearOfficial rechaza un TC (documento en USD) que se aparta más de MaxRateDeviation del TC guardado del día de
// emisión. No hace nada si el documento no es USD, si no hay proveedor de TC (pruebas) o si ese día no hay TC guardado.
func EnsureRateNearOfficial(currency string, rate *float64, issueDate time.Time) error {
	if currency != CurrencyUSD || rate == nil || *rate <= 0 || !dailyfx.Configured() {
		return nil
	}
	official, _, ok := dailyfx.SaleRate(DayOf(issueDate))
	if !ok || official <= 0 {
		return nil
	}
	if dev := *rate/official - 1; dev > MaxRateDeviation || dev < -MaxRateDeviation {
		return fmt.Errorf("el tipo de cambio %.3f se aparta más de %.0f%% del tipo de cambio oficial del día (%.3f); corríjalo o pida a un administrador que lo autorice", *rate, MaxRateDeviation*100, official)
	}
	return nil
}
