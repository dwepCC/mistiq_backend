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
	AppendSUNATLegendText(legends, "2006", "Operación sujeta a detracción")
}

// AppendSUNATLegendText agrega una leyenda (catálogo 52) con texto propio si su código no está ya
// presente. Usado para 2006 con el texto distinto que exige 1004 (transporte de carga) — mismo
// código de leyenda, texto diferente al de 1001.
func AppendSUNATLegendText(legends *[]InvoiceLegend, code, value string) {
	if legends == nil {
		return
	}
	for _, l := range *legends {
		if l.Code == code {
			return
		}
	}
	*legends = append(*legends, InvoiceLegend{
		Code:  code,
		Value: value,
	})
}
