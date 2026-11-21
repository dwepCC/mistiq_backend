package docusage

import (
	"testing"
	"time"

	"tukifac/pkg/database"
)

func seedAnchorFixture(t *testing.T) (*database.SaasSubscription, *database.SaasBillingCycle, *database.SaasBillingCycle) {
	t.Helper()
	db := setupQuotaDB(t)
	plan := database.SaasPlan{Name: "Micro", Price: 29.5, BillingCycle: "monthly", Active: true, MonthlyDocumentsLimit: 200}
	if err := db.Create(&plan).Error; err != nil {
		t.Fatal(err)
	}
	tenant := database.Tenant{Slug: "ancla", DBName: "t_ancla", Status: "active"}
	if err := db.Create(&tenant).Error; err != nil {
		t.Fatal(err)
	}
	// Suscripción creada el 03-sep; el operador acortó la vigencia al 24-sep y el tenant renovó
	// ese día un mes más (hasta el 24-oct). Es el caso del RUC 10329786345.
	sub := database.SaasSubscription{
		TenantID: tenant.ID, PlanID: plan.ID, BillingCycle: "monthly",
		StartDate: day(2026, time.September, 3), EndDate: endOfDay(day(2026, time.October, 24)),
		Status: database.SaasSubActive,
	}
	if err := db.Create(&sub).Error; err != nil {
		t.Fatal(err)
	}
	c1 := database.SaasBillingCycle{
		TenantID: tenant.ID, SubscriptionID: sub.ID, PlanID: plan.ID,
		PeriodStart: day(2026, time.September, 3), PeriodEnd: endOfDay(day(2026, time.September, 24)),
		DueDate: day(2026, time.September, 3), Amount: 29.5, Status: database.SaasInvoicePaid, MonthsCovered: 1,
	}
	c2 := database.SaasBillingCycle{
		TenantID: tenant.ID, SubscriptionID: sub.ID, PlanID: plan.ID,
		PeriodStart: endOfDay(day(2026, time.September, 24)), PeriodEnd: endOfDay(day(2026, time.October, 24)),
		DueDate: day(2026, time.September, 24), Amount: 29.5, Status: database.SaasInvoicePaid, MonthsCovered: 1,
	}
	if err := db.Create(&c1).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&c2).Error; err != nil {
		t.Fatal(err)
	}
	return &sub, &c1, &c2
}

// Renovar el plan debe devolver el cupo completo desde la fecha de la renovación, aunque el
// cupo anterior (anclado al registro) siga agotado.
func TestCuotaSeRenuevaConLaFechaDeRenovacion(t *testing.T) {
	sub, c1, c2 := seedAnchorFixture(t)
	db := database.CentralDB

	// Antes de renovar: cupo del ciclo 1, que se agota.
	p1 := ensurePeriodAt(t, db, sub, c1, day(2026, time.September, 10))
	if err := db.Model(p1).Update("documents_used", 200).Error; err != nil {
		t.Fatal(err)
	}

	// Ya renovado (24-sep): debe abrirse un período nuevo con cupo completo.
	p2 := ensurePeriodAt(t, db, sub, c2, day(2026, time.October, 1))
	if p2.ID == p1.ID {
		t.Fatal("la renovación reutilizó el período agotado: el cupo no se renovó")
	}
	if p2.DocumentsLimit != 200 || p2.DocumentsUsed != 0 {
		t.Fatalf("período renovado: límite %d usados %d, se esperaba 200/0", p2.DocumentsLimit, p2.DocumentsUsed)
	}
	if !p2.PeriodStart.Equal(day(2026, time.September, 24)) {
		t.Errorf("el cupo debe arrancar el día de la renovación, arranca %s", p2.PeriodStart)
	}
	if !p2.PeriodEnd.Equal(endOfDay(day(2026, time.October, 24))) {
		t.Errorf("el cupo debe terminar con el ciclo pagado, termina %s", p2.PeriodEnd)
	}
}

// Si el tenant ya gastó del plan dentro de la ventana nueva (el cupo se re-ancla a una fecha
// pasada), eso se descuenta: no recibe el cupo completo otra vez.
func TestCuotaReAnclajeDescuentaLoYaConsumido(t *testing.T) {
	sub, _, c2 := seedAnchorFixture(t)
	db := database.CentralDB

	for i := 0; i < 15; i++ {
		u := database.SaasElectronicDocumentUsage{
			TenantID: sub.TenantID, SubscriptionID: sub.ID, BillingCycleID: c2.ID,
			DocumentType: "receipt", DocumentID: uint(100 + i), ConsumedFrom: "plan_base",
			ConsumedAt: day(2026, time.September, 28),
		}
		if err := db.Create(&u).Error; err != nil {
			t.Fatal(err)
		}
	}
	p := ensurePeriodAt(t, db, sub, c2, day(2026, time.October, 1))
	if p.DocumentsUsed != 15 {
		t.Fatalf("usados = %d, se esperaban 15 ya consumidos dentro de la ventana", p.DocumentsUsed)
	}
}

