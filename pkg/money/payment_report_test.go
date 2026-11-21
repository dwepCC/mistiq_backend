package money

import "testing"

func TestAllocateSalePaymentReportAmounts_singleCashWithChange(t *testing.T) {
	got := AllocateSalePaymentReportAmounts(12, []SalePaymentLine{{ID: 1, Amount: 20, IsCash: true}})
	if got[1] != 12 {
		t.Fatalf("expected 12, got %v", got[1])
	}
}

func TestAllocateSalePaymentReportAmounts_exactPayment(t *testing.T) {
	got := AllocateSalePaymentReportAmounts(12, []SalePaymentLine{
		{ID: 1, Amount: 7, IsCash: true},
		{ID: 2, Amount: 5},
	})
	if got[1] != 7 || got[2] != 5 {
		t.Fatalf("expected 7 and 5, got %v", got)
	}
}

// El vuelto SOLO sale de efectivo: un pago mixto con vuelto nunca debe reducir la línea
// electrónica (Yape/Plin/tarjeta) — regresión del incidente reportado 2026-09-25 donde el
// prorrateo proporcional anterior repartía el vuelto entre todos los métodos por igual.
func TestAllocateSalePaymentReportAmounts_mixedOverpay_changeOnlyFromCash(t *testing.T) {
	got := AllocateSalePaymentReportAmounts(120, []SalePaymentLine{
		{ID: 1, Amount: 100, IsCash: true},
		{ID: 2, Amount: 50, IsCash: false},
	})
	if got[1] != 70 {
		t.Fatalf("efectivo: expected 70 (100 - 30 de vuelto), got %v", got[1])
	}
	if got[2] != 50 {
		t.Fatalf("yape: expected 50 (intacto, el vuelto nunca lo toca), got %v", got[2])
	}
}

func TestAllocateSalePaymentReportAmounts_multipleCashLinesShareChangeProportionally(t *testing.T) {
	got := AllocateSalePaymentReportAmounts(50, []SalePaymentLine{
		{ID: 1, Amount: 60, IsCash: true},
		{ID: 2, Amount: 40, IsCash: true},
		{ID: 3, Amount: 20, IsCash: false},
	})
	// Vuelto total = 120 - 50 = 70; sale de las 2 líneas de efectivo (suma 100) -> reportan 30 en
	// total, repartido proporcionalmente (60% / 40%): 18 y 12. Yape queda intacto en 20.
	if got[1] != 18 {
		t.Fatalf("efectivo linea 1: expected 18, got %v", got[1])
	}
	if got[2] != 12 {
		t.Fatalf("efectivo linea 2: expected 12, got %v", got[2])
	}
	if got[3] != 20 {
		t.Fatalf("yape: expected 20 (intacto), got %v", got[3])
	}
}

// Dato histórico anómalo (anterior a la validación que ahora bloquea esto en SaleService.Create):
// vuelto sin ninguna línea de efectivo. No hay de dónde restarlo -> las líneas no-efectivo
// conservan su monto íntegro (nunca se reduce un método electrónico).
func TestAllocateSalePaymentReportAmounts_noCashLine_nonCashKeepsRawAmount(t *testing.T) {
	got := AllocateSalePaymentReportAmounts(50, []SalePaymentLine{
		{ID: 1, Amount: 80, IsCash: false},
	})
	if got[1] != 80 {
		t.Fatalf("expected 80 (sin efectivo no hay de donde restar el vuelto), got %v", got[1])
	}
}

// Dato histórico anómalo: el vuelto supera el efectivo disponible. El efectivo se acota a 0 en
// vez de reportar un monto negativo; no se reparte el excedente a las líneas no-efectivo.
func TestAllocateSalePaymentReportAmounts_changeExceedsCash_clampsToZero(t *testing.T) {
	got := AllocateSalePaymentReportAmounts(0.01, []SalePaymentLine{
		{ID: 1, Amount: 0, IsCash: true},
		{ID: 2, Amount: 2588, IsCash: false},
	})
	if got[1] != 0 {
		t.Fatalf("efectivo: expected 0 (nunca negativo), got %v", got[1])
	}
	if got[2] != 2588 {
		t.Fatalf("tarjeta: expected 2588 (intacto), got %v", got[2])
	}
}

func TestAllocateSalePaymentNetAmounts_indexes(t *testing.T) {
	got := AllocateSalePaymentNetAmounts(12, []SalePaymentLine{{Amount: 20, IsCash: true}})
	if len(got) != 1 || got[0] != 12 {
		t.Fatalf("expected [12], got %v", got)
	}
}

// Mismo escenario que el test anterior pero con un método no-efectivo: no se acota al total,
// conserva el monto bruto (bloqueado en la práctica por la validación de SaleService.Create,
// pero esta función por sí sola nunca debe inventar un vuelto electrónico).
func TestAllocateSalePaymentNetAmounts_nonCashKeepsRawAmount(t *testing.T) {
	got := AllocateSalePaymentNetAmounts(12, []SalePaymentLine{{Amount: 20, IsCash: false}})
	if len(got) != 1 || got[0] != 20 {
		t.Fatalf("expected [20], got %v", got)
	}
}
