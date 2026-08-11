package service

import (
	"fmt"
	"testing"
	"time"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func setupPlanRefTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&database.SaasPlan{}, &database.SaasSubscription{}); err != nil {
		t.Fatal(err)
	}
	return db
}

func seedPlan(t *testing.T, db *gorm.DB, name string, active bool) database.SaasPlan {
	t.Helper()
	p := database.SaasPlan{Name: name, Active: active}
	if err := db.Create(&p).Error; err != nil {
		t.Fatal(err)
	}
	return p
}

// La suscripción vigente manda: es la fuente de verdad del SaaS.
func TestPlanRefs_prefersActiveSubscriptionOverTenantPlanText(t *testing.T) {
	db := setupPlanRefTestDB(t)
	basic := seedPlan(t, db, "Basic", true)
	pro := seedPlan(t, db, "Pro", true)

	if err := db.Create(&database.SaasSubscription{
		TenantID: 7, PlanID: pro.ID, Status: "active",
		StartDate: time.Now(), EndDate: time.Now().AddDate(0, 1, 0),
	}).Error; err != nil {
		t.Fatal(err)
	}

	svc := &TenantService{db: db}
	// tenants.plan quedó desactualizado en "basic": debe ganar la suscripción (Pro).
	refs, err := svc.PlanRefsByTenantIDs([]uint{7}, map[uint]string{7: "basic"})
	if err != nil {
		t.Fatal(err)
	}
	if refs[7].PlanID != pro.ID {
		t.Fatalf("plan_id = %d want %d (Pro, de la suscripción)", refs[7].PlanID, pro.ID)
	}
	if refs[7].PlanName != "Pro" {
		t.Fatalf("plan_name = %q want Pro", refs[7].PlanName)
	}
	_ = basic
}

// Sin suscripción se resuelve por nombre, ignorando mayúsculas (el origen del desalineo).
func TestPlanRefs_fallsBackToNameCaseInsensitive(t *testing.T) {
	db := setupPlanRefTestDB(t)
	basic := seedPlan(t, db, "Basic", true)

	svc := &TenantService{db: db}
	refs, err := svc.PlanRefsByTenantIDs([]uint{3}, map[uint]string{3: "basic"})
	if err != nil {
		t.Fatal(err)
	}
	if refs[3].PlanID != basic.ID {
		t.Fatalf("plan_id = %d want %d", refs[3].PlanID, basic.ID)
	}
	if refs[3].PlanName != "Basic" {
		t.Fatalf("plan_name = %q want el nombre canónico Basic", refs[3].PlanName)
	}
}

// Una suscripción cancelada no debe determinar el plan mostrado.
func TestPlanRefs_ignoresCancelledSubscription(t *testing.T) {
	db := setupPlanRefTestDB(t)
	basic := seedPlan(t, db, "Basic", true)
	pro := seedPlan(t, db, "Pro", true)

	if err := db.Create(&database.SaasSubscription{
		TenantID: 9, PlanID: pro.ID, Status: database.SaasSubCancelled,
		StartDate: time.Now(), EndDate: time.Now(),
	}).Error; err != nil {
		t.Fatal(err)
	}

	svc := &TenantService{db: db}
	refs, err := svc.PlanRefsByTenantIDs([]uint{9}, map[uint]string{9: "Basic"})
	if err != nil {
		t.Fatal(err)
	}
	if refs[9].PlanID != basic.ID {
		t.Fatalf("plan_id = %d want %d (la cancelada no cuenta)", refs[9].PlanID, basic.ID)
	}
}

// Plan heredado que ya no existe: plan_id 0 para que el panel pida elegir uno válido
// en vez de preseleccionar cualquiera.
func TestPlanRefs_unknownPlanYieldsZero(t *testing.T) {
	db := setupPlanRefTestDB(t)
	seedPlan(t, db, "Basic", true)

	svc := &TenantService{db: db}
	refs, err := svc.PlanRefsByTenantIDs([]uint{4}, map[uint]string{4: "plan-que-ya-no-existe"})
	if err != nil {
		t.Fatal(err)
	}
	if refs[4].PlanID != 0 {
		t.Fatalf("plan_id = %d want 0", refs[4].PlanID)
	}
}
