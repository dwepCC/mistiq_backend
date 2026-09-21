package service

import (
	"testing"

	"tukifac/pkg/database"
)

// mustReachListoParaDespacho crea un pedido y lo hace avanzar por el flujo real
// (CONFIRMADO->EN_PREPARACION->EMPAQUETADO->LISTO_PARA_DESPACHO) usando UpdateOrderStatus, nunca
// escribiendo el status directo en la fila — así cada test de despacho también ejercita el camino
// real por el que un pedido llega a poder despacharse.
func mustReachListoParaDespacho(t *testing.T, svc *EcommerceService) *database.TenantEcommerceOrder {
	t.Helper()
	order := mustCreateOrder(t, svc)
	for _, to := range []string{OrderStatusConfirmado, OrderStatusEnPreparacion, OrderStatusEmpaquetado, OrderStatusListoParaDespacho} {
		if err := svc.UpdateOrderStatus(order.ID, UpdateOrderStatusInput{NewStatus: to}); err != nil {
			t.Fatalf("UpdateOrderStatus(%s): %v", to, err)
		}
	}
	got, err := svc.GetOrder(order.ID)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// ── 1/13/14/15. Creación exitosa ────────────────────────────────────────

func TestCreateDispatch_DesdeListoParaDespacho_Exitoso(t *testing.T) {
	db := setupEcommerceServiceDB(t)
	svc := &EcommerceService{db: db}
	order := mustReachListoParaDespacho(t, svc)

	dispatch, err := svc.CreateDispatch(order.ID, CreateDispatchInput{
		CarrierName: strPtr("Olva Courier"), TrackingCode: strPtr("ABC123456"), UserID: 9,
	})
	if err != nil {
		t.Fatalf("CreateDispatch: %v", err)
	}
	if dispatch.OrderID != order.ID { // punto 12
		t.Errorf("Dispatch.OrderID = %d, quería %d", dispatch.OrderID, order.ID)
	}
	if dispatch.Status != DispatchStatusDespachado { // punto 14 — nunca PENDIENTE_DESPACHO
		t.Errorf("Dispatch.Status = %q, quería %q", dispatch.Status, DispatchStatusDespachado)
	}
	if dispatch.DispatchedAt == nil {
		t.Error("DispatchedAt debe quedar seteado")
	}

	updatedOrder, err := svc.GetOrder(order.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updatedOrder.Status != OrderStatusDespachado { // punto 13
		t.Errorf("Order.Status = %q, quería DESPACHADO", updatedOrder.Status)
	}

	var hist database.TenantEcommerceOrderStatusHistory
	if err := db.Where("order_id = ? AND to_status = ?", order.ID, OrderStatusDespachado).First(&hist).Error; err != nil {
		t.Fatalf("debía registrarse StatusHistory LISTO_PARA_DESPACHO->DESPACHADO: %v", err)
	}
	if hist.FromStatus != OrderStatusListoParaDespacho {
		t.Errorf("FromStatus = %q, quería LISTO_PARA_DESPACHO", hist.FromStatus)
	}
	if hist.UserID == nil || *hist.UserID != 9 {
		t.Errorf("StatusHistory.UserID debía ser 9, quedó %v", hist.UserID)
	}
}

// ── 2/3/4/5. Rechazo desde estados incompatibles ────────────────────────

func TestCreateDispatch_RechazadoDesdeEstadosIncompatibles(t *testing.T) {
	cases := []string{
		OrderStatusPendiente, OrderStatusConfirmado, OrderStatusEnPreparacion, OrderStatusEmpaquetado,
		OrderStatusCancelado, OrderStatusRechazado, OrderStatusDespachado, OrderStatusEntregado, OrderStatusDevuelto,
	}
	for _, status := range cases {
		t.Run(status, func(t *testing.T) {
			db := setupCreateOrderTestDB(t)
			svc := &EcommerceService{db: db}
			product := seedSimpleProduct(t, db, "D-"+status, "Producto", 10)
			order := seedOrderRow(t, db, database.TenantEcommerceOrder{
				CustomerName: "X", CustomerPhone: "999", Total: 10, Status: status,
			})
			_ = product

			_, err := svc.CreateDispatch(order.ID, CreateDispatchInput{})
			if err == nil {
				t.Fatalf("un pedido en %s nunca debe poder despacharse", status)
			}
			var count int64
			db.Model(&database.TenantEcommerceDispatch{}).Where("order_id = ?", order.ID).Count(&count)
			if count != 0 {
				t.Fatalf("un intento rechazado no debe dejar ningún despacho creado (estado %s)", status)
			}
		})
	}
}

func TestCreateDispatch_PedidoInexistente(t *testing.T) {
	db := setupEcommerceServiceDB(t)
	svc := &EcommerceService{db: db}
	if _, err := svc.CreateDispatch(99999, CreateDispatchInput{}); err == nil {
		t.Fatal("un pedido inexistente debe rechazarse")
	}
	var count int64
	db.Model(&database.TenantEcommerceDispatch{}).Count(&count)
	if count != 0 {
		t.Fatalf("no debe quedar ningún despacho huérfano, hay %d", count) // punto 17
	}
}

// ── 11. Tenant isolation ─────────────────────────────────────────────────

func TestCreateDispatch_TenantIsolation(t *testing.T) {
	dbA := setupIsolatedEcommerceServiceDB(t)
	dbB := setupIsolatedEcommerceServiceDB(t)
	svcA := &EcommerceService{db: dbA}
	svcB := &EcommerceService{db: dbB}

	orderA := mustReachListoParaDespacho(t, svcA)
	orderB := mustReachListoParaDespacho(t, svcB)
	if orderA.ID != orderB.ID {
		t.Fatalf("setup inválido: se esperaba el mismo ID en ambas BDs (A=%d B=%d)", orderA.ID, orderB.ID)
	}

	if _, err := svcA.CreateDispatch(orderA.ID, CreateDispatchInput{}); err != nil {
		t.Fatal(err)
	}

	// El pedido con el MISMO id en el tenant B nunca se tocó — debe seguir LISTO_PARA_DESPACHO,
	// sin despacho.
	stillB, err := svcB.GetOrder(orderB.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stillB.Status != OrderStatusListoParaDespacho {
		t.Fatalf("REGRESIÓN de aislamiento: el pedido del tenant B cambió a %q", stillB.Status)
	}
	dispatchB, err := svcB.GetDispatchByOrderID(orderB.ID)
	if err != nil {
		t.Fatal(err)
	}
	if dispatchB != nil {
		t.Fatal("REGRESIÓN de aislamiento: el tenant B tiene un despacho que nunca creó")
	}
}

// ── 16/17/18. Atomicidad y doble despacho ────────────────────────────────

func TestCreateDispatch_DobleDespacho_RechazadoLimpio(t *testing.T) {
	db := setupEcommerceServiceDB(t)
	svc := &EcommerceService{db: db}
	order := mustReachListoParaDespacho(t, svc)

	if _, err := svc.CreateDispatch(order.ID, CreateDispatchInput{}); err != nil {
		t.Fatalf("primer despacho: %v", err)
	}
	_, err := svc.CreateDispatch(order.ID, CreateDispatchInput{})
	if err == nil {
		t.Fatal("un segundo intento de despacho sobre el mismo pedido debe rechazarse limpiamente")
	}
	if err.Error() == "" {
		t.Fatal("el rechazo debe traer un mensaje de negocio, nunca un error vacío/crudo de SQL")
	}

	var count int64
	db.Model(&database.TenantEcommerceDispatch{}).Where("order_id = ?", order.ID).Count(&count)
	if count != 1 {
		t.Fatalf("debe existir EXACTAMENTE 1 despacho para el pedido, hay %d", count) // nunca 2
	}
}

// ── 20. Recojo en tienda no exige carrier ────────────────────────────────

func TestCreateDispatch_RecojoEnTienda_SinCarrierNiTracking(t *testing.T) {
	db := setupEcommerceServiceDB(t)
	svc := &EcommerceService{db: db}
	order := mustReachListoParaDespacho(t, svc) // mustCreateOrder ya usa DeliveryMethodPickup

	dispatch, err := svc.CreateDispatch(order.ID, CreateDispatchInput{})
	if err != nil {
		t.Fatalf("un pedido RECOJO_TIENDA debe poder despacharse sin carrier/tracking: %v", err)
	}
	if dispatch.CarrierName != nil || dispatch.TrackingCode != nil {
		t.Errorf("no se envió carrier/tracking, no deben quedar seteados: %+v", dispatch)
	}
}

// ── 21. Envío a domicilio conserva la dirección del pedido ──────────────

func TestCreateDispatch_EnvioADomicilio_NoTocaDireccionDelPedido(t *testing.T) {
	db := setupCreateOrderTestDB(t)
	svc := &EcommerceService{db: db}
	p := seedSimpleProduct(t, db, "ENV1", "Producto envío", 20)
	order, _, err := svc.CreateOrder(CreateOrderInput{
		CustomerName: "Envio", CustomerPhone: "999888777", DeliveryMethod: DeliveryMethodShipping,
		GuestAddressLine: "Av. Real 123", GuestReference: "Puerta azul", GuestUbigeo: "150101",
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

	if _, err := svc.CreateDispatch(order.ID, CreateDispatchInput{CarrierName: strPtr("Shalom")}); err != nil {
		t.Fatalf("CreateDispatch: %v", err)
	}

	after, err := svc.GetOrder(order.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.GuestAddressLine != "Av. Real 123" || after.GuestReference != "Puerta azul" || after.GuestUbigeo != "150101" {
		t.Fatalf("el despacho no debe modificar la dirección ya persistida del pedido: %+v", after)
	}
}

// ── 22/23/24. Tracking, peso decimal, bultos ─────────────────────────────

func TestCreateDispatch_ConservaTrackingPesoDecimalYBultos(t *testing.T) {
	db := setupEcommerceServiceDB(t)
	svc := &EcommerceService{db: db}
	order := mustReachListoParaDespacho(t, svc)

	weight := 4.5
	packages := 2
	dispatch, err := svc.CreateDispatch(order.ID, CreateDispatchInput{
		CarrierName: strPtr("Olva Courier"), TrackingCode: strPtr("XYZ-999"),
		PackageCount: &packages, WeightKg: &weight,
	})
	if err != nil {
		t.Fatalf("CreateDispatch: %v", err)
	}
	if dispatch.TrackingCode == nil || *dispatch.TrackingCode != "XYZ-999" {
		t.Errorf("TrackingCode no se conservó: %v", dispatch.TrackingCode)
	}
	if dispatch.WeightKg == nil || *dispatch.WeightKg != 4.5 {
		t.Fatalf("el peso decimal (4.5) debe preservarse tal cual, quedó %v", dispatch.WeightKg)
	}
	if dispatch.PackageCount == nil || *dispatch.PackageCount != 2 {
		t.Errorf("PackageCount no se conservó: %v", dispatch.PackageCount)
	}

	reloaded, err := svc.GetDispatchByOrderID(order.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded == nil || reloaded.WeightKg == nil || *reloaded.WeightKg != 4.5 {
		t.Fatal("el peso decimal debe seguir intacto al releerlo de la BD")
	}
}

// ── 25. Valores negativos/absurdos rechazados ────────────────────────────

func TestCreateDispatch_ValoresInvalidosRechazados(t *testing.T) {
	db := setupEcommerceServiceDB(t)
	svc := &EcommerceService{db: db}
	order := mustReachListoParaDespacho(t, svc)

	negWeight := -1.0
	if _, err := svc.CreateDispatch(order.ID, CreateDispatchInput{WeightKg: &negWeight}); err == nil {
		t.Error("peso negativo debe rechazarse")
	}
	zeroPackages := 0
	if _, err := svc.CreateDispatch(order.ID, CreateDispatchInput{PackageCount: &zeroPackages}); err == nil {
		t.Error("0 bultos debe rechazarse (si se informa, debe ser > 0)")
	}
	negLength := -5.0
	if _, err := svc.CreateDispatch(order.ID, CreateDispatchInput{LengthCm: &negLength}); err == nil {
		t.Error("largo negativo debe rechazarse")
	}

	var count int64
	db.Model(&database.TenantEcommerceDispatch{}).Where("order_id = ?", order.ID).Count(&count)
	if count != 0 {
		t.Fatalf("ningún intento inválido debe dejar un despacho creado, hay %d", count)
	}
}

// ── 26/27/28. NO modifica stock, NO reserva, NO crea movimiento ─────────

func TestCreateDispatch_NoTocaStockNiKardex(t *testing.T) {
	db := setupConvertTestDB(t)
	p := seedConvertProduct(t, db, "DISP1", 15)
	db.Model(&p).Update("manage_stock", true)
	if err := db.Create(&database.TenantProductStock{ProductID: p.ID, BranchID: 1, Quantity: 30}).Error; err != nil {
		t.Fatal(err)
	}
	svc := &EcommerceService{db: db}
	order := mustReachListoParaDespacho(t, svc)

	if _, err := svc.CreateDispatch(order.ID, CreateDispatchInput{}); err != nil {
		t.Fatalf("CreateDispatch: %v", err)
	}

	var stock database.TenantProductStock
	if err := db.Where("product_id = ? AND branch_id = ?", p.ID, 1).First(&stock).Error; err != nil {
		t.Fatal(err)
	}
	if stock.Quantity != 30 {
		t.Fatalf("REGRESIÓN: el stock cambió de 30 a %.2f al crear el despacho — Fase 8 nunca toca inventario", stock.Quantity)
	}
	var movementCount int64
	db.Model(&database.TenantStockMovement{}).Where("product_id = ?", p.ID).Count(&movementCount)
	if movementCount != 0 {
		t.Fatalf("no debe registrarse ningún movimiento de kardex al despachar, hay %d", movementCount)
	}
}

// ── 29. Conversión a venta sigue independiente del despacho ─────────────

func TestCreateDispatch_ConversionAVentaSigueIndependiente(t *testing.T) {
	db := setupConvertTestDB(t)
	p := seedConvertProduct(t, db, "DISP2", 25)
	series := seedNotaVentaSeries(t, db)
	svc := &EcommerceService{db: db}
	order, _, err := svc.CreateOrder(CreateOrderInput{
		CustomerName: "Ana", CustomerPhone: "999111222", DeliveryMethod: DeliveryMethodPickup,
		Items: []CreateOrderItemInput{{ProductID: p.ID, Quantity: 1}},
	})
	if err != nil {
		t.Fatalf("CreateOrder: %v", err)
	}
	for _, to := range []string{OrderStatusConfirmado, OrderStatusEnPreparacion, OrderStatusEmpaquetado, OrderStatusListoParaDespacho} {
		if err := svc.UpdateOrderStatus(order.ID, UpdateOrderStatusInput{NewStatus: to}); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := svc.CreateDispatch(order.ID, CreateDispatchInput{}); err != nil {
		t.Fatalf("CreateDispatch: %v", err)
	}

	convSvc, input := convertServiceAndInput(db, series.ID, 1)
	sale, err := convSvc.ConvertToSale(order.ID, input)
	if err != nil {
		t.Fatalf("ConvertToSale después de despachar debe seguir funcionando: %v", err)
	}

	after, err := svc.GetOrder(order.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Status != OrderStatusDespachado {
		t.Errorf("REGRESIÓN: convertir a venta no debe tocar Order.Status, quedó %q (esperaba seguir DESPACHADO)", after.Status)
	}
	if after.ConvertedSaleID == nil || *after.ConvertedSaleID != sale.ID {
		t.Error("ConvertedSaleID debía quedar seteado igual que siempre")
	}
	dispatch, err := svc.GetDispatchByOrderID(order.ID)
	if err != nil {
		t.Fatal(err)
	}
	if dispatch == nil || dispatch.Status != DispatchStatusDespachado {
		t.Error("convertir a venta no debe alterar el despacho existente")
	}
}

// ── UpdateDispatch: solo metadata ────────────────────────────────────────

func TestUpdateDispatch_ActualizaMetadataSinTocarStatus(t *testing.T) {
	db := setupEcommerceServiceDB(t)
	svc := &EcommerceService{db: db}
	order := mustReachListoParaDespacho(t, svc)
	dispatch, err := svc.CreateDispatch(order.ID, CreateDispatchInput{})
	if err != nil {
		t.Fatal(err)
	}

	updated, err := svc.UpdateDispatch(dispatch.ID, UpdateDispatchInput{
		TrackingCode: strPtr("NUEVO-TRACKING"),
		PackageCount: intPtr(3),
	})
	if err != nil {
		t.Fatalf("UpdateDispatch: %v", err)
	}
	if updated.TrackingCode == nil || *updated.TrackingCode != "NUEVO-TRACKING" {
		t.Errorf("TrackingCode no se actualizó: %v", updated.TrackingCode)
	}
	if updated.PackageCount == nil || *updated.PackageCount != 3 {
		t.Errorf("PackageCount no se actualizó: %v", updated.PackageCount)
	}
	if updated.Status != DispatchStatusDespachado {
		t.Errorf("REGRESIÓN: UpdateDispatch no debe poder cambiar Status, quedó %q", updated.Status)
	}
	if updated.OrderID != order.ID {
		t.Errorf("REGRESIÓN: UpdateDispatch no debe poder cambiar OrderID, quedó %d", updated.OrderID)
	}
}

func TestUpdateDispatch_ValoresInvalidosRechazados(t *testing.T) {
	db := setupEcommerceServiceDB(t)
	svc := &EcommerceService{db: db}
	order := mustReachListoParaDespacho(t, svc)
	dispatch, err := svc.CreateDispatch(order.ID, CreateDispatchInput{})
	if err != nil {
		t.Fatal(err)
	}

	negWeight := -2.0
	if _, err := svc.UpdateDispatch(dispatch.ID, UpdateDispatchInput{WeightKg: &negWeight}); err == nil {
		t.Error("un peso negativo debe rechazarse también en UpdateDispatch")
	}
}

func TestUpdateDispatch_Inexistente(t *testing.T) {
	db := setupEcommerceServiceDB(t)
	svc := &EcommerceService{db: db}
	if _, err := svc.UpdateDispatch(99999, UpdateDispatchInput{}); err == nil {
		t.Fatal("un despacho inexistente debe rechazarse")
	}
}

func strPtr(s string) *string { return &s }
func intPtr(i int) *int       { return &i }
