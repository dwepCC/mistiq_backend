package billingstate

import (
	"testing"

	"tukifac/pkg/database"
)

// Casos tomados de comprobantes reales de producción (códigos y mensajes tal como llegan del facturador).
func TestPipelineFromFiscal(t *testing.T) {
	const sentNoCdr = `Enviado al proveedor (PSE); SUNAT aún no devolvió el CDR real. Use "Consultar CDR" para verificar`
	cases := []struct {
		name            string
		status, code, m string
		want            string
		outage          bool
	}{
		// SUNAT caído / sin veredicto: NUNCA rechazado.
		{"PSE enviado sin CDR (200)", "sent", "200", sentNoCdr, SENDING_TO_SUNAT, true},
		{"PSE sin CDR tras consultas", "error", "200", "La guía fue enviada correctamente, pero SUNAT/PSE no confirmó el CDR tras 8 consultas automáticas", SENDING_TO_SUNAT, true},
		{"SUNAT en proceso (98)", "rejected", "98", "En proceso", SENDING_TO_SUNAT, true},
		{"Bad Gateway", "error", "HTTP", "Bad Gateway", SENDING_TO_SUNAT, true},
		{"No conecta", "error", "HTTP", "Could not connect to host", SENDING_TO_SUNAT, true},
		{"Headers", "error", "HTTP", "Error Fetching http headers", SENDING_TO_SUNAT, true},
		{"0109 autenticación no disponible", "error", "0109", "El servicio de autenticación no está disponible", SENDING_TO_SUNAT, true},
		{"0109 reintentando", "retrying", "0109", "El servicio de autenticación no está disponible", RETRYING, true},
		{"0100 sistema no responde", "error", "0100", "El sistema no puede responder su solicitud", SENDING_TO_SUNAT, true},
		{"timeout sin código", "error", "", "cURL error 28: Operation timed out after 30001 milliseconds", SENDING_TO_SUNAT, true},

		// Rechazos reales: siguen siendo rechazo.
		{"2017 receptor", "rejected", "2017", "El numero de documento de identidad del receptor debe ser RUC", SUNAT_REJECTED, false},
		{"3105 tributo", "rejected", "3105", "El XML debe contener al menos un tributo", SUNAT_REJECTED, false},
		{"2108 fuera de fecha", "rejected", "2108", "Presentacion fuera de fecha", SUNAT_REJECTED, false},
		{"1032 ya rechazado", "rejected", "1032", "El comprobante ya esta informado y se encuentra con estado anulado o rechazado", SUNAT_REJECTED, false},
		{"error con código de negocio", "error", "2800", "tipo de documento no permitido", SUNAT_REJECTED, false},
		{"sin código, mensaje de negocio", "error", "error", "El XML no contiene tag de la cantidad del concepto por linea", SUNAT_REJECTED, false},
		{"0111 perfil (sin cambios)", "error", "0111", "No tiene el perfil para enviar comprobantes electronicos", SUNAT_REJECTED, false},
		{"Bad Request no es caída", "error", "HTTP", "Bad Request", SUNAT_REJECTED, false},
		{"2000+ con estado intermedio", "sent", "2108", "Presentacion fuera de fecha", SUNAT_REJECTED, false},

		// Aceptados y observados.
		{"aceptado", "accepted", "0", "La Factura ha sido aceptada", SUNAT_ACCEPTED, false},
		{"aceptado con observaciones", "accepted", "4000", "observada", OBSERVED, false},
		{"observado", "observed", "0", "", OBSERVED, false},

		// Intermedios.
		{"en cola", "queued", "", "", PENDING_FISCAL, false},
		{"enviando", "sending", "", "", SENDING_TO_SUNAT, false},
		{"reintentando sin código", "retrying", "", "", RETRYING, false},
		{"error sin código ni mensaje", "error", "", "", FAILED, false},
	}
	for _, c := range cases {
		got, outage := PipelineFromFiscal(c.status, c.code, c.m)
		if got != c.want || outage != c.outage {
			t.Errorf("%s: PipelineFromFiscal(%q,%q,%q) = %s/%v, quiero %s/%v", c.name, c.status, c.code, c.m, got, outage, c.want, c.outage)
		}
	}
}

