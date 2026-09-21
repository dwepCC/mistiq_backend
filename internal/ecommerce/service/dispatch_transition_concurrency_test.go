package service

import (
	"os"
	"sync"
	"sync/atomic"
	"testing"

	"tukifac/pkg/database"

	mysqldriver "gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func setupDispatchTransitionConcurrencyDB(t *testing.T, dsn string) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(mysqldriver.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open mysql: %v", err)
	}
	for _, m := range []interface{}{
		&database.TenantProduct{}, &database.TenantProductPresentation{},
		&database.TenantEcommerceOrder{}, &database.TenantEcommerceOrderItem{},
		&database.TenantEcommerceOrderStatusHistory{}, &database.TenantEcommerceDispatch{},
		&database.TenantEcommerceDispatchStatusHistory{}, &database.TenantNotification{},
	} {
		if err := db.AutoMigrate(m); err != nil {
			t.Fatalf("automigrate %T: %v", m, err)
		}
	}
	db.Exec("DELETE FROM tenant_ecommerce_dispatch_status_histories")
	db.Exec("DELETE FROM tenant_ecommerce_dispatches")
	db.Exec("DELETE FROM tenant_ecommerce_order_status_histories")
	db.Exec("DELETE FROM tenant_ecommerce_order_items")
	db.Exec("DELETE FROM tenant_ecommerce_orders")
	db.Exec("DELETE FROM tenant_products")
	return db
}

func mustCreateDispatchedOrderMySQL(t *testing.T, svc *EcommerceService, code string) *database.TenantEcommerceDispatch {
	t.Helper()
	product := database.TenantProduct{Code: code, Name: "Producto " + code, Type: "product", Unit: "NIU", SalePrice: 20, Active: true, ShowInDigitalCatalog: true}
	if err := svc.db.Create(&product).Error; err != nil {
		t.Fatal(err)
	}
	order, _, err := svc.CreateOrder(CreateOrderInput{
		CustomerName: "Race", CustomerPhone: "999000000", DeliveryMethod: DeliveryMethodPickup,
		Items: []CreateOrderItemInput{{ProductID: product.ID, Quantity: 1}},
	})
	if err != nil {
		t.Fatalf("CreateOrder: %v", err)
	}
	for _, to := range []string{OrderStatusConfirmado, OrderStatusEnPreparacion, OrderStatusEmpaquetado, OrderStatusListoParaDespacho} {
		if err := svc.UpdateOrderStatus(order.ID, UpdateOrderStatusInput{NewStatus: to}); err != nil {
			t.Fatalf("UpdateOrderStatus(%s): %v", to, err)
		}
	}
	dispatch, err := svc.CreateDispatch(order.ID, CreateDispatchInput{})
	if err != nil {
		t.Fatalf("CreateDispatch: %v", err)
	}
	return dispatch
}

// TestMarkDispatchInTransit_ConcurrentPost_SoloUnaTransicionExitosa Fase 9 (§7/§12/§18-C):
// N requests simultáneos DESPACHADO->EN_TRANSITO sobre el MISMO despacho — igual patrón real de
// MySQL que dispatch_concurrency_test.go (Fase 8) y inventory_concurrency_test.go (Fase 1.5).
//
// Cómo correrlo: DISPATCH_MYSQL_DSN="user:pass@tcp(127.0.0.1:3306)/<db_de_prueba>" go test -run
// TestMarkDispatchInTransit_ConcurrentPost ./internal/ecommerce/service/...
func TestMarkDispatchInTransit_ConcurrentPost_SoloUnaTransicionExitosa(t *testing.T) {
	dsn := os.Getenv("DISPATCH_MYSQL_DSN")
	if dsn == "" {
		t.Skip("DISPATCH_MYSQL_DSN no configurado: este test necesita MySQL real (sqlite serializa " +
			"escrituras a nivel de archivo y nunca expondría la race). Ver dispatch_concurrency_test.go (Fase 8).")
	}
	db := setupDispatchTransitionConcurrencyDB(t, dsn)
	svc := &EcommerceService{db: db}
	dispatch := mustCreateDispatchedOrderMySQL(t, svc, "RACE-TRANSIT")

	const attackers = 20
	var okCount, rejectedCount, unexpectedErrs int32
	var wg sync.WaitGroup
	wg.Add(attackers)
	for i := 0; i < attackers; i++ {
		go func() {
			defer wg.Done()
			_, err := svc.MarkDispatchInTransit(dispatch.ID, DispatchTransitionInput{})
			switch {
			case err == nil:
				atomic.AddInt32(&okCount, 1)
			case err.Error() == "el despacho debe estar DESPACHADO para pasar a EN_TRANSITO (estado actual: EN_TRANSITO)":
				atomic.AddInt32(&rejectedCount, 1)
			default:
				atomic.AddInt32(&unexpectedErrs, 1)
				t.Logf("error inesperado: %v", err)
			}
		}()
	}
	wg.Wait()

	if unexpectedErrs != 0 {
		t.Fatalf("hubo %d errores inesperados (nunca debe filtrarse un error crudo de SQL)", unexpectedErrs)
	}
	if okCount != 1 {
		t.Fatalf("REGRESIÓN/RACE: se esperaba exactamente 1 transición exitosa, hubo %d", okCount)
	}
	if rejectedCount != attackers-1 {
		t.Fatalf("se esperaban %d rechazos limpios, hubo %d", attackers-1, rejectedCount)
	}

	var histCount int64
	db.Model(&database.TenantEcommerceDispatchStatusHistory{}).Where("dispatch_id = ?", dispatch.ID).Count(&histCount)
	if histCount != 1 {
		t.Fatalf("REGRESIÓN/RACE: debe existir EXACTAMENTE 1 fila de historial, hay %d", histCount)
	}
	var final database.TenantEcommerceDispatch
	db.First(&final, dispatch.ID)
	if final.Status != DispatchStatusEnTransito {
		t.Fatalf("Dispatch.Status final = %q, quería EN_TRANSITO", final.Status)
	}
}

