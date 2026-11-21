package service

import "testing"

func TestNormalizePaymentMethods(t *testing.T) {
	out, err := NormalizePaymentMethods([]PaymentMethod{{Type: " Yape ", Label: "  Yape ", Enabled: true, Account: " 999111222 "}})
	if err != nil || len(out) != 1 || out[0].Type != "yape" || out[0].Label != "Yape" || out[0].Account != "999111222" {
		t.Fatalf("normalización incorrecta: %+v err=%v", out, err)
	}
	if _, err := NormalizePaymentMethods([]PaymentMethod{{Type: "bitcoin"}}); err == nil {
		t.Fatal("un tipo desconocido debe rechazarse")
	}
	if _, err := NormalizePaymentMethods(make([]PaymentMethod, 11)); err == nil {
		t.Fatal("más de 10 medios debe rechazarse")
	}
}

func TestEnabledPaymentMethods_OnlyEnabledAndTolerantToBadJSON(t *testing.T) {
	raw := `[{"type":"yape","enabled":true,"account":"1"},{"type":"plin","enabled":false}]`
	if got := EnabledPaymentMethods(raw); len(got) != 1 || got[0].Type != "yape" {
		t.Fatalf("got %+v", got)
	}
	if got := EnabledPaymentMethods("{no es json"); got == nil || len(got) != 0 {
		t.Fatalf("JSON corrupto debe dar lista vacía, got %+v", got)
	}
}
