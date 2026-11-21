package billingstate

import (
	"strconv"
	"strings"

	"tukifac/pkg/database"
)

// Traducción del estado que informa el facturador (más el código y mensaje de SUNAT/PSE) al pipeline del comprobante.
//
// Regla de fondo: «rechazado» es un veredicto de SUNAT sobre el comprobante. Que SUNAT esté caído, no responda, el
// proveedor devuelva un error de red/HTTP 5xx o que todavía no haya devuelto el CDR NO es un rechazo: el comprobante
// queda «enviado» (el usuario no debe creer que está mal emitido) y pasa a «aceptado» cuando SUNAT por fin lo acepte.

// IsSunatUnavailable indica si el código/mensaje corresponde a una falla del lado de SUNAT, del proveedor (PSE) o de la
// red, y no a un veredicto sobre el comprobante: servicio no disponible, errores de gateway/conexión, tiempos de espera
// o los códigos de excepción de sistema de SUNAT (0100, 0109).
func IsSunatUnavailable(code, message string) bool {
	c := strings.ToUpper(strings.TrimSpace(code))
	switch c {
	case "0100", "0109":
		return true
	}
	m := normalizeText(message)
	if m == "" {
		return false
	}
	for _, needle := range sunatUnavailableNeedles {
		if strings.Contains(m, needle) {
			return true
		}
	}
	return false
}

// sunatUnavailableNeedles fragmentos (sin acentos, en minúsculas) de mensajes de caída de SUNAT/PSE/red.
var sunatUnavailableNeedles = []string{
	"bad gateway",
	"service unavailable",
	"gateway timeout",
	"could not connect",
	"could not resolve host",
	"error fetching http headers",
	"connection refused",
	"connection reset",
	"connection timed out",
	"timed out",
	"operation timed out",
	"servicio no esta disponible",
	"servicio de autenticacion no esta disponible",
	"no puede responder su solicitud",
	"temporarily unavailable",
	"sunat no responde",
}

// IsVerdictPending indica si el comprobante ya llegó al proveedor/SUNAT pero todavía no hay veredicto (no hay CDR):
// código 200 del PSE («enviado, SUNAT aún no devolvió el CDR»), código 98 («en proceso») o mensajes equivalentes.
func IsVerdictPending(code, message string) bool {
	c := strings.TrimSpace(code)
	if c == "200" || c == "98" {
		return true
	}
	m := normalizeText(message)
	for _, needle := range []string{"aun no devolvio el cdr", "no confirmo el cdr", "en proceso"} {
		if m != "" && strings.Contains(m, needle) {
			return true
		}
	}
	return false
}

// PipelineFromFiscal convierte (status, código, mensaje) del facturador en pipeline. El segundo valor es true cuando el
// resultado salió de una falla de SUNAT/red o de un veredicto pendiente: en ese caso puede corregir un «rechazado» o
// «error» que se había marcado por equivocación (ver CanApplyFiscal).
func PipelineFromFiscal(status, code, message string) (pipeline string, outage bool) {
	status = strings.ToLower(strings.TrimSpace(status))
	code = strings.TrimSpace(code)
	n, numeric := 0, false
	if code != "" {
		if v, err := strconv.Atoi(code); err == nil {
			n, numeric = v, true
		}
	}

	switch status {
	case "accepted":
		if numeric && n >= 4000 {
			return OBSERVED, false
		}
		return SUNAT_ACCEPTED, false
	case "observed":
		return OBSERVED, false
	}
	if numeric && n >= 4000 {
		return OBSERVED, false
	}

	// SUNAT caído / sin respuesta / sin veredicto: nunca «rechazado».
	if IsSunatUnavailable(code, message) || IsVerdictPending(code, message) {
		if status == "retrying" {
			return RETRYING, true
		}
		return SENDING_TO_SUNAT, true
	}

	// Un código de negocio (2000+) manda sobre un estado intermedio: SUNAT ya opinó.
	if numeric && n >= 2000 && n < 4000 {
		return SUNAT_REJECTED, false
	}
	switch status {
	case "queued", "pending":
		return PENDING_FISCAL, false
	case "sending", "sent":
		// Estados intermedios: un código residual (p. ej. el 200 del PSE) no los convierte en rechazo.
		return SENDING_TO_SUNAT, false
	case "retrying":
		return RETRYING, false
	}
	if numeric && n != 0 {
		return SUNAT_REJECTED, false
	}
	switch status {
	case "rejected":
		return SUNAT_REJECTED, false
	case "error":
		if code != "" && code != "0" {
			return SUNAT_REJECTED, false
		}
		return FAILED, false
	default:
		if code == "0" {
			return SUNAT_ACCEPTED, false
		}
		return UNKNOWN, false
	}
}

// CanApplyFiscal decide si el pipeline nuevo puede reemplazar al actual. Además de las transiciones normales, un
// comprobante marcado «rechazado» o «error» puede volver a «enviado» cuando la causa fue una caída de SUNAT/red (outage):
// así se corrige el falso rechazo y después sí puede llegar a «aceptado».
func CanApplyFiscal(current, next string, outage bool) bool {
	current, next = NormalizePipeline(current), NormalizePipeline(next)
	if CanTransition(current, next) {
		return true
	}
	return outage && (current == SUNAT_REJECTED || current == FAILED) &&
		(next == SENDING_TO_SUNAT || next == RETRYING)
}

// FalseRejectionSQL condición SQL (sobre tenant_invoices) de los comprobantes marcados «rechazado» cuya causa fue una
// falla de SUNAT/red o un veredicto pendiente. La usa el reconciliador para reabrirlos.
const FalseRejectionSQL = `tenant_invoices.pipeline_status = 'SUNAT_REJECTED' AND (
	tenant_invoices.sunat_cdr_code IN ('200','98','0100','0109')
	OR (tenant_invoices.sunat_cdr_code = 'HTTP' AND (
		tenant_invoices.sunat_message LIKE '%Bad Gateway%'
		OR tenant_invoices.sunat_message LIKE '%Service Unavailable%'
		OR tenant_invoices.sunat_message LIKE '%Gateway Timeout%'
		OR tenant_invoices.sunat_message LIKE '%Could not connect%'
		OR tenant_invoices.sunat_message LIKE '%Error Fetching http headers%'
		OR tenant_invoices.sunat_message LIKE '%timed out%'))
)`

// LooksLikeFalseRejection versión en Go de FalseRejectionSQL para un comprobante ya cargado.
func LooksLikeFalseRejection(inv *database.TenantInvoice) bool {
	if inv == nil || NormalizePipeline(inv.PipelineStatus) != SUNAT_REJECTED {
		return false
	}
	code := strings.TrimSpace(inv.SunatCDRCode)
	switch code {
	case "200", "98", "0100", "0109":
		return true
	}
	return strings.EqualFold(code, "HTTP") && IsSunatUnavailable(code, inv.SunatMessage)
}

func normalizeText(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	return strings.NewReplacer("á", "a", "é", "e", "í", "i", "ó", "o", "ú", "u", "ü", "u", "ñ", "n").Replace(s)
}
