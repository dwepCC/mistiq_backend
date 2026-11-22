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

// PATCH shipping-data: exige equipos.shipments, no pide PIN, rechaza campos ajenos y audita antes/después.
func TestRoutes_shippingData(t *testing.T) {
	logger.Init(&config.Config{LogLevel: "error", AppEnv: "development"})
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&database.SuperAdminUser{}, &database.AuditLog{}, &database.EquipOrder{}, &database.EquipOrderItem{}, &database.EquipShipment{},
		&database.EquipCarrier{}, &database.EquipSettings{}, &database.EquipCarrierAgency{}, &database.EquipPayment{}, &database.EquipPaymentAllocation{},
		&database.EquipCustomer{}, &database.EquipProduct{}, &database.EquipCombo{}, &database.EquipComboItem{}); err != nil {
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
	db.Create(&database.EquipShipment{OrderID: order.ID, Status: "pendiente_envio", IsCurrent: true, DeliveryMode: "agencia", Notes: "no tocar"})

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
	patch := func(tok, body string) int {
		req := httptest.NewRequest("PATCH", fmt.Sprintf("/api/superadmin/equipos/orders/%d/shipping-data", order.ID), strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+tok)
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp.StatusCode
	}
	if c := patch(mint("equipos.view"), `{"guide_number":"G1"}`); c != http.StatusForbidden {
		t.Errorf("sin permiso: %d, want 403", c)
	}
	ok := mint("equipos.shipments")
	if c := patch(ok, `{"guide_number":"G1","notes":"otro"}`); c != http.StatusBadRequest {
		t.Errorf("campo ajeno: %d, want 400", c)
	}
	if c := patch(ok, `{"freight_cost":-5}`); c != http.StatusBadRequest {
		t.Errorf("flete negativo: %d, want 400", c)
	}
	if c := patch(ok, `{"guide_number":"G1","freight_cost":9.5,"scheduled_dispatch_date":"2026-11-02"}`); c != http.StatusOK { // sin X-Security-Pin
		t.Fatalf("guardar sin PIN: %d, want 200", c)
	}
	var sh database.EquipShipment
	db.First(&sh, "order_id = ?", order.ID)
	if sh.GuideNumber != "G1" || sh.FreightCost != 9.5 || sh.Notes != "no tocar" || sh.ScheduledDispatchDate == nil {
		t.Errorf("envío: %+v", sh)
	}
	var a database.AuditLog
	if err := db.Where("action = ? AND entity_id = ?", "equip_shipping_data_updated", order.ID).First(&a).Error; err != nil || a.UserID != u.ID || !strings.Contains(a.Payload, "G1") {
		t.Errorf("auditoría: %v %+v", err, a)
	}
}
