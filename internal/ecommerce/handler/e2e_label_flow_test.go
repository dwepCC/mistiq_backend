package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"tukifac/internal/ecommerce/service"
	"tukifac/pkg/database"
	"tukifac/pkg/middleware"

	"github.com/glebarez/sqlite"
	"github.com/gofiber/fiber/v3"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// TestE2E_EtiquetaLogistica Fase 10 §20 — flujo integral real vía HTTP:
// Pedido -> LISTO_PARA_DESPACHO -> DESPACHADO -> generar etiqueta -> verificar documento ->
// reimprimir -> verificar que no creó otro Dispatch -> verificar que no cambió Order.Status ->
// verificar que no cambió stock.
func TestE2E_EtiquetaLogistica(t *testing.T) {
	dsn := "file:e2e-fase10-label?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&database.TenantProduct{}, &database.TenantProductPresentation{},
		&database.TenantProductStock{}, &database.TenantProductPresentationStock{},
		&database.TenantStockMovement{}, &database.TenantBranch{}, &database.TenantCompanyConfig{},
		&database.TenantEcommerceOrder{}, &database.TenantEcommerceOrderItem{},
		&database.TenantEcommerceOrderStatusHistory{}, &database.TenantEcommerceDispatch{},
		&database.TenantEcommerceDispatchStatusHistory{}, &database.TenantNotification{},
	); err != nil {
		t.Fatal(err)
	}

	h := NewEcommerceHandler()
	app := fiber.New()
	permissions := []string{"ecommerce.manage"}
	app.Use(func(c fiber.Ctx) error {
		c.Locals("tenantDB", db)
		c.Locals("user_id", uint(60))
		c.Locals("tenant_claims", &middleware.TenantClaims{UserID: 60, Permissions: permissions})
		return c.Next()
	})
	app.Put("/api/ecommerce/orders/:id/status", h.UpdateOrderStatusAPI)
	app.Post("/api/ecommerce/orders/:id/dispatch", h.CreateDispatchAPI)
	app.Get("/api/ecommerce/orders/:id/label", h.OrderLabelAPI)

	// Producto con stock — para el chequeo final de inventario intacto.
	branch := database.TenantBranch{Name: "Sucursal E2E"}
	db.Create(&branch)
	db.Create(&database.TenantCompanyConfig{ID: 1, BusinessName: "Mistiq Demo"})
	product := database.TenantProduct{
		Code: "E2ELBL", Name: "Producto etiqueta E2E", Type: "product", Unit: "NIU", SalePrice: 30,
		Active: true, ShowInDigitalCatalog: true, ManageStock: true, BranchID: branch.ID,
	}
	db.Create(&product)
	db.Create(&database.TenantProductStock{ProductID: product.ID, BranchID: branch.ID, Quantity: 12})

	svc := service.NewEcommerceService(db)
	order, _, err := svc.CreateOrder(service.CreateOrderInput{
		CustomerName: "Cliente Etiqueta", CustomerPhone: "999222444", DeliveryMethod: service.DeliveryMethodShipping,
		GuestAddressLine: "Jr. Etiqueta 100", GuestReference: "Casa azul",
		Items: []service.CreateOrderItemInput{{ProductID: product.ID, Quantity: 1}},
	})
	if err != nil {
		t.Fatal(err)
	}

	putStatus := func(status string, branchID uint) *http.Response {
		payload := map[string]any{"status": status}
		if branchID > 0 {
			payload["branch_id"] = branchID
		}
		body, _ := json.Marshal(payload)
		req := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/ecommerce/orders/%d/status", order.ID), bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := app.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}

	// 1. Pedido -> LISTO_PARA_DESPACHO (asigna sucursal en la confirmación, como haría el panel real).
	if resp := putStatus("CONFIRMADO", branch.ID); resp.StatusCode != http.StatusOK {
		t.Fatalf("transición a CONFIRMADO falló, status=%d", resp.StatusCode)
	}
	for _, to := range []string{"EN_PREPARACION", "EMPAQUETADO", "LISTO_PARA_DESPACHO"} {
		if resp := putStatus(to, 0); resp.StatusCode != http.StatusOK {
			t.Fatalf("transición a %s falló, status=%d", to, resp.StatusCode)
		}
	}

	// 2. -> DESPACHADO (crea el Dispatch).
	dispatchBody, _ := json.Marshal(map[string]any{"carrier_name": "Olva Courier", "tracking_code": "E2E-LBL-1", "package_count": 1})
	dReq := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/ecommerce/orders/%d/dispatch", order.ID), bytes.NewReader(dispatchBody))
	dReq.Header.Set("Content-Type", "application/json")
	dResp, err := app.Test(dReq)
	if err != nil {
		t.Fatal(err)
	}
	if dResp.StatusCode != http.StatusCreated {
		t.Fatalf("crear despacho falló, status=%d", dResp.StatusCode)
	}

	// 3. Generar etiqueta -> verificar documento.
	labelReq := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/ecommerce/orders/%d/label", order.ID), nil)
	labelResp, err := app.Test(labelReq)
	if err != nil {
		t.Fatal(err)
	}
	if labelResp.StatusCode != http.StatusOK {
		t.Fatalf("generar etiqueta falló, status=%d", labelResp.StatusCode)
	}
	var labelOut struct {
		Label service.EcommerceLabelData `json:"label"`
	}
	json.NewDecoder(labelResp.Body).Decode(&labelOut)
	if labelOut.Label.CustomerName != "Cliente Etiqueta" {
		t.Errorf("etiqueta.customer_name = %q", labelOut.Label.CustomerName)
	}
	if labelOut.Label.CarrierName != "Olva Courier" || labelOut.Label.TrackingCode != "E2E-LBL-1" {
		t.Errorf("etiqueta no trae carrier/tracking reales: %+v", labelOut.Label)
	}
	if labelOut.Label.AddressLine != "Jr. Etiqueta 100" {
		t.Errorf("etiqueta.address_line = %q, quería la dirección real del pedido", labelOut.Label.AddressLine)
	}
	if labelOut.Label.BranchName != "Sucursal E2E" {
		t.Errorf("etiqueta.branch_name = %q", labelOut.Label.BranchName)
	}

	// 4. Reimprimir.
	labelReq2 := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/ecommerce/orders/%d/label", order.ID), nil)
	labelResp2, err := app.Test(labelReq2)
	if err != nil {
		t.Fatal(err)
	}
	if labelResp2.StatusCode != http.StatusOK {
		t.Fatalf("reimprimir falló, status=%d", labelResp2.StatusCode)
	}

	// 5. Verificar que no creó otro Dispatch.
	var dispatchCount int64
	db.Model(&database.TenantEcommerceDispatch{}).Where("order_id = ?", order.ID).Count(&dispatchCount)
	if dispatchCount != 1 {
		t.Fatalf("reimprimir no debe crear un segundo Dispatch, hay %d", dispatchCount)
	}

	// 6. Verificar que no cambió Order.Status.
	var afterOrder database.TenantEcommerceOrder
	db.First(&afterOrder, order.ID)
	if afterOrder.Status != service.OrderStatusDespachado {
		t.Fatalf("generar/reimprimir la etiqueta no debe cambiar Order.Status, quedó %q", afterOrder.Status)
	}

	// 7. Verificar que no cambió stock.
	var stock database.TenantProductStock
	db.Where("product_id = ? AND branch_id = ?", product.ID, branch.ID).First(&stock)
	if stock.Quantity != 12 {
		t.Fatalf("REGRESIÓN: generar/reimprimir la etiqueta no debe tocar stock, quedó %.2f", stock.Quantity)
	}
	var movementCount int64
	db.Model(&database.TenantStockMovement{}).Where("product_id = ?", product.ID).Count(&movementCount)
	if movementCount != 0 {
		t.Fatalf("no debe registrarse ningún movimiento de kardex, hay %d", movementCount)
	}
}
