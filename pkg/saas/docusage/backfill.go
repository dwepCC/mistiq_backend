package docusage

import (
	"log/slog"
	"time"

	"tukifac/pkg/database"
	"tukifac/pkg/logger"

	"gorm.io/gorm"
)

// BackfillQuotaPeriodsResult resumen para el log de migración.
type BackfillQuotaPeriodsResult struct {
	Subscriptions  int
	PeriodsCreated int
	UsagesLinked   int
}

// BackfillDocumentQuotaPeriods crea los períodos mensuales de cuota que faltan para las
// suscripciones vigentes y les asigna el consumo REAL de cada mes.
//
// Contexto: antes, una suscripción de 6 meses tenía un único contador para todo el
// período, así que el cupo mensual del plan se repartía entre los 6 meses. Al separar
// cuota y cobro hay que reconstruir el pasado, y se puede hacer con exactitud porque
// saas_electronic_document_usages guarda consumed_at de cada documento: basta contar
// cuántos cayeron dentro de cada ventana mensual.
//
// Solo se cuentan los consumos con consumed_from = "plan_base": los que salieron de un
// paquete comprado no gastan cupo del plan.
//
// Es idempotente: un período que ya existe no se toca, así que puede correr en cada
// migrate-central sin alterar contadores en curso.
func BackfillDocumentQuotaPeriods() (BackfillQuotaPeriodsResult, error) {
	var res BackfillQuotaPeriodsResult
	if database.CentralDB == nil {
		return res, nil
	}
	if !database.CentralDB.Migrator().HasTable(&database.SaasDocumentQuotaPeriod{}) {
		return res, nil
	}

	var subs []database.SaasSubscription
	if err := database.CentralDB.
		Where("status NOT IN ?", []string{database.SaasSubCancelled}).
		Find(&subs).Error; err != nil {
		return res, err
	}

	now := nowLima()
	for i := range subs {
		sub := &subs[i]

		var cycle database.SaasBillingCycle
		if database.CentralDB.Where("subscription_id = ?", sub.ID).
			Order("id desc").First(&cycle).Error != nil {
			// Sin ciclo de cobro no hay de dónde colgar el período; lo creará
			// EnsureQuotaPeriod en la primera emisión.
			continue
		}
		var plan database.SaasPlan
		if database.CentralDB.First(&plan, sub.PlanID).Error != nil {
			continue
		}

		created, linked, err := backfillSubscriptionPeriods(sub, &cycle, &plan, now)
		if err != nil {
			return res, err
		}
		res.Subscriptions++
		res.PeriodsCreated += created
		res.UsagesLinked += linked
	}

	if res.PeriodsCreated > 0 || res.UsagesLinked > 0 {
		logger.L.Info("saas_document_quota_periods_backfill",
			slog.Int("subscriptions", res.Subscriptions),
			slog.Int("periods_created", res.PeriodsCreated),
			slog.Int("usages_linked", res.UsagesLinked),
		)
	}
	return res, nil
}

// backfillSubscriptionPeriods recorre los meses de una suscripción, desde su inicio
// hasta el mes en curso, y crea los que falten con el consumo real de cada ventana.
func backfillSubscriptionPeriods(
	sub *database.SaasSubscription,
	cycle *database.SaasBillingCycle,
	plan *database.SaasPlan,
	now time.Time,
) (created int, linked int, err error) {
	lastN := lastQuotaPeriodIndex(sub)
	_, _, currentIndex := QuotaPeriodBoundsAt(sub, now)

	// No crear meses futuros: se abren solos al llegar su fecha.
	upTo := currentIndex - 1
	if upTo > lastN {
		upTo = lastN
	}

	for n := 0; n <= upTo; n++ {
		anchor := quotaAnchor(sub)
		start := addMonthsClamped(anchor, n)
		end := addMonthsClamped(anchor, n+1)
		if n >= lastN || end.After(sub.EndDate.In(lima())) {
			end = sub.EndDate.In(lima())
		}

		var existing database.SaasDocumentQuotaPeriod
		e := database.CentralDB.Where("subscription_id = ? AND period_start = ?", sub.ID, start).
			First(&existing).Error
		if e == nil {
			continue
		}
		if e != gorm.ErrRecordNotFound {
			return created, linked, e
		}

		var used int64
		if e := database.CentralDB.Model(&database.SaasElectronicDocumentUsage{}).
			Where("subscription_id = ? AND consumed_from = ?", sub.ID, "plan_base").
			Where("consumed_at >= ? AND consumed_at < ?", start, end).
			Count(&used).Error; e != nil {
			return created, linked, e
		}

		limit := planLimitFromPlan(plan)
		period := database.SaasDocumentQuotaPeriod{
			TenantID:             sub.TenantID,
			SubscriptionID:       sub.ID,
			BillingCycleID:       cycle.ID,
			PlanID:               sub.PlanID,
			PeriodStart:          start,
			PeriodEnd:            end,
			PeriodIndex:          n + 1,
			IsUnlimitedDocuments: plan.IsUnlimitedDocuments,
			DocumentsLimit:       limit,
			DocumentsUsed:        int(used),
		}
		if e := database.CentralDB.Create(&period).Error; e != nil {
			if isDuplicateKey(e) {
				continue
			}
			return created, linked, e
		}
		created++

		// Enlazar los consumos de esa ventana con su período, para poder auditar
		// después cuántos documentos se emitieron en cada mes.
		res := database.CentralDB.Model(&database.SaasElectronicDocumentUsage{}).
			Where("subscription_id = ? AND quota_period_id = 0", sub.ID).
			Where("consumed_at >= ? AND consumed_at < ?", start, end).
			Update("quota_period_id", period.ID)
		if res.Error != nil {
			return created, linked, res.Error
		}
		linked += int(res.RowsAffected)
	}
	return created, linked, nil
}
