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

// Caso del bug real: un ítem con afectación 40 (Exportación) no debe desaparecer de los
// totales. Sin IGV (tasa 0), pero sí forma parte del valor de venta (ValorVenta/LineExtensionAmount).
func TestComputeInvoiceSunatTotals_Exportacion(t *testing.T) {
	items := []database.TenantSaleItem{
		{IgvAffectationType: "40", Subtotal: 250, TaxAmount: 0, Total: 250},
	}
	tot := ComputeInvoiceSunatTotals(items, 250)
	if tot.MtoOperExportacion != 250 {
		t.Fatalf("mtoOperExportacion=%v want 250", tot.MtoOperExportacion)
	}
	if tot.MtoOperGravadas != 0 || tot.MtoOperExoneradas != 0 || tot.MtoOperInafectas != 0 {
		t.Fatalf("exportación no debe contaminar otros grupos: gravadas=%v exoneradas=%v inafectas=%v",
			tot.MtoOperGravadas, tot.MtoOperExoneradas, tot.MtoOperInafectas)
	}
	if tot.MtoIGV != 0 {
		t.Fatalf("mtoIGV=%v want 0 (exportación no tiene IGV)", tot.MtoIGV)
	}
	if tot.ValorVenta != 250 {
		t.Fatalf("valorVenta=%v want 250 (exportación SÍ forma parte del valor de venta)", tot.ValorVenta)
	}
	if tot.MtoImpVenta != 250 {
		t.Fatalf("mtoImpVenta=%v want 250", tot.MtoImpVenta)
	}
}

// Mezcla realista: gravado nacional + línea de exportación en el mismo comprobante. Cada grupo
// debe mantenerse independiente y el total general debe cuadrar la suma de todos.
func TestComputeInvoiceSunatTotals_MixedGravadoAndExportacion(t *testing.T) {
	items := []database.TenantSaleItem{
		{IgvAffectationType: "10", Subtotal: 100, TaxAmount: 18, Total: 118},
		{IgvAffectationType: "40", Subtotal: 300, TaxAmount: 0, Total: 300},
	}
	tot := ComputeInvoiceSunatTotals(items, 418)
	if tot.MtoOperGravadas != 100 || tot.MtoIGV != 18 {
		t.Fatalf("gravado=%v/%v want 100/18", tot.MtoOperGravadas, tot.MtoIGV)
	}
	if tot.MtoOperExportacion != 300 {
		t.Fatalf("mtoOperExportacion=%v want 300", tot.MtoOperExportacion)
	}
	if tot.ValorVenta != 400 {
		t.Fatalf("valorVenta=%v want 400 (100 gravado + 300 exportación)", tot.ValorVenta)
	}
	if tot.TotalImpuestos != 18 {
		t.Fatalf("totalImpuestos=%v want 18 (exportación no aporta IGV)", tot.TotalImpuestos)
	}
	if tot.MtoImpVenta != 418 {
		t.Fatalf("mtoImpVenta=%v want 418 (saleTotal explícito)", tot.MtoImpVenta)
	}
}

// Regresión explícita del bug reportado: antes de este fix, un ítem 40 no aparecía en NINGÚN
// total (ni gravadas, ni exoneradas, ni inafectas) y ValorVenta quedaba en 0 pese a existir venta.
func TestComputeInvoiceSunatTotals_Exportacion_NoDesaparece(t *testing.T) {
	items := []database.TenantSaleItem{
		{IgvAffectationType: "40", Subtotal: 500, TaxAmount: 0, Total: 500},
	}
	tot := ComputeInvoiceSunatTotals(items, 500)
	if tot.ValorVenta == 0 {
		t.Fatal("regresión del bug: el ítem de exportación desapareció de ValorVenta")
	}
}
