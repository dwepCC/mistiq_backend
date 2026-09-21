package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"tukifac/internal/ecommerce/service"
	"tukifac/pkg/database"

	"github.com/gofiber/fiber/v3"
)

// registerCustomerAndGetToken registro real vía HTTP — mismo patrón que
// TestCustomerMeAPI_TokenValido_Concede, reutilizado acá para los tests de tracking del cliente.
func registerCustomerAndGetToken(t *testing.T, app *fiber.App, name, phone string) string {
	t.Helper()
	resp := doJSON(t, app, http.MethodPost, "/public/ecommerce/auth/register",
		map[string]string{"name": name, "phone": phone, "password": "clave123"}, "")
	var reg struct {
		Token    string `json:"token"`
		Customer struct {
			ID uint `json:"id"`
		} `json:"customer"`
	}
	json.NewDecoder(resp.Body).Decode(&reg)
	if reg.Token == "" {
		t.Fatalf("registro debía devolver token, status=%d", resp.StatusCode)
	}
	return reg.Token
}

// TestGetCustomerOrderAPI_IncluyeTrackingSinDatosInternos Fase 9 (Contrato v2 §11): el cliente
// autenticado ve el estado de su despacho, pero la respuesta JSON nunca debe traer campos internos
// (user_id, notes, bultos/peso/dimensiones) — CustomerDispatchView los excluye estructuralmente,
// esto lo prueba end-to-end contra la respuesta HTTP real.
func TestGetCustomerOrderAPI_IncluyeTrackingSinDatosInternos(t *testing.T) {
	withEcommerceCustomerJWTSecret(t)
	db := setupCustomerHandlerTestDB(t)
	app := newCustomerTestApp(db)

	token := registerCustomerAndGetToken(t, app, "Cliente Tracking", "999222333")
	var customerID uint
	db.Model(&database.TenantEcommerceCustomerAccount{}).Select("id").Where("phone = ?", "999222333").Scan(&customerID)

	// Pedido directo con CustomerAccountID (evita tener que armar todo el catálogo público solo
	// para este test — el contrato de ownership ya está probado en otros tests).
	order := database.TenantEcommerceOrder{
		CustomerName: "Cliente Tracking", CustomerPhone: "999222333", CustomerAccountID: &customerID,
		ItemsJSON: "[]", Total: 50, Status: service.OrderStatusListoParaDespacho,
	}
	if err := db.Create(&order).Error; err != nil {
		t.Fatal(err)
	}
	svc := service.NewEcommerceService(db)
	dispatch, err := svc.CreateDispatch(order.ID, service.CreateDispatchInput{
		CarrierName: strPtrH("Olva Courier"), TrackingCode: strPtrH("TRK-999"),
		PackageCount: intPtrH(3), Notes: "dejar en recepción", UserID: 77,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.MarkDispatchInTransit(dispatch.ID, service.DispatchTransitionInput{UserID: 77}); err != nil {
		t.Fatal(err)
	}

	resp := doJSON(t, app, http.MethodGet, "/public/ecommerce/account/orders/"+itoaH(order.ID), nil, token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	var raw map[string]json.RawMessage
	json.NewDecoder(resp.Body).Decode(&raw)
	var dispatchOut map[string]json.RawMessage
	if err := json.Unmarshal(raw["dispatch"], &dispatchOut); err != nil {
		t.Fatalf("la respuesta debía traer \"dispatch\": %v (raw=%s)", err, raw["dispatch"])
	}
	if _, has := dispatchOut["status"]; !has {
		t.Error("dispatch.status debía estar presente")
	}
	var status string
	json.Unmarshal(dispatchOut["status"], &status)
	if status != service.DispatchStatusEnTransito {
		t.Errorf("dispatch.status = %q, quería EN_TRANSITO", status)
	}
	var carrier string
	json.Unmarshal(dispatchOut["carrier_name"], &carrier)
	if carrier != "Olva Courier" {
		t.Errorf("dispatch.carrier_name = %q", carrier)
	}
	// Campos internos que NUNCA deben aparecer en la respuesta del cliente.
	for _, forbidden := range []string{"user_id", "notes", "package_count", "weight_kg", "id", "order_id"} {
		if _, has := dispatchOut[forbidden]; has {
			t.Errorf("REGRESIÓN de privacidad: el campo interno %q no debe exponerse al cliente, pero está en la respuesta", forbidden)
		}
	}
}

// TestGetCustomerOrderAPI_PedidoDeOtroCliente_NoExponeDispatch: un cliente autenticado no puede
// ver el pedido (ni el tracking) de otro cliente, aunque adivine el ID.
func TestGetCustomerOrderAPI_PedidoDeOtroCliente_NoExponeDispatch(t *testing.T) {
	withEcommerceCustomerJWTSecret(t)
	db := setupCustomerHandlerTestDB(t)
	app := newCustomerTestApp(db)

	tokenA := registerCustomerAndGetToken(t, app, "Cliente A", "999333444")
	_ = registerCustomerAndGetToken(t, app, "Cliente B", "999444555")
	var customerBID uint
	db.Model(&database.TenantEcommerceCustomerAccount{}).Select("id").Where("phone = ?", "999444555").Scan(&customerBID)

	orderB := database.TenantEcommerceOrder{
		CustomerName: "Cliente B", CustomerPhone: "999444555", CustomerAccountID: &customerBID,
		ItemsJSON: "[]", Total: 30, Status: service.OrderStatusListoParaDespacho,
	}
	if err := db.Create(&orderB).Error; err != nil {
		t.Fatal(err)
	}
	svc := service.NewEcommerceService(db)
	if _, err := svc.CreateDispatch(orderB.ID, service.CreateDispatchInput{CarrierName: strPtrH("Shalom")}); err != nil {
		t.Fatal(err)
	}

	// Cliente A, con SU token real, intenta ver el pedido de Cliente B.
	resp := doJSON(t, app, http.MethodGet, "/public/ecommerce/account/orders/"+itoaH(orderB.ID), nil, tokenA)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("un cliente no debe poder ver el pedido/despacho de otro, status=%d", resp.StatusCode)
	}
}

func strPtrH(s string) *string { return &s }
func intPtrH(i int) *int       { return &i }
func itoaH(id uint) string {
	return fmt.Sprintf("%d", id)
}
