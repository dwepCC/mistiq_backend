package service

import (
	"testing"

	"tukifac/pkg/database"
)

// TestBuildLabelDataForOrder_Exitoso Fase 10 (§18 "generación correcta / datos de Order / datos
// de Dispatch"): la etiqueta trae exactamente lo que existe en Order+Dispatch, nada inventado.
func TestBuildLabelDataForOrder_Exitoso(t *testing.T) {
	db := setupEcommerceServiceDB(t)
	svc := &EcommerceService{db: db}
	order, dispatch := mustCreateDispatchedOrder(t, svc)

	updated, err := svc.UpdateDispatch(dispatch.ID, UpdateDispatchInput{
		CarrierName: strPtr("Olva Courier"), TrackingCode: strPtr("LBL-1"), PackageCount: intPtr(2),
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = updated

	label, err := BuildLabelDataForOrder(db, order.ID)
	if err != nil {
		t.Fatalf("BuildLabelDataForOrder: %v", err)
	}
	if label.OrderID != order.ID {
		t.Errorf("OrderID = %d, quería %d", label.OrderID, order.ID)
	}
	if label.CustomerName != order.CustomerName || label.CustomerPhone != order.CustomerPhone {
		t.Errorf("datos del cliente no coinciden con el pedido: %+v", label)
	}
	if label.CarrierName != "Olva Courier" {
		t.Errorf("CarrierName = %q, quería Olva Courier", label.CarrierName)
	}
	if label.TrackingCode != "LBL-1" {
		t.Errorf("TrackingCode = %q, quería LBL-1", label.TrackingCode)
	}
	if label.PackageCount == nil || *label.PackageCount != 2 {
		t.Errorf("PackageCount = %v, quería 2", label.PackageCount)
	}
	if label.DispatchedAt == "" {
		t.Error("DispatchedAt debía venir seteado (el pedido ya está despachado)")
	}
}

// TestBuildLabelDataForOrder_SinDespacho_Rechazado: no se puede imprimir una etiqueta de un
// pedido que todavía no tiene despacho — nunca se inventa transportista/tracking.
func TestBuildLabelDataForOrder_SinDespacho_Rechazado(t *testing.T) {
	db := setupEcommerceServiceDB(t)
	svc := &EcommerceService{db: db}
	order := mustCreateOrder(t, svc) // PENDIENTE, sin dispatch

	_, err := BuildLabelDataForOrder(db, order.ID)
	if err == nil {
		t.Fatal("un pedido sin despacho no debe poder generar etiqueta")
	}
}

func TestBuildLabelDataForOrder_PedidoInexistente(t *testing.T) {
	db := setupEcommerceServiceDB(t)
	if _, err := BuildLabelDataForOrder(db, 99999); err == nil {
		t.Fatal("un pedido inexistente debe rechazarse")
	}
}

// TestBuildLabelDataForOrder_EnvioADomicilio_ResuelveDireccionReal: a diferencia del panel (que
// solo mostraba "dirección guardada del cliente" como placeholder), la etiqueta SÍ debe traer el
// texto real de la dirección — tanto snapshot de invitado como dirección de cliente autenticado.
func TestBuildLabelDataForOrder_EnvioADomicilio_ResuelveDireccionReal(t *testing.T) {
	db := setupCreateOrderTestDB(t)
	svc := &EcommerceService{db: db}
	p := seedSimpleProduct(t, db, "LBL1", "Producto etiqueta", 20)
	order, _, err := svc.CreateOrder(CreateOrderInput{
		CustomerName: "Envio Real", CustomerPhone: "999888777", DeliveryMethod: DeliveryMethodShipping,
		GuestAddressLine: "Av. Etiqueta 456", GuestReference: "Portón verde", GuestUbigeo: "150101",
		Items: []CreateOrderItemInput{{ProductID: p.ID, Quantity: 1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, to := range []string{OrderStatusConfirmado, OrderStatusEnPreparacion, OrderStatusEmpaquetado, OrderStatusListoParaDespacho} {
		if err := svc.UpdateOrderStatus(order.ID, UpdateOrderStatusInput{NewStatus: to}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := svc.CreateDispatch(order.ID, CreateDispatchInput{}); err != nil {
		t.Fatal(err)
	}

	label, err := BuildLabelDataForOrder(db, order.ID)
	if err != nil {
		t.Fatal(err)
	}
	if label.AddressLine != "Av. Etiqueta 456" || label.Reference != "Portón verde" {
		t.Fatalf("la etiqueta debe traer la dirección real, trajo: %+v", label)
	}
}

// TestBuildLabelDataForOrder_TenantIsolation: mismo ID de pedido en dos tenants — la etiqueta de
// uno nunca debe filtrar datos del otro.
func TestBuildLabelDataForOrder_TenantIsolation(t *testing.T) {
	dbA := setupIsolatedEcommerceServiceDB(t)
	dbB := setupIsolatedEcommerceServiceDB(t)
	svcA := &EcommerceService{db: dbA}
	svcB := &EcommerceService{db: dbB}

	orderA, dispatchA := mustCreateDispatchedOrder(t, svcA)
	orderB, _ := mustCreateDispatchedOrder(t, svcB)
	if orderA.ID != orderB.ID {
		t.Fatalf("setup inválido: se esperaba el mismo ID (A=%d B=%d)", orderA.ID, orderB.ID)
	}
	if _, err := svcA.UpdateDispatch(dispatchA.ID, UpdateDispatchInput{TrackingCode: strPtr("SOLO-TENANT-A")}); err != nil {
		t.Fatal(err)
	}

	labelB, err := BuildLabelDataForOrder(dbB, orderB.ID)
	if err != nil {
		t.Fatal(err)
	}
	if labelB.TrackingCode == "SOLO-TENANT-A" {
		t.Fatal("REGRESIÓN de aislamiento: la etiqueta del tenant B trae un tracking_code que solo se escribió en el tenant A")
	}
}

// TestBuildLabelDataForOrder_Reimpresion_NoModificaNada Fase 10 (§8 "reimpresión"): generar la
// etiqueta dos veces (regenerar/reimprimir) nunca crea un segundo Dispatch, nunca cambia
// Order.Status, nunca toca stock.
func TestBuildLabelDataForOrder_Reimpresion_NoModificaNada(t *testing.T) {
	db := setupConvertTestDB(t)
	p := seedConvertProduct(t, db, "LBL2", 15)
	db.Model(&p).Update("manage_stock", true)
	if err := db.Create(&database.TenantProductStock{ProductID: p.ID, BranchID: 1, Quantity: 25}).Error; err != nil {
		t.Fatal(err)
	}
	svc := &EcommerceService{db: db}
	order, _, err := svc.CreateOrder(CreateOrderInput{
		CustomerName: "Reimpresion", CustomerPhone: "999", DeliveryMethod: DeliveryMethodPickup,
		Items: []CreateOrderItemInput{{ProductID: p.ID, Quantity: 1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, to := range []string{OrderStatusConfirmado, OrderStatusEnPreparacion, OrderStatusEmpaquetado, OrderStatusListoParaDespacho} {
		if err := svc.UpdateOrderStatus(order.ID, UpdateOrderStatusInput{NewStatus: to}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := svc.CreateDispatch(order.ID, CreateDispatchInput{}); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 3; i++ {
		if _, err := BuildLabelDataForOrder(db, order.ID); err != nil {
			t.Fatalf("generación #%d falló: %v", i+1, err)
		}
	}

	var dispatchCount int64
	db.Model(&database.TenantEcommerceDispatch{}).Where("order_id = ?", order.ID).Count(&dispatchCount)
	if dispatchCount != 1 {
		t.Fatalf("reimprimir 3 veces no debe crear más de 1 Dispatch, hay %d", dispatchCount)
	}
	after, err := svc.GetOrder(order.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Status != OrderStatusDespachado {
		t.Fatalf("Order.Status no debe cambiar por generar la etiqueta, quedó %q", after.Status)
	}
	var stock database.TenantProductStock
	db.Where("product_id = ? AND branch_id = ?", p.ID, 1).First(&stock)
	if stock.Quantity != 25 {
		t.Fatalf("REGRESIÓN: generar la etiqueta no debe tocar stock, quedó %.2f", stock.Quantity)
	}
}
