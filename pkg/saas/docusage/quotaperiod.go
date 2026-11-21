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
	return quotaBoundsFor(quotaAnchor(sub), sub.EndDate, at)
}

// CycleQuotaPeriodBoundsAt: igual que QuotaPeriodBoundsAt pero anclado al INICIO DEL CICLO DE
// COBRO (la fecha de la renovación), no al inicio de la suscripción. Es lo que usa el consumo.
//
// Anclar a la fecha de registro desfasaba el cupo cuando la renovación no caía justo en el
// aniversario —vigencia ajustada a mano, renovación anticipada o tardía—: el tenant pagaba un
// mes nuevo pero el cupo seguía corriendo con el calendario del primer día de su suscripción,
// y podía quedarse sin comprobantes recién renovado.
func CycleQuotaPeriodBoundsAt(cycle *database.SaasBillingCycle, at time.Time) (start, end time.Time, index int) {
	return quotaBoundsFor(calendarDateLima(cycle.PeriodStart), cycle.PeriodEnd, at)
}

// TotalCycleQuotaPeriods: cuántos meses de cuota cubre el ciclo de cobro ("mes 2 de 6").
func TotalCycleQuotaPeriods(cycle *database.SaasBillingCycle) int {
	if cycle == nil {
		return 0
	}
	return lastQuotaIndexFor(calendarDateLima(cycle.PeriodStart), cycle.PeriodEnd) + 1
}

func quotaBoundsFor(anchor, endDate, at time.Time) (start, end time.Time, index int) {
	subEnd := endDate.In(lima())
	target := at.In(lima())
	if target.Before(anchor) {
		target = anchor
	}

	lastN := lastQuotaIndexFor(anchor, endDate)

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
	return lastQuotaIndexFor(quotaAnchor(sub), sub.EndDate)
}

func lastQuotaIndexFor(anchor, endDate time.Time) int {
	subEndDay := calendarDateLima(endDate)
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

// quotaCycleAt devuelve el ciclo de cobro que cubre la fecha at. Normalmente es el ciclo vigente, pero
// tras una renovación anticipada el ciclo nuevo todavía no empezó: mientras tanto el cupo sigue
// siendo el del ciclo que ya está corriendo.
func quotaCycleAt(tx *gorm.DB, sub *database.SaasSubscription, cycle *database.SaasBillingCycle, at time.Time) *database.SaasBillingCycle {
	if !calendarDateLima(cycle.PeriodStart).After(at.In(lima())) {
		return cycle
	}
	var prev database.SaasBillingCycle
	err := tx.Where("subscription_id = ? AND status = ? AND period_start <= ? AND period_end >= ?",
		sub.ID, database.SaasInvoicePaid, at, at).
		Order("period_start desc").First(&prev).Error
	if err != nil {
		return cycle
	}
	return &prev
}

// ensureQuotaPeriodTx crea el período si falta. El UNIQUE (subscription_id, period_start)
// hace que dos emisiones simultáneas no puedan duplicarlo: la perdedora relee la fila.
//
// Los períodos se calculan desde el inicio del ciclo de cobro (fecha de renovación), no desde el
// de la suscripción: ver CycleQuotaPeriodBoundsAt.
func ensureQuotaPeriodTx(
	tx *gorm.DB,
	sub *database.SaasSubscription,
	cycle *database.SaasBillingCycle,
	at time.Time,
) (*database.SaasDocumentQuotaPeriod, error) {
	if sub == nil || cycle == nil {
		return nil, ErrNoActiveCycle
	}
	cycle = quotaCycleAt(tx, sub, cycle, at)
	start, end, index := CycleQuotaPeriodBoundsAt(cycle, at)
	if calendarDateLima(cycle.PeriodStart).After(at.In(lima())) {
		// Ningún ciclo pagado cubre hoy y el vigente empieza más adelante (cobro adelantado, o el
		// ciclo anterior quedó rechazado/en otra suscripción): abrir ya la ventana futura haría
		// consumir su cupo antes de tiempo. Mientras tanto vale el cálculo desde el registro.
		start, end, index = QuotaPeriodBoundsAt(sub, at)
	}

	var period database.SaasDocumentQuotaPeriod
	err := tx.Where("subscription_id = ? AND period_start = ?", sub.ID, start).First(&period).Error
	if err == nil {
		syncPeriodQuotaFromPlanTx(tx, &period, sub.PlanID)
		syncPeriodWindowTx(tx, &period, cycle.ID, end, index)
		return &period, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}

	var plan database.SaasPlan
	if err := tx.First(&plan, sub.PlanID).Error; err != nil {
		return nil, err
	}

	// Lo ya consumido del plan dentro de esta ventana cuenta: pasa cuando el cupo se re-ancla a la
	// renovación y la ventana nueva arranca antes de hoy. Sin esto el tenant recibiría el cupo
	// completo otra vez por documentos que ya gastó desde esa fecha.
	var alreadyUsed int64
	tx.Model(&database.SaasElectronicDocumentUsage{}).
		Where("subscription_id = ? AND consumed_from = ? AND consumed_at >= ? AND consumed_at < ?",
			sub.ID, "plan_base", start, end).
		Count(&alreadyUsed)

	// Transición: un período anclado al registro (el esquema anterior) que ya cubre hoy se
	// respeta mientras le quede igual o más cupo que el que daría la ventana nueva. Así el
	// re-anclaje a la renovación corrige a quien se quedó sin cupo, pero nadie pierde el que ya
	// tenía disponible por haber desplegado este cambio.
	if !plan.IsUnlimitedDocuments {
		var legacy database.SaasDocumentQuotaPeriod
		if tx.Where("subscription_id = ? AND period_start <= ? AND period_end > ?", sub.ID, at, at).
			Order("period_start desc").First(&legacy).Error == nil &&
			!legacy.IsUnlimitedDocuments &&
			legacy.DocumentsLimit-legacy.DocumentsUsed >= planLimitFromPlan(&plan)-int(alreadyUsed) {
			syncPeriodQuotaFromPlanTx(tx, &legacy, sub.PlanID)
			return &legacy, nil
		}
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
		DocumentsUsed:        int(alreadyUsed),
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

// syncPeriodWindowTx mantiene el fin, el índice y el ciclo del período alineados con el ciclo de
// cobro: si el operador ajusta la vigencia, el cupo debe renovarse en la fecha nueva y no en la
// que quedó guardada.
func syncPeriodWindowTx(tx *gorm.DB, period *database.SaasDocumentQuotaPeriod, cycleID uint, end time.Time, index int) {
	if period.PeriodEnd.Equal(end) && period.PeriodIndex == index && period.BillingCycleID == cycleID {
		return
	}
	if tx.Model(period).Updates(map[string]interface{}{
		"period_end":       end,
		"period_index":     index,
		"billing_cycle_id": cycleID,
	}).Error == nil {
		period.PeriodEnd = end
		period.PeriodIndex = index
		period.BillingCycleID = cycleID
	}
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
