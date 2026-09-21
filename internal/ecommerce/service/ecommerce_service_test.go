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

func setupEcommerceServiceDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []interface{}{
		&database.TenantEcommerceOrder{},
		&database.TenantEcommerceOrderItem{},
		&database.TenantEcommerceOrderStatusHistory{},
		&database.TenantEcommerceDispatch{},
		&database.TenantNotification{},
		&database.TenantProduct{},
		&database.TenantProductPresentation{},
	} {
		if err := db.AutoMigrate(m); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

// seedOrderableProduct producto real, publicado y activo — CreateOrder resuelve nombre/precio
// SIEMPRE contra filas como esta, nunca contra lo que mande el cliente (Fase 3).
func seedOrderableProduct(t *testing.T, db *gorm.DB, code, name string, price float64) database.TenantProduct {
	t.Helper()
	p := database.TenantProduct{
		Code: code, Name: name, Type: "product", Unit: "NIU", SalePrice: price,
		Active: true, ShowInDigitalCatalog: true,
	}
	if err := db.Create(&p).Error; err != nil {
		t.Fatal(err)
	}
	return p
}

// mustCreateOrder siembra "Polo" (S/ 25 x2) y "Pantalón" (S/ 60 x1), ambos productos simples, y
// crea el pedido a través del contrato público real (solo product_id/quantity, sin precio/nombre).
func mustCreateOrder(t *testing.T, svc *EcommerceService) *database.TenantEcommerceOrder {
	t.Helper()
	polo := seedOrderableProduct(t, svc.db, "POLO", "Polo", 25)
	pantalon := seedOrderableProduct(t, svc.db, "PANT", "Pantalón", 60)
	order, _, err := svc.CreateOrder(CreateOrderInput{
		CustomerName:   "Juan Pérez",
		CustomerPhone:  "999888777",
		DeliveryMethod: DeliveryMethodPickup,
		Items: []CreateOrderItemInput{
			{ProductID: polo.ID, Quantity: 2},
			{ProductID: pantalon.ID, Quantity: 1},
		},
	})
	if err != nil {
		t.Fatalf("CreateOrder: %v", err)
	}
	return order
}

// TestCreateOrder_DualWrite verifica el punto central de compatibilidad: el pedido nuevo escribe
// ItemsJSON exactamente como antes (nada que lea ese campo hoy debe notar el cambio) Y ADEMÁS
// normaliza las mismas líneas en TenantEcommerceOrderItem.
func TestCreateOrder_DualWrite(t *testing.T) {
	db := setupEcommerceServiceDB(t)
	svc := &EcommerceService{db: db}

	order := mustCreateOrder(t, svc)

	if order.Status != OrderStatusPendiente {
		t.Errorf("Status = %q, quería %q", order.Status, OrderStatusPendiente)
	}
	if order.PaymentStatus != "NO_APLICA" {
		t.Errorf("PaymentStatus = %q, quería NO_APLICA (marcador inerte, punto 2 aprobado)", order.PaymentStatus)
	}
	wantTotal := 2*25.0 + 60.0
	if order.Total != wantTotal || order.Subtotal != wantTotal {
		t.Errorf("Total=%.2f Subtotal=%.2f, quería ambos %.2f", order.Total, order.Subtotal, wantTotal)
	}

	// ItemsJSON legacy sigue funcionando exactamente igual.
	var legacyItems []OrderItemInput
	if err := json.Unmarshal([]byte(order.ItemsJSON), &legacyItems); err != nil {
		t.Fatalf("ItemsJSON debe seguir siendo deserializable: %v", err)
	}
	if len(legacyItems) != 2 {
		t.Fatalf("ItemsJSON debe tener 2 líneas, tiene %d", len(legacyItems))
	}

	// Tabla normalizada nueva tiene las mismas líneas.
	var rows []database.TenantEcommerceOrderItem
	if err := db.Where("order_id = ?", order.ID).Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("TenantEcommerceOrderItem debe tener 2 líneas, tiene %d", len(rows))
	}
}

// TestCreateOrder_PaymentStatusNoEsConfigurableDesdeElInput: CreateOrderInput no expone
// PaymentStatus — el cliente público no tiene forma de enviarlo ni modificarlo (punto 2 aprobado).
func TestCreateOrder_PaymentStatusNoEsConfigurableDesdeElInput(t *testing.T) {
	db := setupEcommerceServiceDB(t)
	svc := &EcommerceService{db: db}
	order := mustCreateOrder(t, svc)
	if order.PaymentStatus != "NO_APLICA" {
		t.Fatalf("PaymentStatus siempre debe quedar NO_APLICA en esta fase, got %q", order.PaymentStatus)
	}
}

