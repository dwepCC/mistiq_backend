package service

import (
	"testing"

	"tukifac/pkg/database"
)

func TestComputeInvoiceSunatTotals_MixedGravadoAndBonificacion15(t *testing.T) {
	items := []database.TenantSaleItem{
		{IgvAffectationType: "10", Subtotal: 100, TaxAmount: 18, Total: 118},
		{IgvAffectationType: "15", Subtotal: 29.66, TaxAmount: 5.34, Total: 0},
	}
	tot := ComputeInvoiceSunatTotals(items, 118)
	if tot.MtoOperGravadas != 100 || tot.MtoIGV != 18 {
		t.Fatalf("gravado cobrable=%v/%v want 100/18", tot.MtoOperGravadas, tot.MtoIGV)
	}
	if tot.MtoOperGratuitas != 29.66 || tot.MtoIGVGratuitas != 5.34 {
		t.Fatalf("gratuitas=%v/%v want 29.66/5.34", tot.MtoOperGratuitas, tot.MtoIGVGratuitas)
	}
	if tot.ValorVenta != 100 {
		t.Fatalf("valorVenta=%v want 100 (sin gratuitas)", tot.ValorVenta)
	}
	if tot.MtoImpVenta != 118 {
		t.Fatalf("mtoImpVenta=%v want 118", tot.MtoImpVenta)
	}
	if tot.TotalImpuestos != 18 {
		t.Fatalf("totalImpuestos=%v want 18 (Sumatoria IGV sin IGV gratuito)", tot.TotalImpuestos)
	}
}
