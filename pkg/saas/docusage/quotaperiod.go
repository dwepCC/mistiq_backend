package docusage

import (
	"errors"
	"time"

	"tukifac/pkg/database"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// daysInMonth devuelve los días del mes indicado (día 0 del mes siguiente).
func daysInMonth(year int, month time.Month) int {
	return time.Date(year, month+1, 0, 0, 0, 0, 0, lima()).Day()
}

// addMonthsClamped suma n meses recortando el día al último válido del mes destino.
//
// time.AddDate normaliza hacia adelante (31/01 + 1 mes = 03/03), lo que rompería el
// aniversario de la suscripción. Aquí 31/01 + 1 mes = 28/02 (o 29 en bisiesto), que es
// el criterio habitual en suscripciones.
func addMonthsClamped(t time.Time, n int) time.Time {
	lt := t.In(lima())
	year, month, day := lt.Date()
	firstOfTarget := time.Date(year, month, 1, 0, 0, 0, 0, lima()).AddDate(0, n, 0)
	if last := daysInMonth(firstOfTarget.Year(), firstOfTarget.Month()); day > last {
		day = last
	}
	return time.Date(firstOfTarget.Year(), firstOfTarget.Month(), day,
		lt.Hour(), lt.Minute(), lt.Second(), lt.Nanosecond(), lima())
}

// quotaAnchor: día calendario en que arranca el primer período de cuota. Se ancla al
// inicio de la suscripción para que los períodos caigan en su mes aniversario.
func quotaAnchor(sub *database.SaasSubscription) time.Time {
	return calendarDateLima(sub.StartDate)
}

// maxQuotaPeriods es un tope de seguridad para el bucle de búsqueda: ninguna suscripción
// real supera unos pocos años, y evita que una fecha corrupta cuelgue el proceso.
const maxQuotaPeriods = 600

// QuotaPeriodBoundsAt devuelve el período de cuota que contiene `at`, junto con su
// índice (1 = primer mes de la suscripción).
//
// PeriodStart es inclusivo y PeriodEnd exclusivo. El último período se recorta al fin
// de la suscripción, de modo que los períodos cubren [StartDate, EndDate] sin huecos ni
// solapes, aunque la suscripción no dure un número exacto de meses (pasa al renovar
// antes de tiempo: la nueva arranca hoy pero termina N meses después del vencimiento
// anterior).
func QuotaPeriodBoundsAt(sub *database.SaasSubscription, at time.Time) (start, end time.Time, index int) {
	anchor := quotaAnchor(sub)
	subEnd := sub.EndDate.In(lima())
	target := at.In(lima())
	if target.Before(anchor) {
		target = anchor
	}

	lastN := lastQuotaPeriodIndex(sub)

	// Avanzar mientras el siguiente período ya haya empezado, sin pasar del último: el
	// tope evita que el fin-de-día de EndDate genere un período extra de unas horas.
	n := 0
	for n < lastN && !addMonthsClamped(anchor, n+1).After(target) {
		n++
	}

	start = addMonthsClamped(anchor, n)
	if n >= lastN {
		// El último período absorbe el resto de la suscripción.
		end = subEnd
	} else {
		end = addMonthsClamped(anchor, n+1)
	}
	if !end.After(start) {
		end = subEnd
	}
	return start, end, n + 1
}

// lastQuotaPeriodIndex: índice (base 0) del último período de la suscripción. Un período
// solo existe si empieza antes del último día de la suscripción.
func lastQuotaPeriodIndex(sub *database.SaasSubscription) int {
	anchor := quotaAnchor(sub)
	subEndDay := calendarDateLima(sub.EndDate)
	n := 0
	for n+1 < maxQuotaPeriods && addMonthsClamped(anchor, n+1).Before(subEndDay) {
		n++
	}
	return n
}

// TotalQuotaPeriods: cuántos meses de cuota cubre la suscripción (para "mes 2 de 6").
func TotalQuotaPeriods(sub *database.SaasSubscription) int {
	if sub == nil {
		return 0
	}
	return lastQuotaPeriodIndex(sub) + 1
}

// EnsureQuotaPeriod obtiene (o crea) el período de cuota vigente del tenant.
func EnsureQuotaPeriod(tenantID uint) (*database.SaasDocumentQuotaPeriod, *database.SaasSubscription, error) {
	if database.CentralDB == nil {
		return nil, nil, errors.New("BD central no disponible")
	}
	cycle, sub, err := CurrentBillingCycle(tenantID)
	if err != nil {
		return nil, sub, err
	}
	var out *database.SaasDocumentQuotaPeriod
	err = database.CentralDB.Transaction(func(tx *gorm.DB) error {
		p, e := ensureQuotaPeriodTx(tx, sub, cycle, nowLima())
		out = p
		return e
	})
	if err != nil {
		return nil, sub, err
	}
	return out, sub, nil
}

// ensureQuotaPeriodTx crea el período si falta. El UNIQUE (subscription_id, period_start)
// hace que dos emisiones simultáneas no puedan duplicarlo: la perdedora relee la fila.
func ensureQuotaPeriodTx(
	tx *gorm.DB,
	sub *database.SaasSubscription,
	cycle *database.SaasBillingCycle,
	at time.Time,
) (*database.SaasDocumentQuotaPeriod, error) {
	if sub == nil || cycle == nil {
		return nil, ErrNoActiveCycle
	}
	start, end, index := QuotaPeriodBoundsAt(sub, at)

	var period database.SaasDocumentQuotaPeriod
	err := tx.Where("subscription_id = ? AND period_start = ?", sub.ID, start).First(&period).Error
	if err == nil {
		syncPeriodQuotaFromPlanTx(tx, &period, sub.PlanID)
		return &period, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	var plan database.SaasPlan
	if err := tx.First(&plan, sub.PlanID).Error; err != nil {
		return nil, err
	}
	period = database.SaasDocumentQuotaPeriod{
		TenantID:             sub.TenantID,
		SubscriptionID:       sub.ID,
		BillingCycleID:       cycle.ID,
		PlanID:               sub.PlanID,
		PeriodStart:          start,
		PeriodEnd:            end,
		PeriodIndex:          index,
		IsUnlimitedDocuments: plan.IsUnlimitedDocuments,
		DocumentsLimit:       planLimitFromPlan(&plan),
		DocumentsUsed:        0,
	}
	if err := tx.Create(&period).Error; err != nil {
		if isDuplicateKey(err) {
			return &period, tx.Where("subscription_id = ? AND period_start = ?", sub.ID, start).
				First(&period).Error
		}
		return nil, err
	}
	return &period, nil
}

// syncPeriodQuotaFromPlanTx alinea el cupo del período con el plan vigente (por si el
// tenant cambió de plan a mitad de mes), sin bajarlo por debajo de lo ya consumido.
func syncPeriodQuotaFromPlanTx(tx *gorm.DB, period *database.SaasDocumentQuotaPeriod, planID uint) {
	var plan database.SaasPlan
	if tx.First(&plan, planID).Error != nil {
		return
	}
	limit := planLimitFromPlan(&plan)
	if !plan.IsUnlimitedDocuments && period.DocumentsUsed > limit {
		limit = period.DocumentsUsed
	}
	if period.IsUnlimitedDocuments == plan.IsUnlimitedDocuments && period.DocumentsLimit == limit {
		return
	}
	_ = tx.Model(period).Updates(map[string]interface{}{
		"is_unlimited_documents": plan.IsUnlimitedDocuments,
		"documents_limit":        limit,
	}).Error
	period.IsUnlimitedDocuments = plan.IsUnlimitedDocuments
	period.DocumentsLimit = limit
}

func lockQuotaPeriod(tx *gorm.DB, periodID uint) (*database.SaasDocumentQuotaPeriod, error) {
	var period database.SaasDocumentQuotaPeriod
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&period, periodID).Error; err != nil {
		return nil, err
	}
	return &period, nil
}
