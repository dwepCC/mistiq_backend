package billingstate

import (
	"strings"
	"time"

	"tukifac/pkg/database"

	"gorm.io/gorm"
)

// InvoicePatch campos a persistir tras evaluar respuesta.
type InvoicePatch struct {
	PipelineStatus string
	SunatStatus    string
	SunatMessage   string
	SunatCDRCode   string
	SunatCDRNotes  string
	SunatHash      string
	XMLURL         string
	CDRURL         string
	JobStatus      string
	JobLastError   string
}

// BuildPatch construye actualización conservadora.
func BuildPatch(pipeline string, inv *database.TenantInvoice, msg string) InvoicePatch {
	p := NormalizePipeline(pipeline)
	if !CanTransition(NormalizePipeline(inv.PipelineStatus), p) {
		p = UNKNOWN
	}
	patch := InvoicePatch{
		PipelineStatus: p,
		SunatStatus:    LegacySunatStatus(p),
		JobStatus:      JobStatusFromPipeline(p),
		SunatMessage:   msg,
	}
	if inv != nil {
		patch.SunatCDRCode = inv.SunatCDRCode
		patch.SunatCDRNotes = inv.SunatCDRNotes
		patch.SunatHash = inv.SunatHash
		patch.XMLURL = inv.XMLURL
		patch.CDRURL = inv.CDRURL
	}
	if p == FAILED || p == SUNAT_REJECTED || p == DEAD_LETTER {
		if msg != "" {
			patch.JobLastError = msg
		}
	}
	return patch
}

// ApplyToInvoice aplica patch en memoria (caller hace Save).
func ApplyToInvoice(inv *database.TenantInvoice, patch InvoicePatch) {
	if inv == nil {
		return
	}
	inv.PipelineStatus = patch.PipelineStatus
	inv.SunatStatus = patch.SunatStatus
	if patch.SunatMessage != "" {
		inv.SunatMessage = patch.SunatMessage
	}
	if patch.SunatCDRCode != "" {
		inv.SunatCDRCode = patch.SunatCDRCode
	}
	if patch.SunatCDRNotes != "" {
		inv.SunatCDRNotes = patch.SunatCDRNotes
	}
	if patch.SunatHash != "" {
		inv.SunatHash = patch.SunatHash
	}
	if patch.XMLURL != "" {
		inv.XMLURL = patch.XMLURL
	}
	if patch.CDRURL != "" {
		inv.CDRURL = patch.CDRURL
	}
	if patch.JobStatus != "" {
		inv.JobStatus = patch.JobStatus
	}
	if patch.JobLastError != "" {
		inv.JobLastError = patch.JobLastError
	} else if patch.PipelineStatus == SUNAT_ACCEPTED || patch.PipelineStatus == OBSERVED {
		inv.JobLastError = ""
	}
}

// SyncSaleBillingStatus actualiza tenant_sales.billing_status desde pipeline.
func SyncSaleBillingStatus(db *gorm.DB, saleID uint, pipeline string) error {
	if db == nil || saleID == 0 {
		return nil
	}
	return db.Model(&database.TenantSale{}).Where("id = ?", saleID).
		Update("billing_status", LegacyBillingStatus(pipeline)).Error
}

// TransitionPipeline actualiza solo pipeline (y derivados) sin respuesta facturador.
func TransitionPipeline(db *gorm.DB, saleID uint, pipeline string) error {
	if db == nil {
		return nil
	}
	p := NormalizePipeline(pipeline)
	updates := map[string]interface{}{
		"pipeline_status": p,
		"job_status":      JobStatusFromPipeline(p),
	}
	return db.Model(&database.TenantInvoice{}).Where("sale_id = ?", saleID).Updates(updates).Error
}

// StatusView DTO para GET /billing/status/:saleId
type StatusView struct {
	Status        string `json:"status"`
	SunatCode     string `json:"sunat_code"`
	CDRReceived   bool   `json:"cdr_received"`
	SunatMessage  string `json:"sunat_message"`
	XMLSigned     bool   `json:"xml_signed"`
	SafeToPrint   bool   `json:"safe_to_print"`
	LastAttemptAt string `json:"last_attempt_at"`
	RetryCount    int    `json:"retry_count"`
	JobStatus     string `json:"job_status"`
	BillingStatus string `json:"billing_status"`
	Pipeline      string `json:"pipeline_status"`
	DisplayPhase  string `json:"display_phase"`
	DisplayLabel  string `json:"display_label"`
	Async         bool   `json:"async_in_progress"`
}

