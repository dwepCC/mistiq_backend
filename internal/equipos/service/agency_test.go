package service

import (
	"strings"
	"testing"
	"time"

	"tukifac/pkg/database"
)

func TestAgencyReturnStatusBoundaries(t *testing.T) {
	cases := []struct {
		days  int
		level string
		label string
		left  int
	}{
		{0, "verde", "Faltan 15 días", 15}, {7, "verde", "Faltan 8 días", 8}, {8, "amarillo", "Faltan 7 días", 7},
		{11, "amarillo", "Faltan 4 días", 4}, {12, "rojo", "Faltan 3 días", 3}, {14, "rojo", "Falta 1 día", 1},
		{15, "critico", "Retornar ya", 0}, {30, "critico", "Retornar ya", 0},
	}
	for _, c := range cases {
		level, label, left := agencyReturnStatus(c.days)
		if level != c.level || label != c.label || left != c.left {
			t.Errorf("%d días: %s %q %d, want %s %q %d", c.days, level, label, left, c.level, c.label, c.left)
		}
	}
}

func TestDaysInAgencyUsesLimaCalendarDays(t *testing.T) {
	lima := limaLoc()
	arrived := time.Date(2026, 10, 1, 23, 30, 0, 0, lima) // 04:30 UTC del día 2
	today := time.Date(2026, 10, 3, 0, 10, 0, 0, lima)
	if d := daysInAgency(arrived, today); d != 2 {
		t.Errorf("días calendario en Lima: %d, want 2", d)
	}
	if d := daysInAgency(today, arrived); d != 0 {
		t.Errorf("nunca negativo: %d", d)
	}
}

func TestMarkArrivedDateRules(t *testing.T) {
	f := newFixture(t)
	mk := func() uint {
		o := f.confirmed(t, 1, 100)
		if _, err := f.s.ValidateOrder(o.ID, "", 1); err != nil {
			t.Fatal(err)
		}
		if _, err := f.s.UpdateShipment(o.ID, ShipmentInput{CarrierID: &f.carrier.ID, GuideNumber: "G1", DestinationDepartment: "Cusco", DeliveryMode: "agencia"}); err != nil {
			t.Fatal(err)
		}
		if _, err := f.s.DispatchOrder(o.ID, ""); err != nil {
			t.Fatal(err)
		}
		return o.ID
	}
	id := mk()
	tomorrow := todayNoon().AddDate(0, 0, 1).Format("2006-01-02")
	if _, err := f.s.MarkArrived(id, tomorrow); err == nil || !strings.Contains(err.Error(), "futura") {
		t.Errorf("fecha futura: %v", err)
	}
	r, err := f.s.MarkArrived(id, "")
	if err != nil || r.Order.Shipment.ArrivedAt == nil || r.Order.Shipment.ArrivedAt.In(limaLoc()).Format("2006-01-02") != todayNoon().Format("2006-01-02") {
		t.Fatalf("por defecto hoy en Lima: %v %+v", err, r)
	}
}

func TestListOrdersAgencyView(t *testing.T) {
	f := newFixture(t)
	ids := []uint{}
	for i := 0; i < 3; i++ {
		o := f.shipped(t, 1, 100)
		ids = append(ids, o.ID)
	}
	arrive := func(id uint, daysAgo int) {
		d := todayNoon().AddDate(0, 0, -daysAgo)
		if err := f.s.db.Model(&database.EquipShipment{}).Where("order_id = ?", id).Update("arrived_at", d).Error; err != nil {
			t.Fatal(err)
		}
	}
	arrive(ids[0], 3)
	arrive(ids[1], 15)
	// ids[2]: en agencia pero sin fecha de llegada (dato antiguo)
	f.s.db.Model(&database.EquipShipment{}).Where("order_id = ?", ids[2]).Update("arrived_at", nil)

	res, err := f.s.ListOrders(OrderFilter{ShipmentStatus: "en_agencia", Agency: "con_llegada", Sort: "agency_days"})
	if err != nil || res.Total != 2 || len(res.Rows) != 2 {
		t.Fatalf("con llegada: %v %+v", err, res)
	}
	if res.Rows[0].ID != ids[1] || *res.Rows[0].DaysInAgency != 15 || res.Rows[0].AgencyLevel != "critico" || res.Rows[0].AgencyLabel != "Retornar ya" {
		t.Errorf("primero el de más días: %+v", res.Rows[0])
	}
	if r0 := res.Rows[0]; r0.TotalAmount != 100 || r0.BalanceAmount != 100 || r0.PaymentStatus != "pendiente" {
		t.Errorf("total, saldo y pago en la fila: %+v", r0)
	}
	if res.Rows[1].AgencyLevel != "verde" || *res.Rows[1].DaysLeft != 12 {
		t.Errorf("segundo: %+v", res.Rows[1])
	}
	all, _ := f.s.ListOrders(OrderFilter{ShipmentStatus: "en_agencia", Sort: "agency_days"})
	if all.Total != 3 || all.Rows[2].ID != ids[2] || all.Rows[2].AgencyLevel != "sin_fecha" || all.Rows[2].DaysInAgency != nil {
		t.Errorf("sin fecha al final y sin contador: %+v", all.Rows)
	}
}

