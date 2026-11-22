package equipos

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"tukifac/config"
	"tukifac/pkg/database"
	"tukifac/pkg/logger"
	"tukifac/pkg/middleware"

	"github.com/glebarez/sqlite"
	"github.com/gofiber/fiber/v3"
	"github.com/golang-jwt/jwt/v5"
	"gorm.io/gorm"
)

// Asignar usuario a un pedido sin usuario es solo del superadmin (403 para el resto), 404/409/200 según el pedido.
func TestRoutes_assignOrderCreator(t *testing.T) {
	logger.Init(&config.Config{LogLevel: "error", AppEnv: "development"})
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&database.SuperAdminUser{}, &database.AuditLog{}, &database.EquipOrder{}); err != nil {
		t.Fatal(err)
	}
	prevDB := database.CentralDB
	database.CentralDB = db
	t.Cleanup(func() { database.CentralDB = prevDB })
	prevCfg := config.AppConfig
	config.AppConfig = &config.Config{AppEnv: "development", SAJWTSecret: testSecret}
	t.Cleanup(func() { config.AppConfig = prevCfg })

	mk := func(name, role string) database.SuperAdminUser {
		u := database.SuperAdminUser{Name: name, Email: name + "@example.com", Role: role, Active: true, Password: "x"}
		if err := db.Create(&u).Error; err != nil {
			t.Fatal(err)
		}
		return u
	}
	root, plain, target := mk("root", "superadmin"), mk("plain", "admin"), mk("target", "admin")
	free := database.EquipOrder{OrderNumber: 1}
	owned := database.EquipOrder{OrderNumber: 2, CreatedBy: &plain.ID}
	db.Create(&free)
	db.Create(&owned)

	app := fiber.New()
	RegisterRoutes(app.Group("/api/superadmin", middleware.SuperAdminAuthAPI()))
	mint := func(u database.SuperAdminUser) string {
		claims := &middleware.SuperAdminClaims{
			UserID: u.ID, Email: u.Email, Role: u.Role, Type: "superadmin", Permissions: []string{"equipos.view", "equipos.update"},
			SAJWTVersion:     middleware.CurrentSuperAdminJWTVersion(),
			RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)), IssuedAt: jwt.NewNumericDate(time.Now())},
		}
		s, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(testSecret))
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	do := func(method, path, tok, body string) int {
		req := httptest.NewRequest(method, "/api/superadmin"+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+tok)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp.StatusCode
	}
	body := fmt.Sprintf(`{"user_id":%d}`, target.ID)
	p := fmt.Sprintf("/equipos/orders/%d/creator", free.ID)
	if c := do("PATCH", p, mint(plain), body); c != http.StatusForbidden {
		t.Errorf("no superadmin: %d, want 403", c)
	}
	if c := do("GET", "/equipos/orders/assignable-users", mint(plain), ""); c != http.StatusForbidden {
		t.Errorf("lista para no superadmin: %d, want 403", c)
	}
	if c := do("GET", "/equipos/orders/assignable-users", mint(root), ""); c != http.StatusOK {
		t.Errorf("lista superadmin: %d", c)
	}
	if c := do("PATCH", "/equipos/orders/999/creator", mint(root), body); c != http.StatusNotFound {
		t.Errorf("pedido inexistente: %d, want 404", c)
	}
	if c := do("PATCH", fmt.Sprintf("/equipos/orders/%d/creator", owned.ID), mint(root), body); c != http.StatusConflict {
		t.Errorf("pedido con usuario: %d, want 409", c)
	}
	if c := do("PATCH", p, mint(root), `{"user_id":9999}`); c != http.StatusBadRequest {
		t.Errorf("usuario inexistente: %d, want 400", c)
	}
	for i := 0; i < 2; i++ { // la segunda vez es idempotente
		if c := do("PATCH", p, mint(root), body); c != http.StatusOK {
			t.Errorf("asignar #%d: %d, want 200", i+1, c)
		}
	}
	var n int64
	db.Model(&database.AuditLog{}).Where("action = ? AND entity_id = ?", "equip_order_creator_assigned", free.ID).Count(&n)
	if n < 1 {
		t.Error("falta el registro de auditoría")
	}
}
