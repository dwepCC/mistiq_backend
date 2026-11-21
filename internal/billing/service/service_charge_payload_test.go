package service

import (
	"math"
	"testing"

	"tukifac/pkg/database"
	"tukifac/pkg/facturador"
)

func scNear(a, b float64) bool { return math.Abs(a-b) < 0.005 }

// Factura/boleta: el cargo 46 va como cargo global, valor de venta e IGV no cambian, el subtotal no lo incluye y el importe total sí.
func TestApplyServiceChargeToInvoicePayload(t *testing.T) {
	p := &facturador.InvoicePayload{ValorVenta: 100, MtoIGV: 18, SubTotal: 128, MtoImpVenta: 128}
	applyServiceChargeToInvoicePayload(p, &database.TenantSale{ServiceChargeAmount: 10})
	if len(p.Cargos) != 1 || p.Cargos[0].CodTipo != "46" || !scNear(p.Cargos[0].Monto, 10) || !scNear(p.Cargos[0].MontoBase, 100) || !scNear(p.Cargos[0].Factor, 0.1) {
		t.Fatalf("cargo 46: %+v", p.Cargos)
	}
	if !scNear(p.SumOtrosCargos, 10) || !scNear(p.SubTotal, 118) || !scNear(p.MtoImpVenta, 128) || !scNear(p.ValorVenta, 100) || !scNear(p.MtoIGV, 18) {
		t.Fatalf("totales: %+v", p)
	}
	// Sin recargo no cambia nada.
	q := &facturador.InvoicePayload{ValorVenta: 100, SubTotal: 118, MtoImpVenta: 118}
	applyServiceChargeToInvoicePayload(q, &database.TenantSale{})
	if q.Cargos != nil || q.SumOtrosCargos != 0 || q.SubTotal != 118 {
		t.Fatalf("sin recargo debe quedar igual: %+v", q)
	}
}

// Nota de crédito: solo el total de otros cargos (la guía UBL de la NC no tiene nodo de cargo global).
func TestApplyServiceChargeToNotePayload(t *testing.T) {
	p := &facturador.NotePayload{MtoImpVenta: 128, SubTotal: 128}
	applyServiceChargeToNotePayload(p, &database.TenantSale{ServiceChargeAmount: 10})
	if !scNear(p.SumOtrosCargos, 10) || !scNear(p.SubTotal, 118) || !scNear(p.MtoImpVenta, 128) {
		t.Fatalf("nota: %+v", p)
	}
}

// Nota parcial: recargo proporcional; la que devuelve lo último se lleva el resto exacto; nunca se acredita más de lo cobrado.
func TestPartialNoteServiceCharge(t *testing.T) {
	db := setupReturnedQuantityTestDB(t)
	orig := database.TenantSale{DocType: "FACTURA", Series: "F001", Number: "F001-1", Status: "paid", BillingStatus: "accepted", Subtotal: 100, Total: 128, ServiceChargeAmount: 10, ServiceChargeRate: 10}
	if err := db.Create(&orig).Error; err != nil {
		t.Fatal(err)
	}
	items := []database.TenantSaleItem{
		{SaleID: orig.ID, Description: "A", Quantity: 2, Subtotal: 50, Total: 59},
		{SaleID: orig.ID, Description: "B", Quantity: 2, Subtotal: 50, Total: 59},
	}
	for i := range items {
		if err := db.Create(&items[i]).Error; err != nil {
			t.Fatal(err)
		}
	}
	// 1ª nota: 1 de las 2 unidades de A (valor 25) → 10 × 25/100 = 2.50.
	c1, err := partialNoteServiceCharge(db, &orig, items, []NoteItemSelection{{OriginalItemID: items[0].ID, Quantity: 1}}, 25)
	if err != nil || !scNear(c1, 2.5) {
		t.Fatalf("1ª nota: %v err=%v", c1, err)
	}
	// Se registra esa nota (venta + ítem devuelto).
	oid := orig.ID
	nc := database.TenantSale{DocType: "NOTA_CREDITO", Series: "FC01", Number: "FC01-1", Status: "paid", BillingStatus: "accepted", OriginalSaleID: &oid, ServiceChargeAmount: c1}
	db.Create(&nc)
	ret := items[0].ID
	db.Create(&database.TenantSaleItem{SaleID: nc.ID, Description: "A", Quantity: 1, OriginalSaleItemID: &ret})
	// 2ª nota: lo que queda de todo (A 1 + B 2 = 75 de valor): cubre todo lo pendiente → se lleva el resto exacto 7.50.
	c2, err := partialNoteServiceCharge(db, &orig, items, []NoteItemSelection{{OriginalItemID: items[0].ID, Quantity: 1}, {OriginalItemID: items[1].ID, Quantity: 2}}, 75)
	if err != nil || !scNear(c2, 7.5) {
		t.Fatalf("2ª nota (última): %v err=%v", c2, err)
	}
	// Una nota rechazada por SUNAT no consume recargo.
	db.Model(&nc).Update("billing_status", "rejected")
	c3, _ := partialNoteServiceCharge(db, &orig, items, []NoteItemSelection{{OriginalItemID: items[0].ID, Quantity: 1}}, 25)
	if !scNear(c3, 2.5) {
		t.Fatalf("nota rechazada no debe contar: %v", c3)
	}
	// Venta sin recargo → 0.
	if c, _ := partialNoteServiceCharge(db, &database.TenantSale{ID: 99}, items, nil, 10); c != 0 {
		t.Fatalf("sin recargo: %v", c)
	}
}
