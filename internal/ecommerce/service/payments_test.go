package service

import (
	"strings"
	"testing"
	"time"

	"tukifac/pkg/database"

	"gorm.io/gorm"
)

func setupPaymentsDB(t *testing.T) (*gorm.DB, *EcommerceService) {
	t.Helper()
	db := setupConvertTestDB(t)
	for _, m := range []interface{}{&database.TenantCarrier{}, &database.TenantEcommerceOrderPayment{}, &database.TenantEcommerceSettings{}, &database.TenantSaleCreditInstallment{}} {
		if err := db.AutoMigrate(m); err != nil {
			t.Fatal(err)
		}
	}
	db.Create(&database.TenantPaymentMethod{Code: "yape", Name: "Yape", Active: true})
	db.Create(&database.TenantPaymentMethod{Code: "transfer", Name: "Transferencia", Active: true})
	return db, &EcommerceService{db: db}
}

func orderOf(t *testing.T, svc *EcommerceService, price, qty float64) *database.TenantEcommerceOrder {
	t.Helper()
	p := seedConvertProduct(t, svc.db, "PAY"+time.Now().Format("150405.000000"), price)
	o, _, err := svc.CreateOrder(CreateOrderInput{
		CustomerName: "Walter", CustomerPhone: "951700442", DeliveryMethod: DeliveryMethodPickup,
		Items: []CreateOrderItemInput{{ProductID: p.ID, Quantity: qty}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func TestAbonos_ResumenYEstadoDePago(t *testing.T) {
	_, svc := setupPaymentsDB(t)
	o := orderOf(t, svc, 100, 1) // total 100

	if _, err := svc.AddOrderPayment(o.ID, AddPaymentInput{Amount: 40, Method: "yape", Reference: "0482913", UserID: 3}); err != nil {
		t.Fatal(err)
	}
	rows, sum, err := svc.ListOrderPayments(o.ID)
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows=%d err=%v", len(rows), err)
	}
	if rows[0].Status != database.OrderPaymentValidated {
		t.Errorf("sin «validar pagos» el abono nace validado, quedó %s", rows[0].Status)
	}
	if sum.Paid != 40 || sum.Balance != 60 || sum.Status != PaymentStatusPartial {
		t.Errorf("resumen tras abonar 40 de 100: %+v", sum)
	}
	got, _ := svc.GetOrder(o.ID)
	if got.PaymentStatus != PaymentStatusPartial {
		t.Errorf("payment_status del pedido = %s, quería ABONADO", got.PaymentStatus)
	}

	if _, err := svc.AddOrderPayment(o.ID, AddPaymentInput{Amount: 60, Method: "transfer"}); err != nil {
		t.Fatal(err)
	}
	_, sum, _ = svc.ListOrderPayments(o.ID)
	if sum.Balance != 0 || sum.Status != PaymentStatusPaid {
		t.Errorf("con el total abonado debe quedar PAGADO: %+v", sum)
	}
	got, _ = svc.GetOrder(o.ID)
	if got.PaymentStatus != PaymentStatusPaid {
		t.Errorf("payment_status = %s, quería PAGADO", got.PaymentStatus)
	}
}

func TestAbonos_Validaciones(t *testing.T) {
	_, svc := setupPaymentsDB(t)
	o := orderOf(t, svc, 100, 1)

	if _, err := svc.AddOrderPayment(o.ID, AddPaymentInput{Amount: 0, Method: "yape"}); err == nil {
		t.Error("monto cero debe rechazarse")
	}
	if _, err := svc.AddOrderPayment(o.ID, AddPaymentInput{Amount: -5, Method: "yape"}); err == nil {
		t.Error("monto negativo debe rechazarse")
	}
	if _, err := svc.AddOrderPayment(o.ID, AddPaymentInput{Amount: 10, Method: ""}); err == nil {
		t.Error("sin medio de pago debe rechazarse")
	}
	if _, err := svc.AddOrderPayment(o.ID, AddPaymentInput{Amount: 10, Method: "no-existe"}); err == nil {
		t.Error("un medio de pago inexistente debe rechazarse")
	}
	if _, err := svc.AddOrderPayment(o.ID, AddPaymentInput{Amount: 10, Method: "credit"}); err == nil {
		t.Error("el crédito no es un abono")
	}
	if _, err := svc.AddOrderPayment(o.ID, AddPaymentInput{Amount: 150, Method: "yape"}); err == nil || !strings.Contains(err.Error(), "supera") {
		t.Errorf("un abono mayor al saldo debe rechazarse, err=%v", err)
	}
	future := time.Now().Add(10 * 24 * time.Hour)
	if _, err := svc.AddOrderPayment(o.ID, AddPaymentInput{Amount: 10, Method: "yape", PaidAt: &future}); err == nil {
		t.Error("una fecha futura debe rechazarse")
	}
	// Pedido cancelado: no admite abonos.
	svc.UpdateOrderStatus(o.ID, UpdateOrderStatusInput{NewStatus: OrderStatusCancelado, Notes: "x"})
	if _, err := svc.AddOrderPayment(o.ID, AddPaymentInput{Amount: 10, Method: "yape"}); err == nil {
		t.Error("un pedido cancelado no admite abonos")
	}
}

func TestAbonos_ValidarPagosBloqueaPreparar(t *testing.T) {
	db, svc := setupPaymentsDB(t)
	on := true
	svc.UpdateSettings(UpdateSettingsInput{RequirePaymentValidation: &on})
	o := orderOf(t, svc, 100, 1)

	p, err := svc.AddOrderPayment(o.ID, AddPaymentInput{Amount: 50, Method: "yape"})
	if err != nil {
		t.Fatal(err)
	}
	if p.Status != database.OrderPaymentPending {
		t.Fatalf("con «validar pagos» el abono nace PENDIENTE, quedó %s", p.Status)
	}
	_, sum, _ := svc.ListOrderPayments(o.ID)
	if sum.Paid != 0 || !sum.NeedsReview || sum.Pending != 50 {
		t.Errorf("un abono pendiente no cuenta como pagado: %+v", sum)
	}
	if err := svc.UpdateOrderStatus(o.ID, UpdateOrderStatusInput{NewStatus: OrderStatusConfirmado}); err != nil {
		t.Fatal(err)
	}
	err = svc.UpdateOrderStatus(o.ID, UpdateOrderStatusInput{NewStatus: OrderStatusEnPreparacion})
	if err == nil || !strings.Contains(err.Error(), "sin validar") {
		t.Fatalf("no se prepara con abonos por validar, err=%v", err)
	}

	if _, err := svc.ReviewOrderPayment(p.ID, ReviewPaymentInput{Approve: false}); err == nil {
		t.Error("observar exige motivo")
	}
	if _, err := svc.ReviewOrderPayment(p.ID, ReviewPaymentInput{Approve: false, Reason: "voucher borroso", UserID: 9}); err != nil {
		t.Fatal(err)
	}
	var obs database.TenantEcommerceOrderPayment
	db.First(&obs, p.ID)
	if obs.Status != database.OrderPaymentObserved || obs.ObservedReason != "voucher borroso" {
		t.Errorf("abono observado: %+v", obs)
	}
	if err := svc.UpdateOrderStatus(o.ID, UpdateOrderStatusInput{NewStatus: OrderStatusEnPreparacion}); err == nil {
		t.Error("un abono observado tampoco deja preparar")
	}
	if _, err := svc.ReviewOrderPayment(p.ID, ReviewPaymentInput{Approve: true, UserID: 9}); err != nil {
		t.Fatal(err)
	}
	_, sum, _ = svc.ListOrderPayments(o.ID)
	if sum.Paid != 50 || sum.NeedsReview {
		t.Errorf("validado: %+v", sum)
	}
	if err := svc.UpdateOrderStatus(o.ID, UpdateOrderStatusInput{NewStatus: OrderStatusEnPreparacion}); err != nil {
		t.Fatalf("con el abono validado ya se puede preparar: %v", err)
	}
}

func TestAbonos_PedidoSinAbonosSePuedePrepararAunqueSeValide(t *testing.T) {
	_, svc := setupPaymentsDB(t)
	on := true
	svc.UpdateSettings(UpdateSettingsInput{RequirePaymentValidation: &on})
	o := orderOf(t, svc, 100, 1)
	for _, to := range []string{OrderStatusConfirmado, OrderStatusEnPreparacion} {
		if err := svc.UpdateOrderStatus(o.ID, UpdateOrderStatusInput{NewStatus: to}); err != nil {
			t.Fatalf("un pedido sin abonos (paga al recoger) avanza normal: %v", err)
		}
	}
}

func TestAbonos_EliminarAntesDeLaVenta(t *testing.T) {
	_, svc := setupPaymentsDB(t)
	series := seedNotaVentaSeries(t, svc.db)
	o := orderOf(t, svc, 100, 1)
	p, _ := svc.AddOrderPayment(o.ID, AddPaymentInput{Amount: 100, Method: "yape"})
	if err := svc.DeleteOrderPayment(p.ID); err != nil {
		t.Fatal(err)
	}
	_, sum, _ := svc.ListOrderPayments(o.ID)
	if sum.Paid != 0 || sum.Status != PaymentStatusNone {
		t.Errorf("tras quitar el abono: %+v", sum)
	}
	p, _ = svc.AddOrderPayment(o.ID, AddPaymentInput{Amount: 100, Method: "yape"})
	svc.UpdateOrderStatus(o.ID, UpdateOrderStatusInput{NewStatus: OrderStatusConfirmado})
	_, input := convertServiceAndInput(svc.db, series.ID, 1)
	if _, err := svc.ConvertToSale(o.ID, input); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteOrderPayment(p.ID); err == nil {
		t.Error("tras convertir en venta el abono ya forma parte de sus pagos")
	}
	if _, err := svc.ReviewOrderPayment(p.ID, ReviewPaymentInput{Approve: false, Reason: "x"}); err == nil {
		t.Error("tampoco se puede observar tras la venta")
	}
}

// ── La venta toma los abonos ─────────────────────────────────────────

func TestConvertir_AbonosSonLosPagosYElSaldoQuedaACredito(t *testing.T) {
	db, svc := setupPaymentsDB(t)
	series := seedNotaVentaSeries(t, db)
	o := orderOf(t, svc, 100, 1) // total 100
	// Quien recoge: DNI → se crea el cliente para cobrarle el saldo.
	db.Model(&database.TenantEcommerceOrder{}).Where("id = ?", o.ID).Updates(map[string]interface{}{
		"recipient_name": "Walter Chambi", "recipient_doc_type": "1", "recipient_doc_number": "29330145",
	})
	svc.UpdateOrderStatus(o.ID, UpdateOrderStatusInput{NewStatus: OrderStatusConfirmado})
	svc.AddOrderPayment(o.ID, AddPaymentInput{Amount: 30, Method: "yape", Reference: "A1"})
	svc.AddOrderPayment(o.ID, AddPaymentInput{Amount: 25, Method: "yape", Reference: "A2"})
	svc.AddOrderPayment(o.ID, AddPaymentInput{Amount: 20, Method: "transfer", Reference: "T1"})

	_, input := convertServiceAndInput(db, series.ID, 1)
	sale, err := svc.ConvertToSale(o.ID, input)
	if err != nil {
		t.Fatalf("ConvertToSale con saldo: %v", err)
	}
	if sale.Status != "credit" {
		t.Errorf("con saldo pendiente la venta queda a crédito, status=%s", sale.Status)
	}
	var pays []database.TenantSalePayment
	db.Where("sale_id = ?", sale.ID).Order("amount DESC").Find(&pays)
	if len(pays) != 2 || pays[0].Method != "yape" || pays[0].Amount != 55 || pays[1].Method != "transfer" || pays[1].Amount != 20 {
		t.Errorf("los abonos validados son los pagos, agrupados por medio: %+v", pays)
	}
	var inst []database.TenantSaleCreditInstallment
	db.Where("sale_id = ?", sale.ID).Find(&inst)
	if len(inst) != 1 || inst[0].Amount != 25 {
		t.Errorf("el saldo de 25 queda en una cuota: %+v", inst)
	}
	if sale.ContactID == nil {
		t.Error("el saldo a crédito necesita un cliente: se crea con el documento de quien recoge")
	} else {
		var c database.TenantContact
		db.First(&c, *sale.ContactID)
		if c.DocNumber != "29330145" || c.BusinessName != "Walter Chambi" {
			t.Errorf("cliente creado: %+v", c)
		}
	}
	got, _ := svc.GetOrder(o.ID)
	if got.ConvertedSaleID == nil || *got.ConvertedSaleID != sale.ID {
		t.Error("el pedido debe quedar convertido")
	}
}

func TestConvertir_AbonosCompletosSeVendeAlContado(t *testing.T) {
	db, svc := setupPaymentsDB(t)
	series := seedNotaVentaSeries(t, db)
	o := orderOf(t, svc, 100, 1)
	svc.UpdateOrderStatus(o.ID, UpdateOrderStatusInput{NewStatus: OrderStatusConfirmado})
	svc.AddOrderPayment(o.ID, AddPaymentInput{Amount: 100, Method: "yape"})
	_, input := convertServiceAndInput(db, series.ID, 1)
	sale, err := svc.ConvertToSale(o.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	if sale.Status != "paid" {
		t.Errorf("pagado completo → contado, status=%s", sale.Status)
	}
}

func TestConvertir_ConAbonoSinValidarSeRechaza(t *testing.T) {
	db, svc := setupPaymentsDB(t)
	series := seedNotaVentaSeries(t, db)
	on := true
	svc.UpdateSettings(UpdateSettingsInput{RequirePaymentValidation: &on})
	o := orderOf(t, svc, 100, 1)
	svc.UpdateOrderStatus(o.ID, UpdateOrderStatusInput{NewStatus: OrderStatusConfirmado})
	svc.AddOrderPayment(o.ID, AddPaymentInput{Amount: 100, Method: "yape"})
	_, input := convertServiceAndInput(db, series.ID, 1)
	if _, err := svc.ConvertToSale(o.ID, input); err == nil || !strings.Contains(err.Error(), "sin validar") {
		t.Fatalf("no se emite la venta con abonos por validar, err=%v", err)
	}
}

func TestConvertir_SinAbonosConservaElComportamientoDeSiempre(t *testing.T) {
	db, svc := setupPaymentsDB(t)
	series := seedNotaVentaSeries(t, db)
	o := orderOf(t, svc, 100, 1)
	_, input := convertServiceAndInput(db, series.ID, 1)
	sale, err := svc.ConvertToSale(o.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	if sale.Status != "paid" {
		t.Errorf("sin abonos se asume cobrado completo (comportamiento previo), status=%s", sale.Status)
	}
}

func TestConvertir_PagaAlRecoger(t *testing.T) {
	db, svc := setupPaymentsDB(t)
	series := seedNotaVentaSeries(t, db)
	o := orderOf(t, svc, 100, 1)
	db.Model(&database.TenantEcommerceOrder{}).Where("id = ?", o.ID).Updates(map[string]interface{}{
		"recipient_name": "Rosa", "recipient_doc_type": "1", "recipient_doc_number": "45128803",
	})
	_, input := convertServiceAndInput(db, series.ID, 1)
	input.PayOnPickup = true
	sale, err := svc.ConvertToSale(o.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	if sale.Status != "credit" {
		t.Errorf("pagar al recoger deja todo a crédito, status=%s", sale.Status)
	}
	var n int64
	db.Model(&database.TenantSalePayment{}).Where("sale_id = ?", sale.ID).Count(&n)
	if n != 0 {
		t.Errorf("sin abonos no hay pagos directos, hay %d", n)
	}
}

// ── Despachar y emitir la venta ──────────────────────────────────────

func TestDespacharYEmitirVenta(t *testing.T) {
	db, svc := setupPaymentsDB(t)
	seedNotaVentaSeries(t, db)
	carrier := seedCarrier(t, svc, "Shalom", 10)
	p := seedConvertProduct(t, db, "EMIT1", 100)
	o, _, err := svc.CreateOrder(CreateOrderInput{
		CustomerName: "Walter", CustomerPhone: "951700442", DeliveryMethod: DeliveryMethodAgency,
		Items: []CreateOrderItemInput{{ProductID: p.ID, Quantity: 1}}, CarrierID: &carrier.ID,
		DestinationUbigeo: "080101", DestinationAgency: "Shalom Av. El Sol", RecipientDocNumber: "29330145",
	})
	if err != nil {
		t.Fatal(err)
	}
	branch := uint(1)
	svc.UpdateOrderStatus(o.ID, UpdateOrderStatusInput{NewStatus: OrderStatusConfirmado, BranchID: &branch})
	svc.AddOrderPayment(o.ID, AddPaymentInput{Amount: 40, Method: "yape"})
	for _, to := range []string{OrderStatusEnPreparacion, OrderStatusEmpaquetado, OrderStatusListoParaDespacho} {
		if err := svc.UpdateOrderStatus(o.ID, UpdateOrderStatusInput{NewStatus: to}); err != nil {
			t.Fatal(err)
		}
	}
	guia := "SH-1"
	res, err := svc.BatchDispatch([]BatchDispatchItem{{OrderID: o.ID, TrackingCode: &guia}}, 1, &EmitSaleInput{})
	if err != nil {
		t.Fatal(err)
	}
	if !res[0].OK || res[0].SaleID == nil || res[0].SaleError != "" {
		t.Fatalf("despachó y emitió la venta: %+v", res[0])
	}
	got, _ := svc.GetOrder(o.ID)
	if got.ConvertedSaleID == nil || got.Status != OrderStatusDespachado {
		t.Errorf("pedido despachado y convertido: status=%s sale=%v", got.Status, got.ConvertedSaleID)
	}
	var sale database.TenantSale
	db.First(&sale, *got.ConvertedSaleID)
	if sale.Status != "credit" || sale.Total != 100 {
		t.Errorf("saldo de 60 a crédito: status=%s total=%v", sale.Status, sale.Total)
	}
	var inst database.TenantSaleCreditInstallment
	db.Where("sale_id = ?", sale.ID).First(&inst)
	if inst.Amount != 60 {
		t.Errorf("cuota = %v, quería 60", inst.Amount)
	}
	// El plazo del saldo es el de la empresa de envío (10 días).
	if d := inst.DueDate.Sub(time.Now()); d < 9*24*time.Hour || d > 11*24*time.Hour {
		t.Errorf("vencimiento a %v, quería ~10 días", d)
	}
}

func TestDespacharYEmitirVenta_SiFallaLaVentaElDespachoQueda(t *testing.T) {
	db, svc := setupPaymentsDB(t)
	// sin serie de nota de venta en la sucursal
	carrier := seedCarrier(t, svc, "Shalom", 10)
	p := seedConvertProduct(t, db, "EMIT2", 100)
	o, _, _ := svc.CreateOrder(CreateOrderInput{
		CustomerName: "Walter", CustomerPhone: "951700442", DeliveryMethod: DeliveryMethodAgency,
		Items: []CreateOrderItemInput{{ProductID: p.ID, Quantity: 1}}, CarrierID: &carrier.ID,
		DestinationUbigeo: "080101", DestinationAgency: "Shalom", RecipientDocNumber: "29330145",
	})
	branch := uint(1)
	svc.UpdateOrderStatus(o.ID, UpdateOrderStatusInput{NewStatus: OrderStatusConfirmado, BranchID: &branch})
	for _, to := range []string{OrderStatusEnPreparacion, OrderStatusEmpaquetado, OrderStatusListoParaDespacho} {
		svc.UpdateOrderStatus(o.ID, UpdateOrderStatusInput{NewStatus: to})
	}
	guia := "SH-2"
	res, _ := svc.BatchDispatch([]BatchDispatchItem{{OrderID: o.ID, TrackingCode: &guia}}, 1, &EmitSaleInput{})
	if !res[0].OK || res[0].SaleID != nil || res[0].SaleError == "" {
		t.Fatalf("el despacho sale aunque la venta falle: %+v", res[0])
	}
	got, _ := svc.GetOrder(o.ID)
	if got.Status != OrderStatusDespachado || got.ConvertedSaleID != nil {
		t.Errorf("despachado y sin venta: status=%s sale=%v", got.Status, got.ConvertedSaleID)
	}
}

func TestAjustesDePedidos(t *testing.T) {
	_, svc := setupPaymentsDB(t)
	on := true
	target := "03"
	s, err := svc.UpdateSettings(UpdateSettingsInput{RequirePaymentValidation: &on, EmitSaleOnDispatch: &on, EmitSaleTarget: &target})
	if err != nil {
		t.Fatal(err)
	}
	if !s.RequirePaymentValidation || !s.EmitSaleOnDispatch || s.EmitSaleTarget != "03" {
		t.Errorf("ajustes: %+v", s)
	}
	bad := "99"
	if _, err := svc.UpdateSettings(UpdateSettingsInput{EmitSaleTarget: &bad}); err == nil {
		t.Error("comprobante inválido debe rechazarse")
	}
}
