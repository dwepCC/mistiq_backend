package docusage

import (
	"errors"
	"log/slog"
	"strings"

	"tukifac/pkg/database"
	"tukifac/pkg/logger"
	"tukifac/pkg/saas/pricing"

	"gorm.io/gorm"
)

// MigrateBillingCycleConstraints deduplica ciclos, aplica UNIQUE y backfill de cupo documentos.
// Idempotente: seguro ejecutar en cada migrate-central.
func MigrateBillingCycleConstraints() error {
	if database.CentralDB == nil {
		return nil
	}
	if err := deduplicateBillingCycles(); err != nil {
		return err
	}
	if err := ensureBillingCycleUniqueIndex(); err != nil {
		return err
	}
	n, err := BackfillBillingCycleDocumentQuota()
	if err != nil {
		return err
	}
	if n > 0 {
		logger.L.Info("saas_billing_cycles_document_quota_backfill",
			slog.Int("rows_updated", n),
		)
	}
	// Reconstruye los períodos mensuales de cuota de las suscripciones ya existentes.
	if _, err := BackfillDocumentQuotaPeriods(); err != nil {
		return err
	}
	return nil
}

// BackfillBillingCycleDocumentQuota sincroniza documents_limit desde el plan sin resetear documents_used.
func BackfillBillingCycleDocumentQuota() (int, error) {
	if database.CentralDB == nil {
		return 0, nil
	}
	var cycles []database.SaasBillingCycle
	if err := database.CentralDB.Find(&cycles).Error; err != nil {
		return 0, err
	}
	updated := 0
	for i := range cycles {
		c := &cycles[i]
		var plan database.SaasPlan
		if database.CentralDB.First(&plan, c.PlanID).Error != nil {
			continue
		}
		limit := planLimitFromPlan(&plan)
		if c.IsUnlimitedDocuments == plan.IsUnlimitedDocuments &&
			c.DocumentsLimit == limit {
			continue
		}
		// No bloquear tenants que ya consumieron más que el límite nuevo del plan.
		if !plan.IsUnlimitedDocuments && c.DocumentsUsed > limit {
			limit = c.DocumentsUsed
		}
		if err := database.CentralDB.Model(c).Updates(map[string]interface{}{
			"is_unlimited_documents": plan.IsUnlimitedDocuments,
			"documents_limit":        limit,
		}).Error; err != nil {
			return updated, err
		}
		updated++
	}
	return updated, nil
}

func deduplicateBillingCycles() error {
	mig := database.CentralDB.Migrator()
	if !mig.HasTable(&database.SaasBillingCycle{}) {
		return nil
	}
	// Conservar el ciclo con mayor documents_used (o menor id si empatan).
	res := database.CentralDB.Exec(`
		DELETE c1 FROM saas_billing_cycles c1
		INNER JOIN saas_billing_cycles c2
			ON c1.subscription_id = c2.subscription_id
			AND c1.period_end = c2.period_end
			AND (
				c1.documents_used < c2.documents_used
				OR (c1.documents_used = c2.documents_used AND c1.id > c2.id)
			)
	`)
	return res.Error
}

func ensureBillingCycleUniqueIndex() error {
	mig := database.CentralDB.Migrator()
	if !mig.HasTable(&database.SaasBillingCycle{}) {
		return nil
	}
	const idx = "idx_billing_cycle_sub_period"
	if mig.HasIndex(&database.SaasBillingCycle{}, idx) {
		return nil
	}
	return mig.CreateIndex(&database.SaasBillingCycle{}, idx)
}

