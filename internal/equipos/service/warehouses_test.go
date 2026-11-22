package service

import (
	"errors"
	"strings"
	"testing"
)

// warehouses devuelve el id del Almacén 1 (operativo) y el del depósito sembrado (Almacén 2).
func (f *fixture) warehouses(t *testing.T) (main, depot uint) {
	t.Helper()
	ws, err := f.s.ListWarehouses(false)
	if err != nil || len(ws) < 2 {
		t.Fatalf("almacenes sembrados: %v %v", ws, err)
	}
	for _, w := range ws {
		if w.IsDefault {
			main = w.ID
		} else if depot == 0 {
			depot = w.ID
		}
	}
	return main, depot
}

func (f *fixture) stockAt(t *testing.T, wh, product uint) int {
	t.Helper()
	m, err := stockAtTx(f.s.db, wh, []uint{product})
	if err != nil {
		t.Fatal(err)
	}
	return m[product]
}

func TestWarehouses_seedAndRules(t *testing.T) {
	f := newFixture(t)
	main, depot := f.warehouses(t)
	if main == 0 || depot == 0 {
		t.Fatal("deben existir el Almacén 1 (operativo) y el depósito")
	}
	off := false
	if _, err := f.s.UpdateWarehouse(main, WarehouseInput{Active: &off}); err == nil || !IsValidation(err) {
		t.Fatalf("el Almacén 1 no se desactiva: %v", err)
	}
	if _, err := f.s.CreateWarehouse(WarehouseInput{Name: "Almacén 2 (Cerro Colorado)"}); err == nil {
		t.Error("nombre duplicado")
	}
	w, err := f.s.CreateWarehouse(WarehouseInput{Name: "Almacén Lima Norte"})
	if err != nil || w.IsDefault || !w.Active {
		t.Fatalf("crear almacén: %v %+v", err, w)
	}
}

func TestWarehouses_depotStockDoesNotSell(t *testing.T) {
	f := newFixture(t)
	_, depot := f.warehouses(t)
	// 5 equipos importados entran al depósito: el Almacén 1 sigue en 10.
	if _, err := f.s.AddMovement(MovementInput{ProductID: f.printer.ID, MovementType: "ingreso", Quantity: 5, Note: "importación", WarehouseID: &depot}, 1); err != nil {
		t.Fatal(err)
	}
	if got := f.stock(t, f.printer.ID); got != 10 {
		t.Fatalf("el Almacén 1 no cambia con un ingreso al depósito: %d", got)
	}
	rows, err := f.s.ListProducts("", "", true)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, r := range rows {
		if r.ID == f.printer.ID {
			found = true
			if r.Stock != 10 || r.StockDepot != 5 {
				t.Fatalf("catálogo: stock=%d depósito=%d", r.Stock, r.StockDepot)
			}
		}
	}
	if !found {
		t.Fatal("producto no listado")
	}
	// Vender 12 requiere 12 en el Almacén 1: el depósito no cuenta.
	r, err := f.s.CreateOrder(f.input(12, 100), 1)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.s.ConfirmOrder(r.Order.ID, ConfirmInput{}, 1)
	var neg *NegativeStockError
	if !errors.As(err, &neg) {
		t.Fatalf("confirmar 12 con 10 en el Almacén 1 (y 5 en el depósito) debe advertir stock negativo: %v", err)
	}
	by, err := f.s.StockByWarehouse()
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range by {
		if row.ProductID == f.printer.ID && (row.Total != 15 || len(row.ByWarehouse) != 2) {
			t.Fatalf("stock por almacén: %+v", row)
		}
	}
}

