package detraccion

import (
	"testing"

	"tukifac/pkg/salecurrency"
)

func TestEvaluateDetraccion1001(t *testing.T) {
	cat, err := DefaultCatalog()
	if err != nil {
		t.Fatal(err)
	}
	res, err := Evaluate(cat, CalcInput{
		OperationTypeCode: salecurrency.OpDetraccion,
		SunatDocCode:      "01",
		Currency:          salecurrency.CurrencyPEN,
		GravadoTotalPEN:   1180,
		SaleTotalPEN:      1180,
		GoodCode:          "014",
		BankAccount:       "0004-1234567890",
		PaymentMethodCode: "001",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Applicable {
		t.Fatalf("expected applicable: %s", res.Reason)
	}
	if res.RatePercent != 4 {
		t.Fatalf("expected 4%%, got %v", res.RatePercent)
	}
	if res.DetractionAmountPEN != 47.2 {
		t.Fatalf("expected 47.20, got %v", res.DetractionAmountPEN)
	}
	if res.NetPayablePEN != 1132.8 {
		t.Fatalf("expected net 1132.80, got %v", res.NetPayablePEN)
	}
}

func TestEvaluateRejectsBoleta(t *testing.T) {
	cat, _ := DefaultCatalog()
	_, err := Evaluate(cat, CalcInput{
		OperationTypeCode: salecurrency.OpDetraccion,
		SunatDocCode:      "03",
		Currency:          salecurrency.CurrencyPEN,
		GravadoTotalPEN:   1000,
		SaleTotalPEN:      1000,
		GoodCode:          "014",
		BankAccount:       "0004-123",
		PaymentMethodCode: "001",
	})
	if err == nil {
		t.Fatal("expected error for boleta")
	}
}

func TestEvaluateRejectsTransportGood(t *testing.T) {
	cat, _ := DefaultCatalog()
	_, err := Evaluate(cat, CalcInput{
		OperationTypeCode: salecurrency.OpDetraccion,
		SunatDocCode:      "01",
		Currency:          salecurrency.CurrencyPEN,
		GravadoTotalPEN:   1000,
		SaleTotalPEN:      1000,
		GoodCode:          "027",
		BankAccount:       "0004-123",
		PaymentMethodCode: "001",
	})
	if err == nil {
		t.Fatal("expected error for transport code 027")
	}
}

func TestEvaluateThreshold(t *testing.T) {
	cat, _ := DefaultCatalog()
	_, err := Evaluate(cat, CalcInput{
		OperationTypeCode: salecurrency.OpDetraccion,
		SunatDocCode:      "01",
		Currency:          salecurrency.CurrencyPEN,
		GravadoTotalPEN:   500,
		SaleTotalPEN:      500,
		GoodCode:          "014",
		BankAccount:       "0004-123",
		PaymentMethodCode: "001",
	})
	if err == nil {
		t.Fatal("expected threshold error")
	}
}

// Caso oficial de SUNAT (guía cpe.sunat.gob.pe): TRANSPORTE SIEMPRE RAPIDO S.A.C., ruta Lima -
// Casma, valor del servicio S/2,000 (incluido IGV), valor referencial S/1,232.28 (menor). La
// detracción usa el MAYOR de los dos — Resolución 073-2006-SUNAT Art. 4 — así que sale sobre el
// importe de la operación: 2000 × 4% = 80.
func TestEvaluateDetraccion1004_CasoOficialSUNAT(t *testing.T) {
	cat, err := DefaultCatalog()
	if err != nil {
		t.Fatal(err)
	}
	res, err := Evaluate(cat, CalcInput{
		OperationTypeCode:   salecurrency.OpDetraccionTransporte,
		SunatDocCode:        "01",
		Currency:            salecurrency.CurrencyPEN,
		SaleTotalPEN:        2000,
		GoodCode:            "027",
		BankAccount:         "0004-1234567890",
		PaymentMethodCode:   "001",
		ValorReferencialPEN: 1232.28,
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !res.Applicable {
		t.Fatalf("expected applicable: %s", res.Reason)
	}
	if res.RatePercent != 4 {
		t.Fatalf("expected 4%%, got %v", res.RatePercent)
	}
	if res.DetractionAmountPEN != 80 {
		t.Fatalf("expected detracción 80.00 (caso oficial SUNAT), got %v", res.DetractionAmountPEN)
	}
	if res.NetPayablePEN != 1920 {
		t.Fatalf("expected net 1920.00, got %v", res.NetPayablePEN)
	}
}

// Cuando el valor referencial supera el importe de la operación, la base es el valor referencial
// (el mayor de los dos, no siempre el importe de la operación).
func TestEvaluateDetraccion1004_ValorReferencialMayor(t *testing.T) {
	cat, _ := DefaultCatalog()
	res, err := Evaluate(cat, CalcInput{
		OperationTypeCode:   salecurrency.OpDetraccionTransporte,
		SunatDocCode:        "01",
		Currency:            salecurrency.CurrencyPEN,
		SaleTotalPEN:        1000,
		GoodCode:            "027",
		BankAccount:         "0004-123",
		PaymentMethodCode:   "001",
		ValorReferencialPEN: 1500,
	})
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if res.DetractionAmountPEN != 60 {
		t.Fatalf("expected detracción sobre 1500 (valor referencial, el mayor): 1500*4%%=60, got %v", res.DetractionAmountPEN)
	}
}

// Umbral de 1004 es S/400 (min_amount_pen del código 027), no S/700 (el de 1001) — mismo
// mecanismo (good.MinAmountPEN), sin necesidad de una constante de umbral separada.
func TestEvaluateDetraccion1004_Umbral400(t *testing.T) {
	cat, _ := DefaultCatalog()
	// Por debajo de 400: rechazada.
	if _, err := Evaluate(cat, CalcInput{
		OperationTypeCode: salecurrency.OpDetraccionTransporte,
		SunatDocCode:      "01",
		Currency:          salecurrency.CurrencyPEN,
		SaleTotalPEN:      350,
		GoodCode:          "027",
		BankAccount:       "0004-123",
		PaymentMethodCode: "001",
	}); err == nil {
		t.Fatal("expected threshold error bajo S/400")
	}
	// Sobre 400 (pero bajo el umbral de 1001, S/700): debe aplicarse igual, porque el umbral de
	// transporte es 400, no 700.
	res, err := Evaluate(cat, CalcInput{
		OperationTypeCode: salecurrency.OpDetraccionTransporte,
		SunatDocCode:      "01",
		Currency:          salecurrency.CurrencyPEN,
		SaleTotalPEN:      500,
		GoodCode:          "027",
		BankAccount:       "0004-123",
		PaymentMethodCode: "001",
	})
	if err != nil {
		t.Fatalf("S/500 debe superar el umbral de transporte (400): %v", err)
	}
	if !res.Applicable {
		t.Fatal("esperaba detracción aplicable sobre S/500 en transporte de carga")
	}
}

// 1004 exige exclusivamente el código 027 — cualquier otro código sujeto de detracción general
// debe rechazarse (evita declarar un tipo de operación que no corresponde al bien facturado).
func TestEvaluateDetraccion1004_RechazaCodigoDistintoDe027(t *testing.T) {
	cat, _ := DefaultCatalog()
	_, err := Evaluate(cat, CalcInput{
		OperationTypeCode: salecurrency.OpDetraccionTransporte,
		SunatDocCode:      "01",
		Currency:          salecurrency.CurrencyPEN,
		SaleTotalPEN:      1000,
		GoodCode:          "014",
		BankAccount:       "0004-123",
		PaymentMethodCode: "001",
	})
	if err == nil {
		t.Fatal("esperaba error: 1004 exige el código 027")
	}
}

func TestListGoodsExcludesTransport(t *testing.T) {
	cat, err := DefaultCatalog()
	if err != nil {
		t.Fatal(err)
	}
	goods := cat.ListGoods(true)
	for _, g := range goods {
		if g.Code == "027" {
			t.Fatal("027 should be excluded when exclude_transport")
		}
	}
}
