package worker

import (
	"log/slog"
	"time"
	"tukifac/pkg/branchscope"

	billingsvc "tukifac/internal/billing/service"
	"tukifac/pkg/billinglock"
	"tukifac/pkg/billingstate"
	"tukifac/pkg/database"
	"tukifac/pkg/logger"
)

const reconcileBatchSize = 100

// reconcileHealBatchSize tope de falsos «rechazados» que se reabren por pasada y por tenant.
const reconcileHealBatchSize = 20
const reconcileMinAge = 2 * time.Minute

type reconcileSaleRow struct {
	SaleID uint `gorm:"column:sale_id"`
}

// ReconcileTenantPending sincroniza ventas stale contra facturador SSOT (fallback webhook).
func ReconcileTenantPending(tenant database.Tenant) (synced int) {
	// Background: este barrido corre cada 7 min sobre todos los tenants activos; por el LRU de 200 pools
	// abría y desalojaba ~600 pools por pasada, expulsando a los tenants que sí están operando.
	db, release, err := database.GetTenantDBBackground(tenant.DBName)
	if err != nil {
		return 0
	}
	defer release()
	db = branchscope.System(db, "billing worker: reconciliación de pendientes")

	cutoff := time.Now().Add(-reconcileMinAge)
	var rows []reconcileSaleRow
	err = db.Table("tenant_sales").
		Select("tenant_sales.id AS sale_id").
		Joins("LEFT JOIN tenant_invoices ON tenant_invoices.sale_id = tenant_sales.id").
		Where("tenant_sales.created_at < ?", cutoff).
		Where(`(
			tenant_sales.billing_status IN ('pending', 'sent')
			OR tenant_invoices.pipeline_status IN ('PENDING_FISCAL','PENDING_QUEUE','PROCESSING','RETRYING','SENDING_TO_FACTURADOR','FACTURADOR_RECEIVED','SENDING_TO_SUNAT')
		)`).
		Where(`tenant_sales.billing_status NOT IN ('accepted', 'rejected')`).
		Where(`(tenant_invoices.pipeline_status IS NULL OR tenant_invoices.pipeline_status NOT IN ('SUNAT_ACCEPTED','SUNAT_REJECTED','OBSERVED','FAILED','DEAD_LETTER'))`).
		Order("tenant_sales.id ASC").
		Limit(reconcileBatchSize).
		Find(&rows).Error
	if err != nil {
		return 0
	}
	// Falsos «rechazados» (SUNAT caído / CDR pendiente): se reabren de a pocos por pasada, aparte del lote normal para no
	// competir con los comprobantes pendientes de ahora. Al reconciliarse dejan de coincidir con la condición.
	var healRows []reconcileSaleRow
	if err := db.Table("tenant_sales").
		Select("tenant_sales.id AS sale_id").
		Joins("JOIN tenant_invoices ON tenant_invoices.sale_id = tenant_sales.id").
		Where(billingstate.FalseRejectionSQL).
		Order("tenant_sales.id DESC").
		Limit(reconcileHealBatchSize).
		Find(&healRows).Error; err == nil {
		rows = append(rows, healRows...)
	}
	if len(rows) == 0 {
		return 0
	}

	svc := billingsvc.NewBillingService(db)
	svc.SetCentralTenantID(tenant.ID)
	svc.SetTenantSlug(tenant.Slug)

	for _, row := range rows {
		acquired, _ := billinglock.TryAcquire(tenant.ID, row.SaleID, string(billingsvc.FiscalSourceReconcile))
		if !acquired {
			continue
		}
		if svc.ReconcileSaleFromFacturador(row.SaleID) {
			synced++
			continue
		}
		if svc.RequeueStaleFiscalJob(row.SaleID, tenant.DBName) {
			synced++
		}
		billinglock.Release(tenant.ID, row.SaleID, string(billingsvc.FiscalSourceReconcile))
	}
	return synced
}

// ReconcileAllTenants ejecuta reconciliación en todos los tenants activos.
func ReconcileAllTenants() {
	tenants, err := database.ListTenantsForMigration(true)
	if err != nil {
		logger.L.Warn("fiscal_reconcile_list_tenants_failed", slog.Any("error", err))
		return
	}
	total := 0
	for _, t := range tenants {
		n := ReconcileTenantPending(t)
		if n > 0 {
			logger.L.Info("fiscal_reconcile_tenant",
				slog.String("tenant", t.Slug),
				slog.Uint64("tenant_id", uint64(t.ID)),
				slog.Int("synced", n),
			)
		}
		total += n
	}
	if total > 0 {
		logger.L.Info("fiscal_reconcile_done", slog.Int("synced_total", total))
	}
}