// BuildStatusView desde invoice + sale.
func BuildStatusView(inv *database.TenantInvoice, sale *database.TenantSale) StatusView {
	v := StatusView{
		Status:    DRAFT,
		JobStatus: "pending",
	}
	if sale != nil {
		v.BillingStatus = sale.BillingStatus
	}
	if inv == nil {
		v.DisplayPhase = PhasePending
		v.DisplayLabel = DisplayLabelSpanish(PhasePending)
		return v
	}
	p := NormalizePipeline(inv.PipelineStatus)
	if p == DRAFT && inv.ID > 0 {
		p = inferPipelineFromLegacy(inv)
	}
	v.Pipeline = p
	v.Status = p
	v.SunatCode = strings.TrimSpace(inv.SunatCDRCode)
	v.SunatMessage = inv.SunatMessage
	if v.SunatMessage == "" && strings.TrimSpace(inv.JobLastError) != "" {
		v.SunatMessage = strings.TrimSpace(inv.JobLastError)
	}
	v.CDRReceived = strings.TrimSpace(inv.CDRURL) != "" && inv.CDRURL != "(CDR recibido)"
	v.XMLSigned = strings.TrimSpace(inv.XMLURL) != "" || strings.TrimSpace(inv.SunatHash) != ""
	v.RetryCount = inv.RetryCount
	v.JobStatus = inv.JobStatus
	if inv.SentAt != nil {
		v.LastAttemptAt = inv.SentAt.Format(time.RFC3339)
	} else if inv.ResponseAt != nil {
		v.LastAttemptAt = inv.ResponseAt.Format(time.RFC3339)
	}
	ev := Evidence{
		HasSignedXML: v.XMLSigned,
		HasCDR:       v.CDRReceived,
		HasHash:      strings.TrimSpace(inv.SunatHash) != "",
		CDRCode:      v.SunatCode,
		SunatSuccess: inv.SunatStatus == "accepted" || inv.SunatCDRCode == "0",
		CDRAccepted:  inv.SunatCDRCode == "0",
	}
	v.SafeToPrint = p == SUNAT_ACCEPTED || p == OBSERVED || IsAcceptanceEvidence(ev)
	v.Async = isAsyncInProgress(p, inv.JobStatus)
	v.BillingStatus = EffectiveBillingStatus(sale, inv)
	v.DisplayPhase = DisplayPhaseFromPipeline(p, inv.JobStatus)
	v.DisplayLabel = DisplayLabelSpanish(v.DisplayPhase)
	return v
}

func isAsyncInProgress(pipeline, jobStatus string) bool {
	switch NormalizePipeline(pipeline) {
	case PENDING_QUEUE, PROCESSING, SENDING_TO_FACTURADOR, FACTURADOR_RECEIVED, SENDING_TO_SUNAT, RETRYING:
		return true
	}
	switch jobStatus {
	case "pending", "processing", "retrying":
		return true
	}
	return false
}

func inferPipelineFromLegacy(inv *database.TenantInvoice) string {
	if HasAcceptanceEvidence(inv) {
		if inv.SunatStatus == "observed" {
			return OBSERVED
		}
		return SUNAT_ACCEPTED
	}
	switch inv.SunatStatus {
	case "accepted":
		// Legacy sin CDR: no afirmar aceptación.
		if HasAcceptanceEvidence(inv) {
			return SUNAT_ACCEPTED
		}
		return UNKNOWN
	case "rejected":
		return SUNAT_REJECTED
	case "error":
		return FAILED
	case "pending":
		switch inv.JobStatus {
		case "processing", "retrying":
			return PROCESSING
		case "sent":
			return UNKNOWN
		default:
			return PENDING_QUEUE
		}
	default:
		return DRAFT
	}
}

// ReconcileInconsistent detecta estados mentirosos (para script repair).
func ReconcileInconsistent(inv *database.TenantInvoice) (fixedPipeline string, reason string) {
	if inv == nil {
		return "", ""
	}
	p := NormalizePipeline(inv.PipelineStatus)
	if p == "" {
		p = inferPipelineFromLegacy(inv)
	}
	claimsAccepted := p == SUNAT_ACCEPTED || inv.SunatStatus == "accepted" || inv.JobStatus == "sent"
	hasEvidence := HasAcceptanceEvidence(inv)
	if claimsAccepted && !hasEvidence {
		return UNKNOWN, "marcado aceptado/enviado sin CDR+XML evidencia"
	}
	if inv.JobStatus == "sent" && !hasEvidence {
		return FAILED, "job_status=sent sin evidencia SUNAT"
	}
	if hasEvidence && p != SUNAT_ACCEPTED && p != OBSERVED {
		if inv.SunatStatus == "observed" {
			return OBSERVED, "evidencia ok, pipeline desalineado"
		}
		return SUNAT_ACCEPTED, "evidencia ok, pipeline desalineado"
	}
	return "", ""
}