// EnsureBillingCycleForSubscription obtiene o crea ciclo (TX + UNIQUE subscription_id/period_end).
func EnsureBillingCycleForSubscription(sub *database.SaasSubscription) (*database.SaasBillingCycle, error) {
	if sub == nil {
		return nil, ErrNoActiveCycle
	}
	if database.CentralDB == nil {
		return nil, errors.New("BD central no disponible")
	}

	var out database.SaasBillingCycle
	err := database.CentralDB.Transaction(func(tx *gorm.DB) error {
		var locked database.SaasSubscription
		if err := tx.First(&locked, sub.ID).Error; err != nil {
			return err
		}
		if err := tx.Where("subscription_id = ? AND period_end = ?", locked.ID, locked.EndDate).
			First(&out).Error; err == nil {
			return nil
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}

		var plan database.SaasPlan
		if err := tx.First(&plan, locked.PlanID).Error; err != nil {
			return err
		}
		reconnFee, _ := LoadSettingsInTx(tx)
		limit := planLimitFromPlan(&plan)
		// Mismo cálculo que ensureBillingCycleTx: precio mensual × meses contratados, menos el
		// descuento pactado en la suscripción.
		amounts := pricing.ComputeCycleAmounts(
			plan.Price,
			pricing.BillableMonths(locked.BilledMonths, locked.StartDate, locked.EndDate, lima()),
			pricing.Discount{Type: locked.DiscountType, Value: locked.DiscountValue},
		)
		out = database.SaasBillingCycle{
			TenantID: locked.TenantID, SubscriptionID: locked.ID, PlanID: locked.PlanID,
			// Prepago: la deuda vence al INICIO del período, igual que ensureBillingCycleTx y
			// que la emisión manual. Antes aquí vencía al final, así que un mismo cobro tenía
			// due_date distinto según quién lo hubiera creado y la ventana de pago no era fiable.
			PeriodStart: locked.StartDate, PeriodEnd: locked.EndDate, DueDate: locked.StartDate,
			Amount: amounts.Net, GrossAmount: amounts.Gross, MonthsCovered: amounts.Months,
			DiscountType: amounts.Discount.Type, DiscountValue: amounts.Discount.Value,
			ReconnectionFee: reconnFee, Currency: "PEN",
			Status: database.SaasInvoicePending,
			IsUnlimitedDocuments: plan.IsUnlimitedDocuments, DocumentsLimit: limit, DocumentsUsed: 0,
		}
		if err := tx.Create(&out).Error; err != nil {
			if isDuplicateKey(err) {
				return tx.Where("subscription_id = ? AND period_end = ?", locked.ID, locked.EndDate).
					First(&out).Error
			}
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	_ = SyncCycleDocumentQuotaFromPlan(&out, sub.PlanID)
	_ = database.CentralDB.First(&out, out.ID).Error
	return &out, nil
}

func isDuplicateKey(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "duplicate") || strings.Contains(msg, "unique constraint") ||
		strings.Contains(msg, "1062") || strings.Contains(msg, "2067")
}

// LoadSettingsInTx evita import cycle con saas; lee reconnection fee mínima.
func LoadSettingsInTx(tx *gorm.DB) (reconnectionFee float64, err error) {
	var row database.SaasPlatformSettings
	if e := tx.First(&row, 1).Error; e != nil {
		return 50, e
	}
	return row.ReconnectionFee, nil
}

func planLimitFromPlan(plan *database.SaasPlan) int {
	if plan == nil || plan.IsUnlimitedDocuments {
		return 0
	}
	return plan.MonthlyDocumentsLimit
}

// SyncCycleDocumentQuotaFromPlan alinea documents_limit del ciclo con el plan vigente (sin bajar por debajo de used).
func SyncCycleDocumentQuotaFromPlan(cycle *database.SaasBillingCycle, planID uint) error {
	if cycle == nil {
		return nil
	}
	var plan database.SaasPlan
	if database.CentralDB.First(&plan, planID).Error != nil {
		return nil
	}
	limit := planLimitFromPlan(&plan)
	if !plan.IsUnlimitedDocuments && cycle.DocumentsUsed > limit {
		limit = cycle.DocumentsUsed
	}
	if cycle.IsUnlimitedDocuments == plan.IsUnlimitedDocuments && cycle.DocumentsLimit == limit {
		return nil
	}
	return database.CentralDB.Model(cycle).Updates(map[string]interface{}{
		"is_unlimited_documents": plan.IsUnlimitedDocuments,
		"documents_limit":        limit,
	}).Error
}

