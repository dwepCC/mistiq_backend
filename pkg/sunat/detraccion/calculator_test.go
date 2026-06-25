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
