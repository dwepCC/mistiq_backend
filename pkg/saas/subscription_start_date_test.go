package saas

import (
	"os"
	"testing"
	"time"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func setupStartDateDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := "file:start_date_test.db?_journal_mode=WAL&_busy_timeout=15000"
	t.Cleanup(func() { os.Remove("start_date_test.db") })
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	database.CentralDB = db
	if err := db.AutoMigrate(
		&database.Tenant{}, &database.SaasPlan{}, &database.SaasPlanModule{},
		&database.TenantModule{}, &database.SaasSubscription{}, &database.SaasBillingCycle{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	for _, tbl := range []string{
		"saas_billing_cycles", "saas_subscriptions", "saas_plans", "tenants", "tenant_modules",
	} {
		db.Exec("DELETE FROM " + tbl)
	}
	return db
}

// Empresa registrada hoy, pero cuya suscripción arranca en el futuro (caso real: se da de alta
// hoy, pero recién va a pagar/operar dentro de unos días).
func TestExtendSubscription_futureStartDate(t *testing.T) {
	db := setupStartDateDB(t)

	plan := database.SaasPlan{Name: "Pro", Price: 99, BillingCycle: database.SaasCycleMonthly, Active: true}
	db.Create(&plan)
	tenant := database.Tenant{Name: "ACME", Slug: "acme", DBName: "acme", Status: database.TenantStatusActive}
	db.Create(&tenant)

	future := NowLima().AddDate(0, 0, 5)
	sub, err := ExtendSubscription(tenant.ID, plan.ID, 1, "alta con inicio futuro", &future)
	if err != nil {
		t.Fatalf("ExtendSubscription: %v", err)
	}

	wantStart := CalendarDateLima(future)
	if got := CalendarDateLima(sub.StartDate); !got.Equal(wantStart) {
		t.Errorf("StartDate = %s, want %s", got.Format("2006-01-02"), wantStart.Format("2006-01-02"))
	}
	wantEnd := CalendarDateLima(future.AddDate(0, 1, 0))
	if got := CalendarDateLima(sub.EndDate); !got.Equal(wantEnd) {
		t.Errorf("EndDate = %s, want %s (inicio futuro + 1 mes, no hoy + 1 mes)", got.Format("2006-01-02"), wantEnd.Format("2006-01-02"))
	}
}

// No se puede elegir una fecha de inicio pasada.
func TestExtendSubscription_rejectsPastStartDate(t *testing.T) {
	db := setupStartDateDB(t)

	plan := database.SaasPlan{Name: "Pro", Price: 99, BillingCycle: database.SaasCycleMonthly, Active: true}
	db.Create(&plan)
	tenant := database.Tenant{Name: "ACME", Slug: "acme", DBName: "acme", Status: database.TenantStatusActive}
	db.Create(&tenant)

	past := NowLima().AddDate(0, 0, -3)
	if _, err := ExtendSubscription(tenant.ID, plan.ID, 1, "no debería pasar", &past); err == nil {
		t.Fatal("esperaba error por fecha de inicio pasada")
	}
}

// nil (sin elegir fecha) sigue arrancando hoy, comportamiento de siempre — no debe romperse.
func TestExtendSubscription_nilStartDate_defaultsToToday(t *testing.T) {
	db := setupStartDateDB(t)

	plan := database.SaasPlan{Name: "Pro", Price: 99, BillingCycle: database.SaasCycleMonthly, Active: true}
	db.Create(&plan)
	tenant := database.Tenant{Name: "ACME", Slug: "acme", DBName: "acme", Status: database.TenantStatusActive}
	db.Create(&tenant)

	sub, err := ExtendSubscription(tenant.ID, plan.ID, 1, "alta normal", nil)
	if err != nil {
		t.Fatalf("ExtendSubscription: %v", err)
	}
	today := CalendarDateLima(NowLima())
	if got := CalendarDateLima(sub.StartDate); !got.Equal(today) {
		t.Errorf("StartDate = %s, want hoy (%s)", got.Format("2006-01-02"), today.Format("2006-01-02"))
	}
}

// Una fecha de inicio futura no debe interferir con una renovación EN SITIO (mismo plan): esa
// rama sigue encadenando sola desde el fin de la suscripción vigente, ignorando startDate.
func TestExtendSubscription_startDateIgnoredOnInPlaceRenewal(t *testing.T) {
	db := setupStartDateDB(t)
	_ = time.Now // referencia solo para claridad del test; NowLima es la fuente real

	plan := database.SaasPlan{Name: "Pro", Price: 99, BillingCycle: database.SaasCycleMonthly, Active: true}
	db.Create(&plan)
	tenant := database.Tenant{Name: "ACME", Slug: "acme", DBName: "acme", Status: database.TenantStatusActive}
	db.Create(&tenant)

	curEnd := CalendarDateLima(NowLima().AddDate(0, 0, 10))
	sub := database.SaasSubscription{
		TenantID: tenant.ID, PlanID: plan.ID, BillingCycle: database.SaasCycleMonthly,
		StartDate: NowLima(), EndDate: EndOfDayLima(curEnd), Status: database.SaasSubActive,
	}
	db.Create(&sub)

	// Aunque se pase un startDate futuro absurdo, la renovación del MISMO plan debe seguir
	// encadenando desde curEnd (renewInPlaceTx), no usar ese startDate.
	weird := NowLima().AddDate(1, 0, 0)
	renewed, err := ExtendSubscription(tenant.ID, plan.ID, 1, "renovación", &weird)
	if err != nil {
		t.Fatalf("ExtendSubscription: %v", err)
	}
	if renewed.ID != sub.ID {
		t.Fatalf("esperaba renovar EN SITIO la misma fila (id %d), se creó otra (id %d)", sub.ID, renewed.ID)
	}
	wantEnd := CalendarDateLima(curEnd.AddDate(0, 1, 0))
	if got := CalendarDateLima(renewed.EndDate); !got.Equal(wantEnd) {
		t.Errorf("EndDate = %s, want %s (encadenado desde curEnd, no desde startDate)", got.Format("2006-01-02"), wantEnd.Format("2006-01-02"))
	}
}
