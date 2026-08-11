package service

import (
	"testing"

	"tukifac/pkg/facturador"
)

func TestValidateRegimenTasaRetention(t *testing.T) {
	if err := validateRegimenTasa("01", 3.0, retentionRegimenTasa, "retención"); err != nil {
		t.Fatalf("expected ok: %v", err)
	}
	if err := validateRegimenTasa("01", 6.0, retentionRegimenTasa, "retención"); err == nil {
		t.Fatal("expected tasa mismatch error")
	}
}

func TestValidateRegimenTasaPerception(t *testing.T) {
	if err := validateRegimenTasa("02", 1.0, perceptionRegimenTasa, "percepción"); err != nil {
		t.Fatalf("expected ok: %v", err)
	}
	if err := validateRegimenTasa("01", 1.0, perceptionRegimenTasa, "percepción"); err == nil {
		t.Fatal("expected tasa mismatch error")
	}
}

func TestValidateReversionDetail(t *testing.T) {
	if err := validateReversionDetail("20", "R001", "5", "Error material"); err != nil {
		t.Fatalf("retention reversion: %v", err)
	}
	if err := validateReversionDetail("40", "P001", "10", "Duplicado"); err != nil {
		t.Fatalf("perception reversion: %v", err)
	}
	if err := validateReversionDetail("20", "P001", "1", "x"); err == nil {
		t.Fatal("expected serie mismatch for tipo 20")
	}
}

func TestMapPaymentsInput(t *testing.T) {
	pagos, err := mapPaymentsInput([]retentionPaymentInput{
		{Moneda: "PEN", Importe: 1000, Fecha: "2026-03-08"},
	}, "PEN", "2026-03-08T12:00:00-05:00")
	if err != nil {
		t.Fatal(err)
	}
	if len(pagos) != 1 || pagos[0].Importe != 1000 {
		t.Fatalf("unexpected pagos: %+v", pagos)
	}
}

func TestValidateRetentionTotals(t *testing.T) {
	details := []facturador.RetentionDetail{
		{ImpRetenido: 30, ImpPagar: 970},
	}
	if err := validateRetentionTotals(details, 30, 970); err != nil {
		t.Fatal(err)
	}
	if err := validateRetentionTotals(details, 31, 970); err == nil {
		t.Fatal("expected total mismatch")
	}
}
