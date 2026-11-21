package service

import (
	"fmt"
	"math"
	"strings"
	"testing"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// Nota de crédito por monto (note_amount.go). Los tests de payload recorren el MISMO camino que la
// emisión real: líneas de la nota → BuildInvoiceDetailsFromSaleItems + ComputeInvoiceSunatTotals
// (lo que buildNotePayload manda al facturador), no importes armados a mano.

func near(a, b float64) bool { return math.Abs(a-b) < 0.005 }

func gravadaItem(id uint, desc string, qty, unitPrice float64) database.TenantSaleItem {
	total := round2(qty * unitPrice)
	sub := round2(total / 1.18)
	return database.TenantSaleItem{
		ID: id, Code: fmt.Sprintf("P%d", id), Description: desc, Unit: "NIU", Quantity: qty, UnitPrice: unitPrice,
		TaxRate: 18, IgvAffectationType: "10", Subtotal: sub, TaxAmount: round2(total - sub), Total: total,
	}
}

func exoneradaItem(id uint, desc string, qty, unitPrice float64) database.TenantSaleItem {
	total := round2(qty * unitPrice)
	return database.TenantSaleItem{
		ID: id, Code: fmt.Sprintf("P%d", id), Description: desc, Unit: "NIU", Quantity: qty, UnitPrice: unitPrice,
		IgvAffectationType: "20", Subtotal: total, TaxAmount: 0, Total: total,
	}
}

// Caso real: venta de 5,521.22 de una sola línea, el cliente pide restar S/ 90.
func TestAmountNote_RestarNoventaSolesAVentaDeUnaLinea(t *testing.T) {
	orig := gravadaItem(1, "Servicios prestados", 1, 5521.22)
	amounts, err := distributeNoteAmount([]database.TenantSaleItem{orig}, nil, 90)
	if err != nil {
		t.Fatal(err)
	}
	items, sub, taxAmt, total, err := buildAmountNoteItems(100, []database.TenantSaleItem{orig}, amounts, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !near(total, 90) || !near(sub, 76.27) || !near(taxAmt, 13.73) {
		t.Fatalf("totales de la nota: sub=%.2f igv=%.2f total=%.2f, quería 76.27 / 13.73 / 90.00", sub, taxAmt, total)
	}
	if items[0].OriginalSaleItemID != nil {
		t.Fatal("una nota por monto no debe apuntar a la línea original (consumiría cupo de cantidad y repondría stock)")
	}
	// Payload final que se envía a SUNAT.
	details, err := BuildInvoiceDetailsFromSaleItems(items, 18, func(s string) string { return s })
	if err != nil {
		t.Fatal(err)
	}
	totals := ComputeInvoiceSunatTotals(items, total)
	d := details[0]
	if !near(d.MtoValorVenta, 76.27) || !near(d.MtoBaseIgv, 76.27) || !near(d.Igv, 13.73) || !near(d.MtoPrecioUnitario, 90) {
		t.Fatalf("detalle: valorVenta=%.2f base=%.2f igv=%.2f precio=%.2f", d.MtoValorVenta, d.MtoBaseIgv, d.Igv, d.MtoPrecioUnitario)
	}
	if !near(totals.MtoOperGravadas, 76.27) || !near(totals.MtoIGV, 13.73) || !near(totals.MtoImpVenta, 90) {
		t.Fatalf("cabecera: gravadas=%.2f igv=%.2f importe=%.2f", totals.MtoOperGravadas, totals.MtoIGV, totals.MtoImpVenta)
	}
	// El IGV de línea debe ser coherente con base × 18% (SUNAT recalcula).
	if math.Abs(d.MtoBaseIgv*0.18-d.Igv) > 0.01 {
		t.Fatalf("IGV %.2f no cuadra con base %.2f × 18%%", d.Igv, d.MtoBaseIgv)
	}
	// Lo que queda de la venta.
	if !near(5521.22-total, 5431.22) {
		t.Fatalf("la venta debía quedar en 5431.22")
	}
}

func TestAmountNote_RepartoProporcionalEntreLineasMixtas(t *testing.T) {
	items := []database.TenantSaleItem{
		gravadaItem(1, "Gravado", 1, 118),    // total 118
		exoneradaItem(2, "Exonerado", 1, 82), // total 82  → 200 en total
	}
	amounts, err := distributeNoteAmount(items, nil, 50)
	if err != nil {
		t.Fatal(err)
	}
	if !near(amounts[1], 29.5) || !near(amounts[2], 20.5) {
		t.Fatalf("reparto = %v, quería 29.50 / 20.50 (proporcional al valor)", amounts)
	}
	lines, sub, tax, total, err := buildAmountNoteItems(1, items, amounts, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !near(total, 50) {
		t.Fatalf("total %.2f", total)
	}
	var gravIGV, exoIGV float64
	for _, l := range lines {
		if l.IgvAffectationType == "10" {
			gravIGV = l.TaxAmount
		} else {
			exoIGV = l.TaxAmount
		}
	}
	if !near(gravIGV, 4.5) || exoIGV != 0 {
		t.Fatalf("IGV gravado %.2f (quería 4.50), exonerado %.2f (quería 0)", gravIGV, exoIGV)
	}
	if !near(sub+tax, total) {
		t.Fatalf("base+igv=%.2f ≠ total %.2f", sub+tax, total)
	}
	totals := ComputeInvoiceSunatTotals(lines, total)
	if !near(totals.MtoOperGravadas, 25) || !near(totals.MtoOperExoneradas, 20.5) || !near(totals.MtoIGV, 4.5) {
		t.Fatalf("cabecera: grav=%.2f exo=%.2f igv=%.2f", totals.MtoOperGravadas, totals.MtoOperExoneradas, totals.MtoIGV)
	}
}

func TestAmountNote_RepartoSumaExactaAlCentimo(t *testing.T) {
	items := []database.TenantSaleItem{
		gravadaItem(1, "A", 1, 10), gravadaItem(2, "B", 1, 10), gravadaItem(3, "C", 1, 10),
	}
	amounts, err := distributeNoteAmount(items, nil, 10) // 3.333… por línea
	if err != nil {
		t.Fatal(err)
	}
	var sum float64
	for _, a := range amounts {
		sum += a
	}
	if !near(sum, 10) {
		t.Fatalf("el reparto suma %.4f, debía ser 10.00 exacto: %v", sum, amounts)
	}
}

func TestAmountNote_SoloLasLineasElegidas(t *testing.T) {
	items := []database.TenantSaleItem{gravadaItem(1, "A", 1, 100), gravadaItem(2, "B", 1, 100)}
	amounts, err := distributeNoteAmount(items, []uint{2}, 30)
	if err != nil {
		t.Fatal(err)
	}
	if len(amounts) != 1 || !near(amounts[2], 30) {
		t.Fatalf("debía caer solo en la línea 2: %v", amounts)
	}
}

func TestAmountNote_MontoPorLineaExplicito(t *testing.T) {
	items := []database.TenantSaleItem{gravadaItem(1, "A", 1, 100), gravadaItem(2, "B", 1, 200)}
	amounts, err := resolveAmountNoteAmounts(items, []NoteItemSelection{{OriginalItemID: 1, Amount: 10}, {OriginalItemID: 2, Amount: 25}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !near(amounts[1], 10) || !near(amounts[2], 25) {
		t.Fatalf("%v", amounts)
	}
	if _, err := resolveAmountNoteAmounts(items, []NoteItemSelection{{OriginalItemID: 1, Amount: 10}}, 50); err == nil {
		t.Fatal("monto total y por línea a la vez debía rechazarse")
	}
}

func TestAmountNote_Rechazos(t *testing.T) {
	item := gravadaItem(1, "A", 1, 100)
	one := []database.TenantSaleItem{item}
	if _, err := distributeNoteAmount(one, nil, 0); err == nil {
		t.Error("monto 0 debía rechazarse")
	}
	if _, err := distributeNoteAmount(one, nil, 100.01); err == nil {
		t.Error("monto mayor al valor de las líneas debía rechazarse")
	}
	if _, _, _, _, err := buildAmountNoteItems(1, one, map[uint]float64{99: 10}, 0); err == nil {
		t.Error("ítem ajeno a la venta debía rechazarse")
	}
	if _, _, _, _, err := buildAmountNoteItems(1, one, map[uint]float64{1: 101}, 0); err == nil {
		t.Error("monto de línea mayor al valor de la línea debía rechazarse")
	}
	free := database.TenantSaleItem{ID: 2, Description: "Regalo", Quantity: 1, IgvAffectationType: "15", Total: 0}
	if _, err := distributeNoteAmount([]database.TenantSaleItem{free}, nil, 5); err == nil {
		t.Error("una venta solo con líneas sin valor no admite descuento")
	}
}

func TestAmountNote_TopeAcumuladoPorImporte(t *testing.T) {
	item := gravadaItem(1, "A", 1, 100)
	one := []database.TenantSaleItem{item}
	// Ya se acreditaron 70: quedan 30.
	if _, _, _, _, err := buildAmountNoteItems(1, one, map[uint]float64{1: 30}, 70); err != nil {
		t.Fatalf("30 con 70 acreditados cabe exacto: %v", err)
	}
	_, _, _, _, err := buildAmountNoteItems(1, one, map[uint]float64{1: 30.01}, 70)
	if err == nil || !strings.Contains(err.Error(), "disponible") {
		t.Fatalf("30.01 con 70 acreditados debía rechazarse indicando lo disponible, got %v", err)
	}
}

func TestAmountNote_Motivos(t *testing.T) {
	for _, c := range []string{"04", "05", "09", "10"} {
		if !IsAmountCreditNoteReason(c) {
			t.Errorf("%s debía admitir monto", c)
		}
	}
	for _, c := range []string{"01", "02", "03", "06", "07", "08", "11", "12", "13"} {
		if IsAmountCreditNoteReason(c) {
			t.Errorf("%s NO debía admitir monto", c)
		}
	}
	// Descuento / disminución: el stock no vuelve. Devolución: sí.
	for c, want := range map[string]bool{"04": false, "05": false, "09": false, "06": true, "07": true, "08": true, "10": true} {
		if CreditNoteReturnsStock(c) != want {
			t.Errorf("CreditNoteReturnsStock(%s) = %v, quería %v", c, !want, want)
		}
	}
}

// creditedAmountOnSale: suma notas no rechazadas ni anuladas y no cuenta el recargo al consumo.
func TestCreditedAmountOnSale(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&database.TenantSale{}); err != nil {
		t.Fatal(err)
	}
	orig := uint(7)
	mk := func(num, billing, status string, total, charge float64) {
		n := database.TenantSale{DocType: "NOTA_CREDITO", Series: "FC01", Number: num, Status: status, BillingStatus: billing,
			Total: total, Subtotal: total, OriginalSaleID: &orig, ServiceChargeAmount: charge}
		if err := db.Create(&n).Error; err != nil {
			t.Fatal(err)
		}
	}
	mk("FC01-1", "accepted", "paid", 90, 0)
	mk("FC01-2", "pending", "paid", 10, 0)
	mk("FC01-3", "rejected", "paid", 500, 0)     // no cuenta
	mk("FC01-4", "accepted", "cancelled", 500, 0) // no cuenta
	mk("FC01-5", "accepted", "paid", 55, 5)       // 55 con recargo 5 → 50
	got, err := creditedAmountOnSale(db, 7)
	if err != nil {
		t.Fatal(err)
	}
	if !near(got, 150) {
		t.Fatalf("acreditado = %.2f, quería 150.00", got)
	}
}
