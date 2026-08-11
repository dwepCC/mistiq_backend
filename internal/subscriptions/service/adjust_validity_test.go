package service

import (
	"path/filepath"
	"testing"
	"time"

	"tukifac/pkg/database"
	"tukifac/pkg/saas"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func setupAdjustValidityDB(t *testing.T) *gorm.DB {
	t.Helper()
	// En el directorio temporal del test, no en el paquete: en Windows el archivo sigue
	// bloqueado hasta cerrar la conexión y quedaba un .db suelto dentro del repo.
	dsn := "file:" + filepath.Join(t.TempDir(), "adjust_validity.db") + "?_busy_timeout=15000"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	database.CentralDB = db
	if err := db.AutoMigrate(
		&database.Tenant{},
		&database.SaasPlan{},
		&database.SaasSubscription{},
		&database.SaasBillingCycle{},
		&database.SaasPlatformSettings{},
		&database.SaasSubscriptionEvent{},
		&database.AuditLog{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func lima(y int, m time.Month, d int) time.Time {
	return saas.EndOfDayLima(time.Date(y, m, d, 0, 0, 0, 0, saas.LimaLocation()))
}

// seedSubscriptionWithCycles arma el escenario real: suscripción vigente hasta `end` con los
// ciclos indicados abiertos.
func seedSubscriptionWithCycles(t *testing.T, db *gorm.DB, end time.Time, periodEnds ...time.Time) *database.SaasSubscription {
	t.Helper()
	tenant := database.Tenant{Name: "ACME", Slug: "acme", Status: database.TenantStatusActive}
	if err := db.Create(&tenant).Error; err != nil {
		t.Fatalf("crear tenant: %v", err)
	}
	plan := database.SaasPlan{Name: "PREMIUM", Price: 99, BillingCycle: "monthly", Active: true}
	if err := db.Create(&plan).Error; err != nil {
		t.Fatalf("crear plan: %v", err)
	}
	sub := database.SaasSubscription{
		TenantID: tenant.ID, PlanID: plan.ID, Status: database.SaasSubActive,
		BillingCycle: "monthly",
		StartDate:    time.Date(2026, 7, 20, 0, 0, 0, 0, saas.LimaLocation()),
		EndDate:      end,
	}
	if err := db.Create(&sub).Error; err != nil {
		t.Fatalf("crear suscripción: %v", err)
	}
	for _, pe := range periodEnds {
		c := database.SaasBillingCycle{
			TenantID: tenant.ID, SubscriptionID: sub.ID, PlanID: plan.ID,
			PeriodStart: sub.StartDate, PeriodEnd: pe, DueDate: pe,
			Amount: 99, Currency: "PEN", Status: database.SaasInvoicePending,
		}
		if err := db.Create(&c).Error; err != nil {
			t.Fatalf("crear ciclo %v: %v", pe, err)
		}
	}
	return &sub
}

// Con dos ciclos abiertos, el realineo tocaba los dos y MySQL abortaba por el índice único
// (subscription_id, period_end). Debe moverse solo el del período vigente.
func TestAdjustValidityConDosCiclosAbiertos(t *testing.T) {
	db := setupAdjustValidityDB(t)
	oldEnd := lima(2026, 9, 30)
	nextEnd := lima(2026, 10, 30)
	sub := seedSubscriptionWithCycles(t, db, oldEnd, oldEnd, nextEnd)

	newEnd := lima(2026, 8, 5)
	svc := NewSubscriptionService()
	if _, err := svc.AdjustValidity(sub.ID, 1, "127.0.0.1", AdjustValidityInput{
		EndDate: "2026-08-05", Reason: "corrección de prueba",
	}); err != nil {
		t.Fatalf("AdjustValidity devolvió error: %v", err)
	}

	var updated database.SaasSubscription
	if err := db.First(&updated, sub.ID).Error; err != nil {
		t.Fatalf("releer suscripción: %v", err)
	}
	if !updated.EndDate.Equal(newEnd) {
		t.Errorf("end_date = %v, se esperaba %v", updated.EndDate, newEnd)
	}

	var cycles []database.SaasBillingCycle
	db.Where("subscription_id = ?", sub.ID).Order("period_end asc").Find(&cycles)
	if len(cycles) != 2 {
		t.Fatalf("se esperaban 2 ciclos, hay %d", len(cycles))
	}
	if !cycles[0].PeriodEnd.Equal(newEnd) || !cycles[0].DueDate.Equal(newEnd) {
		t.Errorf("el ciclo vigente no se realineó: period_end=%v due_date=%v", cycles[0].PeriodEnd, cycles[0].DueDate)
	}
	if !cycles[1].PeriodEnd.Equal(nextEnd) {
		t.Errorf("el ciclo del período siguiente se movió: period_end=%v, se esperaba %v", cycles[1].PeriodEnd, nextEnd)
	}
}

// Mover la vigencia justo al cierre de otro ciclo existente duplicaría el cobro de ese
// período: debe rechazarse con un mensaje accionable y sin dejar nada a medias.
func TestAdjustValidityRechazaColisionConOtroCiclo(t *testing.T) {
	db := setupAdjustValidityDB(t)
	oldEnd := lima(2026, 9, 30)
	nextEnd := lima(2026, 10, 30)
	sub := seedSubscriptionWithCycles(t, db, oldEnd, oldEnd, nextEnd)

	_, err := NewSubscriptionService().AdjustValidity(sub.ID, 1, "127.0.0.1", AdjustValidityInput{
		EndDate: "2026-10-30", Reason: "colisión",
	})
	if err == nil {
		t.Fatal("se esperaba error por colisión de ciclos, no hubo ninguno")
	}

	var updated database.SaasSubscription
	db.First(&updated, sub.ID)
	if !updated.EndDate.Equal(oldEnd) {
		t.Errorf("end_date cambió pese al error: %v (debía seguir en %v)", updated.EndDate, oldEnd)
	}
}

// Caso corriente (un solo ciclo abierto): sigue realineándose como antes.
func TestAdjustValidityConUnSoloCiclo(t *testing.T) {
	db := setupAdjustValidityDB(t)
	oldEnd := lima(2026, 9, 30)
	sub := seedSubscriptionWithCycles(t, db, oldEnd, oldEnd)

	newEnd := lima(2026, 8, 5)
	if _, err := NewSubscriptionService().AdjustValidity(sub.ID, 1, "127.0.0.1", AdjustValidityInput{
		EndDate: "2026-08-05", Reason: "corrección",
	}); err != nil {
		t.Fatalf("AdjustValidity devolvió error: %v", err)
	}

	var cycle database.SaasBillingCycle
	db.Where("subscription_id = ?", sub.ID).First(&cycle)
	if !cycle.PeriodEnd.Equal(newEnd) || !cycle.DueDate.Equal(newEnd) {
		t.Errorf("ciclo no realineado: period_end=%v due_date=%v, se esperaba %v", cycle.PeriodEnd, cycle.DueDate, newEnd)
	}
}

// Un tenant que renueva varias veces acumula suscripciones, pero solo la última gobierna.
// La lista del panel recalculaba el estado efectivo de todas y pintaba «Activa» cualquiera
// cuyo end_date siguiera en el futuro, así que DEMO aparecía con cuatro activas a la vez.
func TestListMarcaUnaSolaSuscripcionVigente(t *testing.T) {
	db := setupAdjustValidityDB(t)

	tenant := database.Tenant{Name: "DEMO", Slug: "demo", Status: database.TenantStatusActive}
	if err := db.Create(&tenant).Error; err != nil {
		t.Fatalf("crear tenant: %v", err)
	}
	plan := database.SaasPlan{Name: "PREMIUM", Price: 99, BillingCycle: "monthly", Active: true}
	if err := db.Create(&plan).Error; err != nil {
		t.Fatalf("crear plan: %v", err)
	}

	// Tres renovaciones: las dos primeras quedaron expiradas al renovar, pero con end_date
	// todavía futura (renovación anticipada). La tercera es la vigente.
	seed := []struct {
		status  string
		end     time.Time
		created time.Time
	}{
		{database.SaasSubExpired, lima(2026, 8, 30), time.Date(2026, 7, 20, 16, 0, 0, 0, time.UTC)},
		{database.SaasSubExpired, lima(2026, 10, 30), time.Date(2026, 8, 4, 12, 21, 0, 0, time.UTC)},
		{database.SaasSubActive, lima(2026, 11, 1), time.Date(2026, 8, 4, 12, 23, 0, 0, time.UTC)},
	}
	var vigenteID uint
	for _, s := range seed {
		sub := database.SaasSubscription{
			TenantID: tenant.ID, PlanID: plan.ID, BillingCycle: "monthly",
			StartDate: time.Date(2026, 7, 20, 0, 0, 0, 0, saas.LimaLocation()),
			EndDate:   s.end, Status: s.status, CreatedAt: s.created,
		}
		if err := db.Create(&sub).Error; err != nil {
			t.Fatalf("crear suscripción: %v", err)
		}
		if s.status == database.SaasSubActive {
			vigenteID = sub.ID
		}
	}

	rows, total, err := NewSubscriptionService().List(SubscriptionListParams{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 3 {
		t.Fatalf("total = %d, se esperaban 3", total)
	}

	activas := 0
	for _, r := range rows {
		if r.Status == database.SaasSubActive {
			activas++
		}
		if r.IsCurrent != (r.ID == vigenteID) {
			t.Errorf("suscripción %d: is_current=%v (vigente real: %d)", r.ID, r.IsCurrent, vigenteID)
		}
		if !r.IsCurrent && (r.DaysOverdue != 0 || r.DaysInGrace != 0) {
			t.Errorf("suscripción histórica %d reporta mora/gracia: %d/%d", r.ID, r.DaysOverdue, r.DaysInGrace)
		}
	}
	if activas != 1 {
		t.Errorf("se esperaba 1 suscripción activa, la lista devolvió %d", activas)
	}
}

// Las acciones del panel afectan al tenant, no a la fila. Sobre una suscripción histórica
// «suspender» dejaba al tenant sin acceso pese a tener una vigente al día, y «reactivar»
// creaba otra suscripción expirando la vigente.
func TestAccionesRechazadasSobreSuscripcionHistorica(t *testing.T) {
	db := setupAdjustValidityDB(t)

	tenant := database.Tenant{Name: "DEMO", Slug: "demo", Status: database.TenantStatusActive}
	if err := db.Create(&tenant).Error; err != nil {
		t.Fatalf("crear tenant: %v", err)
	}
	plan := database.SaasPlan{Name: "PREMIUM", Price: 99, BillingCycle: "monthly", Active: true}
	if err := db.Create(&plan).Error; err != nil {
		t.Fatalf("crear plan: %v", err)
	}

	historica := database.SaasSubscription{
		TenantID: tenant.ID, PlanID: plan.ID, BillingCycle: "monthly",
		StartDate: time.Date(2026, 7, 20, 0, 0, 0, 0, saas.LimaLocation()),
		EndDate:   lima(2026, 8, 30), Status: database.SaasSubExpired,
		CreatedAt: time.Date(2026, 7, 20, 16, 0, 0, 0, time.UTC),
	}
	if err := db.Create(&historica).Error; err != nil {
		t.Fatalf("crear histórica: %v", err)
	}
	vigente := database.SaasSubscription{
		TenantID: tenant.ID, PlanID: plan.ID, BillingCycle: "monthly",
		StartDate: time.Date(2026, 8, 4, 0, 0, 0, 0, saas.LimaLocation()),
		EndDate:   lima(2026, 11, 1), Status: database.SaasSubActive,
		CreatedAt: time.Date(2026, 8, 4, 12, 23, 0, 0, time.UTC),
	}
	if err := db.Create(&vigente).Error; err != nil {
		t.Fatalf("crear vigente: %v", err)
	}

	svc := NewSubscriptionService()

	if err := svc.Suspend(historica.ID, "prueba"); err != ErrSubscriptionNotCurrent {
		t.Errorf("Suspend sobre histórica: err = %v, se esperaba ErrSubscriptionNotCurrent", err)
	}
	if err := svc.Reactivate(historica.ID, 1); err != ErrSubscriptionNotCurrent {
		t.Errorf("Reactivate sobre histórica: err = %v, se esperaba ErrSubscriptionNotCurrent", err)
	}
	if _, err := svc.AdjustValidity(historica.ID, 1, "127.0.0.1", AdjustValidityInput{
		EndDate: "2026-12-01", Reason: "prueba",
	}); err != ErrSubscriptionNotCurrent {
		t.Errorf("AdjustValidity sobre histórica: err = %v, se esperaba ErrSubscriptionNotCurrent", err)
	}

	// El tenant y la suscripción vigente quedan intactos.
	var t2 database.Tenant
	db.First(&t2, tenant.ID)
	if t2.Status != database.TenantStatusActive {
		t.Errorf("el tenant quedó en %q; debía seguir activo", t2.Status)
	}
	var count int64
	db.Model(&database.SaasSubscription{}).Where("tenant_id = ?", tenant.ID).Count(&count)
	if count != 2 {
		t.Errorf("se crearon suscripciones extra: hay %d, se esperaban 2", count)
	}
	var v2 database.SaasSubscription
	db.First(&v2, vigente.ID)
	if v2.Status != database.SaasSubActive {
		t.Errorf("la vigente quedó en %q; debía seguir activa", v2.Status)
	}

	// Sobre la vigente las acciones sí proceden.
	if err := svc.Suspend(vigente.ID, "prueba"); err != nil {
		t.Errorf("Suspend sobre la vigente falló: %v", err)
	}
}

// Renovar el MISMO plan debe alargar la suscripción vigente, no crear otra: una fila nueva
// dejaba el cobro impago anterior suelto y solapado con el período nuevo (mismos días
// facturados dos veces) y llenaba el panel de «históricas» en cada renovación.
func TestRenovarMismoPlanExtiendeEnSitio(t *testing.T) {
	db := setupAdjustValidityDB(t)
	tenant := database.Tenant{Name: "ACME", Slug: "acme", Status: database.TenantStatusActive}
	if err := db.Create(&tenant).Error; err != nil {
		t.Fatalf("crear tenant: %v", err)
	}
	plan := database.SaasPlan{Name: "PREMIUM", Price: 99, BillingCycle: "monthly", Active: true}
	if err := db.Create(&plan).Error; err != nil {
		t.Fatalf("crear plan: %v", err)
	}

	svc := NewSubscriptionService()
	first, err := svc.Create(CreateSubscriptionInput{TenantID: tenant.ID, PlanID: plan.ID, Months: 1})
	if err != nil {
		t.Fatalf("alta: %v", err)
	}
	firstEnd := first.EndDate

	renewed, err := svc.Create(CreateSubscriptionInput{TenantID: tenant.ID, PlanID: plan.ID, Months: 6})
	if err != nil {
		t.Fatalf("renovación: %v", err)
	}

	if renewed.ID != first.ID {
		t.Errorf("se creó una suscripción nueva (id %d ≠ %d): debía extenderse en sitio", renewed.ID, first.ID)
	}
	var total int64
	db.Model(&database.SaasSubscription{}).Where("tenant_id = ?", tenant.ID).Count(&total)
	if total != 1 {
		t.Errorf("hay %d suscripciones, se esperaba 1", total)
	}
	if !renewed.EndDate.After(firstEnd) {
		t.Errorf("la vigencia no se extendió: %v ≤ %v", renewed.EndDate, firstEnd)
	}
	if renewed.BilledMonths != 6 {
		t.Errorf("billed_months = %d, se esperaba 6", renewed.BilledMonths)
	}

	// Dos cobros encadenados, sin días compartidos y con el importe de sus meses.
	var cycles []database.SaasBillingCycle
	db.Where("subscription_id = ?", first.ID).Order("period_start asc").Find(&cycles)
	if len(cycles) != 2 {
		t.Fatalf("se esperaban 2 cobros, hay %d", len(cycles))
	}
	if cycles[1].PeriodStart.Before(cycles[0].PeriodEnd) {
		t.Errorf("los cobros solapan: el segundo abre %v y el primero cierra %v",
			cycles[1].PeriodStart, cycles[0].PeriodEnd)
	}
	if cycles[0].Amount != 99 || cycles[0].MonthsCovered != 1 {
		t.Errorf("cobro del alta: %.2f por %d mes(es), se esperaba 99.00 por 1", cycles[0].Amount, cycles[0].MonthsCovered)
	}
	if cycles[1].Amount != 594 || cycles[1].MonthsCovered != 6 {
		t.Errorf("cobro de renovación: %.2f por %d mes(es), se esperaba 594.00 por 6", cycles[1].Amount, cycles[1].MonthsCovered)
	}
}

// Cambiar de plan sí abre otro contrato: ahí la suscripción nueva es lo correcto.
func TestCambioDePlanCreaSuscripcionNueva(t *testing.T) {
	db := setupAdjustValidityDB(t)
	tenant := database.Tenant{Name: "ACME", Slug: "acme", Status: database.TenantStatusActive}
	db.Create(&tenant)
	basico := database.SaasPlan{Name: "EMPRENDEDOR", Price: 49, BillingCycle: "monthly", Active: true}
	premium := database.SaasPlan{Name: "PREMIUM", Price: 99, BillingCycle: "monthly", Active: true}
	db.Create(&basico)
	db.Create(&premium)

	svc := NewSubscriptionService()
	first, err := svc.Create(CreateSubscriptionInput{TenantID: tenant.ID, PlanID: basico.ID, Months: 1})
	if err != nil {
		t.Fatalf("alta: %v", err)
	}
	upgraded, err := svc.Create(CreateSubscriptionInput{TenantID: tenant.ID, PlanID: premium.ID, Months: 1})
	if err != nil {
		t.Fatalf("cambio de plan: %v", err)
	}
	if upgraded.ID == first.ID {
		t.Error("al cambiar de plan debía crearse una suscripción nueva")
	}
	var anterior database.SaasSubscription
	db.First(&anterior, first.ID)
	if anterior.Status != database.SaasSubExpired {
		t.Errorf("la suscripción anterior quedó en %q, se esperaba expired", anterior.Status)
	}
}

// El descuento pactado se aplica también al cobro de la renovación.
func TestRenovacionAplicaDescuento(t *testing.T) {
	db := setupAdjustValidityDB(t)
	tenant := database.Tenant{Name: "ACME", Slug: "acme", Status: database.TenantStatusActive}
	db.Create(&tenant)
	plan := database.SaasPlan{Name: "PREMIUM", Price: 99, BillingCycle: "monthly", Active: true}
	db.Create(&plan)

	svc := NewSubscriptionService()
	if _, err := svc.Create(CreateSubscriptionInput{TenantID: tenant.ID, PlanID: plan.ID, Months: 1}); err != nil {
		t.Fatalf("alta: %v", err)
	}
	sub, err := svc.Create(CreateSubscriptionInput{
		TenantID: tenant.ID, PlanID: plan.ID, Months: 6,
		DiscountType: "percent", DiscountValue: 10,
	})
	if err != nil {
		t.Fatalf("renovación con descuento: %v", err)
	}

	var cycle database.SaasBillingCycle
	db.Where("subscription_id = ?", sub.ID).Order("period_start desc").First(&cycle)
	if cycle.GrossAmount != 594 || cycle.Amount != 534.60 {
		t.Errorf("cobro = bruto %.2f neto %.2f, se esperaba bruto 594.00 neto 534.60",
			cycle.GrossAmount, cycle.Amount)
	}
}

// Al abrir otro contrato (cambio de plan) el cobro impago que el nuevo período vuelve a
// cubrir se anula —cobrarlo duplicaría los mismos días—, pero el de un período ya cerrado
// sobrevive: ahí sí hubo servicio prestado y no pagado.
func TestCambioDePlanAnulaSoloCobrosSolapados(t *testing.T) {
	db := setupAdjustValidityDB(t)
	tenant := database.Tenant{Name: "ACME", Slug: "acme", Status: database.TenantStatusActive}
	db.Create(&tenant)
	basico := database.SaasPlan{Name: "EMPRENDEDOR", Price: 49, BillingCycle: "monthly", Active: true}
	premium := database.SaasPlan{Name: "PREMIUM", Price: 99, BillingCycle: "monthly", Active: true}
	db.Create(&basico)
	db.Create(&premium)

	// Suscripción anterior con dos cobros impagos: uno de un período ya cerrado (deuda real)
	// y otro todavía corriendo, que el contrato nuevo va a volver a cubrir.
	vieja := database.SaasSubscription{
		TenantID: tenant.ID, PlanID: basico.ID, BillingCycle: "monthly",
		StartDate: time.Date(2026, 5, 1, 0, 0, 0, 0, saas.LimaLocation()),
		EndDate:   lima(2026, 12, 31), Status: database.SaasSubActive,
	}
	db.Create(&vieja)
	cerrado := database.SaasBillingCycle{
		TenantID: tenant.ID, SubscriptionID: vieja.ID, PlanID: basico.ID,
		PeriodStart: lima(2026, 5, 1), PeriodEnd: lima(2026, 6, 1), DueDate: lima(2026, 5, 1),
		Amount: 49, Currency: "PEN", Status: database.SaasInvoicePending,
	}
	db.Create(&cerrado)
	corriendo := database.SaasBillingCycle{
		TenantID: tenant.ID, SubscriptionID: vieja.ID, PlanID: basico.ID,
		PeriodStart: lima(2026, 6, 1), PeriodEnd: lima(2026, 12, 31), DueDate: lima(2026, 6, 1),
		Amount: 49, Currency: "PEN", Status: database.SaasInvoicePending,
	}
	db.Create(&corriendo)

	// Cambio de plan: abre contrato nuevo desde hoy.
	if _, err := NewSubscriptionService().Create(CreateSubscriptionInput{
		TenantID: tenant.ID, PlanID: premium.ID, Months: 1,
	}); err != nil {
		t.Fatalf("cambio de plan: %v", err)
	}

	var c1, c2 database.SaasBillingCycle
	db.First(&c1, cerrado.ID)
	db.First(&c2, corriendo.ID)

	if c1.Status != database.SaasInvoicePending {
		t.Errorf("el cobro del período cerrado quedó en %q: es deuda real, debía conservarse", c1.Status)
	}
	if c2.Status != database.SaasInvoiceRejected {
		t.Errorf("el cobro solapado quedó en %q, se esperaba anulado", c2.Status)
	}

	// La anulación queda auditada con el monto, para poder justificarla después.
	var ev int64
	db.Model(&database.SaasSubscriptionEvent{}).
		Where("tenant_id = ? AND event_type = ?", tenant.ID, saas.EventInvoiceSuperseded).Count(&ev)
	if ev != 1 {
		t.Errorf("se registraron %d eventos de anulación, se esperaba 1", ev)
	}
}
