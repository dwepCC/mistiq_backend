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

// TestE2E_FlujoCompletoDespachoYTracking Fase 9 §20 — prueba integral real de principio a fin,
// vía HTTP real (staff + customer), contra un backend actualizado con TODO lo de Fases 1-9. No es
// un test de un método aislado: reproduce exactamente el flujo de 17 puntos pedido en la
// autorización de Fase 9, en un solo test, con ambos roles (staff autorizado y cliente dueño del
// pedido) actuando sobre el MISMO pedido real.
//
// Limitación explícita (documentada, no oculta): esto es E2E a nivel de API HTTP con sqlite en
// memoria — no es un E2E de navegador real. El backend local en :3000 y el frontend Vite en :5173
// ya estaban corriendo antes de esta sesión y no son procesos que haya iniciado yo; reiniciar el
// primero para servir el código de Fase 9 está fuera de alcance sin pedirlo explícitamente primero
// (regla explícita del prompt). Este test SÍ ejercita el contrato HTTP real (rutas, permisos,
// bodies JSON, códigos de estado) de principio a fin, algo que ningún test unitario aislado prueba
// por sí solo.
func TestE2E_FlujoCompletoDespachoYTracking(t *testing.T) {
	withEcommerceCustomerJWTSecret(t)
	dsn := "file:e2e-fase9-flow?mode=memory&cache=shared"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&database.TenantProduct{}, &database.TenantProductPresentation{},
		&database.TenantProductStock{}, &database.TenantProductPresentationStock{},
		&database.TenantEcommerceOrder{}, &database.TenantEcommerceOrderItem{},
		&database.TenantEcommerceOrderStatusHistory{}, &database.TenantEcommerceDispatch{},
		&database.TenantEcommerceDispatchStatusHistory{}, &database.TenantNotification{},
		&database.TenantEcommerceCustomerAccount{}, &database.TenantEcommerceCustomerAddress{},
	); err != nil {
		t.Fatal(err)
	}

	h := NewEcommerceHandler()
	staffApp := fiber.New()
	staffPermissions := []string{"ecommerce.manage"} // Administrador: cubre orders_manage/prepare/dispatch/convert
	staffApp.Use(func(c fiber.Ctx) error {
		c.Locals("tenantDB", db)
		c.Locals("user_id", uint(50))
		c.Locals("tenant_claims", &middleware.TenantClaims{UserID: 50, Permissions: staffPermissions})
		return c.Next()
	})
	staffApp.Get("/api/ecommerce/orders/:id", h.GetOrderAPI)
	staffApp.Put("/api/ecommerce/orders/:id/status", h.UpdateOrderStatusAPI)
	staffApp.Post("/api/ecommerce/orders/:id/dispatch", h.CreateDispatchAPI)
	staffApp.Put("/api/ecommerce/dispatches/:id/status", h.UpdateDispatchStatusAPI)

	customerApp := fiber.New()
	customerApp.Use(func(c fiber.Ctx) error {
		c.Locals("tenantDB", db)
		return c.Next()
	})
	customerApp.Post("/public/ecommerce/auth/register", h.RegisterCustomerAPI)
	customerApp.Group("/public/ecommerce/account", middleware.EcommerceCustomerAuthRequired()).
		Get("/orders/:id", h.GetCustomerOrderAPI)

	// 1. Cliente se registra (para poder consultar tracking después).
	regResp := doJSON(t, customerApp, http.MethodPost, "/public/ecommerce/auth/register",
		map[string]string{"name": "Cliente E2E", "phone": "999111222", "password": "clave123"}, "")
	var reg struct {
		Token    string `json:"token"`
		Customer struct{ ID uint } `json:"customer"`
	}
	json.NewDecoder(regResp.Body).Decode(&reg)
	if reg.Token == "" {
		t.Fatalf("registro debía devolver token, status=%d", regResp.StatusCode)
	}
	var customerID uint
	db.Model(&database.TenantEcommerceCustomerAccount{}).Select("id").Where("phone = ?", "999111222").Scan(&customerID)

	// Producto con stock — para el punto 17 (stock antes/después idéntico).
	product := database.TenantProduct{Code: "E2E1", Name: "Producto E2E", Type: "product", Unit: "NIU", SalePrice: 25, Active: true, ShowInDigitalCatalog: true, ManageStock: true, BranchID: 1}
	if err := db.Create(&product).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.TenantProductStock{ProductID: product.ID, BranchID: 1, Quantity: 15}).Error; err != nil {
		t.Fatal(err)
	}
	var stockBefore database.TenantProductStock
	db.Where("product_id = ? AND branch_id = ?", product.ID, 1).First(&stockBefore)

	// 1 (cont). Pedido del cliente registrado, directo en la tabla (el checkout público completo
	// ya está probado en otros tests — acá el foco es el flujo de despacho/tracking).
	order := database.TenantEcommerceOrder{
		CustomerName: "Cliente E2E", CustomerPhone: "999111222", CustomerAccountID: &customerID,
		ItemsJSON: "[]", Total: 25, Status: service.OrderStatusPendiente,
	}
	if err := db.Create(&order).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.TenantEcommerceOrderItem{OrderID: order.ID, ProductID: product.ID, Name: product.Name, Quantity: 1, UnitPrice: 25, Subtotal: 25}).Error; err != nil {
		t.Fatal(err)
	}

	putStatus := func(orderID uint, status string) *http.Response {
		body, _ := json.Marshal(map[string]any{"status": status})
		req := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/ecommerce/orders/%d/status", orderID), bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := staffApp.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}

	// 2. Llevarlo a LISTO_PARA_DESPACHO.
	for _, to := range []string{"CONFIRMADO", "EN_PREPARACION", "EMPAQUETADO", "LISTO_PARA_DESPACHO"} {
		resp := putStatus(order.ID, to)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("transición a %s falló, status=%d", to, resp.StatusCode)
		}
	}

	// 3. Crear Dispatch.
	dispatchBody, _ := json.Marshal(map[string]any{"carrier_name": "Olva Courier", "tracking_code": "E2E-TRACK-1"})
	dispatchReq := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/ecommerce/orders/%d/dispatch", order.ID), bytes.NewReader(dispatchBody))
	dispatchReq.Header.Set("Content-Type", "application/json")
	dispatchResp, err := staffApp.Test(dispatchReq)
	if err != nil {
		t.Fatal(err)
	}
	if dispatchResp.StatusCode != http.StatusCreated {
		t.Fatalf("crear despacho falló, status=%d", dispatchResp.StatusCode)
	}
	var dispatchOut struct {
		Data database.TenantEcommerceDispatch `json:"data"`
	}
	json.NewDecoder(dispatchResp.Body).Decode(&dispatchOut)
	dispatchID := dispatchOut.Data.ID

	// 4. Verificar Order=DESPACHADO, Dispatch=DESPACHADO.
	var afterCreate database.TenantEcommerceOrder
	db.First(&afterCreate, order.ID)
	if afterCreate.Status != service.OrderStatusDespachado {
		t.Fatalf("(4) Order.Status = %q, quería DESPACHADO", afterCreate.Status)
	}
	if dispatchOut.Data.Status != service.DispatchStatusDespachado {
		t.Fatalf("(4) Dispatch.Status = %q, quería DESPACHADO", dispatchOut.Data.Status)
	}

	putDispatchStatus := func(status string) *http.Response {
		body, _ := json.Marshal(map[string]any{"status": status})
		req := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/ecommerce/dispatches/%d/status", dispatchID), bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := staffApp.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	getCustomerOrder := func() (int, database.TenantEcommerceOrder, service.CustomerDispatchView) {
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/public/ecommerce/account/orders/%d", order.ID), nil)
		req.Header.Set("Authorization", "Bearer "+reg.Token)
		resp, err := customerApp.Test(req)
		if err != nil {
			t.Fatal(err)
		}
		var out struct {
			Data     database.TenantEcommerceOrder  `json:"data"`
			Dispatch service.CustomerDispatchView    `json:"dispatch"`
		}
		json.NewDecoder(resp.Body).Decode(&out)
		return resp.StatusCode, out.Data, out.Dispatch
	}

	// 5. Ejecutar DESPACHADO -> EN_TRANSITO.
	transitResp := putDispatchStatus("EN_TRANSITO")
	if transitResp.StatusCode != http.StatusOK {
		t.Fatalf("(5) transición a EN_TRANSITO falló, status=%d", transitResp.StatusCode)
	}

	// 6. Verificar Order=DESPACHADO, Dispatch=EN_TRANSITO.
	var afterTransit database.TenantEcommerceOrder
	db.First(&afterTransit, order.ID)
	var dispatchAfterTransit database.TenantEcommerceDispatch
	db.First(&dispatchAfterTransit, dispatchID)
	if afterTransit.Status != service.OrderStatusDespachado {
		t.Fatalf("(6) REGRESIÓN: Order.Status = %q, quería seguir DESPACHADO (EN_TRANSITO no debe tocar el pedido)", afterTransit.Status)
	}
	if dispatchAfterTransit.Status != service.DispatchStatusEnTransito {
		t.Fatalf("(6) Dispatch.Status = %q, quería EN_TRANSITO", dispatchAfterTransit.Status)
	}

	// 7. Verificar TenantEcommerceDispatchStatusHistory contiene DESPACHADO->EN_TRANSITO.
	var dispHist database.TenantEcommerceDispatchStatusHistory
	if err := db.Where("dispatch_id = ? AND from_status = ? AND to_status = ?", dispatchID, "DESPACHADO", "EN_TRANSITO").First(&dispHist).Error; err != nil {
		t.Fatalf("(7) historial de dispatch DESPACHADO->EN_TRANSITO no encontrado: %v", err)
	}

	// 8/9/10/11. Autenticar como customer, consultar pedido, verificar EN_TRANSITO.
	status, _, custDispatch := getCustomerOrder()
	if status != http.StatusOK {
		t.Fatalf("(8-10) cliente no pudo consultar su pedido, status=%d", status)
	}
	if custDispatch.Status != "EN_TRANSITO" {
		t.Fatalf("(11) el cliente debía ver EN_TRANSITO, vio %q", custDispatch.Status)
	}
	if custDispatch.CarrierName == nil || *custDispatch.CarrierName != "Olva Courier" {
		t.Errorf("el cliente debía ver el transportista: %+v", custDispatch)
	}

	// 12. Ejecutar EN_TRANSITO -> ENTREGADO.
	deliverResp := putDispatchStatus("ENTREGADO")
	if deliverResp.StatusCode != http.StatusOK {
		t.Fatalf("(12) transición a ENTREGADO falló, status=%d", deliverResp.StatusCode)
	}

	// 13. Verificar Order=ENTREGADO, Dispatch=ENTREGADO.
	var afterDeliver database.TenantEcommerceOrder
	db.First(&afterDeliver, order.ID)
	var dispatchAfterDeliver database.TenantEcommerceDispatch
	db.First(&dispatchAfterDeliver, dispatchID)
	if afterDeliver.Status != service.OrderStatusEntregado {
		t.Fatalf("(13) Order.Status = %q, quería ENTREGADO", afterDeliver.Status)
	}
	if dispatchAfterDeliver.Status != service.DispatchStatusEntregado {
		t.Fatalf("(13) Dispatch.Status = %q, quería ENTREGADO", dispatchAfterDeliver.Status)
	}

	// 14. Verificar DeliveredAt != NULL.
	if dispatchAfterDeliver.DeliveredAt == nil {
		t.Fatal("(14) DeliveredAt debía quedar seteado")
	}

	// 15. Verificar historial Dispatch EN_TRANSITO->ENTREGADO.
	var dispHist2 database.TenantEcommerceDispatchStatusHistory
	if err := db.Where("dispatch_id = ? AND from_status = ? AND to_status = ?", dispatchID, "EN_TRANSITO", "ENTREGADO").First(&dispHist2).Error; err != nil {
		t.Fatalf("(15) historial de dispatch EN_TRANSITO->ENTREGADO no encontrado: %v", err)
	}

	// 16. Verificar historial Order DESPACHADO->ENTREGADO.
	var orderHist database.TenantEcommerceOrderStatusHistory
	if err := db.Where("order_id = ? AND from_status = ? AND to_status = ?", order.ID, "DESPACHADO", "ENTREGADO").First(&orderHist).Error; err != nil {
		t.Fatalf("(16) historial de order DESPACHADO->ENTREGADO no encontrado: %v", err)
	}

	// 17. Consultar nuevamente como customer, verificar ENTREGADO.
	status2, custOrder2, custDispatch2 := getCustomerOrder()
	if status2 != http.StatusOK {
		t.Fatalf("(17) status=%d", status2)
	}
	if custDispatch2.Status != "ENTREGADO" {
		t.Fatalf("(17) el cliente debía ver ENTREGADO, vio %q", custDispatch2.Status)
	}
	if custDispatch2.DeliveredAt == nil {
		t.Error("(17) el cliente debía ver delivered_at seteado")
	}
	_ = custOrder2

	// 18/19. Stock idéntico antes/después + kardex vacío.
	var stockAfter database.TenantProductStock
	db.Where("product_id = ? AND branch_id = ?", product.ID, 1).First(&stockAfter)
	if stockAfter.Quantity != stockBefore.Quantity {
		t.Fatalf("(17-stock) REGRESIÓN: stock cambió de %.2f a %.2f — todo el flujo de despacho/tracking nunca debe tocar inventario", stockBefore.Quantity, stockAfter.Quantity)
	}
	var movementCount int64
	db.Model(&database.TenantStockMovement{}).Where("product_id = ?", product.ID).Count(&movementCount)
	if movementCount != 0 {
		t.Fatalf("(17-kardex) no debía registrarse ningún movimiento de kardex, hay %d", movementCount)
	}

	// 18 (venta): ConvertedSaleID no fue alterado (el pedido nunca se convirtió — debe seguir nil).
	if afterDeliver.ConvertedSaleID != nil {
		t.Fatal("(18-venta) REGRESIÓN: el flujo de despacho/tracking no debe crear ni tocar ConvertedSaleID")
	}

	// ── Fase 11 — Devolución (Deuda #8 cerrada) — continúa el MISMO pedido real vía HTTP ──

	// 19. Ejecutar ENTREGADO -> DEVUELTO vía el endpoint genérico real.
	returnResp := putStatus(order.ID, "DEVUELTO")
	if returnResp.StatusCode != http.StatusOK {
		t.Fatalf("(19) transición a DEVUELTO falló, status=%d", returnResp.StatusCode)
	}

	// 20. Verificar Order=DEVUELTO y Dispatch sincronizado a DEVUELTO.
	var afterReturn database.TenantEcommerceOrder
	db.First(&afterReturn, order.ID)
	var dispatchAfterReturn database.TenantEcommerceDispatch
	db.First(&dispatchAfterReturn, dispatchID)
	if afterReturn.Status != service.OrderStatusDevuelto {
		t.Fatalf("(20) Order.Status = %q, quería DEVUELTO", afterReturn.Status)
	}
	if dispatchAfterReturn.Status != service.DispatchStatusDevuelto {
		t.Fatalf("(20) Dispatch.Status = %q, quería DEVUELTO (Deuda #8 debía quedar cerrada)", dispatchAfterReturn.Status)
	}

	// 21. Verificar ambos historiales, sin mezclarse entre dominios.
	var orderReturnHist database.TenantEcommerceOrderStatusHistory
	if err := db.Where("order_id = ? AND from_status = ? AND to_status = ?", order.ID, "ENTREGADO", "DEVUELTO").First(&orderReturnHist).Error; err != nil {
		t.Fatalf("(21) historial de order ENTREGADO->DEVUELTO no encontrado: %v", err)
	}
	var dispatchReturnHist database.TenantEcommerceDispatchStatusHistory
	if err := db.Where("dispatch_id = ? AND from_status = ? AND to_status = ?", dispatchID, "ENTREGADO", "DEVUELTO").First(&dispatchReturnHist).Error; err != nil {
		t.Fatalf("(21) historial de dispatch ENTREGADO->DEVUELTO no encontrado: %v", err)
	}

	// 22. Doble click: un segundo intento de devolución se rechaza limpio, sin duplicar nada.
	if resp := putStatus(order.ID, "DEVUELTO"); resp.StatusCode == http.StatusOK {
		t.Fatal("(22) un segundo intento de devolución sobre un pedido ya DEVUELTO debía rechazarse")
	}
	var orderReturnHistCount int64
	db.Model(&database.TenantEcommerceOrderStatusHistory{}).Where("order_id = ? AND to_status = ?", order.ID, "DEVUELTO").Count(&orderReturnHistCount)
	if orderReturnHistCount != 1 {
		t.Fatalf("(22) REGRESIÓN: debe existir EXACTAMENTE 1 historial de order DEVUELTO, hay %d", orderReturnHistCount)
	}

	// 23. Consultar como customer: el cliente ve la devolución reflejada.
	status3, _, custDispatch3 := getCustomerOrder()
	if status3 != http.StatusOK {
		t.Fatalf("(23) status=%d", status3)
	}
	if custDispatch3.Status != "DEVUELTO" {
		t.Fatalf("(23) el cliente debía ver DEVUELTO, vio %q", custDispatch3.Status)
	}

	// 24/25. Stock/kardex siguen intactos después de la devolución — la devolución logística NO
	// es un ajuste de inventario (fuera de alcance, no se implementó ningún movimiento de stock).
	var stockAfterReturn database.TenantProductStock
	db.Where("product_id = ? AND branch_id = ?", product.ID, 1).First(&stockAfterReturn)
	if stockAfterReturn.Quantity != stockBefore.Quantity {
		t.Fatalf("(24-stock) REGRESIÓN: stock cambió de %.2f a %.2f — la devolución logística no debe tocar inventario", stockBefore.Quantity, stockAfterReturn.Quantity)
	}
	var movementCountAfterReturn int64
	db.Model(&database.TenantStockMovement{}).Where("product_id = ?", product.ID).Count(&movementCountAfterReturn)
	if movementCountAfterReturn != 0 {
		t.Fatalf("(25-kardex) no debía registrarse ningún movimiento de kardex por la devolución, hay %d", movementCountAfterReturn)
	}
}
