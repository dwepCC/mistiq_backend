package worker

import (
	"fmt"
	"time"
	"tukifac/pkg/branchscope"

	billingsvc "tukifac/internal/billing/service"
	"tukifac/pkg/database"

	"gorm.io/gorm"
)

const (
	// ResendMinAge: no se tocan comprobantes recientes (podrían estar emitiéndose en este momento).
	ResendMinAge = 10 * time.Minute
	// ResendMaxBatch: tope por llamada; si quedan más, se repite la acción.
	ResendMaxBatch = 100
)

// ResendResult resumen de "Reenviar pendientes" de un tenant.
type ResendResult struct {
	Found           int `json:"found"`
	Queued          int `json:"queued"`
	AlreadyAccepted int `json:"already_accepted"`
	InProgress      int `json:"in_progress"`
	Failed          int `json:"failed"`
	// Remaining: cuántos más cumplen el criterio y quedaron para otra pasada.
	Remaining int `json:"remaining"`
}

// pendingCandidates devuelve los ids de ventas electrónicas aún no enviadas (pending o error)
// con más de `olderThan` desde su creación, las más antiguas primero, y cuántas hay en total.
// Misma definición de "electrónica" que la campanita del tenant: JOIN por sunat_code, así las
// notas de venta (00) nunca se reenvían.
func pendingCandidates(db *gorm.DB, olderThan time.Time, limit int) (ids []uint, total int64, err error) {
	base := func() *gorm.DB {
		return db.Table("tenant_sales AS s").
			Joins("JOIN tenant_document_series ds ON ds.id = s.series_id").
			Where("s.deleted_at IS NULL").
			Where("ds.sunat_code IN ?", []string{"01", "03", "07", "08"}).
			Where("COALESCE(NULLIF(s.billing_status,''),'pending') IN ?", []string{"pending", "error"}).
			Where("s.created_at < ?", olderThan)
	}
	if err = base().Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if err = base().Order("s.id ASC").Limit(limit).Pluck("s.id", &ids).Error; err != nil {
		return nil, 0, err
	}
	return ids, total, nil
}

// ResendTenantPending reenvía los comprobantes electrónicos pendientes o con error de un tenant.
//
// Para cada uno primero sincroniza contra el facturador (si SUNAT ya lo aceptó solo actualiza el
// estado, sin duplicar) y, si sigue sin aceptarse, lo reencola con la misma operación que usa el
// reenvío manual del tenant (EnqueueSendToSUNAT, con sus candados de idempotencia).
func ResendTenantPending(tenant database.Tenant, limit int) (ResendResult, error) {
	if limit <= 0 || limit > ResendMaxBatch {
		limit = ResendMaxBatch
	}
	db, err := database.GetTenantDB(tenant.DBName)
	if err != nil {
		return ResendResult{}, fmt.Errorf("abrir BD del tenant: %w", err)
	}
	defer database.ReleaseTenantDB(tenant.DBName)
	db = branchscope.System(db, "billing worker: reenvío de pendientes")

	ids, total, err := pendingCandidates(db, time.Now().Add(-ResendMinAge), limit)
	if err != nil {
		return ResendResult{}, err
	}
	res := ResendResult{Found: len(ids), Remaining: int(total) - len(ids)}

	svc := billingsvc.NewBillingService(db)
	svc.SetCentralTenantID(tenant.ID)
	svc.SetTenantSlug(tenant.Slug)

	for _, id := range ids {
		if out := svc.SyncSaleWithSSOT(id); out.ManualStatus == "accepted" || out.ManualStatus == "already_accepted" {
			res.AlreadyAccepted++
			continue
		}
		_, err := svc.EnqueueSendToSUNAT(id, tenant.ID, tenant.Slug, tenant.DBName, billingsvc.FiscalSourceManualResend)
		switch {
		case err == nil:
			res.Queued++
		case billingsvc.IsAlreadyAccepted(err):
			res.AlreadyAccepted++
		case billingsvc.IsAlreadyProcessing(err):
			res.InProgress++
		default:
			res.Failed++
		}
	}
	return res, nil
}
