package saas

import (
	"testing"
	"time"

	"tukifac/pkg/database"
)

func TestCalendarDaysAfterEnd_venceHoyEsCero(t *testing.T) {
	loc, _ := time.LoadLocation(LimaTimezone)
	end := time.Date(2026, 5, 21, 8, 0, 0, 0, loc)
	now := time.Date(2026, 5, 21, 23, 30, 0, 0, loc)
	if got := CalendarDaysAfterEnd(end, now); got != 0 {
		t.Fatalf("expected 0 days after on expiry day, got %d", got)
	}
}

func TestCalendarDaysAfterEnd_graceEmpiezaManiana(t *testing.T) {
	loc, _ := time.LoadLocation(LimaTimezone)
	end := time.Date(2026, 5, 21, 23, 59, 0, 0, loc)
	now := time.Date(2026, 5, 22, 0, 5, 0, 0, loc)
	if got := CalendarDaysAfterEnd(end, now); got != 1 {
		t.Fatalf("expected 1 day after, got %d", got)
	}
}

func TestResolveEffectiveStatus_activeHastaFinDeDia(t *testing.T) {
	loc, _ := time.LoadLocation(LimaTimezone)
	sub := &database.SaasSubscription{
		EndDate: time.Date(2026, 5, 21, 10, 0, 0, 0, loc),
		Status:  "active",
	}
	tenant := &database.Tenant{Status: "active", StrikeCount: 0}
	now := time.Date(2026, 5, 21, 22, 0, 0, 0, loc)
	st := ResolveEffectiveStatus(sub, tenant, now, 3)
	if st != "active" {
		t.Fatalf("expected active on expiry day, got %s", st)
	}
}

func TestResolveEffectiveStatus_graceDia1(t *testing.T) {
	loc, _ := time.LoadLocation(LimaTimezone)
	sub := &database.SaasSubscription{
		EndDate: time.Date(2026, 5, 21, 0, 0, 0, 0, loc),
		Status:  "active",
	}
	tenant := &database.Tenant{Status: "active"}
	now := time.Date(2026, 5, 22, 12, 0, 0, 0, loc)
	st := ResolveEffectiveStatus(sub, tenant, now, 3)
	if st != "grace_period" {
		t.Fatalf("expected grace_period, got %s", st)
	}
}

func TestResolveEffectiveStatus_overdueAfterGrace(t *testing.T) {
	loc, _ := time.LoadLocation(LimaTimezone)
	sub := &database.SaasSubscription{
		EndDate: time.Date(2026, 5, 1, 0, 0, 0, 0, loc),
		Status:  "active",
	}
	tenant := &database.Tenant{Status: "active"}
	now := time.Date(2026, 5, 10, 0, 0, 0, 0, loc)
	st := ResolveEffectiveStatus(sub, tenant, now, 3)
	if st != "overdue" {
		t.Fatalf("expected overdue, got %s", st)
	}
}

func TestResolveEffectiveStatus_blockedStrike(t *testing.T) {
	sub := &database.SaasSubscription{EndDate: time.Now().Add(48 * time.Hour), Status: "active"}
	tenant := &database.Tenant{Status: "blocked", StrikeCount: 2}
	st := ResolveEffectiveStatus(sub, tenant, NowLima(), 3)
	if st != "blocked" {
		t.Fatalf("expected blocked, got %s", st)
	}
}

func TestEffectiveProvisionalHours_cap12(t *testing.T) {
	d := EffectiveProvisionalHours(72)
	if d != 12*time.Hour {
		t.Fatalf("expected 12h cap, got %v", d)
	}
}

func TestCanOperate_provisionalActive(t *testing.T) {
	until := NowLima().Add(2 * time.Hour)
	tenant := &database.Tenant{Status: "suspended", StrikeCount: 0}
	if !CanOperate("suspended", tenant, &until, NowLima()) {
		t.Fatal("expected operate during provisional window")
	}
}

func TestCanOperate_blockedNoOperate(t *testing.T) {
	tenant := &database.Tenant{Status: "blocked", StrikeCount: 2, PaymentBlocked: true}
	if CanOperate("active", tenant, nil, NowLima()) {
		t.Fatal("blocked tenant must not operate")
	}
}

func TestCalendarDaysUntilEnd_venceHoy(t *testing.T) {
	loc, _ := time.LoadLocation(LimaTimezone)
	end := time.Date(2026, 5, 21, 15, 0, 0, 0, loc)
	now := time.Date(2026, 5, 21, 20, 0, 0, 0, loc)
	if got := CalendarDaysUntilEnd(end, now); got != 0 {
		t.Fatalf("expected 0 days until on expiry day, got %d", got)
	}
}

func TestNextLimaDailyRun_afterMidnight(t *testing.T) {
	loc, _ := time.LoadLocation(LimaTimezone)
	from := time.Date(2026, 5, 21, 0, 10, 0, 0, loc)
	next := NextLimaDailyRun(from)
	want := time.Date(2026, 5, 22, 0, 5, 0, 0, loc)
	if !next.Equal(want) {
		t.Fatalf("expected next run %v, got %v", want, next)
	}
}

func TestResolveEffectiveStatus_provisionalActiveWindow(t *testing.T) {
	until := NowLima().Add(6 * time.Hour)
	sub := &database.SaasSubscription{
		EndDate:          NowLima().Add(-48 * time.Hour),
		Status:           database.SaasSubSuspended,
		ProvisionalUntil: &until,
	}
	tenant := &database.Tenant{Status: database.TenantStatusActive}
	st := ResolveEffectiveStatus(sub, tenant, NowLima(), 3)
	if st != database.SaasSubProvisionalActive {
		t.Fatalf("expected provisional_active, got %s", st)
	}
}

// Una empresa desactivada por el administrador no opera aunque su suscripción siga vigente, ni siquiera
// con una reactivación provisional.
func TestCanOperate_inactiveNoOperate(t *testing.T) {
	tenant := &database.Tenant{Status: database.TenantStatusInactive}
	if CanOperate("active", tenant, nil, NowLima()) {
		t.Fatal("un tenant desactivado no debe operar con suscripción vigente")
	}
	until := NowLima().Add(2 * time.Hour)
	if CanOperate("active", tenant, &until, NowLima()) {
		t.Fatal("un tenant desactivado no debe operar ni con ventana provisional")
	}
}

// La consulta de reportes sigue abierta con la cuenta suspendida/vencida, pero no si está bloqueada o
// desactivada.
func TestCanViewReports(t *testing.T) {
	cases := []struct {
		name      string
		tenant    *database.Tenant
		isBlocked bool
		want      bool
	}{
		{"activo", &database.Tenant{Status: database.TenantStatusActive}, false, true},
		{"suspendido por falta de pago", &database.Tenant{Status: database.TenantStatusSuspended}, false, true},
		{"vencido (tenant aún activo)", &database.Tenant{Status: database.TenantStatusActive}, false, true},
		{"bloqueado", &database.Tenant{Status: database.TenantStatusBlocked}, true, false},
		{"bloqueado por strikes", &database.Tenant{Status: database.TenantStatusSuspended}, true, false},
		{"desactivado", &database.Tenant{Status: database.TenantStatusInactive}, false, false},
	}
	for _, c := range cases {
		if got := CanViewReports(c.isBlocked, c.tenant); got != c.want {
			t.Errorf("%s: CanViewReports = %v, want %v", c.name, got, c.want)
		}
	}
	if CanViewReports(false, nil) {
		t.Error("sin tenant no hay acceso")
	}
}
