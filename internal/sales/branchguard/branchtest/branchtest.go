// Package branchtest utilidades SOLO para tests de aislamiento por sucursal: una BD SQLite en
// memoria, tres actores (vendedor de la sucursal 1, cajero de la sucursal 2 y administrador) y una
// app Fiber que inyecta las mismas Locals que el middleware de sucursal.
package branchtest

import (
	"fmt"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"tukifac/config"

	"github.com/glebarez/sqlite"
	"github.com/gofiber/fiber/v3"
	"gorm.io/gorm"
)

// Actor usuario simulado tal como lo deja el middleware.
type Actor struct {
	Name    string
	UserID  uint
	Active  uint
	Admin   bool // IsBranchAdmin: ve todas las sucursales (rol Administrador)
	Allowed []uint
}

var (
	// Vendedor1: vendedor asignado solo a la sucursal 1.
	Vendedor1 = Actor{Name: "vendedor S1", UserID: 11, Active: 1, Allowed: []uint{1}}
	// Cajero2: cajero asignado solo a la sucursal 2.
	Cajero2 = Actor{Name: "cajero S2", UserID: 22, Active: 2, Allowed: []uint{2}}
	// Admin: Administrador, con la sucursal 1 activa; ve todas pero solo escribe en la activa.
	Admin = Actor{Name: "admin (activa S1)", UserID: 1, Active: 1, Admin: true, Allowed: []uint{1, 2}}
)

// OpenDB abre una SQLite en memoria aislada por test y migra los modelos indicados.
func OpenDB(t *testing.T, models ...interface{}) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range models {
		if err := db.AutoMigrate(m); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

// NewApp devuelve una app Fiber donde cada request corre como el actor indicado.
func NewApp(db *gorm.DB, a Actor) *fiber.App {
	if config.AppConfig == nil {
		// Sin facturador configurado (URL/token vacíos): los servicios de facturación no llaman a SUNAT.
		config.AppConfig = &config.Config{}
	}
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		c.Locals("tenantDB", db)
		c.Locals("user_id", a.UserID)
		c.Locals("user_email", strings.ReplaceAll(a.Name, " ", "_")+"@test")
		c.Locals("active_branch_id", a.Active)
		c.Locals("is_branch_admin", a.Admin)
		c.Locals("allowed_branch_ids", a.Allowed)
		return c.Next()
	})
	return app
}

// Do ejecuta una request JSON y devuelve status y cuerpo.
func Do(t *testing.T, app *fiber.App, method, path, body string) (int, string) {
	t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, path, rd)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := app.Test(req, fiber.TestConfig{Timeout: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}
