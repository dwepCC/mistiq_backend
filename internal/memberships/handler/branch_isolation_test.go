package handler

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"github.com/gofiber/fiber/v3"
	"gorm.io/gorm"
)

// Fase 3A: membresías aisladas por sucursal.

type act struct {
	user, active uint
	allowed      []uint
	admin        bool
}

var (
	vendedorS1 = act{user: 1, active: 1, allowed: []uint{1}}
	cajeroS2   = act{user: 2, active: 2, allowed: []uint{2}}
	adminS1    = act{user: 9, active: 1, allowed: []uint{1, 2}, admin: true}
)

func setupMembershipBranchDB(t *testing.T) (*gorm.DB, database.TenantMembership, database.TenantMembership) {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []interface{}{
		&database.TenantMembership{}, &database.TenantMembershipInvoice{}, &database.TenantContact{}, &database.TenantProduct{}, &database.TenantSale{},
	} {
		if err := db.AutoMigrate(m); err != nil {
			t.Fatal(err)
		}
	}
	contact := database.TenantContact{BusinessName: "Cliente", Type: "customer", Active: true}
	if err := db.Create(&contact).Error; err != nil {
		t.Fatal(err)
	}
	mk := func(branch uint) database.TenantMembership {
		m := database.TenantMembership{
			ContactID: contact.ID, BranchID: branch, Title: fmt.Sprintf("M%d", branch), BillingCycle: "monthly",
			Amount: 50, StartDate: time.Now(), NextBillingDate: time.Now().AddDate(0, 0, 3), Status: "active",
		}
		if err := db.Create(&m).Error; err != nil {
			t.Fatal(err)
		}
		return m
	}
	return db, mk(1), mk(2)
}

func doM(t *testing.T, db *gorm.DB, a act, method, path, body string) (int, string) {
	t.Helper()
	h := NewMembershipHandler()
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		c.Locals("tenantDB", db)
		c.Locals("user_id", a.user)
		c.Locals("active_branch_id", a.active)
		c.Locals("allowed_branch_ids", a.allowed)
		c.Locals("is_branch_admin", a.admin)
		return c.Next()
	})
	app.Get("/memberships/reminder-counts", h.ReminderCountsAPI)
	app.Get("/memberships", h.ListAPI)
	app.Post("/memberships", h.CreateAPI)
	app.Get("/memberships/:id", h.GetAPI)
	app.Get("/memberships/:id/billing-history", h.BillingHistoryAPI)
	app.Put("/memberships/:id", h.UpdateAPI)
	app.Patch("/memberships/:id/status", h.SetStatusAPI)
	app.Delete("/memberships/:id", h.DeleteAPI)
	app.Post("/memberships/:id/generate-sale", h.GenerateSaleAPI)
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var sb strings.Builder
	buf := make([]byte, 4096)
	for {
		n, e := resp.Body.Read(buf)
		sb.Write(buf[:n])
		if e != nil {
			break
		}
	}
	return resp.StatusCode, sb.String()
}

func TestMemberships_ReadsAcrossBranches(t *testing.T) {
	db, m1, m2 := setupMembershipBranchDB(t)
	p2 := fmt.Sprintf("/memberships/%d", m2.ID)
	for _, path := range []string{p2, p2 + "/billing-history"} {
		if code, body := doM(t, db, vendedorS1, "GET", path, ""); code != 404 {
			t.Errorf("vendedor S1 GET %s: %d, quiere 404 (%s)", path, code, body)
		}
		if code, body := doM(t, db, cajeroS2, "GET", path, ""); code != 200 {
			t.Errorf("cajero S2 GET %s: %d (%s)", path, code, body)
		}
		if code, body := doM(t, db, adminS1, "GET", path, ""); code != 200 {
			t.Errorf("admin GET %s: %d (%s)", path, code, body)
		}
	}
	ids := func(body string) []uint {
		var r struct {
			Data []struct {
				ID uint `json:"id"`
			} `json:"data"`
		}
		if err := json.Unmarshal([]byte(body), &r); err != nil {
			t.Fatalf("json: %v (%s)", err, body)
		}
		var out []uint
		for _, d := range r.Data {
			out = append(out, d.ID)
		}
		return out
	}
	for _, path := range []string{"/memberships", "/memberships?branch_id=2"} {
		code, body := doM(t, db, vendedorS1, "GET", path, "")
		if got := ids(body); code != 200 || len(got) != 1 || got[0] != m1.ID {
			t.Errorf("vendedor S1 %s: %d %v, quiere solo %d", path, code, got, m1.ID)
		}
	}
	code, body := doM(t, db, adminS1, "GET", "/memberships?branch_id=2", "")
	if got := ids(body); code != 200 || len(got) != 1 || got[0] != m2.ID {
		t.Errorf("admin ?branch_id=2: %d %v, quiere solo %d", code, got, m2.ID)
	}
	// Contadores de recordatorio: solo la sucursal activa (cada una tiene 1 próxima).
	code, body = doM(t, db, vendedorS1, "GET", "/memberships/reminder-counts", "")
	var rc struct{ Upcoming int64 }
	_ = json.Unmarshal([]byte(strings.ReplaceAll(body, `"upcoming"`, `"Upcoming"`)), &rc)
	if code != 200 || rc.Upcoming != 1 {
		t.Errorf("reminder-counts vendedor S1: %d upcoming=%d, quiere 1 (%s)", code, rc.Upcoming, body)
	}
}