func TestAgencyViewAllOrderAndSingleDeadline(t *testing.T) {
	f := newFixture(t)
	arrivedDays := func(id uint, days int) {
		d := todayNoon().AddDate(0, 0, -days)
		f.s.db.Model(&database.EquipShipment{}).Where("order_id = ?", id).Update("arrived_at", d)
	}
	a5 := f.shipped(t, 1, 100)  // en agencia, 5 días
	a15 := f.shipped(t, 1, 100) // en agencia, 15 días
	nodate := f.shipped(t, 1, 100)
	arrivedDays(a5.ID, 5)
	arrivedDays(a15.ID, 15)
	f.s.db.Model(&database.EquipShipment{}).Where("order_id = ?", nodate.ID).Update("arrived_at", nil)
	picked := f.shipped(t, 1, 100)
	if _, err := f.s.MarkPickedUp(picked.ID, ""); err != nil {
		t.Fatal(err)
	}
	pending := f.confirmed(t, 1, 100)

	res, err := f.s.ListOrders(OrderFilter{Status: "registrado", Sort: "agency_days"})
	if err != nil || len(res.Rows) != 5 {
		t.Fatalf("lista: %v %+v", err, res)
	}
	want := []uint{a15.ID, a5.ID, nodate.ID, pending.ID, picked.ID}
	for i, w := range want {
		if res.Rows[i].ID != w {
			t.Fatalf("orden en %d: got %d want %d", i, res.Rows[i].ID, w)
		}
	}
	// El Panel dice «vencido» el mismo día 15 que el semáforo.
	al, err := f.s.Alerts()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, a := range al {
		if a.OrderID == a15.ID && a.Kind == "agencia_vencido" {
			found = true
		}
		if a.OrderID == a5.ID && a.Kind == "agencia_vencido" {
			t.Error("a los 5 días no debe estar vencido")
		}
	}
	if !found {
		t.Errorf("alerta del día 15: %+v", al)
	}
	if !agencyExpired(func() *int { d := 15; return &d }()) || agencyExpired(func() *int { d := 14; return &d }()) || agencyExpired(nil) {
		t.Error("agencyExpired en el límite")
	}
}

