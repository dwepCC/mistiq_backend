package staff

import (
	"errors"
	"testing"

	"tukifac/pkg/database"
	"tukifac/pkg/saas"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

const staffQuotaTenantID = 9

func setupStaffQuotaDB(t *testing.T, maxUsers int) *gorm.DB {
	t.Helper()
	central, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := central.AutoMigrate(&database.SaasPlan{}, &database.SaasSubscription{}); err != nil {
		t.Fatal(err)
	}
	plan := database.SaasPlan{Name: "MICROEMPRESA", MaxUsers: maxUsers, Active: true}
	central.Create(&plan)
	central.Create(&database.SaasSubscription{TenantID: staffQuotaTenantID, PlanID: plan.ID, Status: database.SaasSubActive})
	prev := database.CentralDB
	database.CentralDB = central
	t.Cleanup(func() { database.CentralDB = prev })

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&database.TenantUser{}, &database.TenantRole{}, &database.TenantBranch{}, &database.TenantUserBranch{}, &database.TenantRestaurantStaff{}, &database.TenantRestaurantSetting{}); err != nil {
		t.Fatal(err)
	}
	db.Create(&database.TenantBranch{Name: "Principal", IsMain: true, Active: true})
	db.Create(&database.TenantUser{Name: "Dueño", Email: "owner@t.com", RoleID: 1, Active: true})
	return db
}

func staffInput(email string) CreateStaffUserInput {
	return CreateStaffUserInput{Name: "Mozo", Email: email, EmployeeType: "waiter", Pin: "4821"}
}

// El alta de usuarios operativos de Tukichef también ocupa un cupo del plan: antes no se validaba
// y era la vía por la que los tenants superaban su tope.
func TestCreateStaffUser_respectsPlanUserLimit(t *testing.T) {
	db := setupStaffQuotaDB(t, 2) // dueño + 1
	svc := New(db).WithTenant(staffQuotaTenantID)
	if _, err := svc.CreateStaffUser(staffInput("m1@t.com")); err != nil {
		t.Fatalf("hasta el tope debe pasar: %v", err)
	}
	in := staffInput("m2@t.com")
	in.Pin = "7391"
	_, err := svc.CreateStaffUser(in)
	var q *saas.QuotaError
	if !errors.As(err, &q) {
		t.Fatalf("se esperaba *saas.QuotaError, llegó %v", err)
	}
	if n := saas.CountSeatUsers(db); n != 2 {
		t.Errorf("el alta rechazada no debe crear usuario: activos = %d", n)
	}
}

func TestCreateStaffUser_unlimitedPlan(t *testing.T) {
	db := setupStaffQuotaDB(t, 0)
	svc := New(db).WithTenant(staffQuotaTenantID)
	for i, pin := range []string{"1234", "5678", "9012", "3456"} {
		in := staffInput("m" + string(rune('a'+i)) + "@t.com")
		in.Pin = pin
		if _, err := svc.CreateStaffUser(in); err != nil {
			t.Fatalf("plan ilimitado: %v", err)
		}
	}
}
