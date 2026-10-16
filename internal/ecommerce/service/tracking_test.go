package service

import (
	"testing"
	"time"
)

func TestSeguimientoPublico_ExigeNumeroYCelular(t *testing.T) {
	db := setupShippingTestDB(t)
	svc := &EcommerceService{db: db}
	carrier := seedCarrier(t, svc, "Shalom", 15)
	order := readyAgencyOrder(t, svc, carrier.ID, "TK1")
	phone := "951700442"

	for _, c := range []struct{ ref, phone string }{
		{"", phone}, {"PW-" + "99999", phone}, {orderNumber(order.ID), "999000111"}, {orderNumber(order.ID), ""}, {"abc", phone},
	} {
		if _, err := svc.TrackOrder(c.ref, c.phone); err != ErrTrackNotFound {
			t.Errorf("(%q,%q) debe dar el mismo «no encontrado» siempre, err=%v", c.ref, c.phone, err)
		}
	}
	for _, ref := range []string{orderNumber(order.ID), "pw-" + orderNumber(order.ID)[3:], orderNumber(order.ID)[3:], "#" + orderNumber(order.ID)[3:]} {
		v, err := svc.TrackOrder(ref, "+51 951 700 442")
		if err != nil || v == nil {
			t.Fatalf("ref %q con celular con formato libre debe funcionar: %v", ref, err)
		}
	}
}

func TestSeguimientoPublico_RecorridoYAgencia(t *testing.T) {
	db := setupShippingTestDB(t)
	svc := &EcommerceService{db: db}
	carrier := seedCarrier(t, svc, "Olva", 10)
	url := "https://rastrea.ejemplo.pe/?guia={guia}"
	svc.UpdateCarrier(carrier.ID, CarrierInput{TrackingURL: &url})
	order := readyAgencyOrder(t, svc, carrier.ID, "TK2")

	v, _ := svc.TrackOrder(orderNumber(order.ID), "951700442")
	if v.Stage != StageListo || v.TrackingCode != "" {
		t.Errorf("listo para despachar, sin guía: %+v", v)
	}

	guia := "OL 123"
	d, err := svc.CreateDispatch(order.ID, CreateDispatchInput{TrackingCode: &guia})
	if err != nil {
		t.Fatal(err)
	}
	v, _ = svc.TrackOrder(orderNumber(order.ID), "951700442")
	if v.Stage != StageEnCamino || v.TrackingCode != "OL 123" || v.TrackingURL != "https://rastrea.ejemplo.pe/?guia=OL+123" {
		t.Errorf("en camino con guía y enlace: %+v", v)
	}

	arrived := time.Now().Add(-2 * time.Hour)
	if _, err := svc.MarkDispatchInAgency(d.ID, AgencyArrivalInput{ArrivedAt: &arrived}); err != nil {
		t.Fatal(err)
	}
	v, _ = svc.TrackOrder(orderNumber(order.ID), "951700442")
	if v.Stage != StageEnAgencia || v.PickupDaysLeft == nil || *v.PickupDaysLeft != 10 {
		t.Errorf("en agencia con 10 días: %+v", v)
	}
	if v.AgencyName == "" || v.Headline == "" || v.CarrierName != "Olva" {
		t.Errorf("datos de la agencia y mensaje: %+v", v)
	}
	keys := map[string]bool{}
	for _, e := range v.Events {
		keys[e.Key] = true
	}
	for _, k := range []string{"recibido", "confirmado", "preparando", "empaquetado", "despachado", "agencia"} {
		if !keys[k] {
			t.Errorf("falta el hito %q en el recorrido: %+v", k, v.Events)
		}
	}
	if len(v.Items) != 1 {
		t.Errorf("productos del pedido: %+v", v.Items)
	}
}
