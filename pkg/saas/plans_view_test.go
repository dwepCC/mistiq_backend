package saas

import (
	"os"
	"testing"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func setupPlansViewDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := "file:plans_view_test.db?_journal_mode=WAL&_busy_timeout=15000"
	t.Cleanup(func() { os.Remove("plans_view_test.db") })
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	database.CentralDB = db
	if err := db.AutoMigrate(&database.SaasPlan{}, &database.SaasPlanModule{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	for _, tbl := range []string{"saas_plans", "saas_plan_modules"} {
		db.Exec("DELETE FROM " + tbl)
	}
	return db
}

// El tenant solo debe ver planes activos, ordenados por precio, con sus módulos — nunca planes
// desactivados por el superadmin (aunque algún tenant viejo siga contratado a uno de ellos).
func TestListActivePlansView_onlyActiveOrderedByPrice(t *testing.T) {
	db := setupPlansViewDB(t)

	basic := database.SaasPlan{Name: "Básico", Price: 49.9, BillingCycle: "monthly", Active: true}
	db.Create(&basic)
	pro := database.SaasPlan{Name: "Pro", Price: 99.9, BillingCycle: "monthly", Active: true}
	db.Create(&pro)
	legacy := database.SaasPlan{Name: "Legacy", Price: 19.9, BillingCycle: "monthly", Active: true}
	db.Create(&legacy)
	// Active=false explícito en Create se pierde: GORM omite del INSERT los campos en su valor
	// zero cuando tienen `gorm:"default:..."` (deja que el default de columna gane). Por eso se
	// desactiva con un Update aparte, igual que hace PlanService.ToggleActive en producción.
	db.Model(&legacy).Update("active", false)

	db.Create(&database.SaasPlanModule{PlanID: pro.ID, ModuleKey: "sales"})
	db.Create(&database.SaasPlanModule{PlanID: pro.ID, ModuleKey: "inventory"})

	got, err := ListActivePlansView()
	if err != nil {
		t.Fatalf("ListActivePlansView: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("esperaba 2 planes activos, obtuve %d", len(got))
	}
	if got[0].Name != "Básico" || got[1].Name != "Pro" {
		t.Fatalf("orden por precio incorrecto: %+v", got)
	}
	if len(got[1].Modules) != 2 {
		t.Fatalf("esperaba 2 módulos para Pro, obtuve %v", got[1].Modules)
	}
	if len(got[0].Modules) != 0 {
		t.Fatalf("esperaba slice vacío (no nil) de módulos para Básico, obtuve %v", got[0].Modules)
	}
}

// El plan gratuito/trial (price=0) es para el alta inicial, no algo que se ofrezca al elegir o
// renovar plan — aunque esté activo, no debe aparecer en la lista.
func TestListActivePlansView_excludesFreePlan(t *testing.T) {
	db := setupPlansViewDB(t)

	db.Create(&database.SaasPlan{Name: "Trial", Price: 0, BillingCycle: "monthly", Active: true})
	pro := database.SaasPlan{Name: "Pro", Price: 99.9, BillingCycle: "monthly", Active: true}
	db.Create(&pro)

	got, err := ListActivePlansView()
	if err != nil {
		t.Fatalf("ListActivePlansView: %v", err)
	}
	if len(got) != 1 || got[0].Name != "Pro" {
		t.Fatalf("esperaba solo el plan pagado (Pro), obtuve %+v", got)
	}
}
