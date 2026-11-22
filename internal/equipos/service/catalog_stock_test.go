package service

import (
	"strings"
	"testing"
	"time"

	"tukifac/pkg/database"
)

func mustProduct(t *testing.T, s *Service, code string, y, g int) *database.EquipProduct {
	t.Helper()
	p, err := s.CreateProduct(ProductInput{Code: code, Name: code, YellowThreshold: y, GreenThreshold: g})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestProducts_validation(t *testing.T) {
	s := New(setupEquiposDB(t))
	mustProduct(t, s, "TK-E583", 5, 10)
	cases := []struct {
		name string
		in   ProductInput
		want string
	}{
		{"código repetido sin importar mayúsculas", ProductInput{Code: "tk-e583"}, "ya existe"},
		{"sin código", ProductInput{Name: "x"}, "obligatorio"},
		{"tipo inválido", ProductInput{Code: "A", Kind: "cosa"}, "tipo de producto"},
		{"amarillo mayor que verde", ProductInput{Code: "B", YellowThreshold: 9, GreenThreshold: 3}, "umbral amarillo"},
		{"precio negativo", ProductInput{Code: "C", ReferencePrice: -1}, "negativo"},
	}
	for _, c := range cases {
		if _, err := s.CreateProduct(c.in); err == nil || !strings.Contains(err.Error(), c.want) || !IsValidation(err) {
			t.Errorf("%s: err=%v, want validación con %q", c.name, err, c.want)
		}
	}
	p, err := s.UpdateProduct(1, ProductInput{Code: "TK-E583", Name: "Impresora", Kind: "equipo", YellowThreshold: 2, GreenThreshold: 4})
	if err != nil || p.Name != "Impresora" || p.GreenThreshold != 4 {
		t.Fatalf("UpdateProduct: %+v %v", p, err)
	}
}

func TestCombos_validationAndCodeCollision(t *testing.T) {
	s := New(setupEquiposDB(t))
	a, b := mustProduct(t, s, "PRINT", 0, 0), mustProduct(t, s, "ROLL", 0, 0)
	if _, err := s.CreateCombo(ComboInput{Code: "PRINT", Items: []ComboItemInput{{ProductID: a.ID, Quantity: 1}}}); err == nil || !strings.Contains(err.Error(), "producto") {
		t.Errorf("el código de un combo no puede ser el de un producto: %v", err)
	}
	if _, err := s.CreateCombo(ComboInput{Code: "C1"}); err == nil || !strings.Contains(err.Error(), "al menos un componente") {
		t.Errorf("combo vacío: %v", err)
	}
	if _, err := s.CreateCombo(ComboInput{Code: "C1", Items: []ComboItemInput{{ProductID: a.ID, Quantity: 0}}}); err == nil {
		t.Error("cantidad 0 debe fallar")
	}
	if _, err := s.CreateCombo(ComboInput{Code: "C1", Items: []ComboItemInput{{ProductID: a.ID, Quantity: 1}, {ProductID: a.ID, Quantity: 2}}}); err == nil || !strings.Contains(err.Error(), "repetido") {
		t.Errorf("componente repetido: %v", err)
	}
	c, err := s.CreateCombo(ComboInput{Code: "C1", Items: []ComboItemInput{{ProductID: a.ID, Quantity: 1}, {ProductID: b.ID, Quantity: 10}}})
	if err != nil || len(c.Components) != 2 || c.Components[1].ProductCode != "ROLL" || c.Components[1].Quantity != 10 {
		t.Fatalf("CreateCombo: %+v %v", c, err)
	}
	// Un producto no puede tomar el código de un combo.
	if _, err := s.CreateProduct(ProductInput{Code: "c1"}); err == nil || !strings.Contains(err.Error(), "ya existe") {
		t.Errorf("producto con código de combo: %v", err)
	}
	u, err := s.UpdateCombo(c.ID, ComboInput{Code: "C1", Items: []ComboItemInput{{ProductID: b.ID, Quantity: 3}}})
	if err != nil || len(u.Components) != 1 || u.Components[0].Quantity != 3 {
		t.Fatalf("UpdateCombo reemplaza los componentes: %+v %v", u, err)
	}
}

func TestCarriers_normalizationAndDefault(t *testing.T) {
	s := New(setupEquiposDB(t))
	sh, err := s.CreateCarrier(CarrierInput{Code: "shalom", Name: "Shalom", DispatchDays: "5, 1,3,1", IsDefault: true})
	if err != nil {
		t.Fatal(err)
	}
	if sh.DispatchDays != "1,3,5" || sh.PickupDays != 15 || !sh.IsDefault || sh.GuideLabel != "N° de guía" {
		t.Fatalf("normalización: %+v", sh)
	}
	ol, err := s.CreateCarrier(CarrierInput{Code: "olva", Name: "Olva", DispatchDays: "2,4", PickupDays: 10, IsDefault: true, GuideFormat: `^\d{10}$`, TrackingURLTpl: "https://x/{guia}"})
	if err != nil {
		t.Fatal(err)
	}
	var first database.EquipCarrier
	s.db.First(&first, sh.ID)
	if first.IsDefault || !ol.IsDefault {
		t.Fatalf("solo uno puede ser el predeterminado: shalom=%v olva=%v", first.IsDefault, ol.IsDefault)
	}
	bad := []CarrierInput{
		{Code: "Shalom 2", Name: "x"}, {Code: "ok", Name: ""}, {Code: "ok", Name: "x", DispatchDays: "9"},
		{Code: "ok", Name: "x", PickupDays: 200}, {Code: "ok", Name: "x", GuideFormat: "("}, {Code: "ok", Name: "x", TrackingURLTpl: "https://x/"},
		{Code: "olva", Name: "dup"},
	}
	for i, in := range bad {
		if _, err := s.CreateCarrier(in); err == nil || !IsValidation(err) {
			t.Errorf("caso inválido %d no falló como validación: %v", i, err)
		}
	}
	off := false
	if _, err := s.UpdateCarrier(ol.ID, CarrierInput{Code: "olva", Name: "Olva", IsDefault: true, Active: &off}); err == nil || !strings.Contains(err.Error(), "por defecto") {
		t.Errorf("el predeterminado no puede quedar inactivo: %v", err)
	}
}

func TestSettings_validation(t *testing.T) {
	s := New(setupEquiposDB(t))
	st, err := s.GetSettings()
	if err != nil || st.AlertYellowDays != 5 || st.AlertRedDays != 12 || st.NextOrderNumber != 1 {
		t.Fatalf("defaults: %+v %v", st, err)
	}
	if _, err := s.UpdateSettings(SettingsInput{AlertYellowDays: 12, AlertRedDays: 5, NextOrderNumber: 1}); err == nil {
		t.Error("amarillo ≥ rojo debe fallar")
	}
	s.db.Create(&database.EquipOrder{OrderNumber: 98, OrderDate: time.Now()})
	if _, err := s.UpdateSettings(SettingsInput{AlertYellowDays: 5, AlertRedDays: 12, NextOrderNumber: 50}); err == nil || !strings.Contains(err.Error(), "98") {
		t.Errorf("el siguiente número no puede pisar pedidos existentes: %v", err)
	}
	if st, err = s.UpdateSettings(SettingsInput{AlertYellowDays: 4, AlertRedDays: 10, NextOrderNumber: 99, StockManager: " Ana "}); err != nil || st.StockManager != "Ana" {
		t.Fatalf("UpdateSettings: %+v %v", st, err)
	}
}

func TestAddMovement_rules(t *testing.T) {
	s := New(setupEquiposDB(t))
	p := mustProduct(t, s, "ROLL", 20, 40)
	if _, err := s.AddMovement(MovementInput{ProductID: p.ID, MovementType: "ingreso", Quantity: 10}, 1); err == nil || !strings.Contains(err.Error(), "nota") {
		t.Errorf("la nota es obligatoria: %v", err)
	}
	if _, err := s.AddMovement(MovementInput{ProductID: p.ID, MovementType: "ingreso", Quantity: -3, Note: "x"}, 1); err == nil {
		t.Error("un ingreso negativo debe fallar")
	}
	if _, err := s.AddMovement(MovementInput{ProductID: p.ID, MovementType: "salida_pedido", Quantity: 1, Note: "x"}, 1); err == nil || !strings.Contains(err.Error(), "inválido") {
		t.Errorf("las salidas no se cargan a mano: %v", err)
	}
	if _, err := s.AddMovement(MovementInput{ProductID: p.ID, MovementType: "ingreso", Quantity: 1, Note: "x", OccurredAt: ptrTime(time.Now().AddDate(0, 0, 10))}, 1); err == nil {
		t.Error("fecha futura debe fallar")
	}
	if _, err := s.AddMovement(MovementInput{ProductID: 999, MovementType: "ingreso", Quantity: 1, Note: "x"}, 1); err == nil {
		t.Error("producto inexistente debe fallar")
	}
	m, err := s.AddMovement(MovementInput{ProductID: p.ID, MovementType: "baja", Quantity: 4, Note: "se rompieron"}, 1)
	if err != nil || m.Quantity != -4 {
		t.Fatalf("la baja se escribe en positivo y descuenta: %+v %v", m, err)
	}
	if m, err = s.AddMovement(MovementInput{ProductID: p.ID, MovementType: "ajuste", Quantity: -2, Note: "conteo"}, 1); err != nil || m.Quantity != -2 {
		t.Fatalf("ajuste con signo: %+v %v", m, err)
	}
}

func ptrTime(t time.Time) *time.Time { return &t }

func TestStockReport_carriesOpeningAcrossPeriodsAndSemaphore(t *testing.T) {
	s := New(setupEquiposDB(t))
	p := mustProduct(t, s, "ROLL", 20, 40)
	lima := limaLoc()
	at := func(y int, m time.Month, d int) *time.Time { v := time.Date(y, m, d, 12, 0, 0, 0, lima); return &v }
	add := func(typ string, q int, when *time.Time, note string) {
		t.Helper()
		if _, err := s.AddMovement(MovementInput{ProductID: p.ID, MovementType: typ, Quantity: q, OccurredAt: when, Note: note}, 1); err != nil {
			t.Fatal(err)
		}
	}
	add("apertura", 100, at(2026, 8, 1), "inicial agosto")
	add("ingreso", 50, at(2026, 8, 15), "Reposición")
	add("ajuste", -5, at(2026, 8, 20), "conteo")
	// Salidas por pedido (las generan los pedidos): directa y por combo promo.
	s.db.Create(&database.EquipStockMovement{ProductID: p.ID, OccurredAt: *at(2026, 8, 5), MovementType: "salida_pedido", Quantity: -30, SaleTypeSnapshot: "independiente"})
	s.db.Create(&database.EquipStockMovement{ProductID: p.ID, OccurredAt: *at(2026, 8, 6), MovementType: "salida_pedido", Quantity: -10, SaleTypeSnapshot: "promo_tk", ViaComboID: ptrUint(1)})
	s.db.Create(&database.EquipStockMovement{ProductID: p.ID, OccurredAt: *at(2026, 8, 25), MovementType: "reingreso_retorno", Quantity: 2})
	s.db.Create(&database.EquipStockMovement{ProductID: p.ID, OccurredAt: *at(2026, 9, 3), MovementType: "salida_pedido", Quantity: -40, SaleTypeSnapshot: "independiente"})

	aug, err := s.StockReport("2026-08")
	if err != nil {
		t.Fatal(err)
	}
	r := aug[0]
	// 100 + 50 + 2 − 5 − (30 + 10) = 107
	if r.Opening != 100 || r.Ingresos != 50 || r.DirectIndep != 30 || r.ComboPromo != 10 || r.TotalOut != 40 || r.Reentries != 2 || r.Adjustments != -5 || r.Current != 107 {
		t.Fatalf("agosto: %+v", r)
	}
	if r.Semaphore != "suficiente" {
		t.Errorf("107 ≥ 40 debe ser suficiente: %s", r.Semaphore)
	}
	sep, _ := s.StockReport("2026-09")
	r = sep[0]
	// El inicial de septiembre es el cierre de agosto; la salida de 40 deja 67.
	if r.Opening != 107 || r.DirectIndep != 40 || r.Current != 67 {
		t.Fatalf("septiembre: %+v", r)
	}
	oct, _ := s.StockReport("2026-10")
	if oct[0].Opening != 67 || oct[0].Current != 67 || oct[0].Semaphore != "suficiente" {
		t.Fatalf("octubre: %+v", oct[0])
	}
	for stock, want := range map[int]string{45: "suficiente", 40: "suficiente", 39: "moderado", 20: "moderado", 19: "bajo", -3: "bajo"} {
		if got := semaphore(stock, 20, 40); got != want {
			t.Errorf("semáforo(%d) = %s, want %s", stock, got, want)
		}
	}
	if _, err := s.StockReport("agosto"); err == nil || !IsValidation(err) {
		t.Errorf("período inválido: %v", err)
	}
	mv, err := s.ProductMovements(p.ID, 3)
	if err != nil || len(mv) != 3 || mv[0].OccurredAt.Before(mv[1].OccurredAt) {
		t.Fatalf("kardex: %+v %v", mv, err)
	}
}

func ptrUint(v uint) *uint { return &v }

// gorm aplica el default:true de la columna cuando Active es false al insertar: un producto o
// transportista creado como inactivo debe quedar inactivo.
func TestCreate_inactiveStaysInactive(t *testing.T) {
	s := New(setupEquiposDB(t))
	off := false
	p, err := s.CreateProduct(ProductInput{Code: "OFF1", Name: "Off", Active: &off})
	if err != nil {
		t.Fatal(err)
	}
	var got database.EquipProduct
	if err := s.db.First(&got, p.ID).Error; err != nil || got.Active {
		t.Fatalf("producto creado inactivo quedó active=%v (err %v)", got.Active, err)
	}
	c, err := s.CreateCarrier(CarrierInput{Code: "OFFC", Name: "Off", Active: &off})
	if err != nil {
		t.Fatal(err)
	}
	var gotC database.EquipCarrier
	if err := s.db.First(&gotC, c.ID).Error; err != nil || gotC.Active {
		t.Fatalf("transportista creado inactivo quedó active=%v (err %v)", gotC.Active, err)
	}
}