func TestCanApplyFiscal_recoversFalseRejectionButNotRealOnes(t *testing.T) {
	// Falso rechazo por caída → vuelve a «enviado» y luego puede aceptarse.
	if !CanApplyFiscal(SUNAT_REJECTED, SENDING_TO_SUNAT, true) || !CanApplyFiscal(FAILED, RETRYING, true) {
		t.Error("un rechazo/error causado por caída de SUNAT debe poder volver a enviado")
	}
	if !CanApplyFiscal(SENDING_TO_SUNAT, SUNAT_ACCEPTED, false) || !CanApplyFiscal(SUNAT_REJECTED, SUNAT_ACCEPTED, false) {
		t.Error("cuando SUNAT acepta, siempre pasa a aceptado")
	}
	// Sin señal de caída, un rechazo real no retrocede por un evento atrasado.
	if CanApplyFiscal(SUNAT_REJECTED, SENDING_TO_SUNAT, false) {
		t.Error("un rechazo real no vuelve a enviado sin una señal de caída")
	}
	// Un aceptado nunca retrocede.
	if CanApplyFiscal(SUNAT_ACCEPTED, SENDING_TO_SUNAT, true) {
		t.Error("un comprobante aceptado no retrocede")
	}
}

func TestBillingStatusAfterOutage_isSentNotRejected(t *testing.T) {
	p, _ := PipelineFromFiscal("error", "HTTP", "Bad Gateway")
	if got := LegacyBillingStatus(p); got != BillingSent {
		t.Fatalf("SUNAT caído debe verse «enviado»: %s", got)
	}
	inv := &database.TenantInvoice{PipelineStatus: p, SunatCDRCode: "HTTP", JobStatus: "processing"}
	if got := DisplayPhaseForInvoice(inv, p); got != PhaseSent || DisplayLabelSpanish(got) != "Enviado" {
		t.Fatalf("etiqueta: %s / %s", got, DisplayLabelSpanish(got))
	}
	// En trámite sin código todavía: sigue «Enviando».
	if got := DisplayPhaseForInvoice(&database.TenantInvoice{PipelineStatus: SENDING_TO_SUNAT, JobStatus: "processing"}, SENDING_TO_SUNAT); got != PhaseSending {
		t.Errorf("sin código sigue siendo enviando: %s", got)
	}
}

func TestLooksLikeFalseRejection(t *testing.T) {
	yes := []database.TenantInvoice{
		{PipelineStatus: SUNAT_REJECTED, SunatCDRCode: "200"},
		{PipelineStatus: SUNAT_REJECTED, SunatCDRCode: "98"},
		{PipelineStatus: SUNAT_REJECTED, SunatCDRCode: "HTTP", SunatMessage: "Bad Gateway"},
	}
	no := []database.TenantInvoice{
		{PipelineStatus: SUNAT_REJECTED, SunatCDRCode: "2017"},
		{PipelineStatus: SUNAT_REJECTED, SunatCDRCode: "HTTP", SunatMessage: "Bad Request"},
		{PipelineStatus: SENDING_TO_SUNAT, SunatCDRCode: "200"},
		{PipelineStatus: SUNAT_ACCEPTED, SunatCDRCode: "0"},
	}
	for i := range yes {
		if !LooksLikeFalseRejection(&yes[i]) {
			t.Errorf("debe reabrirse: %+v", yes[i])
		}
	}
	for i := range no {
		if LooksLikeFalseRejection(&no[i]) {
			t.Errorf("no debe reabrirse: %+v", no[i])
		}
	}
}
