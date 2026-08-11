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
	cycle, sub, err := CurrentBillingCycle(tenantID)
	if err != nil {
		return DocumentUsageView{CanEmit: false, WarningLevel: "exhausted"}, err
	}
	var period *database.SaasDocumentQuotaPeriod
	err = database.CentralDB.Transaction(func(tx *gorm.DB) error {
		p, e := ensureQuotaPeriodTx(tx, sub, cycle, nowLima())
		period = p
		return e
	})
	if err != nil {
		return DocumentUsageView{CanEmit: false, WarningLevel: "exhausted"}, err
	}
	return buildView(cycle, period, sub, tenantID), nil
}

// buildView: el cupo del plan sale del período MENSUAL; los paquetes siguen atados al
// ciclo de cobro, porque se compran aparte y valen hasta el fin de la suscripción.
func buildView(
	cycle *database.SaasBillingCycle,
	period *database.SaasDocumentQuotaPeriod,
	sub *database.SaasSubscription,
	tenantID uint,
) DocumentUsageView {
	v := DocumentUsageView{
		IsUnlimited:     period.IsUnlimitedDocuments,
		PlanLimit:       period.DocumentsLimit,
		PlanUsed:        period.DocumentsUsed,
		BillingCycleID:  cycle.ID,
		BillingCycleEnd: cycle.PeriodEnd.In(lima()).Format("2006-01-02"),
		QuotaPeriodID:   period.ID,
		QuotaPeriodEnd:  period.PeriodEnd.In(lima()).Format("2006-01-02"),
	}
	if sub != nil {
		v.QuotaPeriodIndex = period.PeriodIndex
		v.QuotaPeriodTotal = TotalQuotaPeriods(sub)
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
		if v.QuotaPeriodEnd != "" {
			return "exhausted", fmt.Sprintf(
				"Has agotado tus documentos electrónicos de este mes. Tu cupo se renueva el %s; también puedes comprar un paquete adicional o mejorar tu plan.",
				formatDayMonth(v.QuotaPeriodEnd))
		}
		return "exhausted", "Has agotado tus documentos electrónicos. Compra un paquete adicional o mejora tu plan."
	}
	if v.TotalAvailable <= 10 {
		return "low", fmt.Sprintf("Te quedan %d documentos electrónicos este mes.", v.TotalAvailable)
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
		period, err := ensureQuotaPeriodTx(tx, sub, cycle, nowLima())
		if err != nil {
			return err
		}
		if period.IsUnlimitedDocuments {
			return recordUsageTx(tx, in, cycle, period, sub, "plan_base", nil)
		}

		// El lock va sobre el período (es de donde se descuenta el cupo del plan), no
		// sobre el ciclo de cobro.
		period, err = lockQuotaPeriod(tx, period.ID)
		if err != nil {
			return err
		}

		from, pkgID, consumeErr := consumeSlotTx(tx, period, cycle, in.TenantID)
		if consumeErr != nil {
			return consumeErr
		}
		return recordUsageTx(tx, in, cycle, period, sub, from, pkgID)
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

// consumeSlotTx descuenta primero del cupo mensual del plan y, si está agotado, de los
// paquetes comprados (que viven en el ciclo de cobro, no en el mes).
func consumeSlotTx(
	tx *gorm.DB,
	period *database.SaasDocumentQuotaPeriod,
	cycle *database.SaasBillingCycle,
	tenantID uint,
) (from string, pkgID *uint, err error) {
	if period.DocumentsUsed < period.DocumentsLimit {
		if err := tx.Model(period).Update("documents_used", gorm.Expr("documents_used + 1")).Error; err != nil {
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

func recordUsageTx(
	tx *gorm.DB,
	in ReserveInput,
	cycle *database.SaasBillingCycle,
	period *database.SaasDocumentQuotaPeriod,
	sub *database.SaasSubscription,
	from string,
	pkgID *uint,
) error {
	meta := in.MetadataJSON
	if meta == "" {
		b, _ := json.Marshal(map[string]interface{}{"source": in.Source})
		meta = string(b)
	}
	row := &database.SaasElectronicDocumentUsage{
		TenantID:       in.TenantID,
		SubscriptionID: sub.ID,
		BillingCycleID: cycle.ID,
		QuotaPeriodID:  period.ID,
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
