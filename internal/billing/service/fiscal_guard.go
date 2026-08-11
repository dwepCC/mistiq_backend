package service

import (
	"log/slog"

	"tukifac/pkg/billinglock"
	"tukifac/pkg/billingstate"
	"tukifac/pkg/database"
	"tukifac/pkg/logger"
)

// FiscalOpSource origen de la operación fiscal (observabilidad).
type FiscalOpSource string

const (
	FiscalSourceAutoCreate FiscalOpSource = "auto_create"
	FiscalSourceManual     FiscalOpSource = "manual"
	FiscalSourceManualResend FiscalOpSource = "manual_resend"
	FiscalSourceQueue      FiscalOpSource = "queue"
	FiscalSourceFiscalQueue FiscalOpSource = "fiscal_queue"
	FiscalSourceRetry      FiscalOpSource = "retry"
	FiscalSourceReconcile  FiscalOpSource = "reconcile"
	// FiscalSourceReissue corrección de soporte: reenvía un comprobante con otra
	// fecha de emisión aunque ya tenga aceptación registrada. Existe porque una
	// aceptación de SUNAT beta no vale en producción, y sin este origen las
	// guardas de "ya aceptado" impedirían corregirlo. Solo lo dispara el endpoint
	// protegido por acceso maestro.
	FiscalSourceReissue FiscalOpSource = "reissue"
)

// FiscalPrepareResult resultado de sync+lock antes de emitir/encolar.
type FiscalPrepareResult struct {
	Proceed   bool
	Status    string // allow, already_accepted, already_processing, rejected_final
	Message   string
	Invoice   *database.TenantInvoice
	lockOwner string
	tenantID  uint
	saleID    uint
	source    FiscalOpSource
}

// ReleaseLock libera lock distribuido si fue adquirido (idempotente).
func (r *FiscalPrepareResult) ReleaseLock() {
	if r == nil || r.lockOwner == "" {
		return
	}
	if r.tenantID > 0 && r.saleID > 0 {
		billinglock.Release(r.tenantID, r.saleID, r.lockOwner)
	}
	r.lockOwner = ""
}

func (s *BillingService) logFiscalOp(source FiscalOpSource, tenantID, saleID uint, status string, extra ...any) {
	args := []any{
		slog.Uint64("tenant_id", uint64(tenantID)),
		slog.Uint64("sale_id", uint64(saleID)),
		slog.String("source", string(source)),
		slog.String("status", status),
	}
	if s.tenantSlug != "" {
		args = append(args, slog.String("tenant_slug", s.tenantSlug))
	}
	args = append(args, extra...)
	logger.L.Info("fiscal_operation", args...)
}

// PrepareFiscalOperation sync SSOT + lock Redis antes de enqueue/send/worker.
func (s *BillingService) PrepareFiscalOperation(saleID, tenantID uint, source FiscalOpSource, allowResend bool) FiscalPrepareResult {
	res := FiscalPrepareResult{
		saleID:    saleID,
		tenantID:  tenantID,
		source:    source,
		lockOwner: string(source),
		Status:    "allow",
		Proceed:   true,
	}
	if tenantID == 0 {
		tenantID = s.centralTenantID
		res.tenantID = tenantID
	}
	if tenantID == 0 || saleID == 0 {
		res.Proceed = false
		res.Status = "error"
		res.Message = "contexto tenant requerido"
		return res
	}

	acquired, err := billinglock.TryAcquire(tenantID, saleID, res.lockOwner)
	if err != nil {
		res.Proceed = false
		res.Status = "error"
		res.Message = err.Error()
		s.logFiscalOp(source, tenantID, saleID, "lock_error", slog.Any("error", err))
		return res
	}
	if !acquired {
		res.Proceed = false
		res.Status = "already_processing"
		res.Message = "El comprobante ya está en proceso de emisión"
		s.logFiscalOp(source, tenantID, saleID, "already_processing", slog.String("lock_owner", "held"))
		return res
	}

	sync := s.SyncSaleWithSSOT(saleID)
	if sync.Invoice != nil {
		res.Invoice = sync.Invoice
	}

	switch classifyAfterSync(sync, allowResend, source) {
	case "already_accepted":
		res.Proceed = false
		res.Status = "already_accepted"
		res.Message = "El comprobante ya fue aceptado por SUNAT"
		res.ReleaseLock()
		s.logFiscalOp(source, tenantID, saleID, "already_accepted")
		return res
	case "already_processing":
		res.Proceed = false
		res.Status = "already_processing"
		res.Message = "El comprobante ya está en proceso de emisión"
		res.ReleaseLock()
		s.logFiscalOp(source, tenantID, saleID, "already_processing")
		return res
	case "rejected_final":
		res.Proceed = false
		res.Status = "rejected"
		res.Message = "Comprobante rechazado por SUNAT; emita uno nuevo con las correcciones"
		res.ReleaseLock()
		s.logFiscalOp(source, tenantID, saleID, "rejected_final")
		return res
	}

	s.logFiscalOp(source, tenantID, saleID, "allow", slog.Bool("synced", sync.Synced))
	return res
}

