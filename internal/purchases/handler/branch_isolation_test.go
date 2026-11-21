package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"tukifac/config"
	"tukifac/internal/purchases/service"
	"tukifac/pkg/database"
	"tukifac/pkg/tax"

	"github.com/gofiber/fiber/v3"
	"gorm.io/gorm"
)

// Fase 3A: aislamiento entre sucursales en compras. Dos sucursales con una compra cada una.
//   - vendedor S1 (user 1, activa 1), cajero S2 (user 2, activa 2), admin (ve todas, activa 1 o 2).

type actor struct {
	user    uint
	active  uint
	allowed []uint
	admin   bool
}

var (
	vendedorS1 = actor{user: 1, active: 1, allowed: []uint{1}}
	cajeroS2   = actor{user: 2, active: 2, allowed: []uint{2}}
	adminS1    = actor{user: 9, active: 1, allowed: []uint{1, 2}, admin: true}
)

func newBranchPurchaseApp(db *gorm.DB) *fiber.App {
	h := &PurchaseHandler{}
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		c.Locals("tenantDB", db)
		u, _ := strconv.Atoi(c.Get("X-User"))
		c.Locals("user_id", uint(u))
		a, _ := strconv.Atoi(c.Get("X-Active"))
		c.Locals("active_branch_id", uint(a))
		var allowed []uint
		for _, p := range strings.Split(c.Get("X-Allowed"), ",") {
			if n, err := strconv.Atoi(p); err == nil {
				allowed = append(allowed, uint(n))
			}
		}
		c.Locals("allowed_branch_ids", allowed)
		c.Locals("is_branch_admin", c.Get("X-Admin") == "1")
		return c.Next()
	})
	app.Get("/purchases", h.ListAPI)
	app.Get("/purchases/:id", h.GetAPI)
	app.Post("/purchases", h.CreateAPI)
	app.Post("/purchases/:id/void", h.VoidAPI)
	return app
}