// Renovación anticipada: mientras el ciclo nuevo no empieza, el cupo sigue siendo el del ciclo
// que corre; no se abre el nuevo antes de tiempo.
func TestCuotaRenovacionAnticipadaNoAdelantaElCupo(t *testing.T) {
	sub, c1, c2 := seedAnchorFixture(t)
	db := database.CentralDB

	p := ensurePeriodAt(t, db, sub, c2, day(2026, time.September, 20))
	if p.BillingCycleID != c1.ID {
		t.Fatalf("antes del 24-sep el cupo debe ser del ciclo 1 (id %d), es del ciclo %d", c1.ID, p.BillingCycleID)
	}
	if !p.PeriodStart.Equal(day(2026, time.September, 3)) {
		t.Errorf("período anticipado arranca %s, se esperaba 03-sep", p.PeriodStart)
	}
}

// Ajustar la vigencia mueve también el fin del cupo ya creado.
func TestCuotaSigueElAjusteDeVigencia(t *testing.T) {
	sub, c1, _ := seedAnchorFixture(t)
	db := database.CentralDB

	p := ensurePeriodAt(t, db, sub, c1, day(2026, time.September, 10))
	// El operador adelanta el fin del ciclo 1 al 20-sep.
	if err := db.Model(c1).Update("period_end", endOfDay(day(2026, time.September, 20))).Error; err != nil {
		t.Fatal(err)
	}
	c1.PeriodEnd = endOfDay(day(2026, time.September, 20))
	p = ensurePeriodAt(t, db, sub, c1, day(2026, time.September, 10))
	if !p.PeriodEnd.Equal(endOfDay(day(2026, time.September, 20))) {
		t.Fatalf("el fin del cupo quedó en %s, se esperaba 20-sep", p.PeriodEnd)
	}
}

// Transición: si el período anclado al registro todavía tiene más cupo que la ventana nueva,
// se respeta; nadie pierde cupo por el cambio (caso del RUC 10468149589).
func TestCuotaTransicionNoQuitaCupoYaDisponible(t *testing.T) {
	sub, _, c2 := seedAnchorFixture(t)
	db := database.CentralDB

	// Período del esquema anterior, recién abierto, sin consumo.
	legacy := database.SaasDocumentQuotaPeriod{
		TenantID: sub.TenantID, SubscriptionID: sub.ID, BillingCycleID: c2.ID, PlanID: sub.PlanID,
		PeriodStart: day(2026, time.October, 1), PeriodEnd: endOfDay(day(2026, time.October, 24)),
		PeriodIndex: 2, DocumentsLimit: 200, DocumentsUsed: 0,
	}
	if err := db.Create(&legacy).Error; err != nil {
		t.Fatal(err)
	}
	// Gastó 113 del plan desde la renovación del 24-sep: la ventana nueva le dejaría 87.
	for i := 0; i < 113; i++ {
		u := database.SaasElectronicDocumentUsage{
			TenantID: sub.TenantID, SubscriptionID: sub.ID, BillingCycleID: c2.ID,
			DocumentType: "receipt", DocumentID: uint(500 + i), ConsumedFrom: "plan_base",
			ConsumedAt: day(2026, time.September, 28),
		}
		if err := db.Create(&u).Error; err != nil {
			t.Fatal(err)
		}
	}
	p := ensurePeriodAt(t, db, sub, c2, day(2026, time.October, 2))
	if p.ID != legacy.ID {
		t.Fatalf("se creó un período nuevo y el tenant pierde cupo: disponible %d en vez de %d",
			p.DocumentsLimit-p.DocumentsUsed, legacy.DocumentsLimit-legacy.DocumentsUsed)
	}
}

// El relleno histórico no debe crear períodos anclados al registro en una suscripción que ya
// tiene los suyos (ni solapar el cupo vigente).
func TestBackfillNoTocaSuscripcionesConPeriodos(t *testing.T) {
	sub, c1, _ := seedAnchorFixture(t)
	db := database.CentralDB
	ensurePeriodAt(t, db, sub, c1, day(2026, time.September, 10))

	var antes int64
	db.Model(&database.SaasDocumentQuotaPeriod{}).Where("subscription_id = ?", sub.ID).Count(&antes)
	if _, err := BackfillDocumentQuotaPeriods(); err != nil {
		t.Fatal(err)
	}
	var despues int64
	db.Model(&database.SaasDocumentQuotaPeriod{}).Where("subscription_id = ?", sub.ID).Count(&despues)
	if despues != antes {
		t.Fatalf("el relleno creó períodos extra: %d → %d", antes, despues)
	}
}

// Cobro adelantado sin ciclo pagado que cubra hoy: no se abre la ventana futura.
func TestCuotaNoAbreVentanaFuturaSinCicloQueCubraHoy(t *testing.T) {
	sub, c1, c2 := seedAnchorFixture(t)
	db := database.CentralDB
	// Solo queda el ciclo adelantado: el primero deja de contar como pagado.
	if err := db.Model(c1).Update("status", database.SaasInvoiceRejected).Error; err != nil {
		t.Fatal(err)
	}
	c2.PeriodStart = endOfDay(day(2026, time.October, 26))
	c2.PeriodEnd = endOfDay(day(2026, time.November, 26))
	if err := db.Save(c2).Error; err != nil {
		t.Fatal(err)
	}
	sub.EndDate = c2.PeriodEnd

	p := ensurePeriodAt(t, db, sub, c2, day(2026, time.October, 1))
	if p.PeriodStart.After(day(2026, time.October, 1)) {
		t.Fatalf("se abrió una ventana futura (%s): el cupo se consumiría antes de tiempo", p.PeriodStart)
	}
}