func TestMemberships_WritesAcrossBranches(t *testing.T) {
	db, m1, m2 := setupMembershipBranchDB(t)
	p2 := fmt.Sprintf("/memberships/%d", m2.ID)
	type op struct{ name, method, path, body string }
	ops := []op{
		{"put", "PUT", p2, `{"title":"hack","amount":1}`},
		{"status", "PATCH", p2 + "/status", `{"status":"cancelled"}`},
		{"delete", "DELETE", p2, ``},
		{"generate-sale", "POST", p2 + "/generate-sale", `{"series_id":1}`},
	}
	for _, o := range ops {
		if code, body := doM(t, db, vendedorS1, o.method, o.path, o.body); code != 404 {
			t.Errorf("%s: vendedor S1 sobre membresía S2: %d, quiere 404 (%s)", o.name, code, body)
		}
		if code, body := doM(t, db, adminS1, o.method, o.path, o.body); code != 403 || !strings.Contains(body, "BRANCH_FORBIDDEN") {
			t.Errorf("%s: admin (activa S1) sobre membresía S2: %d, quiere 403 BRANCH_FORBIDDEN (%s)", o.name, code, body)
		}
	}
	var after database.TenantMembership
	if err := db.First(&after, m2.ID).Error; err != nil {
		t.Fatalf("la membresía S2 fue borrada: %v", err)
	}
	if after.Title != "M2" || after.Amount != 50 || after.Status != "active" {
		t.Errorf("la membresía S2 fue alterada: %+v", after)
	}

	// Caso propio: edición, cambio de estado y borrado.
	p1 := fmt.Sprintf("/memberships/%d", m1.ID)
	if code, body := doM(t, db, vendedorS1, "PUT", p1, `{"title":"Nuevo","branch_id":2}`); code != 200 {
		t.Errorf("PUT propio: %d (%s)", code, body)
	}
	var upd database.TenantMembership
	db.First(&upd, m1.ID)
	if upd.Title != "Nuevo" || upd.BranchID != 1 {
		t.Errorf("PUT no debe mover de sucursal: title=%q branch=%d", upd.Title, upd.BranchID)
	}
	if code, body := doM(t, db, vendedorS1, "PATCH", p1+"/status", `{"status":"paused"}`); code != 200 {
		t.Errorf("PATCH status propio: %d (%s)", code, body)
	}
	if code, body := doM(t, db, cajeroS2, "DELETE", p2, ""); code != 200 {
		t.Errorf("DELETE propio (S2): %d (%s)", code, body)
	}
}

func TestMemberships_CreateBranchValidated(t *testing.T) {
	db, _, _ := setupMembershipBranchDB(t)
	var contact database.TenantContact
	db.First(&contact)
	body := func(branch uint) string {
		return fmt.Sprintf(`{"contact_id":%d,"branch_id":%d,"amount":30,"billing_cycle":"monthly","start_date":"2026-11-01"}`, contact.ID, branch)
	}
	if code, b := doM(t, db, vendedorS1, "POST", "/memberships", body(2)); code != 403 || !strings.Contains(b, "BRANCH_FORBIDDEN") {
		t.Errorf("vendedor S1 creando con branch_id=2: %d, quiere 403 (%s)", code, b)
	}
	var n int64
	db.Model(&database.TenantMembership{}).Count(&n)
	if n != 2 {
		t.Errorf("no debía crearse ninguna membresía (total %d)", n)
	}
	if code, b := doM(t, db, vendedorS1, "POST", "/memberships", body(0)); code != 201 {
		t.Errorf("vendedor S1 creando propia: %d (%s)", code, b)
	}
	var created database.TenantMembership
	db.Order("id DESC").First(&created)
	if created.BranchID != 1 {
		t.Errorf("la membresía debía quedar en la sucursal activa 1, quedó %d", created.BranchID)
	}
	// Ni siquiera el admin crea en otra sucursal por el body: debe cambiar de sucursal activa.
	if code, b := doM(t, db, adminS1, "POST", "/memberships", body(2)); code != 403 {
		t.Errorf("admin creando en S2 con activa S1: %d (%s), quiere 403", code, b)
	}
}
