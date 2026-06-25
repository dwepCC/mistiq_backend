package salescope

import (
	"testing"

	"tukifac/pkg/database"
)

func TestDomainHelpers(t *testing.T) {
	nvID := uint(10)
	direct := &database.TenantSale{SaleOrigin: SaleOriginDirect, DocType: "NV", Total: 100}
	converted := &database.TenantSale{SaleOrigin: SaleOriginConvertedFromNota, IssuedFromNotaSaleID: &nvID, DocType: "BOLETA", Total: 100}
	fiscal := &database.TenantSale{SaleOrigin: SaleOriginDirect, DocType: "FACTURA"}
	legacy := &database.TenantSale{SaleOrigin: SaleOriginLegacy}

	if !IsCommercial(direct) || IsCommercial(converted) {
		t.Fatal("IsCommercial mismatch")
	}
	if !IsConverted(converted) || IsConverted(direct) {
		t.Fatal("IsConverted mismatch")
	}
	if !IsDirect(direct) || IsDirect(converted) {
		t.Fatal("IsDirect mismatch")
	}
	if !IsFiscal(fiscal) || IsFiscal(direct) {
		t.Fatal("IsFiscal mismatch")
	}
	if !IsLegacy(legacy) || IsLegacy(direct) {
		t.Fatal("IsLegacy mismatch")
	}
}
