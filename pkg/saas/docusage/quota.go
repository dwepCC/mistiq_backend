package docusage

import (
	"encoding/json"
	"errors"
	"fmt"

	"tukifac/pkg/database"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// GetUsageView calcula disponibilidad sin reservar (lectura).
func GetUsageView(tenantID uint) (DocumentUsageView, error) {
	cycle, _, err := CurrentBillingCycle(tenantID)
	if err != nil {
		return DocumentUsageView{CanEmit: false, WarningLevel: "exhausted"}, err
	}
	return buildView(cycle, tenantID), nil
}

func buildView(cycle *database.SaasBillingCycle, tenantID uint) DocumentUsageView {
	v := DocumentUsageView{
		IsUnlimited:     cycle.IsUnlimitedDocuments,
		PlanLimit:       cycle.DocumentsLimit,
		PlanUsed:        cycle.DocumentsUsed,
		BillingCycleID:  cycle.ID,
		BillingCycleEnd: cycle.PeriodEnd.In(lima()).Format("2006-01-02"),
	}
	if v.IsUnlimited {
		v.CanEmit = true
		v.WarningLevel = "none"
		return v
	}
	v.PlanRemaining = v.PlanLimit - v.PlanUsed
	if v.PlanRemaining < 0 {
		v.PlanRemaining = 0
	}

	var pkgs []database.SaasTenantDocumentPackage
	database.CentralDB.Where("tenant_id = ? AND billing_cycle_id = ? AND status = ?",
		tenantID, cycle.ID, database.SaasDocPkgApproved).Find(&pkgs)
	for _, p := range pkgs {
		v.PackageBonus += p.DocumentsQty
		v.PackageUsed += p.UsedDocuments
		v.PackageRemaining += p.RemainingDocuments
	}
	v.TotalAvailable = v.PlanRemaining + v.PackageRemaining
	v.TotalConsumed = v.PlanUsed + v.PackageUsed
	if v.PlanLimit+v.PackageBonus > 0 {
		v.UsagePercent = int(float64(v.TotalConsumed) / float64(v.PlanLimit+v.PackageBonus) * 100)
		if v.UsagePercent > 100 {
			v.UsagePercent = 100
		}
	}
	v.CanEmit = v.TotalAvailable > 0
	v.WarningLevel, v.WarningMessage = warningFromView(v)
	return v
}

func warningFromView(v DocumentUsageView) (string, string) {
	if v.IsUnlimited {
		return "none", ""
	}
	if !v.CanEmit {
		return "exhausted", "Has agotado tus documentos electrónicos. Compra un paquete adicional o mejora tu plan."
	}
	if v.TotalAvailable <= 10 {
		return "low", fmt.Sprintf("Te quedan %d documentos electrónicos en este ciclo.", v.TotalAvailable)
	}
	if v.UsagePercent >= 90 {
		return "high", fmt.Sprintf("Has usado el %d%% de tus documentos. Te quedan %d.", v.UsagePercent, v.TotalAvailable)
	}
	return "none", ""
}

// CanEmitElectronicDocument validación previa (sin consumir).
func CanEmitElectronicDocument(tenantID uint) error {
	v, err := GetUsageView(tenantID)
	if err != nil {
		return err
	}
	if v.IsUnlimited || v.CanEmit {
		return nil
	}
	return ErrQuotaExceeded
}

// GuardCountableSunatQuota bloquea crear comprobantes que consumen cupo (01, 03, 07, …) sin documentos disponibles.
func GuardCountableSunatQuota(tenantID uint, sunatCode string) error {
	if tenantID == 0 || !IsCountableSunatCode(sunatCode) {
		return nil
	}
	return CanEmitElectronicDocument(tenantID)
}

// ReserveElectronicDocument consume cupo de forma transaccional e idempotente.
func ReserveElectronicDocument(in ReserveInput) error {
	if in.TenantID == 0 || in.DocumentType == "" || in.DocumentID == 0 {
		return errors.New("datos de reserva incompletos")
	}
	if in.Source == "" {
		in.Source = "sync"
	}
	return database.CentralDB.Transaction(func(tx *gorm.DB) error {
		var existing database.SaasElectronicDocumentUsage
		err := tx.Where("tenant_id = ? AND document_type = ? AND document_id = ?",
			in.TenantID, in.DocumentType, in.DocumentID).First(&existing).Error
		if err == nil {
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}

		cycle, sub, err := currentBillingCycleTx(tx, in.TenantID)
		if err != nil {
			return err
		}
		if cycle.IsUnlimitedDocuments {
			return recordUsageTx(tx, in, cycle, sub, "plan_base", nil)
		}

		cycle, err = lockCycle(tx, cycle.ID)
		if err != nil {
			return err
		}

		from, pkgID, consumeErr := consumeSlotTx(tx, cycle, in.TenantID)
		if consumeErr != nil {
			return consumeErr
		}
		return recordUsageTx(tx, in, cycle, sub, from, pkgID)
	})
}

func currentBillingCycleTx(tx *gorm.DB, tenantID uint) (*database.SaasBillingCycle, *database.SaasSubscription, error) {
	var sub database.SaasSubscription
	if err := tx.Where("tenant_id = ?", tenantID).
		Where("status NOT IN ?", []string{database.SaasSubCancelled, database.SaasSubExpired}).
		Order("created_at desc").First(&sub).Error; err != nil {
		return nil, nil, ErrNoActiveCycle
	}
	var cycle database.SaasBillingCycle
	if err := tx.Where("subscription_id = ? AND period_end = ?", sub.ID, sub.EndDate).
		Order("id desc").First(&cycle).Error; err != nil {
		return nil, nil, ErrNoActiveCycle
	}
	return &cycle, &sub, nil
}

func consumeSlotTx(tx *gorm.DB, cycle *database.SaasBillingCycle, tenantID uint) (from string, pkgID *uint, err error) {
	if cycle.DocumentsUsed < cycle.DocumentsLimit {
		if err := tx.Model(cycle).Update("documents_used", gorm.Expr("documents_used + 1")).Error; err != nil {
			return "", nil, err
		}
		return "plan_base", nil, nil
	}

	var pkg database.SaasTenantDocumentPackage
	err = tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("tenant_id = ? AND billing_cycle_id = ? AND status = ? AND remaining_documents > 0",
			tenantID, cycle.ID, database.SaasDocPkgApproved).
		Order("approved_at asc, id asc").
		First(&pkg).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", nil, ErrQuotaExceeded
		}
		return "", nil, err
	}
	updates := map[string]interface{}{
		"used_documents":      gorm.Expr("used_documents + 1"),
		"remaining_documents": gorm.Expr("remaining_documents - 1"),
	}
	if err := tx.Model(&pkg).Updates(updates).Error; err != nil {
		return "", nil, err
	}
	id := pkg.ID
	return "package", &id, nil
}

func recordUsageTx(tx *gorm.DB, in ReserveInput, cycle *database.SaasBillingCycle, sub *database.SaasSubscription, from string, pkgID *uint) error {
	meta := in.MetadataJSON
	if meta == "" {
		b, _ := json.Marshal(map[string]interface{}{"source": in.Source})
		meta = string(b)
	}
	row := &database.SaasElectronicDocumentUsage{
		TenantID:       in.TenantID,
		SubscriptionID: sub.ID,
		BillingCycleID: cycle.ID,
		DocumentType:   in.DocumentType,
		DocumentID:     in.DocumentID,
		DocumentNumber: in.DocumentNumber,
		ConsumedFrom:   from,
		PackageID:      pkgID,
		Source:         in.Source,
		MetadataJSON:   meta,
		ConsumedAt:     nowLima(),
	}
	return tx.Create(row).Error
}
