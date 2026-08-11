package prepayment

import (
	"testing"

	"tukifac/pkg/database"
	"tukifac/pkg/facturador"
	sunatpre "tukifac/pkg/sunat/prepayment"
)

func TestApplyEmitToInvoicePayload(t *testing.T) {
	sunatpre.SetEmitOperationTypeForTest(sunatpre.OpVentaInternaAnticipos)
	t.Cleanup(func() { sunatpre.SetEmitOperationTypeForTest(sunatpre.OpVentaInternaAnticipos) })

	payload := &facturador.InvoicePayload{TipoOperacion: "0101"}
	voucher := &database.TenantSalePrepaymentVoucher{SaleID: 1}
	ApplyEmitToInvoicePayload(payload, voucher)
	if payload.TipoOperacion != sunatpre.EmitOperationTypeCode() {
		t.Fatalf("tipoOperacion: got %q want %q", payload.TipoOperacion, sunatpre.EmitOperationTypeCode())
	}
	if payload.Parameters == nil || len(payload.Parameters.User.Extras) == 0 {
		t.Fatal("expected pdf parameters extras")
	}
}
