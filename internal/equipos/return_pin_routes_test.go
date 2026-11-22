package equipos

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"tukifac/config"
	"tukifac/internal/equipos/service"
	"tukifac/pkg/database"
	"tukifac/pkg/logger"
	"tukifac/pkg/middleware"

	"github.com/glebarez/sqlite"
	"github.com/gofiber/fiber/v3"
	"github.com/golang-jwt/jwt/v5"
	"gorm.io/gorm"
)

// Registrar un retorno exige el PIN de seguridad (cabecera X-Security-Pin) y lo deja anotado en la auditoría.
func TestRoutes_createReturnRequiresPin(t *testing.T) {
	logger.Init(&config.Config{LogLevel: "error", AppEnv: "development"})
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&database.SuperAdminUser{}, &database.AuditLog{}, &database.EquipOrder{}, &database.EquipOrderItem{}, &database.EquipShipment{},
		&database.EquipCarrier{}, &database.EquipSettings{}, &database.EquipProduct{}, &database.EquipReturn{}); err != nil {
		t.Fatal(err)
	}
	prevDB := database.CentralDB
	database.CentralDB = db
	t.Cleanup(func() { database.CentralDB = prevDB })
	prevCfg := config.AppConfig
	config.AppConfig = &config.Config{AppEnv: "development", SAJWTSecret: testSecret}
	t.Cleanup(func() { config.AppConfig = prevCfg })

	u := database.SuperAdminUser{Name: "op", Email: "op@example.com", Role: "admin", Active: true, Password: "x"}
	db.Create(&u)
	if err := service.New(db).SetSecurityPin("123456", "", u.ID); err != nil {
		t.Fatal(err)
	}
	prod := database.EquipProduct{Code: "P1", Name: "Equipo"}
	db.Create(&prod)
	order := database.EquipOrder{OrderNumber: 1, Status: "registrado"}
	db.Create(&order)
	db.Create(&database.EquipOrderItem{OrderID: order.ID, LineNo: 1, LineType: "producto", ProductID: &prod.ID, Quantity: 2})
	db.Create(&database.EquipShipment{OrderID: order.ID, Status: "en_agencia", IsCurrent: true})

	app := fiber.New()
	RegisterRoutes(app.Group("/api/superadmin", middleware.SuperAdminAuthAPI()))
	claims := &middleware.SuperAdminClaims{
		UserID: u.ID, Email: u.Email, Role: "admin", Type: "superadmin", Permissions: []string{"equipos.returns"},
		SAJWTVersion:     middleware.CurrentSuperAdminJWTVersion(),
		RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)), IssuedAt: jwt.NewNumericDate(time.Now())},
	}
	tok, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(testSecret))
	post := func(pin, body string) int {
		req := httptest.NewRequest("POST", "/api/superadmin/equipos/returns", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+tok)
		if pin != "" {
			req.Header.Set("X-Security-Pin", pin)
		}
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp.StatusCode
	}
	body := fmt.Sprintf(`{"order_id":%d}`, order.ID)
	if c := post("", body); c != http.StatusBadRequest {
		t.Errorf("sin PIN: %d, want 400", c)
	}
	if c := post("000000", body); c != http.StatusBadRequest {
		t.Errorf("PIN incorrecto: %d, want 400", c)
	}
	var n int64
	db.Model(&database.EquipReturn{}).Count(&n)
	if n != 0 {
		t.Fatalf("no debe crearse retorno sin PIN válido: %d", n)
	}
	partial := fmt.Sprintf(`{"order_id":%d,"items":[{"product_id":%d,"quantity":1}]}`, order.ID, prod.ID)
	if c := post("123456", partial); c != http.StatusBadRequest {
		t.Errorf("retorno parcial: %d, want 400", c)
	}
	if c := post("123456", body); c != http.StatusCreated {
		t.Fatalf("PIN correcto: %d, want 201", c)
	}
	var a database.AuditLog
	if err := db.Where("action = ?", "equip_return_created").First(&a).Error; err != nil || !strings.Contains(a.Payload, "pin_verified") {
		t.Errorf("auditoría: %v %+v", err, a)
	}
}
