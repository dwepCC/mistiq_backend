package service

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"tukifac/pkg/database"
	"tukifac/pkg/tax"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

// setupConvertTestDB mismo set de tablas que internal/sales/service/sale_combos_test.go
// (setupSaleCombosDB) más las tablas de ecommerce — SaleService.Create hace trabajo real
// (impuestos, pagos, movimiento de inventario), así que se necesita el mismo universo mínimo.
func setupConvertTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	models := []interface{}{
		&database.TenantCompanyConfig{}, &database.TenantDocumentSeries{}, &database.TenantContact{},
		&database.TenantSale{}, &database.TenantSaleItem{}, &database.TenantSalePayment{},
		&database.TenantCashSession{}, &database.TenantPaymentMethod{}, &database.TenantProduct{},
		&database.TenantBranch{}, &database.TenantProductStock{}, &database.TenantStockMovement{},
		&database.TenantInventoryOperationType{},
		&database.TenantEcommerceOrder{}, &database.TenantEcommerceOrderItem{},
		&database.TenantEcommerceOrderStatusHistory{}, &database.TenantNotification{},
	}
	for _, m := range models {
		if err := db.AutoMigrate(m); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Create(&database.TenantCompanyConfig{ID: 1, SunatEnabled: false, TaxRate: 18}).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.SeedInventoryOperationTypes(db); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.TenantPaymentMethod{Code: "cash", Name: "Efectivo", IsSystem: true, Active: true}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.TenantCashSession{
		BranchID: 1, UserID: 1, OpenedBy: 1, Status: "open", OpenedAt: time.Now(),
	}).Error; err != nil {
		t.Fatal(err)
	}
	return db
}

func seedConvertProduct(t *testing.T, db *gorm.DB, code string, price float64) database.TenantProduct {
	t.Helper()
	p := database.TenantProduct{
		Code: code, Name: "Producto " + code, Type: "product", Unit: "NIU", SalePrice: price,
		IgvAffectationType: "10", PriceIncludesIgv: true, ManageStock: false, BranchID: 1, Active: true,
		ShowInDigitalCatalog: true, // CreateOrder (Fase 3) solo resuelve productos publicados en el catálogo
	}
	if err := db.Create(&p).Error; err != nil {
		t.Fatal(err)
	}
	return p
}

func seedNotaVentaSeries(t *testing.T, db *gorm.DB) database.TenantDocumentSeries {
	t.Helper()
	s := database.TenantDocumentSeries{Series: "NV01", SunatCode: "00", DocType: "NOTA_VENTA", BranchID: 1}
	if err := db.Create(&s).Error; err != nil {
		t.Fatal(err)
	}
	return s
}

func convertServiceAndInput(db *gorm.DB, seriesID, branchID uint) (*EcommerceService, ConvertInput) {
	return &EcommerceService{db: db}, ConvertInput{
		Target: "nota_venta", SeriesID: seriesID, BranchID: branchID,
		IssueDate: time.Now(), UserID: 1, TaxConfig: tax.Config{TaxRate: 18},
	}
}

