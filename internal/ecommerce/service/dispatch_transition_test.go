package service

import (
	"testing"

	"tukifac/pkg/database"
)

// mustCreateDispatchedOrder crea un pedido, lo recorre hasta LISTO_PARA_DESPACHO y lo despacha —
// punto de partida común para los tests de transición de Fase 9 (DESPACHADO->EN_TRANSITO->
// ENTREGADO).
func mustCreateDispatchedOrder(t *testing.T, svc *EcommerceService) (*database.TenantEcommerceOrder, *database.TenantEcommerceDispatch) {
	t.Helper()
	order := mustReachListoParaDespacho(t, svc)
	dispatch, err := svc.CreateDispatch(order.ID, CreateDispatchInput{CarrierName: strPtr("Olva Courier"), UserID: 5})
	if err != nil {
		t.Fatalf("CreateDispatch: %v", err)
	}
	return order, dispatch
}

// ── A. DESPACHADO -> EN_TRANSITO ─────────────────────────────────────────

func TestMarkDispatchInTransit_Exitoso(t *testing.T) {
	db := setupEcommerceServiceDB(t)
	svc := &EcommerceService{db: db}
	order, dispatch := mustCreateDispatchedOrder(t, svc)

	updated, err := svc.MarkDispatchInTransit(dispatch.ID, DispatchTransitionInput{UserID: 9, Notes: "salió del almacén"})
	if err != nil {
		t.Fatalf("MarkDispatchInTransit: %v", err)
	}
	if updated.Status != DispatchStatusEnTransito {
		t.Errorf("Dispatch.Status = %q, quería EN_TRANSITO", updated.Status)
	}

	// Order.Status NO debe cambiar — punto central de Fase 9 (§7).
	stillOrder, err := svc.GetOrder(order.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stillOrder.Status != OrderStatusDespachado {
		t.Fatalf("REGRESIÓN: Order.Status cambió a %q — DESPACHADO->EN_TRANSITO no debe tocar el pedido", stillOrder.Status)
	}

	var hist database.TenantEcommerceDispatchStatusHistory
	if err := db.Where("dispatch_id = ?", dispatch.ID).First(&hist).Error; err != nil {
		t.Fatalf("debía registrarse DispatchStatusHistory: %v", err)
	}
	if hist.FromStatus != DispatchStatusDespachado || hist.ToStatus != DispatchStatusEnTransito {
		t.Errorf("historial incorrecto: from=%s to=%s", hist.FromStatus, hist.ToStatus)
	}
	if hist.UserID == nil || *hist.UserID != 9 {
		t.Errorf("UserID del historial debía ser 9, quedó %v", hist.UserID)
	}
	if hist.Notes != "salió del almacén" {
		t.Errorf("Notes no se guardó: %q", hist.Notes)
	}

	// NUNCA debe escribirse una fila en el historial del PEDIDO para esta transición.
	var orderHistCount int64
	db.Model(&database.TenantEcommerceOrderStatusHistory{}).Where("order_id = ? AND to_status = ?", order.ID, "EN_TRANSITO").Count(&orderHistCount)
	if orderHistCount != 0 {
		t.Fatal("REGRESIÓN: no debe existir ninguna fila de OrderStatusHistory con to_status=EN_TRANSITO — ese valor ni siquiera es un EcommerceOrderStatus válido")
	}
}

func TestMarkDispatchInTransit_EstadoInvalido_Rechazado(t *testing.T) {
	db := setupEcommerceServiceDB(t)
	svc := &EcommerceService{db: db}
	_, dispatch := mustCreateDispatchedOrder(t, svc)

	if _, err := svc.MarkDispatchInTransit(dispatch.ID, DispatchTransitionInput{}); err != nil {
		t.Fatal(err)
	}
	// Segunda vez: ya está EN_TRANSITO, no DESPACHADO — debe rechazarse.
	if _, err := svc.MarkDispatchInTransit(dispatch.ID, DispatchTransitionInput{}); err == nil {
		t.Fatal("un despacho que ya está EN_TRANSITO no debe poder pasar a EN_TRANSITO de nuevo")
	}
}

func TestMarkDispatchInTransit_DespachoInexistente(t *testing.T) {
	db := setupEcommerceServiceDB(t)
	svc := &EcommerceService{db: db}
	if _, err := svc.MarkDispatchInTransit(99999, DispatchTransitionInput{}); err == nil {
		t.Fatal("un despacho inexistente debe rechazarse")
	}
}

// ── B. EN_TRANSITO -> ENTREGADO ──────────────────────────────────────────

func mustMarkInTransit(t *testing.T, svc *EcommerceService, dispatchID uint) {
	t.Helper()
	if _, err := svc.MarkDispatchInTransit(dispatchID, DispatchTransitionInput{}); err != nil {
		t.Fatal(err)
	}
}

func TestMarkDispatchDelivered_Exitoso(t *testing.T) {
	db := setupEcommerceServiceDB(t)
	svc := &EcommerceService{db: db}
	order, dispatch := mustCreateDispatchedOrder(t, svc)
	mustMarkInTransit(t, svc, dispatch.ID)

	updated, err := svc.MarkDispatchDelivered(dispatch.ID, DispatchTransitionInput{UserID: 12, Notes: "recibido por el cliente"})
	if err != nil {
		t.Fatalf("MarkDispatchDelivered: %v", err)
	}
	if updated.Status != DispatchStatusEntregado {
		t.Errorf("Dispatch.Status = %q, quería ENTREGADO", updated.Status)
	}
	if updated.DeliveredAt == nil {
		t.Fatal("DeliveredAt debe quedar seteado")
	}

	afterOrder, err := svc.GetOrder(order.ID)
	if err != nil {
		t.Fatal(err)
	}
	if afterOrder.Status != OrderStatusEntregado {
		t.Fatalf("Order.Status = %q, quería ENTREGADO", afterOrder.Status)
	}

	var dispHist database.TenantEcommerceDispatchStatusHistory
	if err := db.Where("dispatch_id = ? AND to_status = ?", dispatch.ID, DispatchStatusEntregado).First(&dispHist).Error; err != nil {
		t.Fatalf("debía registrarse DispatchStatusHistory EN_TRANSITO->ENTREGADO: %v", err)
	}
	if dispHist.FromStatus != DispatchStatusEnTransito {
		t.Errorf("FromStatus = %q, quería EN_TRANSITO", dispHist.FromStatus)
	}
	if dispHist.UserID == nil || *dispHist.UserID != 12 {
		t.Errorf("UserID del historial de dispatch debía ser 12, quedó %v", dispHist.UserID)
	}

	var orderHist database.TenantEcommerceOrderStatusHistory
	if err := db.Where("order_id = ? AND to_status = ?", order.ID, OrderStatusEntregado).First(&orderHist).Error; err != nil {
		t.Fatalf("debía registrarse OrderStatusHistory DESPACHADO->ENTREGADO: %v", err)
	}
	if orderHist.FromStatus != OrderStatusDespachado {
		t.Errorf("FromStatus del historial de pedido = %q, quería DESPACHADO", orderHist.FromStatus)
	}
	if orderHist.UserID == nil || *orderHist.UserID != 12 {
		t.Errorf("UserID del historial de pedido debía ser 12, quedó %v", orderHist.UserID)
	}
}

func TestMarkDispatchDelivered_SinPasarPorEnTransito_Rechazado(t *testing.T) {
	db := setupEcommerceServiceDB(t)
	svc := &EcommerceService{db: db}
	_, dispatch := mustCreateDispatchedOrder(t, svc)

	// dispatch sigue DESPACHADO — nunca pasó por EN_TRANSITO.
	if _, err := svc.MarkDispatchDelivered(dispatch.ID, DispatchTransitionInput{}); err == nil {
		t.Fatal("no se debe poder marcar ENTREGADO sin pasar antes por EN_TRANSITO")
	}
}

func TestMarkDispatchDelivered_Atomico_OrderYDispatchNuncaDivergen(t *testing.T) {
	db := setupEcommerceServiceDB(t)
	svc := &EcommerceService{db: db}
	order, dispatch := mustCreateDispatchedOrder(t, svc)
	mustMarkInTransit(t, svc, dispatch.ID)

	if _, err := svc.MarkDispatchDelivered(dispatch.ID, DispatchTransitionInput{}); err != nil {
		t.Fatal(err)
	}

	afterOrder, _ := svc.GetOrder(order.ID)
	afterDispatch, _ := svc.GetDispatch(dispatch.ID)
	if afterOrder.Status != OrderStatusEntregado || afterDispatch.Status != DispatchStatusEntregado {
		t.Fatalf("REGRESIÓN: Order/Dispatch divergieron — Order=%q Dispatch=%q", afterOrder.Status, afterDispatch.Status)
	}
}

// ── D. Idempotencia ───────────────────────────────────────────────────

func TestMarkDispatchInTransit_DobleClick_RechazoLimpio(t *testing.T) {
	db := setupEcommerceServiceDB(t)
	svc := &EcommerceService{db: db}
	_, dispatch := mustCreateDispatchedOrder(t, svc)

	if _, err := svc.MarkDispatchInTransit(dispatch.ID, DispatchTransitionInput{}); err != nil {
		t.Fatal(err)
	}
	_, err := svc.MarkDispatchInTransit(dispatch.ID, DispatchTransitionInput{})
	if err == nil {
		t.Fatal("un segundo click debe rechazarse limpiamente")
	}
	var histCount int64
	db.Model(&database.TenantEcommerceDispatchStatusHistory{}).Where("dispatch_id = ?", dispatch.ID).Count(&histCount)
	if histCount != 1 {
		t.Fatalf("debe existir EXACTAMENTE 1 fila de historial (no 2), hay %d", histCount)
	}
}

func TestMarkDispatchDelivered_DobleClick_UnSoloDeliveredAt(t *testing.T) {
	db := setupEcommerceServiceDB(t)
	svc := &EcommerceService{db: db}
	_, dispatch := mustCreateDispatchedOrder(t, svc)
	mustMarkInTransit(t, svc, dispatch.ID)

	first, err := svc.MarkDispatchDelivered(dispatch.ID, DispatchTransitionInput{})
	if err != nil {
		t.Fatal(err)
	}
	firstDeliveredAt := *first.DeliveredAt

	// Retry después del éxito — debe rechazarse, y el DeliveredAt original nunca debe pisarse.
	_, err = svc.MarkDispatchDelivered(dispatch.ID, DispatchTransitionInput{})
	if err == nil {
		t.Fatal("un reintento después de ENTREGADO debe rechazarse limpiamente")
	}
	reloaded, err := svc.GetDispatch(dispatch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.DeliveredAt == nil || !reloaded.DeliveredAt.Equal(firstDeliveredAt) {
		t.Fatalf("DeliveredAt no debe sobrescribirse en un reintento — original=%v actual=%v", firstDeliveredAt, reloaded.DeliveredAt)
	}
	var dispHistCount, orderHistCount int64
	db.Model(&database.TenantEcommerceDispatchStatusHistory{}).Where("dispatch_id = ? AND to_status = ?", dispatch.ID, DispatchStatusEntregado).Count(&dispHistCount)
	db.Model(&database.TenantEcommerceOrderStatusHistory{}).Where("order_id = ? AND to_status = ?", reloaded.OrderID, OrderStatusEntregado).Count(&orderHistCount)
	if dispHistCount != 1 || orderHistCount != 1 {
		t.Fatalf("no debe duplicarse ningún historial — dispatch=%d order=%d", dispHistCount, orderHistCount)
	}
}

// ── E. Tenant isolation ───────────────────────────────────────────────

func TestMarkDispatchInTransit_TenantIsolation(t *testing.T) {
	dbA := setupIsolatedEcommerceServiceDB(t)
	dbB := setupIsolatedEcommerceServiceDB(t)
	svcA := &EcommerceService{db: dbA}
	svcB := &EcommerceService{db: dbB}

	_, dispatchA := mustCreateDispatchedOrder(t, svcA)
	_, dispatchB := mustCreateDispatchedOrder(t, svcB)
	if dispatchA.ID != dispatchB.ID {
		t.Fatalf("setup inválido: se esperaba el mismo ID en ambas BDs (A=%d B=%d)", dispatchA.ID, dispatchB.ID)
	}

	if _, err := svcA.MarkDispatchInTransit(dispatchA.ID, DispatchTransitionInput{}); err != nil {
		t.Fatal(err)
	}
	// El despacho con el MISMO id en el tenant B nunca se tocó.
	stillB, err := svcB.GetDispatch(dispatchB.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stillB.Status != DispatchStatusDespachado {
		t.Fatalf("REGRESIÓN de aislamiento: el despacho del tenant B cambió a %q", stillB.Status)
	}
}

// ── G. Inventario intacto durante EN_TRANSITO/ENTREGADO ─────────────────

func TestMarkDispatchDelivered_NoTocaStockNiKardex(t *testing.T) {
	db := setupConvertTestDB(t)
	p := seedConvertProduct(t, db, "TRACK1", 15)
	db.Model(&p).Update("manage_stock", true)
	if err := db.Create(&database.TenantProductStock{ProductID: p.ID, BranchID: 1, Quantity: 40}).Error; err != nil {
		t.Fatal(err)
	}
	svc := &EcommerceService{db: db}
	_, dispatch := mustCreateDispatchedOrder(t, svc)

	if _, err := svc.MarkDispatchInTransit(dispatch.ID, DispatchTransitionInput{}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.MarkDispatchDelivered(dispatch.ID, DispatchTransitionInput{}); err != nil {
		t.Fatal(err)
	}

	var stock database.TenantProductStock
	if err := db.Where("product_id = ? AND branch_id = ?", p.ID, 1).First(&stock).Error; err != nil {
		t.Fatal(err)
	}
	if stock.Quantity != 40 {
		t.Fatalf("REGRESIÓN: el stock cambió de 40 a %.2f — EN_TRANSITO/ENTREGADO nunca deben tocar inventario", stock.Quantity)
	}
	var movementCount int64
	db.Model(&database.TenantStockMovement{}).Where("product_id = ?", p.ID).Count(&movementCount)
	if movementCount != 0 {
		t.Fatalf("no debe registrarse ningún movimiento de kardex, hay %d", movementCount)
	}
}

// ── Venta sigue independiente ────────────────────────────────────────────

// TestOrderEntregadoADevuelto_TransicionPreexistenteSigueFuncionando Fase 9 §6/§27-C: la
// transición Order ENTREGADO->DEVUELTO (ecommerce.orders_return) ya existía en order_status.go
// desde Fase 1 — nunca era alcanzable en la práctica porque nada llegaba a ENTREGADO hasta esta
// fase. No se modifica ese código (decisión: no rehacer Fases 1-8 sin bug real); esto solo
// confirma que la infraestructura preexistente sigue funcionando ahora que es alcanzable de
// verdad. NO se sincroniza Dispatch.Status a DEVUELTO — eso requeriría una decisión nueva no
// resuelta por el contrato (ver documentación), así que Dispatch se queda en ENTREGADO.
func TestOrderEntregadoADevuelto_TransicionPreexistenteSigueFuncionando(t *testing.T) {
	db := setupEcommerceServiceDB(t)
	svc := &EcommerceService{db: db}
	order, dispatch := mustCreateDispatchedOrder(t, svc)
	mustMarkInTransit(t, svc, dispatch.ID)
	if _, err := svc.MarkDispatchDelivered(dispatch.ID, DispatchTransitionInput{}); err != nil {
		t.Fatal(err)
	}

	if err := svc.UpdateOrderStatus(order.ID, UpdateOrderStatusInput{NewStatus: OrderStatusDevuelto, Notes: "cliente rechazó el paquete"}); err != nil {
		t.Fatalf("ENTREGADO->DEVUELTO (infraestructura de Fase 1) debía seguir funcionando: %v", err)
	}
	after, err := svc.GetOrder(order.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Status != OrderStatusDevuelto {
		t.Fatalf("Order.Status = %q, quería DEVUELTO", after.Status)
	}

	// Gap documentado a propósito (Fase 9 §6): Dispatch.Status NO se sincroniza automáticamente.
	stillDispatch, err := svc.GetDispatch(dispatch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stillDispatch.Status != DispatchStatusEntregado {
		t.Fatalf("comportamiento inesperado: Dispatch.Status cambió a %q sin que ningún código lo haga — investigar", stillDispatch.Status)
	}
}

func TestMarkDispatchDelivered_ConvertedSaleIDNoSeToca(t *testing.T) {
	db := setupConvertTestDB(t)
	p := seedConvertProduct(t, db, "TRACK2", 20)
	series := seedNotaVentaSeries(t, db)
	svc := &EcommerceService{db: db}
	order, _, err := svc.CreateOrder(CreateOrderInput{
		CustomerName: "X", CustomerPhone: "999", DeliveryMethod: DeliveryMethodPickup,
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
	dispatch, err := svc.CreateDispatch(order.ID, CreateDispatchInput{})
	if err != nil {
		t.Fatal(err)
	}
	convSvc, input := convertServiceAndInput(db, series.ID, 1)
	sale, err := convSvc.ConvertToSale(order.ID, input)
	if err != nil {
		t.Fatalf("ConvertToSale: %v", err)
	}

	if _, err := svc.MarkDispatchInTransit(dispatch.ID, DispatchTransitionInput{}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.MarkDispatchDelivered(dispatch.ID, DispatchTransitionInput{}); err != nil {
		t.Fatal(err)
	}

	after, err := svc.GetOrder(order.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.ConvertedSaleID == nil || *after.ConvertedSaleID != sale.ID {
		t.Fatal("REGRESIÓN: las transiciones logísticas no deben tocar ConvertedSaleID")
	}
	if after.Status != OrderStatusEntregado {
		t.Fatalf("Order.Status = %q, quería ENTREGADO", after.Status)
	}
}
