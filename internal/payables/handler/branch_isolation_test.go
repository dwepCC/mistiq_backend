package handler

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"github.com/gofiber/fiber/v3"
	"gorm.io/gorm"
)

// Fase 3A: cuentas por pagar aisladas por sucursal (listado y pago).

func setupPayablesBranchDB(t *testing.T) (*gorm.DB, database.TenantPurchase, database.TenantPurchase) {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []interface{}{
		&database.TenantPurchase{}, &database.TenantPurchasePayable{}, &database.TenantPurchasePayment{},
		&database.TenantCashSession{}, &database.TenantCashMovement{}, &database.TenantBankAccount{},
		&database.TenantBankMovement{}, &database.TenantPaymentMethod{}, &database.TenantContact{},
	} {
		if err := db.AutoMigrate(m); err != nil {
			t.Fatal(err)
		}
	}
	db.Create(&database.TenantPaymentMethod{Code: "cash", Name: "Efectivo", IsSystem: true, Active: true, DestinationType: "cash"})
	var out [2]database.TenantPurchase
	for i, br := range []uint{1, 2} {
		u := uint(i + 1)
		sess := database.TenantCashSession{BranchID: br, UserID: u, OpenedBy: u, Status: "open", OpenedAt: time.Now()}
		db.Create(&sess)
		p := database.TenantPurchase{
			BranchID: br, UserID: u, CashSessionID: &sess.ID, DocType: "factura", Series: "F001",
			Number: fmt.Sprintf("0000000%d", br), IssueDate: time.Now(), Total: 100, Status: "received",
		}
		if err := db.Create(&p).Error; err != nil {
			t.Fatal(err)
		}
		db.Create(&database.TenantPurchasePayable{PurchaseID: p.ID, OriginalAmount: 100, Status: "pending"})
		out[i] = p
	}
	return db, out[0], out[1]
}

type act struct {
	user, active uint
	allowed      []uint
	admin        bool
}

func doPay(t *testing.T, db *gorm.DB, a act, method, path, body string) (int, string) {
	t.Helper()
	h := NewPayableHandler()
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		c.Locals("tenantDB", db)
		c.Locals("user_id", a.user)
		c.Locals("active_branch_id", a.active)
		c.Locals("allowed_branch_ids", a.allowed)
		c.Locals("is_branch_admin", a.admin)
		return c.Next()
	})
	app.Get("/payables", h.ListAPI)
	app.Post("/payables/:purchaseId/pay", h.PayAPI)
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

func TestPayables_PayAcrossBranches(t *testing.T) {
	db, p1, p2 := setupPayablesBranchDB(t)
	vendedorS1 := act{user: 1, active: 1, allowed: []uint{1}}
	cajeroS2 := act{user: 2, active: 2, allowed: []uint{2}}
	adminS1 := act{user: 9, active: 1, allowed: []uint{1, 2}, admin: true}
	payBody := `{"amount":40,"method":"cash"}`
	path2 := "/payables/" + strconv.Itoa(int(p2.ID)) + "/pay"

	if code, body := doPay(t, db, vendedorS1, "POST", path2, payBody); code != 404 {
		t.Errorf("vendedor S1 pagando CxP de S2: %d, quiere 404 (%s)", code, body)
	}
	if code, body := doPay(t, db, adminS1, "POST", path2, payBody); code != 403 || !strings.Contains(body, "BRANCH_FORBIDDEN") {
		t.Errorf("admin (activa S1) pagando CxP de S2: %d, quiere 403 BRANCH_FORBIDDEN (%s)", code, body)
	}
	var pays int64
	db.Model(&database.TenantPurchasePayment{}).Count(&pays)
	if pays != 0 {
		t.Fatalf("no debía registrarse ningún pago, hay %d", pays)
	}
	// Casos propios.
	if code, body := doPay(t, db, cajeroS2, "POST", path2, payBody); code != 200 {
		t.Errorf("cajero S2 pagando su CxP: %d (%s)", code, body)
	}
	if code, body := doPay(t, db, vendedorS1, "POST", "/payables/"+strconv.Itoa(int(p1.ID))+"/pay", payBody); code != 200 {
		t.Errorf("vendedor S1 pagando su CxP: %d (%s)", code, body)
	}
}

func TestPayables_ListFilteredByBranch(t *testing.T) {
	db, p1, p2 := setupPayablesBranchDB(t)
	ids := func(body string) []uint {
		var r struct {
			Data []struct {
				PurchaseID uint `json:"purchase_id"`
			} `json:"data"`
		}
		if err := json.Unmarshal([]byte(body), &r); err != nil {
			t.Fatalf("json: %v (%s)", err, body)
		}
		var out []uint
		for _, d := range r.Data {
			out = append(out, d.PurchaseID)
		}
		return out
	}
	code, body := doPay(t, db, act{user: 1, active: 1, allowed: []uint{1}}, "GET", "/payables?branch_id=2", "")
	if got := ids(body); code != 200 || len(got) != 1 || got[0] != p1.ID {
		t.Errorf("vendedor S1 con branch_id=2: %d %v, quiere solo %d", code, got, p1.ID)
	}
	code, body = doPay(t, db, act{user: 9, active: 1, allowed: []uint{1, 2}, admin: true}, "GET", "/payables?branch_id=2", "")
	if got := ids(body); code != 200 || len(got) != 1 || got[0] != p2.ID {
		t.Errorf("admin con branch_id=2: %d %v, quiere solo %d", code, got, p2.ID)
	}
}
