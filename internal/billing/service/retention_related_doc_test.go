package service

import (
	"testing"
	"time"

	"tukifac/pkg/facturador"
)

func TestValidateRelatedDocument(t *testing.T) {
	em := "2026-11-13T20:10:00-05:00"
	if err := validateRelatedDocument(1, "01", "F001-00000123", "2026-11-10T00:00:00-05:00", em); err != nil {
		t.Fatalf("válido: %v", err)
	}
	if err := validateRelatedDocument(1, "01", "FE01-00006644-FACTURA", "2026-11-10T00:00:00-05:00", em); err == nil {
		t.Fatal("número mal formado debe fallar")
	}
	if err := validateRelatedDocument(1, "01", "F001-00000123", "2026-11-29T00:00:00-05:00", em); err == nil {
		t.Fatal("fecha posterior a la emisión debe fallar")
	}
}

func TestValidateFiscalEmissionNotFuture(t *testing.T) {
	if err := validateFiscalEmissionNotFuture(facturador.FormatFiscalDateTime(time.Now())); err != nil {
		t.Fatalf("hoy: %v", err)
	}
	if err := validateFiscalEmissionNotFuture(facturador.FormatFiscalDateTime(time.Now().AddDate(0, 0, 3))); err == nil {
		t.Fatal("fecha futura debe fallar")
	}
}
