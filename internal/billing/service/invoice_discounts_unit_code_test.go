package service

import (
	"testing"

	"tukifac/pkg/database"
)

// Casos 4/5 de la corrección "Unidad Comercial Fiscal": el detalle fiscal (InvoiceDetail, lo que
// cruza hacia facturador_lycet/Greenter — ver pkg/facturador/client.go) toma Unidad directamente
// de TenantSaleItem.Unit (BuildInvoiceDetailsFromSaleItems, este archivo, línea ~97) — confirmado
// en la auditoría FRONTEND_PHASE_7E1_UNIT_PROPAGATION_AUDIT.md que es un passthrough puro hasta el
// XML (facturador_lycet no transforma nada). Por eso basta con probar que, una vez
// TenantSaleItem.Unit ya viene correcto (ver internal/sales/service/sale_unit_unit_code_test.go),
// el detalle fiscal lo conserva tal cual — sin necesitar un test de integración contra
// facturador_lycet (otro lenguaje/servicio) para demostrarlo.

// Caso 5: venta por Unidad (sin SaleUnit) → detalle fiscal quantity=1, unitCode=NIU.
func TestBuildInvoiceDetails_UnitSale_UsesBaseUnitCode(t *testing.T) {
	items := []database.TenantSaleItem{{
		Code: "TEC-1", Description: "Teclado", Unit: "NIU", Quantity: 1,
		IgvAffectationType: "10", TaxRate: 18,
		Subtotal: 3.39, TaxAmount: 0.61, Total: 4,
	}}
	details, err := BuildInvoiceDetailsFromSaleItems(items, 18, testNormUnit)
	if err != nil {
		t.Fatal(err)
	}
	d := details[0]
	if d.Unidad != "NIU" {
		t.Errorf("Unidad = %q, want NIU", d.Unidad)
	}
	if d.Cantidad != 1 {
		t.Errorf("Cantidad = %v, want 1", d.Cantidad)
	}
}

// Caso 4: venta por Caja (SaleUnit, factor 12) → detalle fiscal quantity=1, unitCode=BX. La
// cantidad fiscal NUNCA se convierte a base (12) — sigue siendo la comercial (1), exactamente
// como ya viene en TenantSaleItem.Quantity desde que se creó la venta (Fase 7E/Fase 2).
func TestBuildInvoiceDetails_SaleUnitSale_UsesSaleUnitCode_NeverMultipliesQuantity(t *testing.T) {
	suid := uint(45)
	items := []database.TenantSaleItem{{
		Code: "TEC-1", Description: "Teclado", Unit: "BX", Quantity: 1, SaleUnitID: &suid,
		IgvAffectationType: "10", TaxRate: 18,
		Subtotal: 38.14, TaxAmount: 6.86, Total: 45,
	}}
	details, err := BuildInvoiceDetailsFromSaleItems(items, 18, testNormUnit)
	if err != nil {
		t.Fatal(err)
	}
	d := details[0]
	if d.Unidad != "BX" {
		t.Errorf("Unidad = %q, want BX", d.Unidad)
	}
	if d.Cantidad != 1 {
		t.Errorf("Cantidad = %v, want 1 (comercial, nunca 12 = 1×factor)", d.Cantidad)
	}
}

// Venta por gramo (SaleUnit GRM, factor 0.001): el detalle fiscal declara 250 GRM —nunca 250 KGM
// ni 0.25 KGM sin avisar— y el valor/precio unitario conserva los decimales de S/ 0.0045 por gramo.
func TestBuildInvoiceDetails_GramSale_Declares250GRM(t *testing.T) {
	suid := uint(7)
	items := []database.TenantSaleItem{{
		Code: "ARR-GR", Description: "Arroz a granel", Unit: "GRM", Quantity: 250, SaleUnitID: &suid,
		UnitPrice: 0.0045, IgvAffectationType: "10", TaxRate: 18,
		Subtotal: 0.95, TaxAmount: 0.17, Total: 1.13,
	}}
	details, err := BuildInvoiceDetailsFromSaleItems(items, 18, testNormUnit)
	if err != nil {
		t.Fatal(err)
	}
	d := details[0]
	if d.Unidad != "GRM" {
		t.Errorf("Unidad = %q, want GRM", d.Unidad)
	}
	if d.Cantidad != 250 {
		t.Errorf("Cantidad = %v, want 250 (comercial)", d.Cantidad)
	}
	if d.MtoValorUnitario < 0.0037 || d.MtoValorUnitario > 0.0039 {
		t.Errorf("MtoValorUnitario = %v, want ≈0.0038 (0.95/250)", d.MtoValorUnitario)
	}
	if d.MtoPrecioUnitario < 0.0044 || d.MtoPrecioUnitario > 0.0046 {
		t.Errorf("MtoPrecioUnitario = %v, want ≈0.0045", d.MtoPrecioUnitario)
	}
}