func hasInFlightFiscalWork(sync SSOTSyncOutcome) bool {
	if sync.StatusView != nil && sync.StatusView.Async {
		return true
	}
	pipeline := ""
	if sync.StatusView != nil {
		pipeline = billingstate.NormalizePipeline(sync.StatusView.Pipeline)
	} else if sync.Invoice != nil {
		pipeline = billingstate.NormalizePipeline(sync.Invoice.PipelineStatus)
	}
	switch pipeline {
	case billingstate.PENDING_QUEUE, billingstate.PENDING_FISCAL,
		billingstate.PROCESSING, billingstate.SENDING_TO_FACTURADOR,
		billingstate.FACTURADOR_RECEIVED, billingstate.SENDING_TO_SUNAT,
		billingstate.RETRYING:
		return true
	}
	if sync.StatusView != nil {
		switch sync.StatusView.DisplayPhase {
		case billingstate.PhaseQueued, billingstate.PhaseSending, billingstate.PhaseRetrying:
			return true
		}
	}
	return false
}

func classifyAfterSync(sync SSOTSyncOutcome, allowResend bool, source FiscalOpSource) string {
	// La reemisión ignora la aceptación previa a propósito: es justo el caso que
	// viene a corregir. Sigue respetando el trabajo en vuelo para no pisar un
	// envío en curso.
	reissue := source == FiscalSourceReissue

	ms := sync.ManualStatus
	if (ms == "already_accepted" || ms == "accepted") && !reissue {
		return "already_accepted"
	}
	if ms == "rejected" && !allowResend {
		return "rejected_final"
	}
	if ms == "processing" && !allowResend && hasInFlightFiscalWork(sync) {
		return "already_processing"
	}
	if sync.StatusView != nil {
		phase := billingstate.DisplayPhaseFromPipeline(sync.StatusView.Pipeline, sync.StatusView.JobStatus)
		switch phase {
		case billingstate.PhaseQueued, billingstate.PhaseSending, billingstate.PhaseRetrying:
			if !allowResend {
				return "already_processing"
			}
		case billingstate.PhaseAccepted, billingstate.PhaseObserved:
			if !reissue {
				return "already_accepted"
			}
		}
	}
	return "allow"
}

// IsElectronicSunatCode indica si el código requiere emisión fiscal automática.
func IsElectronicSunatCode(code string) bool {
	switch code {
	case "00", "":
		return false
	default:
		return true
	}
}

func (s *BillingService) saleSunatCode(saleID uint) string {
	var sale database.TenantSale
	if err := s.db.Select("series_id").First(&sale, saleID).Error; err != nil {
		return ""
	}
	var ser database.TenantDocumentSeries
	if err := s.db.Select("sunat_code").First(&ser, sale.SeriesID).Error; err != nil {
		return ""
	}
	return ser.SunatCode
}