// TestConvertToSale_NoCierraElPedido es el test central del punto 1 aprobado: convertir a venta
// NO debe forzar ningún cambio de Status — antes de esta fase, ConvertToSale escribía
// status="cerrado" en el mismo Update que fija converted_sale_id/converted_at (convert.go línea
// ~220 previa a esta fase). Se prueba explícitamente en un estado intermedio (EN_PREPARACION) para
// que una regresión que reintroduzca el forzado sea inconfundible.
func TestConvertToSale_NoCierraElPedido(t *testing.T) {
	db := setupConvertTestDB(t)
	p := seedConvertProduct(t, db, "P1", 25)
	series := seedNotaVentaSeries(t, db)

	svc := &EcommerceService{db: db}
	order, _, err := svc.CreateOrder(CreateOrderInput{
		CustomerName: "Ana", CustomerPhone: "999111222", DeliveryMethod: DeliveryMethodPickup,
		Items: []CreateOrderItemInput{{ProductID: p.ID, Quantity: 1}},
	})
	if err != nil {
		t.Fatalf("CreateOrder: %v", err)
	}
	// Simula que el pedido ya avanzó de estado ANTES de convertirse — exactamente el caso que el
	// desacople de §4 tiene que soportar.
	if err := svc.UpdateOrderStatus(order.ID, UpdateOrderStatusInput{NewStatus: OrderStatusConfirmado}); err != nil {
		t.Fatal(err)
	}
	if err := svc.UpdateOrderStatus(order.ID, UpdateOrderStatusInput{NewStatus: OrderStatusEnPreparacion}); err != nil {
		t.Fatal(err)
	}

	convSvc, input := convertServiceAndInput(db, series.ID, 1)
	sale, err := convSvc.ConvertToSale(order.ID, input)
	if err != nil {
		t.Fatalf("ConvertToSale: %v", err)
	}
	if sale == nil || sale.ID == 0 {
		t.Fatal("ConvertToSale debe devolver una venta creada")
	}

	after, err := svc.GetOrder(order.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Status != OrderStatusEnPreparacion {
		t.Fatalf("REGRESIÓN: Status cambió de EN_PREPARACION a %q al convertir — la conversión NO debe tocar Status (Contrato v2 §4)", after.Status)
	}
	if after.ConvertedSaleID == nil || *after.ConvertedSaleID != sale.ID {
		t.Fatalf("ConvertedSaleID debe quedar en %d, quedó %v", sale.ID, after.ConvertedSaleID)
	}
	if after.ConvertedAt == nil {
		t.Fatal("ConvertedAt debe quedar seteado")
	}
}

// TestConvertToSale_LeeDeTenantEcommerceOrderItem: un pedido creado con CreateOrder (Fase 1 en
// adelante) tiene filas normalizadas — ConvertToSale debe usarlas, no solo ItemsJSON.
func TestConvertToSale_LeeDeTenantEcommerceOrderItem(t *testing.T) {
	db := setupConvertTestDB(t)
	p1 := seedConvertProduct(t, db, "A1", 10)
	p2 := seedConvertProduct(t, db, "A2", 15)
	series := seedNotaVentaSeries(t, db)

	svc := &EcommerceService{db: db}
	order, _, err := svc.CreateOrder(CreateOrderInput{
		CustomerName: "Luis", CustomerPhone: "999333444", DeliveryMethod: DeliveryMethodPickup,
		Items: []CreateOrderItemInput{
			{ProductID: p1.ID, Quantity: 2},
			{ProductID: p2.ID, Quantity: 1},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.UpdateOrderStatus(order.ID, UpdateOrderStatusInput{NewStatus: OrderStatusConfirmado}); err != nil {
		t.Fatal(err)
	}

	convSvc, input := convertServiceAndInput(db, series.ID, 1)
	sale, err := convSvc.ConvertToSale(order.ID, input)
	if err != nil {
		t.Fatalf("ConvertToSale: %v", err)
	}

	var saleItems []database.TenantSaleItem
	db.Where("sale_id = ?", sale.ID).Find(&saleItems)
	if len(saleItems) != 2 {
		t.Fatalf("la venta debe tener 2 líneas (una por producto del pedido), tiene %d", len(saleItems))
	}
}

// TestConvertToSale_FallbackAItemsJSONParaPedidosLegacy: un pedido "legacy" simulado (creado
// directo en la tabla, SIN filas en TenantEcommerceOrderItem, como cualquier pedido de antes de la
// Fase 1) debe seguir convirtiéndose correctamente leyendo ItemsJSON — el requisito explícito del
// usuario de "verificar que ItemsJSON legacy siga funcionando".
func TestConvertToSale_FallbackAItemsJSONParaPedidosLegacy(t *testing.T) {
	db := setupConvertTestDB(t)
	p := seedConvertProduct(t, db, "L1", 40)
	series := seedNotaVentaSeries(t, db)

	itemsJSON, _ := json.Marshal([]OrderItemInput{{ProductID: p.ID, Name: p.Name, Quantity: 1, UnitPrice: 40}})
	legacy := database.TenantEcommerceOrder{
		CustomerName: "Pedido Legacy", CustomerPhone: "999000111",
		ItemsJSON: string(itemsJSON), Total: 40, Status: OrderStatusConfirmado,
	}
	if err := db.Create(&legacy).Error; err != nil {
		t.Fatal(err)
	}
	// Confirma la premisa del test: cero filas normalizadas para este pedido.
	var count int64
	db.Model(&database.TenantEcommerceOrderItem{}).Where("order_id = ?", legacy.ID).Count(&count)
	if count != 0 {
		t.Fatalf("setup inválido: este pedido debía simular estar sin filas normalizadas, tiene %d", count)
	}

	convSvc, input := convertServiceAndInput(db, series.ID, 1)
	sale, err := convSvc.ConvertToSale(legacy.ID, input)
	if err != nil {
		t.Fatalf("ConvertToSale sobre pedido legacy (solo ItemsJSON) debe funcionar: %v", err)
	}
	var saleItems []database.TenantSaleItem
	db.Where("sale_id = ?", sale.ID).Find(&saleItems)
	if len(saleItems) != 1 {
		t.Fatalf("la venta debe tener 1 línea leída de ItemsJSON, tiene %d", len(saleItems))
	}
}

// TestConvertToSale_GeneraNotificacionJuntoConConvertedSaleID Fase 6: la notificación de
// "convertido a venta" y el marcado converted_sale_id/converted_at deben quedar en la misma
// transacción — ninguno de los dos debe poder quedar huérfano del otro (Contrato v2 Fase 6,
// transaccionalidad).
func TestConvertToSale_GeneraNotificacionJuntoConConvertedSaleID(t *testing.T) {
	db := setupConvertTestDB(t)
	p := seedConvertProduct(t, db, "N1", 20)
	series := seedNotaVentaSeries(t, db)

	svc := &EcommerceService{db: db}
	order, _, err := svc.CreateOrder(CreateOrderInput{
		CustomerName: "Notif", CustomerPhone: "999444555", DeliveryMethod: DeliveryMethodPickup,
		Items: []CreateOrderItemInput{{ProductID: p.ID, Quantity: 1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.UpdateOrderStatus(order.ID, UpdateOrderStatusInput{NewStatus: OrderStatusConfirmado}); err != nil {
		t.Fatal(err)
	}

	convSvc, input := convertServiceAndInput(db, series.ID, 1)
	sale, err := convSvc.ConvertToSale(order.ID, input)
	if err != nil {
		t.Fatalf("ConvertToSale: %v", err)
	}

	var notif database.TenantNotification
	if err := db.Where("type = ?", "ecommerce.order.converted").First(&notif).Error; err != nil {
		t.Fatalf("debía crear 1 notificación ecommerce.order.converted: %v", err)
	}
	if notif.LinkPath != fmt.Sprintf("/sales/pedidos-web?id=%d", order.ID) {
		t.Errorf("LinkPath = %q, quería apuntar al pedido real", notif.LinkPath)
	}

	after, _ := svc.GetOrder(order.ID)
	if after.ConvertedSaleID == nil || *after.ConvertedSaleID != sale.ID {
		t.Fatalf("ConvertedSaleID debía quedar en %d junto con la notificación, quedó %v", sale.ID, after.ConvertedSaleID)
	}
}
