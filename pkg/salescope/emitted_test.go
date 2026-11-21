package salescope

import "testing"


func emittedIDs(t *testing.T, sales ...testSale) []uint {
	t.Helper()
	db := openScopeTestDB(t)
	for i := range sales {
		db.Create(&sales[i])
	}
	var ids []uint
	if err := EmittedSales(db.Model(&testSale{})).Where("status != ?", "cancelled").Order("id").Pluck("id", &ids).Error; err != nil {
		t.Fatal(err)
	}
	return ids
}

func TestEmittedSales_ConvertedCountsOnlyTheComprobante(t *testing.T) {
	nv := uint(1)
	ids := emittedIDs(t,
		testSale{ID: 1, Total: 100, Status: "paid", SaleOrigin: SaleOriginDirect},
		testSale{ID: 2, Total: 100, Status: "paid", SaleOrigin: SaleOriginConvertedFromNota, IssuedFromNotaSaleID: &nv},
		testSale{ID: 3, Total: 50, Status: "paid", SaleOrigin: SaleOriginDirect},
	)
	if len(ids) != 2 || ids[0] != 2 || ids[1] != 3 {
		t.Fatalf("ids=%v want [2 3]", ids)
	}
}

func TestEmittedSales_CancelledChildReturnsParent(t *testing.T) {
	nv := uint(1)
	ids := emittedIDs(t,
		testSale{ID: 1, Total: 100, Status: "paid", SaleOrigin: SaleOriginDirect},
		testSale{ID: 2, Total: 100, Status: "cancelled", SaleOrigin: SaleOriginConvertedFromNota, IssuedFromNotaSaleID: &nv},
	)
	if len(ids) != 1 || ids[0] != 1 {
		t.Fatalf("ids=%v want [1]", ids)
	}
}

func TestEmittedSales_NoNotesExcludesCreditNotes(t *testing.T) {
	db := openScopeTestDB(t)
	db.Create(&testSale{ID: 1, DocType: "FACTURA", Total: 100, Status: "paid", SaleOrigin: SaleOriginDirect})
	db.Create(&testSale{ID: 2, DocType: "NOTA_CREDITO", Total: 100, Status: "paid", SaleOrigin: SaleOriginDirect})
	var n int64
	EmittedSalesNoNotes(db.Model(&testSale{})).Count(&n)
	if n != 1 {
		t.Fatalf("n=%d want 1", n)
	}
}
