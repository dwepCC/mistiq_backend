package service

import "testing"

func TestReturn_cancelOpenRestoresShipment(t *testing.T) {
	f := newFixture(t)
	o := f.shipped(t, 2, 100) // despachado y ya en agencia
	r, err := f.s.CreateReturn(ReturnInput{OrderID: o.ID}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.CancelReturn(r.ID, "", 1); err == nil || !IsValidation(err) {
		t.Fatalf("sin motivo no se anula: %v", err)
	}
	c, err := f.s.CancelReturn(r.ID, "se registró por error", 1)
	if err != nil || c.Status != "anulado" || c.CancelledAt == nil || c.CancelReason == "" {
		t.Fatalf("anular retorno abierto: %v %+v", err, c)
	}
	if v, _ := f.s.GetOrder(o.ID); v.Shipment.Status != "en_agencia" {
		t.Fatalf("el envío vuelve a su estado previo: %s", v.Shipment.Status)
	}
	if f.stock(t, f.printer.ID) != 8 {
		t.Fatalf("anular un retorno no recibido no toca el stock: %d", f.stock(t, f.printer.ID))
	}
	if _, err := f.s.CancelReturn(r.ID, "otra vez", 1); err == nil {
		t.Error("no se anula dos veces")
	}
	if _, err := f.s.UpdateReturn(r.ID, ReturnUpdate{Status: "recibido"}, 1); err == nil {
		t.Error("un retorno anulado no se puede recibir")
	}
	// El pedido puede tener un retorno nuevo.
	if _, err := f.s.CreateReturn(ReturnInput{OrderID: o.ID}, 1); err != nil {
		t.Fatalf("tras anular se puede registrar otro retorno: %v", err)
	}
	open, _ := f.s.ListReturns("abiertos")
	if len(open) != 1 {
		t.Errorf("el anulado no figura entre los abiertos: %d", len(open))
	}
}

func TestReturn_cancelReceivedReversesStock(t *testing.T) {
	f := newFixture(t)
	o := f.shipped(t, 2, 100)
	r, _ := f.s.CreateReturn(ReturnInput{OrderID: o.ID}, 1)
	if _, err := f.s.UpdateReturn(r.ID, ReturnUpdate{Status: "recibido"}, 1); err != nil {
		t.Fatal(err)
	}
	if f.stock(t, f.printer.ID) != 10 {
		t.Fatalf("precondición: reingresó %d", f.stock(t, f.printer.ID))
	}
	if _, err := f.s.CancelReturn(r.ID, "no llegó nada", 1); err != nil {
		t.Fatal(err)
	}
	if f.stock(t, f.printer.ID) != 8 {
		t.Fatalf("anular un retorno recibido descuenta el reingreso: %d", f.stock(t, f.printer.ID))
	}
}

func TestReturn_cancelBlockedAfterReship(t *testing.T) {
	f := newFixture(t)
	o := f.shipped(t, 2, 100)
	r, _ := f.s.CreateReturn(ReturnInput{OrderID: o.ID}, 1)
	if _, err := f.s.UpdateReturn(r.ID, ReturnUpdate{Status: "recibido"}, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Reship(r.ID, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.CancelReturn(r.ID, "tarde", 1); err == nil || !IsValidation(err) {
		t.Fatalf("un retorno ya reenviado no se anula: %v", err)
	}
	if f.stock(t, f.printer.ID) != 8 {
		t.Fatalf("el stock no cambia: %d", f.stock(t, f.printer.ID))
	}
}
