package prepayment

import "testing"

func TestEmitOperationTypeCode_default(t *testing.T) {
	SetEmitOperationTypeForTest(OpVentaInterna)
	if got := EmitOperationTypeCode(); got != OpVentaInterna {
		t.Fatalf("expected %s, got %s", OpVentaInterna, got)
	}
}

func TestEmitOperationTypeCode_switch0101(t *testing.T) {
	SetEmitOperationTypeForTest(OpVentaInterna)
	t.Cleanup(func() { SetEmitOperationTypeForTest(OpVentaInterna) })
	if got := EmitOperationTypeCode(); got != OpVentaInterna {
		t.Fatalf("expected %s, got %s", OpVentaInterna, got)
	}
	if !IsEmitOperationType(OpVentaInterna) {
		t.Fatal("0101 should be emit operation when configured")
	}
	if IsEmitOperationType(OpVentaInternaAnticipos) {
		t.Fatal("0104 should not match when configured as 0101")
	}
}

func TestNormalizeEmitOperationType(t *testing.T) {
	if _, err := NormalizeEmitOperationType("9999"); err == nil {
		t.Fatal("expected error for invalid code")
	}
	if code, err := NormalizeEmitOperationType(" 0104 "); err != nil || code != OpVentaInternaAnticipos {
		t.Fatalf("normalize 0104: code=%q err=%v", code, err)
	}
}
