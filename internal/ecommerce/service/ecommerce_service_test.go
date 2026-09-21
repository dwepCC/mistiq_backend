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
	} {
		if err := db.AutoMigrate(m); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func mustCreateOrder(t *testing.T, svc *EcommerceService) *database.TenantEcommerceOrder {
	t.Helper()
	order, err := svc.CreateOrder(CreateOrderInput{
		CustomerName:  "Juan Pérez",
		CustomerPhone: "999888777",
		Items: []OrderItemInput{
			{ProductID: 1, Name: "Polo", Quantity: 2, UnitPrice: 25},
			{ProductID: 2, Name: "Pantalón", Quantity: 1, UnitPrice: 60},
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

	var hist []database.TenantEcommerceOrderStatusHistory
	db.Where("order_id = ?", order.ID).Find(&hist)
	if len(hist) != 1 {
		t.Fatalf("debe registrar 1 fila en StatusHistory, hay %d", len(hist))
	}
	if hist[0].FromStatus != OrderStatusPendiente || hist[0].ToStatus != OrderStatusConfirmado {
		t.Errorf("StatusHistory from/to = %s/%s, quería PENDIENTE/CONFIRMADO", hist[0].FromStatus, hist[0].ToStatus)
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
	var hist []database.TenantEcommerceOrderStatusHistory
	db.Where("order_id = ?", order.ID).Find(&hist)
	if len(hist) != 0 {
		t.Errorf("una transición rechazada no debe dejar fila en StatusHistory, hay %d", len(hist))
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
