package service

import (
	"context"
	"tukifac/pkg/branchscope"

	fiscalsvc "tukifac/internal/fiscal/service"
	"tukifac/pkg/billingevents"
	"tukifac/pkg/billingstate"
	"tukifac/pkg/database"
)

// NotifyBillingStatusUpdated emite evento realtime tras actualizar BD tenant.
func NotifyBillingStatusUpdated(tenantID, saleID uint, pipeline, sunatMessage string) {
	if tenantID == 0 || saleID == 0 {
		return
	}
	p := billingstate.NormalizePipeline(pipeline)
	billingevents.PublishStatusUpdated(context.Background(), billingevents.NewStatusUpdated(
		tenantID,
		saleID,
		billingstate.LegacyBillingStatus(p),
		p,
		sunatMessage,
	))
}

// NotifyFromWebhookPayload emite tras webhook fiscal.
func NotifyFromWebhookPayload(tenantID uint, p *fiscalsvc.StatusWebhookPayload) {
	if p == nil {
		return
	}
	pipeline := pipelineFromFiscalStatus(p.Status, p.SunatCode, p.SunatMessage)
	NotifyBillingStatusUpdated(tenantID, p.SaleID, pipeline, p.SunatMessage)
	RunFiscalWebhookSideEffects(tenantID, p.TenantSlug, p.SaleID, pipeline)
}

// RunFiscalWebhookSideEffects sincroniza entidades ligadas (guía, anulación NC).
func RunFiscalWebhookSideEffects(tenantID uint, tenantSlug string, saleID uint, pipeline string) {
	if tenantID == 0 || saleID == 0 {
		return
	}
	var tenant database.Tenant
	if err := database.CentralDB.First(&tenant, tenantID).Error; err != nil {
		return
	}
	if tenant.DBName == "" {
		return
	}
	db, err := database.GetTenantDB(tenant.DBName)
	if err != nil {
		return
	}
	defer database.ReleaseTenantDB(tenant.DBName)
	db = branchscope.System(db, "billing: notificación de comprobante (proceso interno)")
	svc := NewBillingService(db)
	svc.SetCentralTenantID(tenantID)
	if tenant.Slug != "" {
		svc.SetTenantSlug(tenant.Slug)
	} else if tenantSlug != "" {
		svc.SetTenantSlug(tenantSlug)
	}
	svc.PostFiscalAcceptSideEffects(saleID, pipeline)
}

func pipelineFromFiscalStatus(status, sunatCode, sunatMessage string) string {
	p, _ := billingstate.PipelineFromFiscal(status, sunatCode, sunatMessage)
	return p
}
