package service

import (
	"fmt"
	"testing"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

var createOrderTestDBCounter int

// setupCreateOrderTestDB universo completo para CreateOrder (Contrato ecommerce v2 Fase 3):
// productos/presentaciones reales (fuente de verdad de precio/nombre) + stock (para probar que NO
// se toca) + las tablas propias del pedido.
func setupCreateOrderTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	createOrderTestDBCounter++
	dsn := fmt.Sprintf("file:%s-%d?mode=memory&cache=shared", t.Name(), createOrderTestDBCounter)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: gormlogger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []interface{}{
		&database.TenantProduct{}, &database.TenantProductPresentation{},
		&database.TenantProductStock{}, &database.TenantProductPresentationStock{},
		&database.TenantEcommerceOrder{}, &database.TenantEcommerceOrderItem{},
		&database.TenantEcommerceOrderStatusHistory{}, &database.TenantNotification{},
	} {
		if err := db.AutoMigrate(m); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func seedSimpleProduct(t *testing.T, db *gorm.DB, code, name string, price float64) database.TenantProduct {
	t.Helper()
	p := database.TenantProduct{Code: code, Name: name, Type: "product", Unit: "NIU", SalePrice: price, Active: true, ShowInDigitalCatalog: true}
	if err := db.Create(&p).Error; err != nil {
		t.Fatal(err)
	}
	return p
}

func seedVariantProduct(t *testing.T, db *gorm.DB, code, name string, price float64) database.TenantProduct {
	t.Helper()
	p := database.TenantProduct{Code: code, Name: name, Type: "product", Unit: "NIU", SalePrice: price, HasVariants: true, Active: true, ShowInDigitalCatalog: true}
	if err := db.Create(&p).Error; err != nil {
		t.Fatal(err)
	}
	return p
}

func seedPresentation(t *testing.T, db *gorm.DB, productID uint, name string, price float64) database.TenantProductPresentation {
	t.Helper()
	pr := database.TenantProductPresentation{ProductID: productID, Name: name, SalePrice: price, Active: true}
	if err := db.Create(&pr).Error; err != nil {
		t.Fatal(err)
	}
	return pr
}

// ── 1. Pedido simple ────────────────────────────────────────────────

func TestCreateOrder_ProductoSimple(t *testing.T) {
	db := setupCreateOrderTestDB(t)
	svc := &EcommerceService{db: db}
	p := seedSimpleProduct(t, db, "S1", "Producto simple", 30)

	order, items, err := svc.CreateOrder(CreateOrderInput{
		CustomerName: "Carlos", CustomerPhone: "999111000", DeliveryMethod: DeliveryMethodPickup,
		Items: []CreateOrderItemInput{{ProductID: p.ID, Quantity: 2}},
	})
	if err != nil {
		t.Fatalf("CreateOrder: %v", err)
	}
	if order.Status != OrderStatusPendiente { // punto 15
		t.Errorf("Status = %q, quería PENDIENTE", order.Status)
	}
	if order.Total != 60 || order.Subtotal != 60 {
		t.Errorf("Total/Subtotal = %.2f/%.2f, quería 60/60", order.Total, order.Subtotal)
	}
	if len(items) != 1 || items[0].PresentationID != nil {
		t.Fatalf("items = %+v, quería 1 línea sin presentación", items)
	}
	if items[0].Name != "Producto simple" || items[0].UnitPrice != 30 {
		t.Errorf("línea resuelta incorrecta: %+v", items[0])
	}

	var histCount int64
	db.Model(&database.TenantEcommerceOrderStatusHistory{}).Where("order_id = ?", order.ID).Count(&histCount)
	if histCount != 1 {
		t.Errorf("debe registrar 1 fila de StatusHistory (creación), hay %d", histCount)
	}
	var notifCount int64
	db.Model(&database.TenantNotification{}).Where("type = ?", "ecommerce.order.created").Count(&notifCount)
	if notifCount != 1 {
		t.Errorf("debe registrar 1 notificación interna, hay %d", notifCount)
	}
}

// ── 2/17. Pedido con presentación — PresentationID persistido ──────

func TestCreateOrder_ConPresentacion(t *testing.T) {
	db := setupCreateOrderTestDB(t)
	svc := &EcommerceService{db: db}
	p := seedVariantProduct(t, db, "V1", "Polo", 25)
	rojoM := seedPresentation(t, db, p.ID, "Rojo / M", 28)

	_, items, err := svc.CreateOrder(CreateOrderInput{
		CustomerName: "Rosa", CustomerPhone: "999222000", DeliveryMethod: DeliveryMethodPickup,
		Items: []CreateOrderItemInput{{ProductID: p.ID, PresentationID: &rojoM.ID, Quantity: 1}},
	})
	if err != nil {
		t.Fatalf("CreateOrder: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("se esperaba 1 línea, hay %d", len(items))
	}
	if items[0].PresentationID == nil || *items[0].PresentationID != rojoM.ID {
		t.Fatalf("PresentationID no quedó persistido correctamente: %v", items[0].PresentationID)
	}
	// El precio de la presentación (28) reemplaza al del producto (25), no se suman.
	if items[0].UnitPrice != 28 {
		t.Errorf("UnitPrice = %.2f, quería 28 (precio de la presentación)", items[0].UnitPrice)
	}
	if items[0].Name != "Polo — Rojo / M" {
		t.Errorf("Name = %q, quería incluir el nombre de la presentación", items[0].Name)
	}
}

// ── 3. Múltiples presentaciones del mismo producto ──────────────────

func TestCreateOrder_MultiplesPresentacionesDelMismoProducto(t *testing.T) {
	db := setupCreateOrderTestDB(t)
	svc := &EcommerceService{db: db}
	p := seedVariantProduct(t, db, "V2", "Zapatilla", 100)
	talla38 := seedPresentation(t, db, p.ID, "Talla 38", 100)
	talla40 := seedPresentation(t, db, p.ID, "Talla 40", 105)

	order, items, err := svc.CreateOrder(CreateOrderInput{
		CustomerName: "Ana", CustomerPhone: "999333000", DeliveryMethod: DeliveryMethodPickup,
		Items: []CreateOrderItemInput{
			{ProductID: p.ID, PresentationID: &talla38.ID, Quantity: 1},
			{ProductID: p.ID, PresentationID: &talla40.ID, Quantity: 2},
		},
	})
	if err != nil {
		t.Fatalf("CreateOrder: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("se esperaban 2 líneas independientes (mismo producto, distinta presentación), hay %d", len(items))
	}
	wantTotal := 100.0 + 2*105.0
	if order.Total != wantTotal {
		t.Errorf("Total = %.2f, quería %.2f", order.Total, wantTotal)
	}
}

// ── 4. presentation_id de OTRO producto → rechazo ───────────────────

func TestCreateOrder_PresentationDeOtroProducto_Rechazado(t *testing.T) {
	db := setupCreateOrderTestDB(t)
	svc := &EcommerceService{db: db}
	p1 := seedVariantProduct(t, db, "P1", "Producto 1", 20)
	p2 := seedVariantProduct(t, db, "P2", "Producto 2", 30)
	presDeP2 := seedPresentation(t, db, p2.ID, "Única", 30)

	_, _, err := svc.CreateOrder(CreateOrderInput{
		CustomerName: "X", CustomerPhone: "999444000", DeliveryMethod: DeliveryMethodPickup,
		// presentación real, pero pertenece a p2 — se pide contra p1.
		Items: []CreateOrderItemInput{{ProductID: p1.ID, PresentationID: &presDeP2.ID, Quantity: 1}},
	})
	if err == nil {
		t.Fatal("una presentación de otro producto debe rechazarse")
	}
	assertNoOrdersPersisted(t, db) // punto 20
}

// ── 5/21. presentation_id (y producto) de OTRO TENANT → rechazo ────

func TestCreateOrder_PresentationDeOtroTenant_Rechazado(t *testing.T) {
	dbA := setupCreateOrderTestDB(t) // "tenant A"
	dbB := setupCreateOrderTestDB(t) // "tenant B" — BD completamente separada, mismo patrón que Fase 2

	prodA := seedVariantProduct(t, dbA, "TA", "Producto tenant A", 40)
	presA := seedPresentation(t, dbA, prodA.ID, "Única A", 40)

	svcB := &EcommerceService{db: dbB}
	_, _, err := svcB.CreateOrder(CreateOrderInput{
		CustomerName: "Y", CustomerPhone: "999555000", DeliveryMethod: DeliveryMethodPickup,
		// IDs que existen en el tenant A pero se piden contra la conexión del tenant B.
		Items: []CreateOrderItemInput{{ProductID: prodA.ID, PresentationID: &presA.ID, Quantity: 1}},
	})
	if err == nil {
		t.Fatal("un producto/presentación de otro tenant nunca debe resolverse (BD separada) — debía rechazarse")
	}
	assertNoOrdersPersisted(t, dbB)
}

// ── 6. Presentación inactiva → rechazo ──────────────────────────────

func TestCreateOrder_PresentationInactiva_Rechazada(t *testing.T) {
	db := setupCreateOrderTestDB(t)
	svc := &EcommerceService{db: db}
	p := seedVariantProduct(t, db, "V3", "Producto", 20)
	pres := seedPresentation(t, db, p.ID, "Descontinuada", 20)
	db.Model(&pres).Update("active", false) // mismo caso GORM ya documentado en Fase 2

	_, _, err := svc.CreateOrder(CreateOrderInput{
		CustomerName: "Z", CustomerPhone: "999666000", DeliveryMethod: DeliveryMethodPickup,
		Items: []CreateOrderItemInput{{ProductID: p.ID, PresentationID: &pres.ID, Quantity: 1}},
	})
	if err == nil {
		t.Fatal("una presentación inactiva debe rechazarse")
	}
	assertNoOrdersPersisted(t, db)
}

// ── 7/8/9. El cliente NO puede mandar precio/subtotal/total ────────
// CreateOrderItemInput NO TIENE campos de precio/nombre — es estructuralmente imposible que el
// caller "manipule" el precio a este nivel (el compilador lo impide). La prueba de que el HTTP
// handler tampoco los acepta vive en internal/ecommerce/handler (nivel donde sí llega JSON crudo
// con campos arbitrarios que el bind puede ignorar). Acá se prueba el caso equivalente a nivel de
// servicio: aunque el precio del catálogo cambie ENTRE que el frontend lo mostró y el checkout se
// envía, el pedido se cobra al precio VIGENTE en el momento de crear el pedido, nunca a uno viejo.
func TestCreateOrder_SiemprePrecioVigenteDelCatalogo_NuncaUnoDesactualizado(t *testing.T) {
	db := setupCreateOrderTestDB(t)
	svc := &EcommerceService{db: db}
	p := seedSimpleProduct(t, db, "S2", "Producto con precio cambiante", 50)

	// El precio sube DESPUÉS de que el frontend cacheó 50 (simulado: no hay forma de que
	// CreateOrderItemInput lleve un precio, así que esto por sí solo ya prueba el punto — pero se
	// deja el cambio de precio explícito para que la intención del test sea inconfundible).
	db.Model(&p).Update("sale_price", 65)

	order, items, err := svc.CreateOrder(CreateOrderInput{
		CustomerName: "W", CustomerPhone: "999777000", DeliveryMethod: DeliveryMethodPickup,
		Items: []CreateOrderItemInput{{ProductID: p.ID, Quantity: 1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if items[0].UnitPrice != 65 || order.Total != 65 {
		t.Fatalf("el pedido debe cobrar el precio VIGENTE (65), no uno viejo — UnitPrice=%.2f Total=%.2f", items[0].UnitPrice, order.Total)
	}
}

// ── 10. Cantidad inválida ────────────────────────────────────────────

func TestCreateOrder_CantidadInvalida_Rechazada(t *testing.T) {
	db := setupCreateOrderTestDB(t)
	svc := &EcommerceService{db: db}
	p := seedSimpleProduct(t, db, "S3", "Producto", 10)

	for _, qty := range []float64{0, -1, -0.5} {
		_, _, err := svc.CreateOrder(CreateOrderInput{
			CustomerName: "Q", CustomerPhone: "999888000", DeliveryMethod: DeliveryMethodPickup,
			Items: []CreateOrderItemInput{{ProductID: p.ID, Quantity: qty}},
		})
		if err == nil {
			t.Errorf("cantidad=%.1f debía rechazarse", qty)
		}
	}
	assertNoOrdersPersisted(t, db)
}

// ── 11. Producto inexistente ─────────────────────────────────────────

func TestCreateOrder_ProductoInexistente_Rechazado(t *testing.T) {
	db := setupCreateOrderTestDB(t)
	svc := &EcommerceService{db: db}
	_, _, err := svc.CreateOrder(CreateOrderInput{
		CustomerName: "N", CustomerPhone: "999999000", DeliveryMethod: DeliveryMethodPickup,
		Items: []CreateOrderItemInput{{ProductID: 99999, Quantity: 1}},
	})
	if err == nil {
		t.Fatal("un product_id inexistente debe rechazarse")
	}
	assertNoOrdersPersisted(t, db)
}

// Producto inactivo o no publicado en el Catálogo Digital: mismo criterio que "inexistente" — no
// se puede pedir por ID adivinado algo que el tenant no publicó.
func TestCreateOrder_ProductoNoPublicado_Rechazado(t *testing.T) {
	db := setupCreateOrderTestDB(t)
	svc := &EcommerceService{db: db}
	oculto := database.TenantProduct{Code: "OCULTO", Name: "Interno", Type: "product", Unit: "NIU", SalePrice: 10, Active: true, ShowInDigitalCatalog: false}
	db.Create(&oculto)

	_, _, err := svc.CreateOrder(CreateOrderInput{
		CustomerName: "H", CustomerPhone: "999000111", DeliveryMethod: DeliveryMethodPickup,
		Items: []CreateOrderItemInput{{ProductID: oculto.ID, Quantity: 1}},
	})
	if err == nil {
		t.Fatal("un producto no publicado en el Catálogo Digital no debe poder pedirse")
	}
}

// ── 12/21. Producto de otro tenant ──────────────────────────────────

func TestCreateOrder_ProductoDeOtroTenant_Rechazado(t *testing.T) {
	dbA := setupCreateOrderTestDB(t)
	dbB := setupCreateOrderTestDB(t)
	prodA := seedSimpleProduct(t, dbA, "TA2", "Producto tenant A", 15)

	svcB := &EcommerceService{db: dbB}
	_, _, err := svcB.CreateOrder(CreateOrderInput{
		CustomerName: "T", CustomerPhone: "999000222", DeliveryMethod: DeliveryMethodPickup,
		Items: []CreateOrderItemInput{{ProductID: prodA.ID, Quantity: 1}},
	})
	if err == nil {
		t.Fatal("un product_id que solo existe en OTRO tenant debe rechazarse (BD separada por tenant)")
	}
	assertNoOrdersPersisted(t, dbB)
}

// ── 13. Invitado + dirección snapshot ───────────────────────────────

func TestCreateOrder_InvitadoConDireccionSnapshot(t *testing.T) {
	db := setupCreateOrderTestDB(t)
	svc := &EcommerceService{db: db}
	p := seedSimpleProduct(t, db, "S4", "Producto envío", 20)

	order, _, err := svc.CreateOrder(CreateOrderInput{
		CustomerName: "Invitado", CustomerPhone: "999111333", DeliveryMethod: DeliveryMethodShipping,
		GuestAddressLine: "Av. Siempre Viva 742", GuestReference: "Frente al parque", GuestUbigeo: "150101",
		Items: []CreateOrderItemInput{{ProductID: p.ID, Quantity: 1}},
	})
	if err != nil {
		t.Fatalf("CreateOrder: %v", err)
	}
	if order.DeliveryMethod != DeliveryMethodShipping {
		t.Errorf("DeliveryMethod = %q", order.DeliveryMethod)
	}
	if order.GuestAddressLine != "Av. Siempre Viva 742" || order.GuestReference != "Frente al parque" || order.GuestUbigeo != "150101" {
		t.Fatalf("snapshot de dirección de invitado no quedó persistido: %+v", order)
	}
	if order.CustomerAccountID != nil || order.DeliveryAddressID != nil {
		t.Error("un invitado no debe tener CustomerAccountID/DeliveryAddressID")
	}
}

func TestCreateOrder_EnvioSinDireccion_Rechazado(t *testing.T) {
	db := setupCreateOrderTestDB(t)
	svc := &EcommerceService{db: db}
	p := seedSimpleProduct(t, db, "S5", "Producto", 20)

	_, _, err := svc.CreateOrder(CreateOrderInput{
		CustomerName: "Sin dirección", CustomerPhone: "999444555", DeliveryMethod: DeliveryMethodShipping,
		Items: []CreateOrderItemInput{{ProductID: p.ID, Quantity: 1}},
	})
	if err == nil {
		t.Fatal("ENVIO_DOMICILIO sin dirección (ni snapshot ni FK autenticada) debe rechazarse")
	}
}

// ── 14. Cliente autenticado + dirección (nivel de servicio — Fase 4
//        todavía no existe login público, se prueba que el MODELO ya lo soporta) ──

func TestCreateOrder_ClienteAutenticadoConDireccion(t *testing.T) {
	db := setupCreateOrderTestDB(t)
	svc := &EcommerceService{db: db}
	p := seedSimpleProduct(t, db, "S6", "Producto", 20)

	accountID := uint(7)
	addressID := uint(3)
	order, _, err := svc.CreateOrder(CreateOrderInput{
		CustomerName: "Cliente registrado", CustomerPhone: "999666777", DeliveryMethod: DeliveryMethodShipping,
		CustomerAccountID: &accountID, DeliveryAddressID: &addressID,
		Items: []CreateOrderItemInput{{ProductID: p.ID, Quantity: 1}},
	})
	if err != nil {
		t.Fatalf("CreateOrder: %v", err)
	}
	if order.CustomerAccountID == nil || *order.CustomerAccountID != accountID {
		t.Errorf("CustomerAccountID no quedó persistido: %v", order.CustomerAccountID)
	}
	if order.DeliveryAddressID == nil || *order.DeliveryAddressID != addressID {
		t.Errorf("DeliveryAddressID no quedó persistido: %v", order.DeliveryAddressID)
	}
	// Con dirección autenticada (FK), NO debe escribirse el snapshot de invitado — evita datos
	// duplicados/contradictorios entre la FK real y un snapshot viejo.
	if order.GuestAddressLine != "" {
		t.Errorf("no debe guardarse snapshot de invitado cuando hay DeliveryAddressID: %q", order.GuestAddressLine)
	}
}

// ── 18/19. No se modifica stock, no hay reserva/HOLD ────────────────

func TestCreateOrder_NoModificaStockNiReserva(t *testing.T) {
	db := setupCreateOrderTestDB(t)
	svc := &EcommerceService{db: db}
	p := seedSimpleProduct(t, db, "S7", "Producto con stock", 10)
	db.Create(&database.TenantProductStock{ProductID: p.ID, BranchID: 1, Quantity: 5})

	order, _, err := svc.CreateOrder(CreateOrderInput{
		CustomerName: "Stock", CustomerPhone: "999888999", DeliveryMethod: DeliveryMethodPickup,
		Items: []CreateOrderItemInput{{ProductID: p.ID, Quantity: 3}}, // más de lo que "reservaría" si existiera HOLD
	})
	if err != nil {
		t.Fatalf("CreateOrder: %v", err)
	}
	var stock database.TenantProductStock
	db.Where("product_id = ? AND branch_id = ?", p.ID, 1).First(&stock)
	if stock.Quantity != 5 {
		t.Fatalf("REGRESIÓN: el stock cambió de 5 a %.2f al crear el pedido — no debe tocarse (sin reserva/HOLD)", stock.Quantity)
	}
	var movementCount int64
	db.Model(&database.TenantStockMovement{}).Count(&movementCount)
	if movementCount != 0 {
		t.Fatalf("no debe registrarse ningún movimiento de kardex al crear un pedido, hay %d", movementCount)
	}
	if order.PaymentStatus != "NO_APLICA" {
		t.Errorf("PaymentStatus = %q, quería NO_APLICA (sin pasarela de pago en esta fase)", order.PaymentStatus)
	}
}

// ── 20. Fallo de persistencia no debe dejar nada a medias ───────────

func TestCreateOrder_FalloAMitadDeLista_NoPersisteNada(t *testing.T) {
	db := setupCreateOrderTestDB(t)
	svc := &EcommerceService{db: db}
	valido := seedSimpleProduct(t, db, "OK1", "Producto válido", 10)
	// La segunda línea es inválida (producto inexistente) — el pedido completo debe fallar, sin
	// dejar la primera línea "a medias" persistida en ningún lado.
	_, _, err := svc.CreateOrder(CreateOrderInput{
		CustomerName: "Rollback", CustomerPhone: "999000333", DeliveryMethod: DeliveryMethodPickup,
		Items: []CreateOrderItemInput{
			{ProductID: valido.ID, Quantity: 1},
			{ProductID: 88888, Quantity: 1},
		},
	})
	if err == nil {
		t.Fatal("una línea inválida debe rechazar el pedido completo")
	}
	assertNoOrdersPersisted(t, db)
}

func assertNoOrdersPersisted(t *testing.T, db *gorm.DB) {
	t.Helper()
	var orderCount, itemCount, histCount, notifCount int64
	db.Model(&database.TenantEcommerceOrder{}).Count(&orderCount)
	db.Model(&database.TenantEcommerceOrderItem{}).Count(&itemCount)
	db.Model(&database.TenantEcommerceOrderStatusHistory{}).Count(&histCount)
	db.Model(&database.TenantNotification{}).Count(&notifCount)
	if orderCount != 0 || itemCount != 0 || histCount != 0 || notifCount != 0 {
		t.Fatalf("un pedido rechazado no debe dejar NADA persistido: orders=%d items=%d history=%d notifications=%d",
			orderCount, itemCount, histCount, notifCount)
	}
}
