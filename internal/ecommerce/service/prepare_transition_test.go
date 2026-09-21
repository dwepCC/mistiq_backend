package service

import (
	"encoding/json"
	"fmt"
	"testing"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

var prepareIsolationDBCounter int

// setupIsolatedEcommerceServiceDB mismo set de tablas que setupEcommerceServiceDB, pero con un
// sufijo incremental en el DSN — necesario acá porque este test abre DOS bases "de tenant" en la
// MISMA función (setupEcommerceServiceDB usa t.Name() a secas, que colisionaría en una sola base
// compartida por cache=shared; mismo bug ya documentado y evitado en create_order_test.go).
func setupIsolatedEcommerceServiceDB(t *testing.T) *gorm.DB {
	t.Helper()
	prepareIsolationDBCounter++
	dsn := fmt.Sprintf("file:%s-%d?mode=memory&cache=shared", t.Name(), prepareIsolationDBCounter)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []interface{}{
		&database.TenantEcommerceOrder{}, &database.TenantEcommerceOrderItem{},
		&database.TenantEcommerceOrderStatusHistory{}, &database.TenantEcommerceDispatch{},
		&database.TenantEcommerceDispatchStatusHistory{}, &database.TenantNotification{},
		&database.TenantProduct{}, &database.TenantProductPresentation{},
	} {
		if err := db.AutoMigrate(m); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

// TestUpdateOrderStatus_FlujoPreparacionCompleto_NoTocaStock Fase 7 (puntos 6/7/8/9 del checklist
// de tests): recorre CONFIRMADO->EN_PREPARACION->EMPAQUETADO->LISTO_PARA_DESPACHO (el mismo
// PUT /status ya usado desde Fase 1) y confirma que en ningún punto se modifica stock, se crea
// reserva/HOLD (no existe ese concepto en el modelo) ni se registra ningún movimiento de kardex —
// el picking de Fase 7 es puramente visual en frontend, el backend no tiene ninguna pieza nueva que
// tocar.
func TestUpdateOrderStatus_FlujoPreparacionCompleto_NoTocaStock(t *testing.T) {
	db := setupConvertTestDB(t)
	p := seedConvertProduct(t, db, "PREP1", 15)
	db.Model(&p).Update("manage_stock", true)
	if err := db.Create(&database.TenantProductStock{ProductID: p.ID, BranchID: 1, Quantity: 20}).Error; err != nil {
		t.Fatal(err)
	}

	svc := &EcommerceService{db: db}
	order, _, err := svc.CreateOrder(CreateOrderInput{
		CustomerName: "Almacen", CustomerPhone: "999111000", DeliveryMethod: DeliveryMethodPickup,
		Items: []CreateOrderItemInput{{ProductID: p.ID, Quantity: 3}},
	})
	if err != nil {
		t.Fatalf("CreateOrder: %v", err)
	}

	for _, to := range []string{OrderStatusConfirmado, OrderStatusEnPreparacion, OrderStatusEmpaquetado, OrderStatusListoParaDespacho} {
		if err := svc.UpdateOrderStatus(order.ID, UpdateOrderStatusInput{NewStatus: to, UserID: 5}); err != nil {
			t.Fatalf("UpdateOrderStatus(%s): %v", to, err)
		}
	}

	final, err := svc.GetOrder(order.ID)
	if err != nil {
		t.Fatal(err)
	}
	if final.Status != OrderStatusListoParaDespacho {
		t.Fatalf("Status final = %q, quería LISTO_PARA_DESPACHO", final.Status)
	}

	var stock database.TenantProductStock
	if err := db.Where("product_id = ? AND branch_id = ?", p.ID, 1).First(&stock).Error; err != nil {
		t.Fatal(err)
	}
	if stock.Quantity != 20 {
		t.Fatalf("REGRESIÓN: el stock cambió de 20 a %.2f al recorrer todo el flujo de preparación — Fase 7 es puramente visual, nunca debe tocar stock", stock.Quantity)
	}
	var movementCount int64
	db.Model(&database.TenantStockMovement{}).Where("product_id = ?", p.ID).Count(&movementCount)
	if movementCount != 0 {
		t.Fatalf("no debe registrarse ningún movimiento de kardex al preparar/empaquetar un pedido, hay %d", movementCount)
	}

	// Historial: creación + 4 transiciones ejecutadas arriba = 5 filas reales, ninguna fabricada.
	var hist []database.TenantEcommerceOrderStatusHistory
	db.Where("order_id = ?", order.ID).Order("id ASC").Find(&hist)
	if len(hist) != 5 {
		t.Fatalf("StatusHistory debía tener 5 filas (creación + 4 transiciones), tiene %d", len(hist))
	}

	// Notificaciones (Fase 6): solo "created" (CreateOrder) y "confirmed" (PENDIENTE->CONFIRMADO) —
	// EN_PREPARACION/EMPAQUETADO/LISTO_PARA_DESPACHO NO deben generar ningún evento nuevo, Fase 7 no
	// agrega tipos de notificación.
	var notifCount int64
	db.Model(&database.TenantNotification{}).Count(&notifCount)
	if notifCount != 2 {
		t.Fatalf("las transiciones de preparación no deben generar notificaciones — esperaba 2 (created+confirmed), hay %d", notifCount)
	}
}

// TestUpdateOrderStatus_TenantIsolationEnPreparacion Fase 7 (punto 4 del checklist): dos tenants
// con un pedido del mismo ID (BDs completamente separadas, mismo patrón usado en toda la sesión) —
// preparar el pedido del tenant B nunca debe afectar la fila con el mismo ID del tenant A.
func TestUpdateOrderStatus_TenantIsolationEnPreparacion(t *testing.T) {
	dbA := setupIsolatedEcommerceServiceDB(t)
	dbB := setupIsolatedEcommerceServiceDB(t)
	svcA := &EcommerceService{db: dbA}
	svcB := &EcommerceService{db: dbB}

	orderA := mustCreateOrder(t, svcA)
	orderB := mustCreateOrder(t, svcB)
	if orderA.ID != orderB.ID {
		t.Fatalf("setup inválido: se esperaba el mismo ID en ambas BDs para probar aislamiento (A=%d B=%d)", orderA.ID, orderB.ID)
	}

	if err := svcA.UpdateOrderStatus(orderA.ID, UpdateOrderStatusInput{NewStatus: OrderStatusConfirmado}); err != nil {
		t.Fatal(err)
	}
	if err := svcA.UpdateOrderStatus(orderA.ID, UpdateOrderStatusInput{NewStatus: OrderStatusEnPreparacion}); err != nil {
		t.Fatal(err)
	}

	// El pedido con el MISMO id en el tenant B nunca avanzó — debe seguir PENDIENTE.
	stillB, err := svcB.GetOrder(orderB.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stillB.Status != OrderStatusPendiente {
		t.Fatalf("REGRESIÓN de aislamiento: el pedido del tenant B cambió a %q al preparar el del tenant A", stillB.Status)
	}
}

// TestUpdateOrderStatus_ConflictoSecuencial_SegundaTransicionRechazada Fase 7 (punto 10 del
// checklist, "transición concurrente inválida es rechazada por backend"): sin locking persistente
// (explícitamente prohibido en esta fase), la garantía real es que cada llamada relee el estado
// actual antes de validar — así que si una transición ya comprometió el pedido a un estado
// terminal, una segunda llamada que asumía el estado anterior debe rechazarse limpiamente, nunca
// corromper el pedido ni generar una fila de historial inconsistente.
func TestUpdateOrderStatus_ConflictoSecuencial_SegundaTransicionRechazada(t *testing.T) {
	db := setupEcommerceServiceDB(t)
	svc := &EcommerceService{db: db}
	order := mustCreateOrder(t, svc)

	if err := svc.UpdateOrderStatus(order.ID, UpdateOrderStatusInput{NewStatus: OrderStatusConfirmado}); err != nil {
		t.Fatal(err)
	}
	if err := svc.UpdateOrderStatus(order.ID, UpdateOrderStatusInput{NewStatus: OrderStatusEnPreparacion}); err != nil {
		t.Fatal(err)
	}
	// "Usuario A" cancela mientras "usuario B" todavía tenía la vista vieja (EN_PREPARACION).
	if err := svc.UpdateOrderStatus(order.ID, UpdateOrderStatusInput{NewStatus: OrderStatusCancelado, Notes: "sin stock real"}); err != nil {
		t.Fatal(err)
	}

	// "Usuario B" intenta avanzar a EMPAQUETADO asumiendo que seguía EN_PREPARACION — el backend
	// relee el estado real (CANCELADO) y rechaza, no confía en lo que el cliente creía.
	err := svc.UpdateOrderStatus(order.ID, UpdateOrderStatusInput{NewStatus: OrderStatusEmpaquetado})
	if err == nil {
		t.Fatal("una transición basada en un estado que ya cambió (CANCELADO por otro request) debe rechazarse")
	}

	final, _ := svc.GetOrder(order.ID)
	if final.Status != OrderStatusCancelado {
		t.Fatalf("el estado final debe seguir siendo CANCELADO (el conflicto se rechazó limpio), quedó %q", final.Status)
	}
}

// TestGetOrderDetail_ItemsJSONLegacy_FuncionaEnEstadoDePreparacion Fase 7 (punto 14 del
// checklist): un pedido histórico (sin filas normalizadas, ver Fase 5) que además está en una
// etapa de preparación real debe seguir mostrando sus líneas vía el fallback a ItemsJSON.
func TestGetOrderDetail_ItemsJSONLegacy_FuncionaEnEstadoDePreparacion(t *testing.T) {
	db := setupEcommerceServiceDB(t)
	svc := &EcommerceService{db: db}

	legacyItems, _ := json.Marshal([]OrderItemInput{{ProductID: 1, Name: "Producto legacy", Quantity: 2.5, UnitPrice: 10}})
	legacy := database.TenantEcommerceOrder{
		CustomerName: "Legacy", CustomerPhone: "999", ItemsJSON: string(legacyItems),
		Total: 25, Status: OrderStatusEnPreparacion,
	}
	if err := db.Create(&legacy).Error; err != nil {
		t.Fatal(err)
	}

	_, items, _, _, err := svc.GetOrderDetail(legacy.ID)
	if err != nil {
		t.Fatalf("GetOrderDetail: %v", err)
	}
	if len(items) != 1 || items[0].Name != "Producto legacy" {
		t.Fatalf("el fallback a ItemsJSON debe seguir funcionando durante EN_PREPARACION: %+v", items)
	}
	if items[0].Quantity != 2.5 {
		t.Fatalf("la cantidad decimal (2.5) debe preservarse tal cual, llegó %.2f", items[0].Quantity)
	}
}
