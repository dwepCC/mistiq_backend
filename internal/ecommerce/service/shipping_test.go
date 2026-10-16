package service

import (
	"strings"
	"testing"
	"time"

	"tukifac/pkg/database"
	"tukifac/pkg/stockreserve"

	"gorm.io/gorm"
)

// setupShippingTestDB la BD de CreateOrder + transportistas y ubigeo.
func setupShippingTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := setupCreateOrderTestDB(t)
	for _, m := range []interface{}{
		&database.TenantCarrier{}, &database.UbiRegion{}, &database.UbiProvincia{}, &database.UbiDistrito{},
	} {
		if err := db.AutoMigrate(m); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func seedStockedProduct(t *testing.T, db *gorm.DB, code string, price float64, branchID uint, qty float64) database.TenantProduct {
	t.Helper()
	p := database.TenantProduct{Code: code, Name: "Prod " + code, Type: "product", Unit: "NIU", SalePrice: price,
		Active: true, ShowInDigitalCatalog: true, ManageStock: true}
	if err := db.Create(&p).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.TenantProductStock{ProductID: p.ID, BranchID: branchID, Quantity: qty}).Error; err != nil {
		t.Fatal(err)
	}
	return p
}

func newPickupOrder(t *testing.T, svc *EcommerceService, productID uint, qty float64) *database.TenantEcommerceOrder {
	t.Helper()
	o, _, err := svc.CreateOrder(CreateOrderInput{
		CustomerName: "Cliente", CustomerPhone: "999111222", DeliveryMethod: DeliveryMethodPickup,
		Items: []CreateOrderItemInput{{ProductID: productID, Quantity: qty}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func confirmIn(svc *EcommerceService, orderID, branch uint) error {
	return svc.UpdateOrderStatus(orderID, UpdateOrderStatusInput{NewStatus: OrderStatusConfirmado, BranchID: &branch})
}

// ── Reserva de stock ─────────────────────────────────────────────────

func TestReserva_ConfirmarBloqueaSiNoAlcanza(t *testing.T) {
	db := setupShippingTestDB(t)
	svc := &EcommerceService{db: db}
	p := seedStockedProduct(t, db, "R1", 50, 1, 5)

	a := newPickupOrder(t, svc, p.ID, 3)
	if err := confirmIn(svc, a.ID, 1); err != nil {
		t.Fatalf("el primer pedido (3 de 5) debe confirmarse: %v", err)
	}
	b := newPickupOrder(t, svc, p.ID, 3)
	err := confirmIn(svc, b.ID, 1)
	if err == nil || !strings.Contains(err.Error(), "stock insuficiente") {
		t.Fatalf("el segundo pedido (3 más, quedan 2) debe rechazarse por stock, err=%v", err)
	}
	got, _ := svc.GetOrder(b.ID)
	if got.Status != OrderStatusPendiente {
		t.Errorf("el pedido rechazado debe seguir PENDIENTE, quedó %s", got.Status)
	}
	c := newPickupOrder(t, svc, p.ID, 2)
	if err := confirmIn(svc, c.ID, 1); err != nil {
		t.Fatalf("2 de los 2 disponibles deben confirmarse: %v", err)
	}
}

func TestReserva_OtraSucursalNoCuenta(t *testing.T) {
	db := setupShippingTestDB(t)
	svc := &EcommerceService{db: db}
	p := seedStockedProduct(t, db, "R2", 50, 1, 5)
	db.Create(&database.TenantProductStock{ProductID: p.ID, BranchID: 2, Quantity: 5})

	a := newPickupOrder(t, svc, p.ID, 5)
	if err := confirmIn(svc, a.ID, 1); err != nil {
		t.Fatal(err)
	}
	b := newPickupOrder(t, svc, p.ID, 5)
	if err := confirmIn(svc, b.ID, 2); err != nil {
		t.Fatalf("la reserva de la sucursal 1 no debe afectar a la sucursal 2: %v", err)
	}
}

func TestReserva_CancelarLibera(t *testing.T) {
	db := setupShippingTestDB(t)
	svc := &EcommerceService{db: db}
	p := seedStockedProduct(t, db, "R3", 50, 1, 4)

	a := newPickupOrder(t, svc, p.ID, 4)
	if err := confirmIn(svc, a.ID, 1); err != nil {
		t.Fatal(err)
	}
	reserved, _ := stockreserve.ReservedFor(db, 1, 0, p.ID, nil)
	if reserved != 4 {
		t.Fatalf("reservado = %v, quería 4", reserved)
	}
	if err := svc.UpdateOrderStatus(a.ID, UpdateOrderStatusInput{NewStatus: OrderStatusCancelado, Notes: "cliente se arrepintió"}); err != nil {
		t.Fatal(err)
	}
	reserved, _ = stockreserve.ReservedFor(db, 1, 0, p.ID, nil)
	if reserved != 0 {
		t.Errorf("cancelar debe liberar la reserva, quedó %v", reserved)
	}
}

func TestReserva_ConvertidoEnVentaYaNoReserva(t *testing.T) {
	db := setupShippingTestDB(t)
	svc := &EcommerceService{db: db}
	p := seedStockedProduct(t, db, "R4", 50, 1, 4)
	a := newPickupOrder(t, svc, p.ID, 3)
	if err := confirmIn(svc, a.ID, 1); err != nil {
		t.Fatal(err)
	}
	saleID := uint(77)
	db.Model(&database.TenantEcommerceOrder{}).Where("id = ?", a.ID).Update("converted_sale_id", saleID)
	reserved, _ := stockreserve.ReservedFor(db, 1, 0, p.ID, nil)
	if reserved != 0 {
		t.Errorf("un pedido ya convertido en venta no reserva (la venta descuenta el stock), reservado=%v", reserved)
	}
}

func TestReserva_ExcluyeElPropioPedido(t *testing.T) {
	db := setupShippingTestDB(t)
	svc := &EcommerceService{db: db}
	p := seedStockedProduct(t, db, "R5", 50, 1, 4)
	a := newPickupOrder(t, svc, p.ID, 3)
	if err := confirmIn(svc, a.ID, 1); err != nil {
		t.Fatal(err)
	}
	reserved, _ := stockreserve.ReservedFor(db, 1, a.ID, p.ID, nil)
	if reserved != 0 {
		t.Errorf("al convertir, la reserva del propio pedido no debe contarse contra sí mismo, reservado=%v", reserved)
	}
	lines, err := svc.OrderStockLines(a.ID, 1)
	if err != nil || len(lines) != 1 {
		t.Fatalf("lines=%v err=%v", lines, err)
	}
	if lines[0].OnHand != 4 || lines[0].ReservedOthers != 0 || !lines[0].Enough {
		t.Errorf("línea inesperada: %+v", lines[0])
	}
}

func TestReserva_ProductoSinControlDeStockNoBloquea(t *testing.T) {
	db := setupShippingTestDB(t)
	svc := &EcommerceService{db: db}
	p := seedSimpleProduct(t, db, "R6", "Sin control", 10) // ManageStock=false
	a := newPickupOrder(t, svc, p.ID, 999)
	if err := confirmIn(svc, a.ID, 1); err != nil {
		t.Fatalf("un producto sin control de stock no debe bloquear la confirmación: %v", err)
	}
}

// ── Documento del destinatario ───────────────────────────────────────

func TestNormalizeRecipientDoc(t *testing.T) {
	cases := []struct {
		typ, num, wantTyp string
		ok                bool
	}{
		{"", "45128803", "1", true},
		{"1", "45128803", "1", true},
		{"1", "4512880", "", false},
		{"1", "4512880X", "", false},
		{"", "20601234567", "6", true},
		{"6", "2060123456", "", false},
		{"4", "001234567", "4", true},
		{"4", "12", "", false},
		{"9", "45128803", "", false},
		{"1", "", "", false},
	}
	for _, c := range cases {
		typ, _, err := NormalizeRecipientDoc(c.typ, c.num)
		if c.ok && (err != nil || typ != c.wantTyp) {
			t.Errorf("(%q,%q): typ=%q err=%v, quería %q", c.typ, c.num, typ, err, c.wantTyp)
		}
		if !c.ok && err == nil {
			t.Errorf("(%q,%q) debía rechazarse", c.typ, c.num)
		}
	}
}

// ── Pedido por agencia ───────────────────────────────────────────────

func seedCarrier(t *testing.T, svc *EcommerceService, name string, days int) *database.TenantCarrier {
	t.Helper()
	c, err := svc.CreateCarrier(CarrierInput{Name: &name, PickupDeadlineDays: &days})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func agencyInput(productID, carrierID uint) CreateOrderInput {
	return CreateOrderInput{
		CustomerName: "Walter Chambi", CustomerPhone: "951700442", DeliveryMethod: DeliveryMethodAgency,
		Items:              []CreateOrderItemInput{{ProductID: productID, Quantity: 1}},
		CarrierID:          &carrierID,
		DestinationUbigeo:  "080101",
		DestinationAgency:  "Shalom Av. El Sol 745",
		RecipientDocNumber: "29330145",
	}
}

func TestPedidoAgencia_Completo(t *testing.T) {
	db := setupShippingTestDB(t)
	svc := &EcommerceService{db: db}
	p := seedSimpleProduct(t, db, "A1", "Producto", 100)
	carrier := seedCarrier(t, svc, "Shalom", 15)

	order, _, err := svc.CreateOrder(agencyInput(p.ID, carrier.ID))
	if err != nil {
		t.Fatal(err)
	}
	if order.RecipientName != "Walter Chambi" || order.RecipientPhone != "951700442" {
		t.Errorf("nombre y teléfono de quien recoge deben tomar los del comprador: %q %q", order.RecipientName, order.RecipientPhone)
	}
	if order.RecipientDocType != "1" || order.RecipientDocNumber != "29330145" {
		t.Errorf("documento mal guardado: %q %q", order.RecipientDocType, order.RecipientDocNumber)
	}
	if order.CarrierID == nil || *order.CarrierID != carrier.ID || order.DestinationUbigeo != "080101" {
		t.Errorf("transportista/destino mal guardados: %+v", order)
	}
}

func TestPedidoAgencia_Validaciones(t *testing.T) {
	db := setupShippingTestDB(t)
	svc := &EcommerceService{db: db}
	p := seedSimpleProduct(t, db, "A2", "Producto", 100)
	carrier := seedCarrier(t, svc, "Shalom", 15)

	mut := func(f func(*CreateOrderInput)) error {
		in := agencyInput(p.ID, carrier.ID)
		f(&in)
		_, _, err := svc.CreateOrder(in)
		return err
	}
	if mut(func(in *CreateOrderInput) { in.RecipientDocNumber = "" }) == nil {
		t.Error("sin documento debe rechazarse")
	}
	if mut(func(in *CreateOrderInput) { in.RecipientDocNumber = "123" }) == nil {
		t.Error("DNI corto debe rechazarse")
	}
	if mut(func(in *CreateOrderInput) { in.CarrierID = nil }) == nil {
		t.Error("sin transportista debe rechazarse")
	}
	if mut(func(in *CreateOrderInput) { in.DestinationAgency = "  " }) == nil {
		t.Error("sin agencia debe rechazarse")
	}
	if mut(func(in *CreateOrderInput) { in.DestinationUbigeo = "" }) == nil {
		t.Error("sin ciudad de destino debe rechazarse")
	}
	if mut(func(in *CreateOrderInput) { in.DestinationUbigeo = "12" }) == nil {
		t.Error("ubigeo inválido debe rechazarse")
	}
	inactive := false
	svc.UpdateCarrier(carrier.ID, CarrierInput{Active: &inactive})
	if mut(func(in *CreateOrderInput) {}) == nil {
		t.Error("un transportista inactivo no se puede elegir en la tienda")
	}
}

func TestUpdateOrderShipping(t *testing.T) {
	db := setupShippingTestDB(t)
	svc := &EcommerceService{db: db}
	p := seedSimpleProduct(t, db, "A3", "Producto", 100)
	carrier := seedCarrier(t, svc, "Shalom", 15)
	order := newPickupOrder(t, svc, p.ID, 1)

	cost, pk, w := 12.5, 2, 3.4
	doc, name := "45128803", "Ana Paredes"
	updated, err := svc.UpdateOrderShipping(order.ID, OrderShippingInput{
		CarrierID: &carrier.ID, ShippingCost: &cost, PackageCount: &pk, WeightKg: &w,
		RecipientDocNumber: &doc, RecipientName: &name,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.ShippingCost != 12.5 || updated.PackageCount == nil || *updated.PackageCount != 2 ||
		updated.RecipientDocType != "1" || updated.RecipientName != "Ana Paredes" {
		t.Errorf("datos de envío mal guardados: %+v", updated)
	}
	if updated.Total != 100 {
		t.Errorf("el costo de envío no debe alterar el total del pedido: %v", updated.Total)
	}
	neg := -1.0
	if _, err := svc.UpdateOrderShipping(order.ID, OrderShippingInput{ShippingCost: &neg}); err == nil {
		t.Error("costo negativo debe rechazarse")
	}
	bad := "123"
	if _, err := svc.UpdateOrderShipping(order.ID, OrderShippingInput{RecipientDocNumber: &bad}); err == nil {
		t.Error("DNI inválido debe rechazarse")
	}
}

// ── Despacho de un pedido por agencia ────────────────────────────────

func readyAgencyOrder(t *testing.T, svc *EcommerceService, carrierID uint, code string) *database.TenantEcommerceOrder {
	t.Helper()
	p := seedSimpleProduct(t, svc.db, code, "Producto "+code, 100)
	order, _, err := svc.CreateOrder(agencyInput(p.ID, carrierID))
	if err != nil {
		t.Fatal(err)
	}
	pk, w := 2, 3.5
	if _, err := svc.UpdateOrderShipping(order.ID, OrderShippingInput{PackageCount: &pk, WeightKg: &w}); err != nil {
		t.Fatal(err)
	}
	for _, to := range []string{OrderStatusConfirmado, OrderStatusEnPreparacion, OrderStatusEmpaquetado, OrderStatusListoParaDespacho} {
		if err := svc.UpdateOrderStatus(order.ID, UpdateOrderStatusInput{NewStatus: to}); err != nil {
			t.Fatal(err)
		}
	}
	return order
}

func TestDespachoAgencia_ExigeGuiaYCopiaDatosDelPedido(t *testing.T) {
	db := setupShippingTestDB(t)
	svc := &EcommerceService{db: db}
	carrier := seedCarrier(t, svc, "Shalom", 15)
	order := readyAgencyOrder(t, svc, carrier.ID, "D1")

	if _, err := svc.CreateDispatch(order.ID, CreateDispatchInput{}); err == nil {
		t.Fatal("un envío por agencia no sale sin número de guía")
	}
	guia := " SH-48203314 "
	d, err := svc.CreateDispatch(order.ID, CreateDispatchInput{TrackingCode: &guia})
	if err != nil {
		t.Fatal(err)
	}
	if d.TrackingCode == nil || *d.TrackingCode != "SH-48203314" {
		t.Errorf("guía mal guardada: %v", d.TrackingCode)
	}
	if d.CarrierName == nil || *d.CarrierName != "Shalom" || d.CarrierID == nil || *d.CarrierID != carrier.ID {
		t.Errorf("transportista debía copiarse del pedido: %v %v", d.CarrierName, d.CarrierID)
	}
	if d.PackageCount == nil || *d.PackageCount != 2 || d.WeightKg == nil || *d.WeightKg != 3.5 {
		t.Errorf("bultos y peso debían copiarse del pedido: %v %v", d.PackageCount, d.WeightKg)
	}
}

func TestDespachoAgencia_FaltanDatosDelEnvio(t *testing.T) {
	db := setupShippingTestDB(t)
	svc := &EcommerceService{db: db}
	carrier := seedCarrier(t, svc, "Shalom", 15)
	order := readyAgencyOrder(t, svc, carrier.ID, "D2")
	empty := ""
	if _, err := svc.UpdateOrderShipping(order.ID, OrderShippingInput{DestinationAgency: &empty}); err != nil {
		t.Fatal(err)
	}
	guia := "SH-1"
	_, err := svc.CreateDispatch(order.ID, CreateDispatchInput{TrackingCode: &guia})
	if err == nil || !strings.Contains(err.Error(), "agencia de destino") {
		t.Fatalf("debe indicar qué dato falta, err=%v", err)
	}
}

func TestEnAgencia_PlazoSegunTransportista(t *testing.T) {
	db := setupShippingTestDB(t)
	svc := &EcommerceService{db: db}
	carrier := seedCarrier(t, svc, "Olva", 10)
	order := readyAgencyOrder(t, svc, carrier.ID, "G1")
	guia := "OL-1"
	d, err := svc.CreateDispatch(order.ID, CreateDispatchInput{TrackingCode: &guia})
	if err != nil {
		t.Fatal(err)
	}
	db.Model(&database.TenantEcommerceDispatch{}).Where("id = ?", d.ID).Update("dispatched_at", time.Now().Add(-5*24*time.Hour))
	arrived := time.Now().Add(-3 * 24 * time.Hour)
	d, err = svc.MarkDispatchInAgency(d.ID, AgencyArrivalInput{ArrivedAt: &arrived})
	if err != nil {
		t.Fatal(err)
	}
	if d.Status != DispatchStatusEnAgencia || d.PickupDeadline == nil {
		t.Fatalf("debe quedar EN_AGENCIA con fecha límite: %+v", d)
	}
	wantDeadline := arrived.Add(10 * 24 * time.Hour)
	if diff := d.PickupDeadline.Sub(wantDeadline); diff > time.Minute || diff < -time.Minute {
		t.Errorf("fecha límite = %v, quería %v (llegada + 10 días)", d.PickupDeadline, wantDeadline)
	}
	o, _ := svc.GetOrder(order.ID)
	if o.Status != OrderStatusDespachado {
		t.Errorf("el pedido sigue DESPACHADO en agencia, quedó %s", o.Status)
	}

	rows, total, summary, err := svc.ListShippingBoard(ShippingBoardParams{Stage: StageEnAgencia})
	if err != nil || total != 1 || len(rows) != 1 {
		t.Fatalf("tablero en agencia: total=%d rows=%d err=%v", total, len(rows), err)
	}
	if summary[StageEnAgencia] != 1 {
		t.Errorf("resumen en agencia = %d", summary[StageEnAgencia])
	}
	if rows[0].PickupDaysLeft == nil || *rows[0].PickupDaysLeft != 7 {
		t.Errorf("le quedan 7 días (10 − 3), quedó %v", rows[0].PickupDaysLeft)
	}
	if rows[0].CarrierName != "Olva" || rows[0].TrackingCode != "OL-1" {
		t.Errorf("fila del tablero: %+v", rows[0])
	}

	if _, err := svc.MarkDispatchInAgency(d.ID, AgencyArrivalInput{}); err == nil {
		t.Error("no se puede marcar dos veces la llegada")
	}
	if _, err := svc.MarkDispatchDelivered(d.ID, DispatchTransitionInput{}); err != nil {
		t.Fatalf("entrega desde la agencia: %v", err)
	}
	rows, _, _, _ = svc.ListShippingBoard(ShippingBoardParams{Stage: StageEntregado})
	if len(rows) != 1 {
		t.Errorf("debía figurar como ENTREGADO, filas=%d", len(rows))
	}
}

func TestEnAgencia_FechaFuturaRechazada(t *testing.T) {
	db := setupShippingTestDB(t)
	svc := &EcommerceService{db: db}
	carrier := seedCarrier(t, svc, "Shalom", 15)
	order := readyAgencyOrder(t, svc, carrier.ID, "G2")
	guia := "SH-9"
	d, _ := svc.CreateDispatch(order.ID, CreateDispatchInput{TrackingCode: &guia})
	future := time.Now().Add(5 * 24 * time.Hour)
	if _, err := svc.MarkDispatchInAgency(d.ID, AgencyArrivalInput{ArrivedAt: &future}); err == nil {
		t.Error("una llegada futura debe rechazarse")
	}
}

func TestRetorno_LiberaReservaYPideMotivo(t *testing.T) {
	db := setupShippingTestDB(t)
	svc := &EcommerceService{db: db}
	carrier := seedCarrier(t, svc, "Shalom", 15)
	p := seedStockedProduct(t, db, "RT1", 100, 1, 5)
	in := agencyInput(p.ID, carrier.ID)
	order, _, err := svc.CreateOrder(in)
	if err != nil {
		t.Fatal(err)
	}
	if err := confirmIn(svc, order.ID, 1); err != nil {
		t.Fatal(err)
	}
	for _, to := range []string{OrderStatusEnPreparacion, OrderStatusEmpaquetado, OrderStatusListoParaDespacho} {
		if err := svc.UpdateOrderStatus(order.ID, UpdateOrderStatusInput{NewStatus: to}); err != nil {
			t.Fatal(err)
		}
	}
	guia := "SH-RT"
	d, err := svc.CreateDispatch(order.ID, CreateDispatchInput{TrackingCode: &guia})
	if err != nil {
		t.Fatal(err)
	}
	if reserved, _ := stockreserve.ReservedFor(db, 1, 0, p.ID, nil); reserved != 1 {
		t.Fatalf("despachado y sin venta sigue reservado, reservado=%v", reserved)
	}
	if _, err := svc.MarkDispatchReturned(d.ID, DispatchTransitionInput{}); err == nil {
		t.Error("el retorno exige motivo")
	}
	d, err = svc.MarkDispatchReturned(d.ID, DispatchTransitionInput{Notes: "no lo recogió", UserID: 3})
	if err != nil {
		t.Fatal(err)
	}
	if d.Status != DispatchStatusDevuelto || d.ReturnedAt == nil {
		t.Errorf("despacho mal marcado: %+v", d)
	}
	o, _ := svc.GetOrder(order.ID)
	if o.Status != OrderStatusDevuelto {
		t.Errorf("pedido = %s, quería DEVUELTO", o.Status)
	}
	if reserved, _ := stockreserve.ReservedFor(db, 1, 0, p.ID, nil); reserved != 0 {
		t.Errorf("el retorno libera la reserva, reservado=%v", reserved)
	}
	var hist database.TenantEcommerceOrderStatusHistory
	if err := db.Where("order_id = ? AND to_status = ?", order.ID, OrderStatusDevuelto).First(&hist).Error; err != nil || hist.Notes != "no lo recogió" {
		t.Errorf("historial del pedido: %+v err=%v", hist, err)
	}
	if _, err := svc.MarkDispatchReturned(d.ID, DispatchTransitionInput{Notes: "otra vez"}); err == nil {
		t.Error("no se puede devolver dos veces")
	}
}

// ── Lote y rótulos ───────────────────────────────────────────────────

func TestBatchDispatch_ResultadoPorPedido(t *testing.T) {
	db := setupShippingTestDB(t)
	svc := &EcommerceService{db: db}
	carrier := seedCarrier(t, svc, "Shalom", 15)
	o1 := readyAgencyOrder(t, svc, carrier.ID, "B1")
	o2 := readyAgencyOrder(t, svc, carrier.ID, "B2")
	p := seedSimpleProduct(t, db, "B3", "Otro", 10)
	pendiente := newPickupOrder(t, svc, p.ID, 1)

	g1, g2 := "SH-100", "SH-101"
	res, err := svc.BatchDispatch([]BatchDispatchItem{
		{OrderID: o1.ID, TrackingCode: &g1},
		{OrderID: o2.ID, TrackingCode: &g2},
		{OrderID: pendiente.ID},
		{OrderID: o1.ID, TrackingCode: &g1},
	}, 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 4 || !res[0].OK || !res[1].OK {
		t.Fatalf("los dos primeros deben despacharse: %+v", res)
	}
	if res[2].OK || res[2].Error == "" {
		t.Errorf("un pedido pendiente no se despacha: %+v", res[2])
	}
	if res[3].OK {
		t.Errorf("el repetido no debe procesarse dos veces: %+v", res[3])
	}
	if _, err := svc.BatchDispatch(nil, 1); err == nil {
		t.Error("un lote vacío debe rechazarse")
	}
}

func TestRotulosEnLote(t *testing.T) {
	db := setupShippingTestDB(t)
	svc := &EcommerceService{db: db}
	carrier := seedCarrier(t, svc, "Shalom", 15)
	listo := readyAgencyOrder(t, svc, carrier.ID, "L1")
	p := seedSimpleProduct(t, db, "L2", "Otro", 10)
	pendiente := newPickupOrder(t, svc, p.ID, 1)

	res, err := BuildLabelsForOrders(db, []uint{listo.ID, pendiente.ID})
	if err != nil {
		t.Fatal(err)
	}
	if res[0].Label == nil || res[0].Error != "" {
		t.Fatalf("un pedido listo para despachar imprime su rótulo antes de salir: %+v", res[0])
	}
	l := res[0].Label
	if l.CarrierName != "Shalom" || l.RecipientDocNumber != "29330145" || l.AgencyName == "" || l.ItemCount != 1 || l.TrackingCode != "" {
		t.Errorf("rótulo: %+v", l)
	}
	if l.PackageCount == nil || *l.PackageCount != 2 {
		t.Errorf("bultos del rótulo: %v", l.PackageCount)
	}
	if res[1].Label != nil || res[1].Error == "" {
		t.Errorf("un pedido pendiente no imprime rótulo: %+v", res[1])
	}
	if _, err := BuildLabelsForOrders(db, nil); err == nil {
		t.Error("sin pedidos debe rechazarse")
	}
}

// ── Tablero ──────────────────────────────────────────────────────────

func TestTablero_EtapasYBusqueda(t *testing.T) {
	db := setupShippingTestDB(t)
	svc := &EcommerceService{db: db}
	carrier := seedCarrier(t, svc, "Shalom", 15)
	p := seedSimpleProduct(t, db, "T0", "Pend", 10)
	newPickupOrder(t, svc, p.ID, 1) // POR_CONFIRMAR
	readyAgencyOrder(t, svc, carrier.ID, "T1")
	o3 := readyAgencyOrder(t, svc, carrier.ID, "T2")
	guia := "SH-BUSCAR"
	svc.CreateDispatch(o3.ID, CreateDispatchInput{TrackingCode: &guia}) // EN_CAMINO

	_, _, summary, err := svc.ListShippingBoard(ShippingBoardParams{})
	if err != nil {
		t.Fatal(err)
	}
	if summary[StagePorConfirmar] != 1 || summary[StageListo] != 1 || summary[StageEnCamino] != 1 {
		t.Errorf("resumen: %+v", summary)
	}
	rows, total, _, _ := svc.ListShippingBoard(ShippingBoardParams{Query: "SH-BUSCAR"})
	if total != 1 || len(rows) != 1 || rows[0].ID != o3.ID {
		t.Errorf("la búsqueda por guía debe hallar el pedido, total=%d rows=%d", total, len(rows))
	}
	rows, _, _, _ = svc.ListShippingBoard(ShippingBoardParams{Query: "29330145"})
	if len(rows) != 2 {
		t.Errorf("búsqueda por DNI: %d filas, quería 2", len(rows))
	}
	rows, _, _, _ = svc.ListShippingBoard(ShippingBoardParams{Query: orderNumber(o3.ID)})
	if len(rows) != 1 {
		t.Errorf("búsqueda por PW-n: %d filas", len(rows))
	}
	rows, _, _, _ = svc.ListShippingBoard(ShippingBoardParams{Department: "08"})
	if len(rows) != 2 {
		t.Errorf("filtro por departamento 08: %d filas, quería 2", len(rows))
	}
	rows, _, _, _ = svc.ListShippingBoard(ShippingBoardParams{Department: "15"})
	if len(rows) != 0 {
		t.Errorf("filtro por departamento 15: %d filas, quería 0", len(rows))
	}
	if _, _, _, err := svc.ListShippingBoard(ShippingBoardParams{Stage: "XYZ"}); err == nil {
		t.Error("etapa inválida debe rechazarse")
	}
}

// ── Transportistas ───────────────────────────────────────────────────

func TestTransportistas_Validaciones(t *testing.T) {
	db := setupShippingTestDB(t)
	svc := &EcommerceService{db: db}
	name := "Shalom"
	days := "5,1,3,3"
	c, err := svc.CreateCarrier(CarrierInput{Name: &name, DispatchDays: &days})
	if err != nil {
		t.Fatal(err)
	}
	if c.DispatchDays != "1,3,5" || c.PickupDeadlineDays != 15 || c.WarnDaysBefore != 5 || !c.Active {
		t.Errorf("valores por defecto/normalización: %+v", c)
	}
	bad := "9"
	if _, err := svc.UpdateCarrier(c.ID, CarrierInput{DispatchDays: &bad}); err == nil {
		t.Error("día 9 inválido")
	}
	zero := 0
	if _, err := svc.UpdateCarrier(c.ID, CarrierInput{PickupDeadlineDays: &zero}); err == nil {
		t.Error("plazo 0 inválido")
	}
	warn := 20
	if _, err := svc.UpdateCarrier(c.ID, CarrierInput{WarnDaysBefore: &warn}); err == nil {
		t.Error("el aviso previo no puede superar el plazo")
	}
	url := "ftp://x"
	if _, err := svc.UpdateCarrier(c.ID, CarrierInput{TrackingURL: &url}); err == nil {
		t.Error("enlace de rastreo debe ser http(s)")
	}
	empty := ""
	if _, err := svc.CreateCarrier(CarrierInput{Name: &empty}); err == nil {
		t.Error("nombre vacío inválido")
	}
	if err := svc.DeleteCarrier(c.ID); err != nil {
		t.Fatal(err)
	}
	rows, _ := svc.ListCarriers(false, false)
	if len(rows) != 0 {
		t.Errorf("borrado: quedan %d", len(rows))
	}
}
