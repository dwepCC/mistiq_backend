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

// La llegada a agencia no pide PIN: basta equipos.shipments. La fecha futura se rechaza y la auditoría guarda la fecha resuelta.
func TestRoutes_arrivalWithoutPin(t *testing.T) {
	logger.Init(&config.Config{LogLevel: "error", AppEnv: "development"})
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&database.SuperAdminUser{}, &database.AuditLog{}, &database.EquipOrder{}, &database.EquipOrderItem{}, &database.EquipShipment{}, &database.EquipCarrier{}, &database.EquipSettings{}, &database.EquipCarrierAgency{}, &database.EquipPayment{}, &database.EquipPaymentAllocation{}, &database.EquipCustomer{}, &database.EquipProduct{}, &database.EquipCombo{}, &database.EquipComboItem{}); err != nil {
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
	order := database.EquipOrder{OrderNumber: 1, Status: "registrado"}
	db.Create(&order)
	db.Create(&database.EquipShipment{OrderID: order.ID, Status: "en_transito", IsCurrent: true, DeliveryMode: "agencia"})

	app := fiber.New()
	RegisterRoutes(app.Group("/api/superadmin", middleware.SuperAdminAuthAPI()))
	mint := func(perms ...string) string {
		claims := &middleware.SuperAdminClaims{
			UserID: u.ID, Email: u.Email, Role: "admin", Type: "superadmin", Permissions: perms,
			SAJWTVersion:     middleware.CurrentSuperAdminJWTVersion(),
			RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)), IssuedAt: jwt.NewNumericDate(time.Now())},
		}
		s, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(testSecret))
		return s
	}
	post := func(tok, body string) int {
		req := httptest.NewRequest("POST", fmt.Sprintf("/api/superadmin/equipos/orders/%d/arrived", order.ID), strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+tok)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp.StatusCode
	}
	if c := post(mint("equipos.view"), `{}`); c != http.StatusForbidden {
		t.Errorf("sin permiso de envíos: %d, want 403", c)
	}
	tomorrow := time.Now().In(time.FixedZone("Lima", -5*3600)).AddDate(0, 0, 1).Format("2006-01-02")
	if c := post(mint("equipos.shipments"), fmt.Sprintf(`{"date":%q}`, tomorrow)); c != http.StatusBadRequest {
		t.Errorf("fecha futura: %d, want 400", c)
	}
	if c := post(mint("equipos.shipments"), `{}`); c != http.StatusOK { // sin cabecera X-Security-Pin
		t.Fatalf("llegada sin PIN: %d, want 200", c)
	}
	var a database.AuditLog
	if err := db.Where("action = ? AND entity_id = ?", "equip_shipment_arrived", order.ID).First(&a).Error; err != nil || a.UserID != u.ID || !strings.Contains(a.Payload, "arrived_date") {
		t.Errorf("auditoría: %v %+v", err, a)
	}
}