func TestUpdateShippingData(t *testing.T) {
	f := newFixture(t)
	o := f.confirmed(t, 1, 100)
	cost := 12.5
	r, ch, err := f.s.UpdateShippingData(o.ID, ShippingDataInput{GuideNumber: " G-123 ", FreightCost: &cost, ScheduledDispatchDate: "2026-10-30"})
	if err != nil {
		t.Fatal(err)
	}
	sh := r.Order.Shipment
	if sh == nil || sh.GuideNumber != "G-123" || sh.FreightCost != 12.5 || sh.ScheduledDispatchDate == nil || sh.Status != "pendiente_envio" || ch.GuideAfter != "G-123" {
		t.Fatalf("guardado (se crea el envío si no existía): %+v %+v", sh, ch)
	}
	// Lo demás del envío no cambia.
	if r.Order.TotalAmount != 100 || r.Order.BalanceAmount != 100 {
		t.Errorf("el pedido no debe cambiar: %+v", r.Order.EquipOrder)
	}
	// Vaciar guía y fecha las quita; flete nil no cambia.
	r, _, err = f.s.UpdateShippingData(o.ID, ShippingDataInput{})
	if err != nil || r.Order.Shipment.GuideNumber != "" || r.Order.Shipment.ScheduledDispatchDate != nil || r.Order.Shipment.FreightCost != 12.5 {
		t.Fatalf("limpiar: %v %+v", err, r.Order.Shipment)
	}
	bad := -1.0
	frac := 1.234
	for name, in := range map[string]ShippingDataInput{
		"flete negativo":   {FreightCost: &bad},
		"flete 3 decimales": {FreightCost: &frac},
		"guía inválida":    {GuideNumber: "<script>"},
		"guía larga":       {GuideNumber: strings.Repeat("A", 41)},
		"fecha inválida":   {ScheduledDispatchDate: "31/12/2026"},
	} {
		if _, _, err := f.s.UpdateShippingData(o.ID, in); !IsValidation(err) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// Envío entregado: cerrado.
	sh2 := f.shipped(t, 1, 100)
	if _, err := f.s.MarkPickedUp(sh2.ID, ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.s.UpdateShippingData(sh2.ID, ShippingDataInput{GuideNumber: "X1"}); err == nil || !strings.Contains(err.Error(), "no se puede modificar") {
		t.Errorf("envío entregado: %v", err)
	}
	// Pedido anulado.
	an := f.confirmed(t, 1, 100)
	if _, err := f.s.CancelOrder(an.ID, "prueba", 1); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.s.UpdateShippingData(an.ID, ShippingDataInput{GuideNumber: "X1"}); err == nil || !strings.Contains(err.Error(), "anulado") {
		t.Errorf("pedido anulado: %v", err)
	}
}

func TestListOrdersRowCarriesShippingData(t *testing.T) {
	f := newFixture(t)
	o := f.confirmed(t, 1, 100)
	cost := 7.0
	if _, _, err := f.s.UpdateShippingData(o.ID, ShippingDataInput{GuideNumber: "G9", FreightCost: &cost, ScheduledDispatchDate: "2026-11-03"}); err != nil {
		t.Fatal(err)
	}
	res, err := f.s.ListOrders(OrderFilter{})
	if err != nil || len(res.Rows) != 1 {
		t.Fatal(err, res)
	}
	r := res.Rows[0]
	if r.GuideNumber != "G9" || r.FreightCost != 7 || r.ScheduledDispatchDate == nil {
		t.Errorf("fila: %+v", r)
	}
}

// Registro tardío: el despacho se marcó hoy pero el paquete ya había llegado días antes. La llegada
// (y luego el recojo) con fecha anterior no se bloquean: retroceden la fecha previa y avisan.
func TestMarkArrivedAndPickedUpBeforeDispatchAdjustsDates(t *testing.T) {
	f := newFixture(t)
	o := f.confirmed(t, 1, 100)
	if _, err := f.s.ValidateOrder(o.ID, "", 1); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.UpdateShipment(o.ID, ShipmentInput{CarrierID: &f.carrier.ID, GuideNumber: "G1", DestinationDepartment: "Cusco", DeliveryMode: "agencia"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.DispatchOrder(o.ID, ""); err != nil { // despacho = hoy
		t.Fatal(err)
	}
	day := func(ago int) string { return todayNoon().AddDate(0, 0, -ago).Format("2006-01-02") }

	r, err := f.s.MarkArrived(o.ID, day(5))
	if err != nil {
		t.Fatalf("llegada anterior al despacho debe permitirse: %v", err)
	}
	sh := r.Order.Shipment
	if sh.ArrivedAt.In(limaLoc()).Format("2006-01-02") != day(5) || sh.DispatchedAt.In(limaLoc()).Format("2006-01-02") != day(5) {
		t.Errorf("fechas: llegada %v, despacho %v, want ambas %s", sh.ArrivedAt, sh.DispatchedAt, day(5))
	}
	if len(r.Warnings) != 1 || !strings.Contains(r.Warnings[0], "despacho") {
		t.Errorf("avisos: %v", r.Warnings)
	}

	p, err := f.s.MarkPickedUp(o.ID, day(7))
	if err != nil {
		t.Fatalf("recojo anterior a la llegada debe permitirse: %v", err)
	}
	sh = p.Order.Shipment
	if sh.Status != "entregado" || sh.ArrivedAt.In(limaLoc()).Format("2006-01-02") != day(7) || sh.DispatchedAt.In(limaLoc()).Format("2006-01-02") != day(7) {
		t.Errorf("recojo: %+v", sh)
	}
}
