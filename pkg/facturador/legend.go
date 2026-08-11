package facturador

import "tukifac/pkg/numeroletras"

// SetSUNATLegend1000 envía la leyenda de monto en letras (catálogo 52, código 1000) en legends[].
// Lycet/Greenter la serializan como cbc:Note languageLocaleID="1000" (formato aceptado por SUNAT).
func SetSUNATLegend1000(legends *[]InvoiceLegend, mtoImpVenta float64, tipoMoneda string) {
	if legends == nil || mtoImpVenta <= 0 {
		return
	}
	*legends = []InvoiceLegend{{
		Code:  "1000",
		Value: numeroletras.MontoEnLetras(mtoImpVenta, tipoMoneda),
	}}
}

// AppendSUNATLegend2006 agrega leyenda obligatoria para operaciones sujetas a detracción (cat. 52).
func AppendSUNATLegend2006(legends *[]InvoiceLegend) {
	if legends == nil {
		return
	}
	for _, l := range *legends {
		if l.Code == "2006" {
			return
		}
	}
	*legends = append(*legends, InvoiceLegend{
		Code:  "2006",
		Value: "Operación sujeta a detracción",
	})
}
