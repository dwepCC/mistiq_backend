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

// TestCreateDispatch_ConcurrentPost_SoloUnDespachoExitoso reproduce en MySQL real dos (o más)
// requests simultáneos intentando despachar el MISMO pedido — Contrato v2 §5/§8, Fase 8, punto de
// concurrencia explícitamente exigido. Mismo criterio y mismo patrón que
// internal/inventory/service/inventory_concurrency_test.go (Fase 1.5): sqlite serializa TODAS las
// transacciones de escritura a nivel de archivo completo, así que un test contra sqlite pasaría
// igual con o sin el SELECT...FOR UPDATE — no probaría la race condition real que solo existe bajo
// MySQL/InnoDB (locking a nivel de fila).
//
// Cómo correrlo: DISPATCH_MYSQL_DSN="user:pass@tcp(127.0.0.1:3306)/<db_de_prueba_ya_creada>" go test
// -run TestCreateDispatch_ConcurrentPost_SoloUnDespachoExitoso ./internal/ecommerce/service/...
func TestCreateDispatch_ConcurrentPost_SoloUnDespachoExitoso(t *testing.T) {
	dsn := os.Getenv("DISPATCH_MYSQL_DSN")
	if dsn == "" {
		t.Skip("DISPATCH_MYSQL_DSN no configurado: este test necesita MySQL real para poder " +
			"reproducir la race condition de doble despacho (sqlite serializa escrituras a nivel " +
			"de archivo y nunca la expondría, con o sin el SELECT...FOR UPDATE). Ver comentario del test.")
	}

	db, err := gorm.Open(mysqldriver.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open mysql: %v", err)
	}
	for _, m := range []interface{}{
		&database.TenantProduct{}, &database.TenantProductPresentation{},
		&database.TenantEcommerceOrder{}, &database.TenantEcommerceOrderItem{},
		&database.TenantEcommerceOrderStatusHistory{}, &database.TenantEcommerceDispatch{},
		&database.TenantNotification{},
	} {
		if err := db.AutoMigrate(m); err != nil {
			t.Fatalf("automigrate %T: %v", m, err)
		}
	}
	// Repetible contra la misma BD de prueba.
	db.Exec("DELETE FROM tenant_ecommerce_dispatches")
	db.Exec("DELETE FROM tenant_ecommerce_order_status_histories")
	db.Exec("DELETE FROM tenant_ecommerce_order_items")
	db.Exec("DELETE FROM tenant_ecommerce_orders")
	db.Exec("DELETE FROM tenant_products")

	svc := &EcommerceService{db: db}
	product := database.TenantProduct{Code: "RACE-DISPATCH", Name: "Producto race despacho", Type: "product", Unit: "NIU", SalePrice: 20, Active: true, ShowInDigitalCatalog: true}
	if err := db.Create(&product).Error; err != nil {
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

	const attackers = 20 // requests simultáneos sobre el MISMO pedido
	var okCount int32
	var rejectedCount int32
	var unexpectedErrs int32
	var wg sync.WaitGroup
	wg.Add(attackers)
	for i := 0; i < attackers; i++ {
		go func() {
			defer wg.Done()
			_, err := svc.CreateDispatch(order.ID, CreateDispatchInput{CarrierName: strPtr("Olva Courier")})
			switch {
			case err == nil:
				atomic.AddInt32(&okCount, 1)
			case err.Error() == "el pedido debe estar LISTO_PARA_DESPACHO para despachar (estado actual: DESPACHADO)",
				err.Error() == "este pedido ya tiene un despacho registrado",
				err.Error() == "no se pudo crear el despacho (es posible que ya exista uno para este pedido)":
				atomic.AddInt32(&rejectedCount, 1)
			default:
				atomic.AddInt32(&unexpectedErrs, 1)
				t.Logf("error inesperado: %v", err)
			}
		}()
	}
	wg.Wait()

	if unexpectedErrs != 0 {
		t.Fatalf("hubo %d errores inesperados (ni éxito ni un rechazo de negocio limpio conocido) — nunca debe filtrarse un error crudo de SQL", unexpectedErrs)
	}
	// LA ASERCIÓN CENTRAL: exactamente 1 despacho exitoso, nunca más — sin el SELECT...FOR UPDATE,
	// varias goroutines podrían leer el pedido en LISTO_PARA_DESPACHO antes de que cualquiera
	// confirme, y todas crearían su propio despacho.
	if okCount != 1 {
		t.Fatalf("REGRESIÓN/RACE: se esperaba exactamente 1 despacho exitoso, hubo %d", okCount)
	}
	if rejectedCount != attackers-1 {
		t.Fatalf("se esperaban %d rechazos limpios, hubo %d", attackers-1, rejectedCount)
	}

	var dispatchCount int64
	db.Model(&database.TenantEcommerceDispatch{}).Where("order_id = ?", order.ID).Count(&dispatchCount)
	if dispatchCount != 1 {
		t.Fatalf("REGRESIÓN/RACE: debe existir EXACTAMENTE 1 fila de despacho en la base, hay %d", dispatchCount)
	}

	var finalOrder database.TenantEcommerceOrder
	db.First(&finalOrder, order.ID)
	if finalOrder.Status != OrderStatusDespachado {
		t.Fatalf("Order.Status final = %q, quería DESPACHADO", finalOrder.Status)
	}

	var historyCount int64
	db.Model(&database.TenantEcommerceOrderStatusHistory{}).
		Where("order_id = ? AND from_status = ? AND to_status = ?", order.ID, OrderStatusListoParaDespacho, OrderStatusDespachado).
		Count(&historyCount)
	if historyCount != 1 {
		t.Fatalf("debe registrarse EXACTAMENTE 1 fila de StatusHistory LISTO_PARA_DESPACHO->DESPACHADO, hay %d", historyCount)
	}
}