func doP(t *testing.T, app *fiber.App, a actor, method, path, body string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User", strconv.Itoa(int(a.user)))
	req.Header.Set("X-Active", strconv.Itoa(int(a.active)))
	var ids []string
	for _, id := range a.allowed {
		ids = append(ids, strconv.Itoa(int(id)))
	}
	req.Header.Set("X-Allowed", strings.Join(ids, ","))
	if a.admin {
		req.Header.Set("X-Admin", "1")
	}
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

func mkPurchase(t *testing.T, db *gorm.DB, branchID, userID uint, productID uint, number string) database.TenantPurchase {
	t.Helper()
	pid := productID
	p, err := service.NewPurchaseService(db).Create(service.CreatePurchaseInput{
		BranchID: branchID, UserID: userID, DocType: "FACTURA", Series: "F001",
		Number: number, IssueDate: time.Now(), Currency: "PEN",
		Items: []service.PurchaseItemInput{
			{ProductID: &pid, Description: "Arroz", Unit: "KGM", Quantity: 2, UnitCost: 3, IgvAffectationType: "10"},
		},
		TaxConfig: tax.Config{TaxRate: 18},
	})
	if err != nil {
		t.Fatal(err)
	}
	return *p
}

func setupBranchPurchases(t *testing.T) (*gorm.DB, database.TenantPurchase, database.TenantPurchase, uint) {
	t.Helper()
	prevCfg := config.AppConfig
	config.AppConfig = &config.Config{}
	t.Cleanup(func() { config.AppConfig = prevCfg })

	db := setupPurchaseAPITestDB(t)
	db.Create(&database.TenantBranch{Name: "Principal", IsMain: true, Active: true})
	db.Create(&database.TenantBranch{Name: "Sucursal 2", Active: true})
	newOpenCashSession(t, db, 1, 1)
	newOpenCashSession(t, db, 2, 2)
	prod := database.TenantProduct{
		Code: "ARR", Name: "Arroz", Type: "product", Unit: "KGM", SalePrice: 5, ManageStock: true,
		IgvAffectationType: "10", Active: true,
	}
	if err := db.Create(&prod).Error; err != nil {
		t.Fatal(err)
	}
	p1 := mkPurchase(t, db, 1, 1, prod.ID, "0001")
	p2 := mkPurchase(t, db, 2, 2, prod.ID, "0002")
	return db, p1, p2, prod.ID
}

func TestPurchases_GetAndListAcrossBranches(t *testing.T) {
	db, p1, p2, _ := setupBranchPurchases(t)
	app := newBranchPurchaseApp(db)

	// GET /purchases/:id
	if code, body := doP(t, app, vendedorS1, "GET", fmt.Sprintf("/purchases/%d", p2.ID), ""); code != 404 {
		t.Errorf("vendedor S1 viendo compra de S2: %d, quiere 404 (%s)", code, body)
	}
	if code, body := doP(t, app, cajeroS2, "GET", fmt.Sprintf("/purchases/%d", p2.ID), ""); code != 200 {
		t.Errorf("cajero S2 viendo su compra: %d (%s)", code, body)
	}
	if code, body := doP(t, app, adminS1, "GET", fmt.Sprintf("/purchases/%d", p2.ID), ""); code != 200 {
		t.Errorf("admin viendo compra de S2: %d (%s)", code, body)
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
	// GET /purchases: cada uno ve solo su sucursal, incluso pidiendo branch_id ajeno.
	for _, path := range []string{"/purchases", "/purchases?branch_id=2"} {
		code, body := doP(t, app, vendedorS1, "GET", path, "")
		got := ids(body)
		if code != 200 || len(got) != 1 || got[0] != p1.ID {
			t.Errorf("vendedor S1 %s: %d %v, quiere solo %d", path, code, got, p1.ID)
		}
	}
	code, body := doP(t, app, cajeroS2, "GET", "/purchases", "")
	if got := ids(body); code != 200 || len(got) != 1 || got[0] != p2.ID {
		t.Errorf("cajero S2 /purchases: %d %v, quiere solo %d", code, got, p2.ID)
	}
	// Admin: una sucursal accesible sí puede pedirla.
	code, body = doP(t, app, adminS1, "GET", "/purchases?branch_id=2", "")
	if got := ids(body); code != 200 || len(got) != 1 || got[0] != p2.ID {
		t.Errorf("admin /purchases?branch_id=2: %d %v, quiere solo %d", code, got, p2.ID)
	}
}

func TestPurchases_VoidAcrossBranches(t *testing.T) {
	db, p1, p2, _ := setupBranchPurchases(t)
	app := newBranchPurchaseApp(db)

	if code, body := doP(t, app, vendedorS1, "POST", fmt.Sprintf("/purchases/%d/void", p2.ID), ""); code != 404 {
		t.Errorf("vendedor S1 anulando compra S2: %d, quiere 404 (%s)", code, body)
	}
	if code, body := doP(t, app, adminS1, "POST", fmt.Sprintf("/purchases/%d/void", p2.ID), ""); code != 403 || !strings.Contains(body, "BRANCH_FORBIDDEN") {
		t.Errorf("admin (activa S1) anulando compra S2: %d, quiere 403 BRANCH_FORBIDDEN (%s)", code, body)
	}
	var st database.TenantPurchase
	db.First(&st, p2.ID)
	if st.Status == "cancelled" {
		t.Fatal("la compra S2 fue anulada desde otra sucursal")
	}
	// Caso propio.
	if code, body := doP(t, app, cajeroS2, "POST", fmt.Sprintf("/purchases/%d/void", p2.ID), ""); code != 200 {
		t.Errorf("cajero S2 anulando su compra: %d (%s)", code, body)
	}
	db.First(&st, p2.ID)
	if st.Status != "cancelled" {
		t.Errorf("la compra S2 debería quedar anulada, status=%q", st.Status)
	}
	// La de S1 no se tocó.
	var st1 database.TenantPurchase
	db.First(&st1, p1.ID)
	if st1.Status == "cancelled" {
		t.Error("la compra S1 no debía anularse")
	}
}

func TestPurchases_CreateBranchValidated(t *testing.T) {
	db, _, _, prodID := setupBranchPurchases(t)
	app := newBranchPurchaseApp(db)
	body := func(branch uint) string {
		return fmt.Sprintf(`{"branch_id":%d,"doc_type":"FACTURA","series":"F001","number":"9%d","currency":"PEN","items":[{"product_id":%d,"description":"Arroz","unit":"KGM","quantity":1,"unit_cost":3,"igv_affectation_type":"10"}]}`,
			branch, time.Now().UnixNano()%100000, prodID)
	}
	if code, b := doP(t, app, vendedorS1, "POST", "/purchases", body(2)); code != 403 || !strings.Contains(b, "BRANCH_FORBIDDEN") {
		t.Errorf("vendedor S1 creando compra con branch_id=2: %d, quiere 403 (%s)", code, b)
	}
	var n int64
	db.Model(&database.TenantPurchase{}).Where("branch_id = ?", 2).Count(&n)
	if n != 1 {
		t.Errorf("S2 debe seguir con 1 compra, tiene %d", n)
	}
	// Propio: sin branch_id -> la activa.
	if code, b := doP(t, app, vendedorS1, "POST", "/purchases", body(0)); code != http.StatusCreated {
		t.Errorf("vendedor S1 creando compra propia: %d (%s)", code, b)
	}
}
