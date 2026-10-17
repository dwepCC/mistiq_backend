package service

import (
	"strings"
	"testing"

	"tukifac/pkg/database"
)

func TestVoucherCliente_NaceSiemprePendienteYAvisa(t *testing.T) {
	db, svc := setupPaymentsDB(t)
	o := orderOf(t, svc, 100, 1)
	db.Where("1 = 1").Delete(&database.TenantNotification{})

	// El ajuste «validar pagos» está apagado, pero lo que informa el cliente igual espera revisión.
	p, err := svc.AddOrderPayment(o.ID, AddPaymentInput{Amount: 30, Method: "yape", VoucherURL: "/uploads/x/voucher-1.png", ByCustomer: true})
	if err != nil {
		t.Fatal(err)
	}
	if p.Status != database.OrderPaymentPending || p.ReviewedBy != nil || p.Note == "" {
		t.Errorf("abono del cliente: %+v", p)
	}
	_, sum, _ := svc.ListOrderPayments(o.ID)
	if sum.Paid != 0 || sum.Pending != 30 {
		t.Errorf("sin validar no cuenta como pagado: %+v", sum)
	}
	var notes []database.TenantNotification
	db.Find(&notes)
	if len(notes) != 1 || notes[0].Type != "ecommerce.order.payment_submitted" || !strings.Contains(notes[0].LinkPath, "id=") {
		t.Errorf("debe avisar al negocio: %+v", notes)
	}
	// Un abono del personal con el ajuste apagado sí nace validado.
	staff, err := svc.AddOrderPayment(o.ID, AddPaymentInput{Amount: 10, Method: "yape", UserID: 2})
	if err != nil || staff.Status != database.OrderPaymentValidated {
		t.Errorf("el abono del personal nace validado: %+v %v", staff, err)
	}
}

func TestVoucherCliente_TopeDeEnviosPendientes(t *testing.T) {
	_, svc := setupPaymentsDB(t)
	o := orderOf(t, svc, 100, 1)
	for i := 0; i < maxCustomerPendingPayments; i++ {
		if _, err := svc.AddOrderPayment(o.ID, AddPaymentInput{Amount: 5, Method: "yape", ByCustomer: true}); err != nil {
			t.Fatalf("envío %d: %v", i+1, err)
		}
	}
	if _, err := svc.AddOrderPayment(o.ID, AddPaymentInput{Amount: 5, Method: "yape", ByCustomer: true}); err == nil {
		t.Fatal("pasado el tope debe rechazarse")
	}
}

func TestVoucherCliente_SeguimientoOfreceEnviarYValidaIdentidad(t *testing.T) {
	_, svc := setupPaymentsDB(t)
	o := orderOf(t, svc, 100, 1)

	if _, err := svc.AuthorizeTrack("PW-"+itoa(o.ID), "999000111"); err == nil {
		t.Fatal("con otro celular no debe autorizar")
	}
	id, err := svc.AuthorizeTrack("PW-"+itoa(o.ID), "951700442")
	if err != nil || id != o.ID {
		t.Fatalf("autorización: %d %v", id, err)
	}
	v, err := svc.TrackOrder("PW-"+itoa(o.ID), "951700442")
	if err != nil {
		t.Fatal(err)
	}
	if !v.CanSendPayment || len(v.PaymentMethods) < 2 {
		t.Errorf("con saldo debe poder enviar su pago: %+v", v)
	}
	// Con todo cubierto (aun sin validar) ya no hay nada que enviar.
	if _, err := svc.AddOrderPayment(o.ID, AddPaymentInput{Amount: 100, Method: "yape", ByCustomer: true}); err != nil {
		t.Fatal(err)
	}
	v, _ = svc.TrackOrder("PW-"+itoa(o.ID), "951700442")
	if v.CanSendPayment || v.PendingPayments != 100 {
		t.Errorf("tras enviar todo: can=%v pending=%v", v.CanSendPayment, v.PendingPayments)
	}
}

func TestVoucher_CambiarElDeUnAbono(t *testing.T) {
	_, svc := setupPaymentsDB(t)
	o := orderOf(t, svc, 100, 1)
	p, _ := svc.AddOrderPayment(o.ID, AddPaymentInput{Amount: 10, Method: "yape"})
	got, err := svc.SetPaymentVoucher(p.ID, " /uploads/x/voucher-9.png ")
	if err != nil || got.VoucherURL != "/uploads/x/voucher-9.png" {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err := svc.SetPaymentVoucher(9999, "x"); err == nil {
		t.Fatal("abono inexistente debe fallar")
	}
}
