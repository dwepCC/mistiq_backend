package service

import (
	"testing"

	"tukifac/pkg/billingstate"
)

func TestManualStatusFromView_pendingWithoutInvoice(t *testing.T) {
	st := &billingstate.StatusView{
		BillingStatus: billingstate.BillingPending,
		Status:        billingstate.DRAFT,
		DisplayPhase:  billingstate.PhasePending,
	}
	if got := manualStatusFromView(st); got != "pending" {
		t.Fatalf("expected pending, got %q", got)
	}
}

func TestClassifyAfterSync_allowsEnqueueWithoutInvoice(t *testing.T) {
	sync := SSOTSyncOutcome{
		ManualStatus: "pending",
		StatusView: &billingstate.StatusView{
			BillingStatus: billingstate.BillingPending,
			DisplayPhase:  billingstate.PhasePending,
		},
	}
	if got := classifyAfterSync(sync, false, FiscalSourceManual); got != "allow" {
		t.Fatalf("expected allow, got %q", got)
	}
}

func TestClassifyAfterSync_blocksWhenInvoiceQueued(t *testing.T) {
	sync := SSOTSyncOutcome{
		ManualStatus: "queued",
		StatusView: &billingstate.StatusView{
			BillingStatus: billingstate.BillingSent,
			Pipeline:      billingstate.PENDING_FISCAL,
			DisplayPhase:  billingstate.PhaseQueued,
			Async:         true,
		},
	}
	if got := classifyAfterSync(sync, false, FiscalSourceManual); got != "already_processing" {
		t.Fatalf("expected already_processing, got %q", got)
	}
}
