package billingstate

import (
	"strings"

	"tukifac/pkg/database"
)

// Fases UX para tenant (más granulares que billing_status almacenado).
const (
	PhaseDraft      = "draft"
	PhasePending    = "pending"
	PhaseQueued     = "queued"
	PhaseSending    = "sending"
	// PhaseSent: el comprobante ya llegó al proveedor/SUNAT, pero SUNAT no dio veredicto (caída, sin CDR todavía).
	PhaseSent = "sent"
	PhaseRetrying   = "retrying"
	PhaseAccepted   = "accepted"
	PhaseObserved   = "observed"
	PhaseRejected   = "rejected"
	PhaseError      = "error"
	PhaseCancelled  = "cancelled"
)

// DisplayPhaseFromPipeline mapea pipeline+job a fase UX.
func DisplayPhaseFromPipeline(pipeline, jobStatus string) string {
	p := NormalizePipeline(pipeline)
	switch p {
	case DRAFT:
		return PhaseDraft
	case PENDING_QUEUE, PENDING_FISCAL:
		return PhaseQueued
	case RETRYING:
		return PhaseRetrying
	case PROCESSING, SENDING_TO_FACTURADOR, FACTURADOR_RECEIVED, SENDING_TO_SUNAT:
		return PhaseSending
	case SUNAT_ACCEPTED:
		return PhaseAccepted
	case OBSERVED:
		return PhaseObserved
	case SUNAT_REJECTED:
		return PhaseRejected
	case FAILED, DEAD_LETTER, UNKNOWN:
		return PhaseError
	default:
		switch jobStatus {
		case "retrying":
			return PhaseRetrying
		case "processing":
			return PhaseSending
		case "pending":
			return PhaseQueued
		}
		return PhasePending
	}
}

// DisplayLabelSpanish etiqueta legible para UI tenant.
func DisplayLabelSpanish(phase string) string {
	switch phase {
	case PhaseDraft:
		return "Borrador"
	case PhasePending:
		return "Pendiente"
	case PhaseQueued:
		return "En cola"
	case PhaseSending:
		return "Enviando"
	case PhaseSent:
		return "Enviado"
	case PhaseRetrying:
		return "Reintentando"
	case PhaseAccepted:
		return "Aceptado"
	case PhaseObserved:
		return "Aceptado con observaciones"
	case PhaseRejected:
		return "Rechazado"
	case PhaseError:
		return "Error envío"
	case PhaseCancelled:
		return "Anulado"
	default:
		return "Pendiente"
	}
}

// DisplayPhaseForInvoice igual que DisplayPhaseFromPipeline, pero distingue «Enviado»: un comprobante en trámite que ya
// tiene un código/mensaje de SUNAT o del proveedor sin veredicto (SUNAT caído, CDR pendiente) se muestra «Enviado», no
// «Enviando»/«Reintentando», y menos «Rechazado».
func DisplayPhaseForInvoice(inv *database.TenantInvoice, pipeline string) string {
	phase := DisplayPhaseFromPipeline(pipeline, inv.JobStatus)
	if (phase == PhaseSending || phase == PhaseRetrying) && strings.TrimSpace(inv.SunatCDRCode) != "" {
		return PhaseSent
	}
	return phase
}
