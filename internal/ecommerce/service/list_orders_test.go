package service

import (
	"encoding/json"
	"strconv"
	"testing"

	"tukifac/pkg/database"

	"gorm.io/gorm"
)

// seedOrderRow inserta un pedido directo en la tabla (sin pasar por CreateOrder) — los tests de
// este archivo prueban filtros/lectura de ListOrders/GetOrderDetail, no la resolución de catálogo
// (ya cubierta extensamente en create_order_test.go).
func seedOrderRow(t *testing.T, db *gorm.DB, o database.TenantEcommerceOrder) database.TenantEcommerceOrder {
	t.Helper()
	if o.ItemsJSON == "" {
		o.ItemsJSON = "[]"
	}
	if o.Status == "" {
		o.Status = OrderStatusPendiente
	}
	if err := db.Create(&o).Error; err != nil {
		t.Fatal(err)
	}
	return o
}

func TestListOrders_FiltraPorEstado(t *testing.T) {
	db := setupCreateOrderTestDB(t)
	svc := &EcommerceService{db: db}
	seedOrderRow(t, db, database.TenantEcommerceOrder{CustomerName: "A", CustomerPhone: "1", Total: 10, Status: OrderStatusPendiente})
	seedOrderRow(t, db, database.TenantEcommerceOrder{CustomerName: "B", CustomerPhone: "2", Total: 20, Status: OrderStatusConfirmado})

	rows, err := svc.ListOrders(ListOrdersParams{Status: "confirmado"}) // minúsculas: debe normalizar
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].CustomerName != "B" {
		t.Fatalf("se esperaba solo el pedido B (CONFIRMADO), rows=%+v", rows)
	}
}

func TestListOrders_FiltraPorSucursal(t *testing.T) {
	db := setupCreateOrderTestDB(t)
	svc := &EcommerceService{db: db}
	branchA := uint(1)
	branchB := uint(2)
	seedOrderRow(t, db, database.TenantEcommerceOrder{CustomerName: "A", CustomerPhone: "1", Total: 10, BranchID: &branchA})
	seedOrderRow(t, db, database.TenantEcommerceOrder{CustomerName: "B", CustomerPhone: "2", Total: 20, BranchID: &branchB})
	seedOrderRow(t, db, database.TenantEcommerceOrder{CustomerName: "C", CustomerPhone: "3", Total: 30}) // sin sucursal

	rows, err := svc.ListOrders(ListOrdersParams{BranchID: branchA})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].CustomerName != "A" {
		t.Fatalf("se esperaba solo el pedido de la sucursal A, rows=%+v", rows)
	}
}

func TestListOrders_BuscaPorNombreTelefonoOId(t *testing.T) {
	db := setupCreateOrderTestDB(t)
	svc := &EcommerceService{db: db}
	o1 := seedOrderRow(t, db, database.TenantEcommerceOrder{CustomerName: "Juan Pérez", CustomerPhone: "999111222", Total: 10})
	seedOrderRow(t, db, database.TenantEcommerceOrder{CustomerName: "Ana Torres", CustomerPhone: "999333444", Total: 20})

	byName, err := svc.ListOrders(ListOrdersParams{Query: "Juan"})
	if err != nil {
		t.Fatal(err)
	}
	if len(byName) != 1 || byName[0].CustomerName != "Juan Pérez" {
		t.Fatalf("búsqueda por nombre falló: %+v", byName)
	}

	byPhone, err := svc.ListOrders(ListOrdersParams{Query: "999333444"})
	if err != nil {
		t.Fatal(err)
	}
	if len(byPhone) != 1 || byPhone[0].CustomerName != "Ana Torres" {
		t.Fatalf("búsqueda por teléfono falló: %+v", byPhone)
	}

	byID, err := svc.ListOrders(ListOrdersParams{Query: strconv.FormatUint(uint64(o1.ID), 10)})
	if err != nil {
		t.Fatal(err)
	}
	if len(byID) != 1 || byID[0].ID != o1.ID {
		t.Fatalf("búsqueda por N° de pedido falló: %+v", byID)
	}
}

// ── GetOrderDetail ───────────────────────────────────────────────────

func TestGetOrderDetail_UsaItemsNormalizados(t *testing.T) {
	db := setupCreateOrderTestDB(t)
	svc := &EcommerceService{db: db}
	p := seedSimpleProduct(t, db, "GD1", "Producto detalle", 15)
	order, _, err := svc.CreateOrder(CreateOrderInput{
		CustomerName: "X", CustomerPhone: "999000000", DeliveryMethod: DeliveryMethodPickup,
		Items: []CreateOrderItemInput{{ProductID: p.ID, Quantity: 2}},
	})
	if err != nil {
		t.Fatal(err)
	}

	got, items, history, err := svc.GetOrderDetail(order.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != order.ID {
		t.Fatalf("pedido incorrecto")
	}
	if len(items) != 1 || items[0].Name != "Producto detalle" || items[0].UnitPrice != 15 {
		t.Fatalf("items normalizados incorrectos: %+v", items)
	}
	// La creación ya registra 1 fila de historial (""->PENDIENTE, Fase 3) — no se fabrica nada
	// extra.
	if len(history) != 1 || history[0].ToStatus != OrderStatusPendiente {
		t.Fatalf("historial incorrecto: %+v", history)
	}
}

func TestGetOrderDetail_FallbackAItemsJSONParaPedidoLegacy(t *testing.T) {
	db := setupCreateOrderTestDB(t)
	svc := &EcommerceService{db: db}
	legacyItems, _ := json.Marshal([]OrderItemInput{{ProductID: 1, Name: "Legacy", Quantity: 3, UnitPrice: 9}})
	order := seedOrderRow(t, db, database.TenantEcommerceOrder{
		CustomerName: "Legacy", CustomerPhone: "999", Total: 27, ItemsJSON: string(legacyItems),
	})

	_, items, history, err := svc.GetOrderDetail(order.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Name != "Legacy" || items[0].PresentationID != nil {
		t.Fatalf("fallback a ItemsJSON incorrecto: %+v", items)
	}
	// Pedido creado directo en la tabla (no vía CreateOrder) — sin historial fabricado, debe venir
	// vacío, nunca inventado.
	if len(history) != 0 {
		t.Fatalf("no debe fabricarse historial para un pedido sin filas reales, hay %d", len(history))
	}
}

func TestGetOrderDetail_PedidoInexistente(t *testing.T) {
	db := setupCreateOrderTestDB(t)
	svc := &EcommerceService{db: db}
	_, _, _, err := svc.GetOrderDetail(99999)
	if err == nil {
		t.Fatal("un pedido inexistente debe devolver error")
	}
}
