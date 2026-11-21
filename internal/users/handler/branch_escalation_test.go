package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
	"testing"

	"tukifac/pkg/branch"
	"tukifac/pkg/database"
	"tukifac/pkg/middleware"

	"github.com/glebarez/sqlite"
	"github.com/gofiber/fiber/v3"
	"gorm.io/gorm"
)

func escalationEnv(t *testing.T) (*gorm.DB, uint, uint, uint) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.ApplyBaselineSchema(db); err != nil {
		t.Fatal(err)
	}
	db.AutoMigrate(&database.TenantUserBranch{})
	viewAll := database.TenantPermission{Module: "branches", Action: "view_all", Label: "x"}
	manage := database.TenantPermission{Module: "branches", Action: "manage", Label: "x"}
	db.Create(&viewAll)
	db.Create(&manage)
	boss := database.TenantRole{Name: "Gerente global"}
	plain := database.TenantRole{Name: "Vendedor"}
	db.Create(&boss)
	db.Create(&plain)
	db.Create(&database.TenantRolePermission{RoleID: boss.ID, PermissionID: viewAll.ID})
	return db, boss.ID, plain.ID, viewAll.ID
}

func escalationApp(db *gorm.DB, perms []string, viewAllActor bool, allowed []uint) *fiber.App {
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		c.Locals("tenantDB", db)
		c.Locals("tenant_claims", &middleware.TenantClaims{UserID: 1, Permissions: perms})
		c.Locals("is_branch_admin", viewAllActor)
		c.Locals("active_branch_id", allowed[0])
		c.Locals("allowed_branch_ids", allowed)
		return c.Next()
	})
	return app
}

func post(t *testing.T, app *fiber.App, path string, body any) int {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	io.ReadAll(resp.Body)
	return resp.StatusCode
}

// Quien administra usuarios sin branches.manage/view_all no puede otorgar acceso global ni sucursales ajenas.
func TestDenyBranchEscalationForUser(t *testing.T) {
	db, boss, plain, _ := escalationEnv(t)
	probe := func(c fiber.Ctx) error {
		var in struct {
			RoleID    uint   `json:"role_id"`
			BranchIDs []uint `json:"branch_ids"`
		}
		_ = c.Bind().JSON(&in)
		if denyBranchEscalationForUser(c, in.RoleID, nil, in.BranchIDs) {
			return nil
		}
		return c.SendStatus(200)
	}
	limited := escalationApp(db, []string{"users.create"}, false, []uint{1})
	limited.Post("/u", probe)
	if c := post(t, limited, "/u", map[string]any{"role_id": boss}); c != 403 {
		t.Errorf("asignar rol con view_all sin ser admin de sucursales: %d, quiere 403", c)
	}
	if c := post(t, limited, "/u", map[string]any{"role_id": plain, "branch_ids": []uint{2}}); c != 403 {
		t.Errorf("asignar sucursal ajena: %d, quiere 403", c)
	}
	if c := post(t, limited, "/u", map[string]any{"role_id": plain, "branch_ids": []uint{1}}); c != 200 {
		t.Errorf("rol normal en sucursal propia: %d, quiere 200", c)
	}

	manager := escalationApp(db, []string{branch.PermManage}, false, []uint{1})
	manager.Post("/u", probe)
	if c := post(t, manager, "/u", map[string]any{"role_id": boss, "branch_ids": []uint{2}}); c != 200 {
		t.Errorf("con branches.manage puede otorgar: %d", c)
	}
	admin := escalationApp(db, nil, true, []uint{1})
	admin.Post("/u", probe)
	if c := post(t, admin, "/u", map[string]any{"role_id": boss}); c != 200 {
		t.Errorf("quien ve todas puede otorgar: %d", c)
	}
}

func TestDenyBranchEscalationForRole(t *testing.T) {
	db, _, _, viewAllPerm := escalationEnv(t)
	var other database.TenantPermission
	db.Create(&database.TenantPermission{Module: "sales", Action: "view", Label: "x"})
	db.Where("module = ?", "sales").First(&other)
	probe := func(c fiber.Ctx) error {
		var in struct {
			PermissionIDs []uint `json:"permission_ids"`
		}
		_ = c.Bind().JSON(&in)
		if denyBranchEscalationForRole(c, in.PermissionIDs) {
			return nil
		}
		return c.SendStatus(200)
	}
	limited := escalationApp(db, []string{"roles.manage"}, false, []uint{1})
	limited.Post("/r", probe)
	if c := post(t, limited, "/r", map[string]any{"permission_ids": []uint{viewAllPerm}}); c != 403 {
		t.Errorf("otorgar view_all a un rol sin poder: %d, quiere 403", c)
	}
	if c := post(t, limited, "/r", map[string]any{"permission_ids": []uint{other.ID}}); c != 200 {
		t.Errorf("permiso normal: %d, quiere 200", c)
	}
	admin := escalationApp(db, nil, true, []uint{1})
	admin.Post("/r", probe)
	if c := post(t, admin, "/r", map[string]any{"permission_ids": []uint{viewAllPerm}}); c != 200 {
		t.Errorf("quien ve todas puede otorgar view_all: %d", c)
	}
}