// TestMarkDispatchDelivered_ConcurrentPost_SoloUnaTransicionExitosa: mismo patrón para
// EN_TRANSITO->ENTREGADO — caso crítico explícito del prompt (usuario A y B marcando ENTREGADO
// simultáneamente debe producir una única transición efectiva).
func TestMarkDispatchDelivered_ConcurrentPost_SoloUnaTransicionExitosa(t *testing.T) {
	dsn := os.Getenv("DISPATCH_MYSQL_DSN")
	if dsn == "" {
		t.Skip("DISPATCH_MYSQL_DSN no configurado")
	}
	db := setupDispatchTransitionConcurrencyDB(t, dsn)
	svc := &EcommerceService{db: db}
	dispatch := mustCreateDispatchedOrderMySQL(t, svc, "RACE-DELIVER")
	if _, err := svc.MarkDispatchInTransit(dispatch.ID, DispatchTransitionInput{}); err != nil {
		t.Fatalf("MarkDispatchInTransit: %v", err)
	}

	const attackers = 20
	var okCount, rejectedCount, unexpectedErrs int32
	var wg sync.WaitGroup
	wg.Add(attackers)
	for i := 0; i < attackers; i++ {
		go func() {
			defer wg.Done()
			_, err := svc.MarkDispatchDelivered(dispatch.ID, DispatchTransitionInput{})
			switch {
			case err == nil:
				atomic.AddInt32(&okCount, 1)
			case err.Error() == "el despacho debe estar EN_TRANSITO para marcarse ENTREGADO (estado actual: ENTREGADO)":
				atomic.AddInt32(&rejectedCount, 1)
			default:
				atomic.AddInt32(&unexpectedErrs, 1)
				t.Logf("error inesperado: %v", err)
			}
		}()
	}
	wg.Wait()

	if unexpectedErrs != 0 {
		t.Fatalf("hubo %d errores inesperados", unexpectedErrs)
	}
	if okCount != 1 {
		t.Fatalf("REGRESIÓN/RACE: se esperaba exactamente 1 entrega exitosa, hubo %d", okCount)
	}
	if rejectedCount != attackers-1 {
		t.Fatalf("se esperaban %d rechazos limpios, hubo %d", attackers-1, rejectedCount)
	}

	var dispHistCount, orderHistCount int64
	db.Model(&database.TenantEcommerceDispatchStatusHistory{}).Where("dispatch_id = ? AND to_status = ?", dispatch.ID, DispatchStatusEntregado).Count(&dispHistCount)
	if dispHistCount != 1 {
		t.Fatalf("REGRESIÓN/RACE: debe existir EXACTAMENTE 1 historial de dispatch ENTREGADO, hay %d", dispHistCount)
	}

	var final database.TenantEcommerceDispatch
	db.First(&final, dispatch.ID)
	var order database.TenantEcommerceOrder
	db.First(&order, final.OrderID)
	db.Model(&database.TenantEcommerceOrderStatusHistory{}).Where("order_id = ? AND to_status = ?", order.ID, OrderStatusEntregado).Count(&orderHistCount)

	if final.Status != DispatchStatusEntregado || order.Status != OrderStatusEntregado {
		t.Fatalf("REGRESIÓN/RACE: estado final inconsistente — Dispatch=%q Order=%q", final.Status, order.Status)
	}
	if final.DeliveredAt == nil {
		t.Fatal("DeliveredAt debe quedar seteado")
	}
	if orderHistCount != 1 {
		t.Fatalf("REGRESIÓN/RACE: debe existir EXACTAMENTE 1 historial de order ENTREGADO, hay %d", orderHistCount)
	}
}