func TestRequirement_partialTransfersAndStatus(t *testing.T) {
	f := newFixture(t)
	main, depot := f.warehouses(t)
	if _, err := f.s.AddMovement(MovementInput{ProductID: f.printer.ID, MovementType: "ingreso", Quantity: 20, Note: "importación", WarehouseID: &depot}, 1); err != nil {
		t.Fatal(err)
	}
	req, err := f.s.CreateRequirement(RequirementInput{SupplyingWarehouseID: depot, Notes: "para la semana", Items: []TransferItemInput{{ProductID: f.printer.ID, Quantity: 8}}}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if req.Number != 1 || req.Status != "pendiente" || req.RequestingWarehouseID != main {
		t.Fatalf("requerimiento: %+v", req.EquipRequirement)
	}
	if _, err := f.s.CreateRequirement(RequirementInput{SupplyingWarehouseID: main, Items: []TransferItemInput{{ProductID: f.printer.ID, Quantity: 1}}}, 1); err == nil {
		t.Error("el depósito y el solicitante deben ser distintos")
	}

	// Primera entrega: 5 de 8.
	tr, err := f.s.CreateTransfer(TransferInput{RequirementID: &req.ID, Items: []TransferItemInput{{ProductID: f.printer.ID, Quantity: 5}}}, 1)
	if err != nil || tr.Number != 1 || tr.FromWarehouseID != depot || tr.ToWarehouseID != main {
		t.Fatalf("transferencia del requerimiento: %v %+v", err, tr)
	}
	if f.stockAt(t, main, f.printer.ID) != 15 || f.stockAt(t, depot, f.printer.ID) != 15 {
		t.Fatalf("5 pasan del depósito al Almacén 1: %d / %d", f.stockAt(t, main, f.printer.ID), f.stockAt(t, depot, f.printer.ID))
	}
	if v, _ := f.s.GetRequirement(req.ID); v.Status != "parcial" || v.Items[0].Sent != 5 || v.Items[0].Pending != 3 {
		t.Fatalf("estado parcial: %+v", v)
	}
	if _, err := f.s.CreateTransfer(TransferInput{RequirementID: &req.ID, Items: []TransferItemInput{{ProductID: f.printer.ID, Quantity: 4}}}, 1); err == nil || !strings.Contains(err.Error(), "pendiente") {
		t.Fatalf("no se envía más de lo pendiente: %v", err)
	}
	if _, err := f.s.CancelRequirement(req.ID, "ya no"); err == nil {
		t.Error("un requerimiento con transferencias no se anula directamente")
	}

	// Resto: queda atendido.
	if _, err := f.s.CreateTransfer(TransferInput{RequirementID: &req.ID, Items: []TransferItemInput{{ProductID: f.printer.ID, Quantity: 3}}}, 1); err != nil {
		t.Fatal(err)
	}
	if v, _ := f.s.GetRequirement(req.ID); v.Status != "atendido" || len(v.Transfers) != 2 {
		t.Fatalf("atendido: %+v", v)
	}

	// Anular la primera transferencia devuelve el requerimiento a parcial y regresa el stock.
	if _, err := f.s.CancelTransfer(tr.ID, "se envió por error", 1); err != nil {
		t.Fatal(err)
	}
	if v, _ := f.s.GetRequirement(req.ID); v.Status != "parcial" || v.Items[0].Sent != 3 {
		t.Fatalf("tras anular: %+v", v)
	}
	if f.stockAt(t, main, f.printer.ID) != 13 || f.stockAt(t, depot, f.printer.ID) != 17 {
		t.Fatalf("stock tras anular: %d / %d", f.stockAt(t, main, f.printer.ID), f.stockAt(t, depot, f.printer.ID))
	}
	if _, err := f.s.CancelTransfer(tr.ID, "otra vez", 1); err == nil {
		t.Error("no se anula dos veces")
	}
}

func TestRequirement_cancelPending(t *testing.T) {
	f := newFixture(t)
	_, depot := f.warehouses(t)
	req, err := f.s.CreateRequirement(RequirementInput{SupplyingWarehouseID: depot, Items: []TransferItemInput{{ProductID: f.printer.ID, Quantity: 2}}}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.CancelRequirement(req.ID, ""); err == nil {
		t.Error("el motivo es obligatorio")
	}
	c, err := f.s.CancelRequirement(req.ID, "ya no hace falta")
	if err != nil || c.Status != "anulado" || c.CancelledAt == nil {
		t.Fatalf("anular requerimiento pendiente: %v %+v", err, c)
	}
	if _, err := f.s.CreateTransfer(TransferInput{RequirementID: &req.ID, Items: []TransferItemInput{{ProductID: f.printer.ID, Quantity: 1}}}, 1); err == nil {
		t.Error("un requerimiento anulado no admite transferencias")
	}
	open, _ := f.s.ListRequirements("abiertos")
	if len(open) != 0 {
		t.Errorf("el anulado no figura entre los abiertos: %d", len(open))
	}
}

func TestTransfer_negativeOriginWarnsAndCancelBlockedWhenDestinationSold(t *testing.T) {
	f := newFixture(t)
	main, depot := f.warehouses(t)
	in := TransferInput{FromWarehouseID: depot, ToWarehouseID: main, Items: []TransferItemInput{{ProductID: f.printer.ID, Quantity: 4}}}
	_, err := f.s.CreateTransfer(in, 1)
	var neg *NegativeStockError
	if !errors.As(err, &neg) {
		t.Fatalf("el depósito no tiene stock: debe advertir: %v", err)
	}
	in.AllowNegative = true
	if _, err := f.s.CreateTransfer(in, 1); err == nil || !IsValidation(err) {
		t.Fatalf("con stock negativo la nota es obligatoria: %v", err)
	}
	in.NegativeNote = "llegó la importación, se carga después"
	tr, err := f.s.CreateTransfer(in, 1)
	if err != nil {
		t.Fatal(err)
	}
	if f.stockAt(t, main, f.printer.ID) != 14 || f.stockAt(t, depot, f.printer.ID) != -4 {
		t.Fatalf("stock: %d / %d", f.stockAt(t, main, f.printer.ID), f.stockAt(t, depot, f.printer.ID))
	}
	// El Almacén 1 vende casi todo: ya no tiene lo recibido.
	f.confirmed(t, 12, 100)
	if _, err := f.s.CancelTransfer(tr.ID, "equivocada", 1); err == nil || !strings.Contains(err.Error(), "ya no tiene") {
		t.Fatalf("anular debe bloquearse si el destino ya vendió lo recibido: %v", err)
	}
	if f.stockAt(t, depot, f.printer.ID) != -4 {
		t.Fatal("el bloqueo no debe mover stock")
	}
}

func TestTransfer_maestroCountsArrivalsAsIngresos(t *testing.T) {
	f := newFixture(t)
	main, depot := f.warehouses(t)
	if _, err := f.s.AddMovement(MovementInput{ProductID: f.printer.ID, MovementType: "ingreso", Quantity: 6, Note: "importación", WarehouseID: &depot}, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.CreateTransfer(TransferInput{FromWarehouseID: depot, ToWarehouseID: main, Items: []TransferItemInput{{ProductID: f.printer.ID, Quantity: 6}}}, 1); err != nil {
		t.Fatal(err)
	}
	rows, err := f.s.StockReport(currentPeriod())
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.ProductID == f.printer.ID {
			// 10 iniciales + 6 que llegaron del depósito; lo que se quedó en el depósito no entra al Maestro.
			if r.Current != 16 || r.Ingresos != 16 {
				t.Fatalf("Maestro del Almacén 1: %+v", r)
			}
		}
	}
	// El historial filtra por producto y almacén.
	res, err := f.s.ListTransfers(TransferFilter{ProductID: f.printer.ID, WarehouseID: depot})
	if err != nil || res.Total != 1 || len(res.Rows[0].Items) != 1 || res.Rows[0].FromName == "" {
		t.Fatalf("historial: %v %+v", err, res)
	}
	if res, _ := f.s.ListTransfers(TransferFilter{ProductID: f.roll.ID}); res.Total != 0 {
		t.Error("filtrar por otro producto no debe traer esta transferencia")
	}
}

func TestWarehouses_ensureIsIdempotent(t *testing.T) {
	f := newFixture(t)
	for i := 0; i < 3; i++ {
		if err := f.s.EnsureWarehouses(); err != nil {
			t.Fatal(err)
		}
	}
	ws, err := f.s.ListWarehouses(true)
	if err != nil || len(ws) != 2 {
		t.Fatalf("la siembra repetida no duplica almacenes: %v %v", len(ws), err)
	}
}
