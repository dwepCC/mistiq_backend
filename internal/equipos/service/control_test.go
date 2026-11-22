package service

import (
	"strings"
	"testing"
	"time"

	"tukifac/pkg/database"
)

// shipped deja un pedido confirmado, validado, despachado y en agencia.
func (f *fixture) shipped(t *testing.T, qty, price float64) *OrderView {
	t.Helper()
	o := f.confirmed(t, qty, price)
	if _, err := f.s.ValidateOrder(o.ID, "", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.UpdateShipment(o.ID, ShipmentInput{CarrierID: &f.carrier.ID, GuideNumber: "G1", DestinationDepartment: "Cusco", DeliveryMode: "agencia"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.DispatchOrder(o.ID, ""); err != nil {
		t.Fatal(err)
	}
	r, err := f.s.MarkArrived(o.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	return r.Order
}

func TestReturn_goodConditionReentersStockAndReship(t *testing.T) {
	f := newFixture(t)
	o := f.shipped(t, 2, 100)
	if f.stock(t, f.printer.ID) != 8 {
		t.Fatal("precondición")
	}
	r, err := f.s.CreateReturn(ReturnInput{OrderID: o.ID, ReturnCost: 26}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if r.ReturnNumber != 1 || r.Status != "solicitado" || r.UnpaidBalance != 200 || len(r.Items) != 1 {
		t.Fatalf("retorno: %+v", r)
	}
	if v, _ := f.s.GetOrder(o.ID); v.Shipment.Status != "retorno" {
		t.Fatalf("el envío queda en retorno: %s", v.Shipment.Status)
	}
	if _, err := f.s.UpdateReturn(r.ID, ReturnUpdate{Status: "en_camino", ReturnCost: 26, UnpaidBalance: 200}, 1); err != nil {
		t.Fatal(err)
	}
	if f.stock(t, f.printer.ID) != 8 {
		t.Fatal("en camino aún no reingresa")
	}
	if _, err := f.s.UpdateReturn(r.ID, ReturnUpdate{Status: "recibido", ReturnCost: 26, UnpaidBalance: 200}, 1); err != nil {
		t.Fatal(err)
	}
	if f.stock(t, f.printer.ID) != 10 {
		t.Fatalf("recibido en buen estado reingresa: %d", f.stock(t, f.printer.ID))
	}
	if _, err := f.s.UpdateReturn(r.ID, ReturnUpdate{Status: "recibido", ReturnCost: 26, UnpaidBalance: 200, Notes: "ok"}, 1); err != nil || f.stock(t, f.printer.ID) != 10 {
		t.Fatalf("recibir dos veces no duplica el reingreso: %v %d", err, f.stock(t, f.printer.ID))
	}
	if _, err := f.s.UpdateReturn(r.ID, ReturnUpdate{Status: "solicitado"}, 1); err == nil {
		t.Error("un retorno cerrado no cambia de estado")
	}
	res, err := f.s.Reship(r.ID, 1)
	if err != nil || res.Order.Shipment.Status != "pendiente_envio" {
		t.Fatalf("reenvío crea un segundo envío: %v", err)
	}
	if f.stock(t, f.printer.ID) != 8 {
		t.Fatalf("el reenvío vuelve a descontar: %d", f.stock(t, f.printer.ID))
	}
	if _, err := f.s.Reship(r.ID, 1); err == nil {
		t.Error("no se reenvía dos veces el mismo retorno")
	}
	var n int64
	f.s.db.Model(&database.EquipShipment{}).Where("order_id = ?", o.ID).Count(&n)
	if n != 2 {
		t.Errorf("el historial conserva el primer envío: %d", n)
	}
}

func TestReturn_damagedDoesNotCreateStockAndValidation(t *testing.T) {
	f := newFixture(t)
	o := f.shipped(t, 1, 100)
	if _, err := f.s.CreateReturn(ReturnInput{OrderID: o.ID, Items: []ReturnItemInput{{ProductID: f.printer.ID, Quantity: 5}}}, 1); err == nil || !strings.Contains(err.Error(), "más de lo enviado") {
		t.Errorf("no se devuelve más de lo enviado: %v", err)
	}
	r, err := f.s.CreateReturn(ReturnInput{OrderID: o.ID, Condition: "danado"}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.UpdateReturn(r.ID, ReturnUpdate{Status: "recibido"}, 1); err != nil {
		t.Fatal(err)
	}
	if f.stock(t, f.printer.ID) != 9 {
		t.Fatalf("un equipo dañado no vuelve al stock: %d", f.stock(t, f.printer.ID))
	}
	if _, err := f.s.Reship(r.ID, 1); err == nil {
		t.Error("un equipo dañado no se reenvía")
	}
	if _, err := f.s.CreateReturn(ReturnInput{OrderID: o.ID}, 1); err == nil {
		t.Error("el pedido ya está en retorno")
	}
}

func TestDashboardAndAlerts(t *testing.T) {
	f := newFixture(t)
	f.confirmed(t, 1, 100) // por despachar
	o := f.shipped(t, 1, 100)
	// Llegó hace 12 días: alerta amarilla/roja según configuración.
	old := time.Now().AddDate(0, 0, -12)
	f.s.db.Model(&database.EquipShipment{}).Where("order_id = ?", o.ID).Updates(map[string]any{"arrived_at": old, "dispatched_at": old.AddDate(0, 0, -3)})
	d, err := f.s.Dashboard("", true)
	if err != nil {
		t.Fatal(err)
	}
	if d.Orders != 2 || d.PendingValidation != 0 || d.InAgency != 1 || d.ReadyToDispatch != 1 {
		t.Fatalf("dashboard: %+v", d)
	}
	al, err := f.s.Alerts()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, a := range al {
		if a.OrderID == o.ID && strings.HasPrefix(a.Kind, "agencia_") {
			found = true
		}
	}
	if !found {
		t.Errorf("debe alertar el paquete con 12 días en agencia: %+v", al)
	}
}

func TestReports_summaryCollectionsProfitAndReplenishment(t *testing.T) {
	f := newFixture(t)
	cost := 40.0
	if _, err := f.s.AddMovement(MovementInput{ProductID: f.printer.ID, MovementType: "ingreso", Quantity: 10, Note: "con costo", UnitCost: &cost}, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.AddMovement(MovementInput{ProductID: f.printer.ID, MovementType: "ajuste", Quantity: 1, Note: "x", UnitCost: &cost}, 1); err == nil {
		t.Error("el costo solo aplica a ingresos")
	}
	o := f.confirmed(t, 2, 100)
	if _, err := f.s.UpdateShipment(o.ID, ShipmentInput{CarrierID: &f.carrier.ID, DestinationDepartment: "Lima", DeliveryMode: "agencia", FreightCost: ptr(15.0)}); err != nil {
		t.Fatal(err)
	}
	cid := *o.CustomerID
	if _, err := f.s.CreatePayment(PaymentInput{CustomerID: &cid, Amount: 50, Method: "yape", Moment: "anticipado", AutoAllocate: true}, 1); err != nil {
		t.Fatal(err)
	}
	sum, err := f.s.Summary("")
	if err != nil || sum.Total.Orders != 1 || sum.Total.Sales != 200 || sum.Total.Paid != 50 || sum.Collected != 50 {
		t.Fatalf("resumen: %+v %v", sum, err)
	}
	sales, err := f.s.SalesByProduct("")
	if err != nil || len(sales.Lines) != 1 || sales.Lines[0].Amount != 200 || sales.Units[0].DirectIndep != 2 {
		t.Fatalf("ventas por producto: %+v %v", sales, err)
	}
	col, err := f.s.Collections()
	if err != nil || col.Total != 150 || len(col.Customers) != 1 || col.B0 != 150 {
		t.Fatalf("cobranza: %+v %v", col, err)
	}
	pr, err := f.s.Profitability("")
	if err != nil {
		t.Fatal(err)
	}
	// ventas 200 − costo 2×40 − flete 15 = 105
	if pr.Total.Revenue != 200 || pr.Total.Cogs != 80 || pr.Total.Freight != 15 || pr.Total.Profit != 105 || pr.CostCoverage != 1 {
		t.Fatalf("utilidad: %+v", pr)
	}
	rep, err := f.s.Replenishment()
	if err != nil || len(rep) != 2 {
		t.Fatalf("reposición: %v %+v", err, rep)
	}
}

func TestClosePeriodFreezesManualMovements(t *testing.T) {
	f := newFixture(t)
	if _, err := f.s.ClosePeriod(currentPeriod(), 1); err == nil {
		t.Error("el mes en curso no se cierra")
	}
	prev := time.Now().In(limaLoc()).AddDate(0, -1, 0)
	period := prev.Format("2006-01")
	c, err := f.s.ClosePeriod(period, 1)
	if err != nil || c.Products != 2 {
		t.Fatalf("cierre: %+v %v", c, err)
	}
	if _, err := f.s.ClosePeriod(period, 1); err == nil {
		t.Error("no se cierra dos veces")
	}
	at := prev
	if _, err := f.s.AddMovement(MovementInput{ProductID: f.roll.ID, MovementType: "ingreso", Quantity: 1, Note: "tarde", OccurredAt: &at}, 1); err == nil || !strings.Contains(err.Error(), "cerrado") {
		t.Errorf("período cerrado bloquea movimientos manuales: %v", err)
	}
	if err := f.s.ReopenPeriod(period); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.AddMovement(MovementInput{ProductID: f.roll.ID, MovementType: "ingreso", Quantity: 1, Note: "tarde", OccurredAt: &at}, 1); err != nil {
		t.Errorf("reabierto permite movimientos: %v", err)
	}
}

func ptr[T any](v T) *T { return &v }