func TestUpdateOrderStatus_TransicionValidaConPermisoDelegadoAlCaller(t *testing.T) {
	db := setupEcommerceServiceDB(t)
	svc := &EcommerceService{db: db}
	order := mustCreateOrder(t, svc)

	branchID := uint(3)
	if err := svc.UpdateOrderStatus(order.ID, UpdateOrderStatusInput{
		NewStatus: "confirmado", // minúsculas: debe normalizar a mayúsculas
		UserID:    7,
		BranchID:  &branchID,
	}); err != nil {
		t.Fatalf("UpdateOrderStatus PENDIENTE->CONFIRMADO: %v", err)
	}

	updated, err := svc.GetOrder(order.ID)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != OrderStatusConfirmado {
		t.Errorf("Status = %q, quería CONFIRMADO", updated.Status)
	}
	if updated.BranchID == nil || *updated.BranchID != branchID {
		t.Errorf("BranchID debe asignarse al confirmar si no tenía: %v", updated.BranchID)
	}

	// 2 filas: la de creación (""->PENDIENTE, escrita por CreateOrder, Fase 3) + esta transición.
	var hist []database.TenantEcommerceOrderStatusHistory
	db.Where("order_id = ?", order.ID).Order("id ASC").Find(&hist)
	if len(hist) != 2 {
		t.Fatalf("debe haber 2 filas en StatusHistory (creación + confirmación), hay %d", len(hist))
	}
	if hist[0].FromStatus != "" || hist[0].ToStatus != OrderStatusPendiente {
		t.Errorf("primera fila (creación) from/to = %q/%q, quería \"\"/PENDIENTE", hist[0].FromStatus, hist[0].ToStatus)
	}
	if hist[1].FromStatus != OrderStatusPendiente || hist[1].ToStatus != OrderStatusConfirmado {
		t.Errorf("segunda fila from/to = %s/%s, quería PENDIENTE/CONFIRMADO", hist[1].FromStatus, hist[1].ToStatus)
	}
}

// TestUpdateOrderStatus_NoReasignaSucursalYaAsignada: si el pedido ya tenía sucursal, un
// BranchID distinto en el body de confirmación NO debe pisarla (Contrato v2 §1.1: "asignada en la
// confirmación... solo si no tenía" — reasignar es un endpoint/fase aparte).
func TestUpdateOrderStatus_NoReasignaSucursalYaAsignada(t *testing.T) {
	db := setupEcommerceServiceDB(t)
	svc := &EcommerceService{db: db}
	order := mustCreateOrder(t, svc)

	original := uint(1)
	db.Model(&database.TenantEcommerceOrder{}).Where("id = ?", order.ID).Update("branch_id", original)

	other := uint(99)
	if err := svc.UpdateOrderStatus(order.ID, UpdateOrderStatusInput{NewStatus: OrderStatusConfirmado, BranchID: &other}); err != nil {
		t.Fatal(err)
	}
	updated, _ := svc.GetOrder(order.ID)
	if updated.BranchID == nil || *updated.BranchID != original {
		t.Errorf("BranchID no debía cambiar (ya tenía %d), quedó %v", original, updated.BranchID)
	}
}

func TestUpdateOrderStatus_TransicionInvalida(t *testing.T) {
	db := setupEcommerceServiceDB(t)
	svc := &EcommerceService{db: db}
	order := mustCreateOrder(t, svc)

	// PENDIENTE -> DESPACHADO: salto directo, nunca válido (flujo de despacho corregido).
	err := svc.UpdateOrderStatus(order.ID, UpdateOrderStatusInput{NewStatus: OrderStatusDespachado})
	if err == nil {
		t.Fatal("esperaba error: no se puede pasar de PENDIENTE a DESPACHADO directamente")
	}

	updated, _ := svc.GetOrder(order.ID)
	if updated.Status != OrderStatusPendiente {
		t.Errorf("una transición inválida no debe modificar el status, quedó %q", updated.Status)
	}
	// 1 fila (la de creación, de CreateOrder) — la transición rechazada no debe agregar ninguna más.
	var hist []database.TenantEcommerceOrderStatusHistory
	db.Where("order_id = ?", order.ID).Find(&hist)
	if len(hist) != 1 {
		t.Errorf("una transición rechazada no debe agregar filas a StatusHistory (solo la de creación), hay %d", len(hist))
	}
}

func TestUpdateOrderStatus_CancelarExigeMotivo(t *testing.T) {
	db := setupEcommerceServiceDB(t)
	svc := &EcommerceService{db: db}
	order := mustCreateOrder(t, svc)

	if err := svc.UpdateOrderStatus(order.ID, UpdateOrderStatusInput{NewStatus: OrderStatusCancelado}); err == nil {
		t.Fatal("cancelar sin Notes debe fallar (motivo obligatorio)")
	}
	if err := svc.UpdateOrderStatus(order.ID, UpdateOrderStatusInput{
		NewStatus: OrderStatusCancelado, Notes: "cliente se arrepintió",
	}); err != nil {
		t.Fatalf("cancelar con motivo debe funcionar: %v", err)
	}
}

// TestUpdateOrderStatus_EstadoInvalidoEsRechazado: un string arbitrario (no parte del enum) nunca
// debe aceptarse, sin importar si "existe" como transición o no.
func TestUpdateOrderStatus_EstadoInvalidoEsRechazado(t *testing.T) {
	db := setupEcommerceServiceDB(t)
	svc := &EcommerceService{db: db}
	order := mustCreateOrder(t, svc)

	if err := svc.UpdateOrderStatus(order.ID, UpdateOrderStatusInput{NewStatus: "no_existe"}); err == nil {
		t.Fatal("un estado fuera del enum debe rechazarse")
	}
}

func countNotificationsByType(t *testing.T, db *gorm.DB, notifType string) int64 {
	t.Helper()
	var n int64
	db.Model(&database.TenantNotification{}).Where("type = ?", notifType).Count(&n)
	return n
}

// TestUpdateOrderStatus_ConfirmarGeneraNotificacion Fase 6: PENDIENTE->CONFIRMADO es la única
// transición "de confirmación" que notifica — ver notificationForTransition.
func TestUpdateOrderStatus_ConfirmarGeneraNotificacion(t *testing.T) {
	db := setupEcommerceServiceDB(t)
	svc := &EcommerceService{db: db}
	order := mustCreateOrder(t, svc) // ya deja 1 notificación "ecommerce.order.created"

	if err := svc.UpdateOrderStatus(order.ID, UpdateOrderStatusInput{NewStatus: OrderStatusConfirmado}); err != nil {
		t.Fatalf("UpdateOrderStatus: %v", err)
	}
	if got := countNotificationsByType(t, db, "ecommerce.order.confirmed"); got != 1 {
		t.Fatalf("debía crear exactamente 1 notificación ecommerce.order.confirmed, hay %d", got)
	}

	var notif database.TenantNotification
	db.Where("type = ?", "ecommerce.order.confirmed").First(&notif)
	if notif.LinkPath != fmt.Sprintf("/sales/pedidos-web?id=%d", order.ID) {
		t.Errorf("LinkPath = %q, quería apuntar al pedido real", notif.LinkPath)
	}
	if notif.UserID != nil {
		t.Errorf("la notificación de confirmación debe ser broadcast (UserID nil), quedó %v", notif.UserID)
	}
}

// TestUpdateOrderStatus_CancelarYRechazarGeneranNotificacion Fase 6: ambas transiciones comparten
// el mismo Type ("ecommerce.order.cancelled") — Contrato v2 Fase 6, evento #3 "cancelado/rechazado"
// es UN tipo, diferenciado por Title/Body, no dos tipos separados.
func TestUpdateOrderStatus_CancelarYRechazarGeneranNotificacion(t *testing.T) {
	db := setupEcommerceServiceDB(t)
	svc := &EcommerceService{db: db}

	cancelado := mustCreateOrder(t, svc)
	if err := svc.UpdateOrderStatus(cancelado.ID, UpdateOrderStatusInput{
		NewStatus: OrderStatusCancelado, Notes: "motivo",
	}); err != nil {
		t.Fatalf("UpdateOrderStatus (cancelado): %v", err)
	}

	rechazado := mustCreateOrder(t, svc)
	if err := svc.UpdateOrderStatus(rechazado.ID, UpdateOrderStatusInput{
		NewStatus: OrderStatusRechazado, Notes: "motivo",
	}); err != nil {
		t.Fatalf("UpdateOrderStatus (rechazado): %v", err)
	}

	if got := countNotificationsByType(t, db, "ecommerce.order.cancelled"); got != 2 {
		t.Fatalf("debía haber 2 notificaciones ecommerce.order.cancelled (una por pedido), hay %d", got)
	}
}

// TestUpdateOrderStatus_TransicionesDePreparacionNoNotifican Fase 6: las transiciones de
// picking/empaquetado/despacho NO deben generar notificación (solo las 3 "relevantes" pedidas) —
// evita saturar el panel con un aviso por cada micro-cambio operativo.
func TestUpdateOrderStatus_TransicionesDePreparacionNoNotifican(t *testing.T) {
	db := setupEcommerceServiceDB(t)
	svc := &EcommerceService{db: db}
	order := mustCreateOrder(t, svc)

	if err := svc.UpdateOrderStatus(order.ID, UpdateOrderStatusInput{NewStatus: OrderStatusConfirmado}); err != nil {
		t.Fatal(err)
	}
	if err := svc.UpdateOrderStatus(order.ID, UpdateOrderStatusInput{NewStatus: OrderStatusEnPreparacion}); err != nil {
		t.Fatal(err)
	}
	if err := svc.UpdateOrderStatus(order.ID, UpdateOrderStatusInput{NewStatus: OrderStatusEmpaquetado}); err != nil {
		t.Fatal(err)
	}

	var total int64
	db.Model(&database.TenantNotification{}).Count(&total)
	// Solo 2: "ecommerce.order.created" (CreateOrder) + "ecommerce.order.confirmed" (la única
	// transición notificable de las tres ejecutadas acá).
	if total != 2 {
		t.Fatalf("EN_PREPARACION/EMPAQUETADO no deben generar notificación — total esperado 2, hay %d", total)
	}
}
