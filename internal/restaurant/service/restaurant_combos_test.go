package service

import (
	"encoding/json"
	"strings"
	"testing"

	invsvc "tukifac/internal/inventory/service"
	"tukifac/pkg/database"

	"gorm.io/gorm"
)

// setupComboOrderTestDB extiende el fixture de mesas con catálogo y combos.
func setupComboOrderTestDB(t *testing.T) (*gorm.DB, *database.TenantRestaurantTable) {
	t.Helper()
	db, table := setupTableSessionTestDB(t)
	if err := db.AutoMigrate(
		&database.TenantProduct{},
		&database.TenantPreparationArea{},
		&database.TenantComboGroup{},
		&database.TenantComboGroupItem{},
		// resolveRestaurantOrderItem consulta presentaciones y extras de todo producto.
		&database.TenantProductPresentation{},
		&database.TenantModifierGroup{},
		&database.TenantModifierOption{},
		&database.TenantProductModifierGroup{},
	); err != nil {
		t.Fatal(err)
	}
	return db, table
}

type comboFixture struct {
	Combo   database.TenantProduct
	Pollo   database.TenantProduct
	Agua    database.TenantProduct
	Gaseosa database.TenantProduct
	Papas   database.TenantProduct
	BebidaG database.TenantComboGroup
	GuarniG database.TenantComboGroup
}

// seedComboFamiliar arma el Combo Familiar: pollo fijo (cocina) + bebida a elegir (bar).
func seedComboFamiliar(t *testing.T, db *gorm.DB) comboFixture {
	t.Helper()
	areas := []database.TenantPreparationArea{
		{Name: "Cocina", Slug: "cocina", Active: true},
		{Name: "Bar", Slug: "bar", Active: true},
	}
	for i := range areas {
		if err := db.Create(&areas[i]).Error; err != nil {
			t.Fatal(err)
		}
	}
	cocinaID, barID := areas[0].ID, areas[1].ID

	newProduct := func(name, code string, price float64, areaID uint) database.TenantProduct {
		p := database.TenantProduct{
			Code: code, Name: name, Type: "product", Unit: "NIU", SalePrice: price,
			IgvAffectationType: "10", PriceIncludesIgv: true, IsRestaurant: true, BranchID: 1,
			PreparationAreaID: &areaID, Active: true,
		}
		if err := db.Create(&p).Error; err != nil {
			t.Fatal(err)
		}
		return p
	}

	f := comboFixture{
		Pollo:   newProduct("Pollo a la brasa", "POLLO", 20, cocinaID),
		Agua:    newProduct("Agua mineral", "AGUA", 2.50, barID),
		Gaseosa: newProduct("Gaseosa", "GAS", 4, barID),
		Papas:   newProduct("Papas fritas", "PAPAS", 6, cocinaID),
	}

	combo := database.TenantProduct{
		Code: "COMBO-FAM", Name: "Combo Familiar", Type: "product", Unit: "NIU",
		SalePrice: 18, IgvAffectationType: "10", PriceIncludesIgv: true,
		IsRestaurant: true, BranchID: 1, HasCombo: true, Active: true,
	}
	if err := db.Create(&combo).Error; err != nil {
		t.Fatal(err)
	}
	f.Combo = combo

	plato := database.TenantComboGroup{
		ProductID: combo.ID, Name: "Plato principal", SelectionType: database.ComboSelectionFixed,
		MinSelect: 1, MaxSelect: 1, SortOrder: 0, Active: true,
	}
	bebida := database.TenantComboGroup{
		ProductID: combo.ID, Name: "Bebida", SelectionType: database.ComboSelectionSingle,
		MinSelect: 1, MaxSelect: 1, SortOrder: 1, Active: true,
	}
	for _, g := range []*database.TenantComboGroup{&plato, &bebida} {
		if err := db.Create(g).Error; err != nil {
			t.Fatal(err)
		}
	}
	f.BebidaG = bebida

	items := []database.TenantComboGroupItem{
		{GroupID: plato.ID, ProductID: f.Pollo.ID, PreparationAreaID: &cocinaID, DefaultQuantity: 1, MaxQuantity: 1, Active: true},
		{GroupID: bebida.ID, ProductID: f.Agua.ID, PreparationAreaID: &barID, DefaultQuantity: 1, MaxQuantity: 1, IsDefault: true, Active: true},
		{GroupID: bebida.ID, ProductID: f.Gaseosa.ID, PreparationAreaID: &barID, DefaultQuantity: 1, MaxQuantity: 1, ExtraPrice: 1.50, Active: true},
	}
	for i := range items {
		if err := db.Create(&items[i]).Error; err != nil {
			t.Fatal(err)
		}
	}
	return f
}

func comboSelectionJSON(t *testing.T, groupID, productID uint, qty float64) string {
	t.Helper()
	sel := []comboSelectionInput{{GroupID: groupID, Items: []comboSelectionItemInput{{ProductID: productID, Quantity: qty}}}}
	b, err := json.Marshal(sel)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// TestAddOrder_ComboExplodesByPreparationArea: el pollo va a cocina y el agua al bar,
// en comandas separadas atadas por el mismo combo_parent_key.
func TestAddOrder_ComboExplodesByPreparationArea(t *testing.T) {
	db, table := setupComboOrderTestDB(t)
	f := seedComboFamiliar(t, db)
	svc := New(db)

	sess, err := svc.OpenTableExtended(openInput(table.ID))
	if err != nil {
		t.Fatal(err)
	}

	comboID := f.Combo.ID
	_, err = svc.AddOrder(sess.ID, nil, 1, []NewOrderItem{{
		ProductID: &comboID, Quantity: 1,
		ComboJSON: comboSelectionJSON(t, f.BebidaG.ID, f.Agua.ID, 1),
	}}, "")
	if err != nil {
		t.Fatalf("AddOrder: %v", err)
	}

	var comandas []database.TenantComanda
	db.Where("session_id = ?", sess.ID).Order("id ASC").Find(&comandas)
	if len(comandas) != 2 {
		t.Fatalf("esperaba 2 comandas (una por componente), got %d", len(comandas))
	}

	byArea := map[string]database.TenantComanda{}
	for _, c := range comandas {
		byArea[c.PreparationArea] = c
	}
	pollo, okCocina := byArea["cocina"]
	agua, okBar := byArea["bar"]
	if !okCocina || !okBar {
		t.Fatalf("esperaba una comanda en cocina y otra en bar, got áreas %v", byArea)
	}
	if pollo.ProductName != "Pollo a la brasa" {
		t.Errorf("cocina: esperaba el pollo, got %q", pollo.ProductName)
	}
	if agua.ProductName != "Agua mineral" {
		t.Errorf("bar: esperaba el agua, got %q", agua.ProductName)
	}

	// El área debe quedar vinculada por id, no solo por slug.
	if pollo.PreparationAreaID == nil || agua.PreparationAreaID == nil {
		t.Fatal("esperaba preparation_area_id en las comandas del combo")
	}
	if *pollo.PreparationAreaID == *agua.PreparationAreaID {
		t.Error("pollo y agua deben rutear a áreas distintas")
	}

	// Todas atadas por el mismo combo, y sin dinero: el precio vive en la línea de venta.
	if pollo.ComboParentKey == "" || pollo.ComboParentKey != agua.ComboParentKey {
		t.Errorf("esperaba el mismo combo_parent_key, got %q y %q", pollo.ComboParentKey, agua.ComboParentKey)
	}
	for _, c := range comandas {
		if c.UnitPrice != 0 {
			t.Errorf("%s: las comandas de componentes van a 0, got %.2f", c.ProductName, c.UnitPrice)
		}
	}

	// El total de la sesión cobra el combo una sola vez.
	var after database.TenantTableSession
	db.First(&after, sess.ID)
	if after.TotalAmount != 18 {
		t.Errorf("total de sesión = %.2f, want 18.00", after.TotalAmount)
	}
}

// TestAddOrder_ComboQuantityMultipliesComponents: 3 combos = 3 pollos y 3 aguas.
func TestAddOrder_ComboQuantityMultipliesComponents(t *testing.T) {
	db, table := setupComboOrderTestDB(t)
	f := seedComboFamiliar(t, db)
	svc := New(db)

	sess, _ := svc.OpenTableExtended(openInput(table.ID))
	comboID := f.Combo.ID
	if _, err := svc.AddOrder(sess.ID, nil, 1, []NewOrderItem{{
		ProductID: &comboID, Quantity: 3,
		ComboJSON: comboSelectionJSON(t, f.BebidaG.ID, f.Agua.ID, 1),
	}}, ""); err != nil {
		t.Fatalf("AddOrder: %v", err)
	}

	var comandas []database.TenantComanda
	db.Where("session_id = ?", sess.ID).Find(&comandas)
	for _, c := range comandas {
		if c.Quantity != 3 {
			t.Errorf("%s: esperaba cantidad 3, got %g", c.ProductName, c.Quantity)
		}
	}
	var after database.TenantTableSession
	db.First(&after, sess.ID)
	if after.TotalAmount != 54 {
		t.Errorf("total = %.2f, want 54.00 (3 x 18)", after.TotalAmount)
	}
}

// TestAddOrder_ComboExtraPriceApplies: elegir gaseosa sube el combo de 18 a 19.50.
func TestAddOrder_ComboExtraPriceApplies(t *testing.T) {
	db, table := setupComboOrderTestDB(t)
	f := seedComboFamiliar(t, db)
	svc := New(db)

	sess, _ := svc.OpenTableExtended(openInput(table.ID))
	comboID := f.Combo.ID
	if _, err := svc.AddOrder(sess.ID, nil, 1, []NewOrderItem{{
		ProductID: &comboID, Quantity: 1,
		ComboJSON: comboSelectionJSON(t, f.BebidaG.ID, f.Gaseosa.ID, 1),
	}}, ""); err != nil {
		t.Fatalf("AddOrder: %v", err)
	}

	var comandas []database.TenantComanda
	db.Where("session_id = ?", sess.ID).Find(&comandas)
	lines := comandasToBillLines(comandas)
	if len(lines) != 1 {
		t.Fatalf("esperaba 1 línea cobrable, got %d", len(lines))
	}
	if lines[0].UnitPrice != 19.50 {
		t.Errorf("precio = %.2f, want 19.50 (18 + 1.50 de la gaseosa)", lines[0].UnitPrice)
	}
}

func TestResolveComboOrderItem_SelectionValidations(t *testing.T) {
	db, _ := setupComboOrderTestDB(t)
	f := seedComboFamiliar(t, db)
	comboID := f.Combo.ID

	cases := []struct {
		name    string
		combo   string
		wantErr string
	}{
		{
			name:    "sin elegir bebida",
			combo:   "",
			wantErr: "debe elegir una opción",
		},
		{
			name:    "producto que no pertenece al grupo",
			combo:   comboSelectionJSON(t, f.BebidaG.ID, f.Papas.ID, 1),
			wantErr: "no pertenece",
		},
		{
			name:    "combo_json corrupto",
			combo:   "{no es json}",
			wantErr: "combo_json inválido",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			item := &NewOrderItem{ProductID: &comboID, Quantity: 1, ComboJSON: tc.combo}
			product, err := resolveRestaurantOrderItem(db, item)
			if err != nil {
				t.Fatal(err)
			}
			_, err = resolveComboOrderItem(db, item, product)
			if err == nil {
				t.Fatalf("esperaba error con %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %q, esperaba que contuviera %q", err.Error(), tc.wantErr)
			}
		})
	}
}

// TestResolveComboOrderItem_NonComboIsIgnored: un producto normal no pasa por el motor de combos.
func TestResolveComboOrderItem_NonComboIsIgnored(t *testing.T) {
	db, _ := setupComboOrderTestDB(t)
	f := seedComboFamiliar(t, db)

	polloID := f.Pollo.ID
	item := &NewOrderItem{ProductID: &polloID, Quantity: 1}
	product, err := resolveRestaurantOrderItem(db, item)
	if err != nil {
		t.Fatal(err)
	}
	drafts, err := resolveComboOrderItem(db, item, product)
	if err != nil {
		t.Fatalf("un producto normal no debe fallar: %v", err)
	}
	if drafts != nil {
		t.Errorf("esperaba nil para un producto sin combo, got %d drafts", len(drafts))
	}
}

// TestGetPrecuenta_ComboShowsFixedPrice: la precuenta debe cuadrar con lo que se cobrará.
// Si mostrara los componentes a 0, el cliente vería el combo gratis.
func TestGetPrecuenta_ComboShowsFixedPrice(t *testing.T) {
	db, table := setupComboOrderTestDB(t)
	f := seedComboFamiliar(t, db)
	svc := New(db)

	sess, _ := svc.OpenTableExtended(openInput(table.ID))
	comboID := f.Combo.ID
	if _, err := svc.AddOrder(sess.ID, nil, 1, []NewOrderItem{{
		ProductID: &comboID, Quantity: 2,
		ComboJSON: comboSelectionJSON(t, f.BebidaG.ID, f.Agua.ID, 1),
	}}, ""); err != nil {
		t.Fatalf("AddOrder: %v", err)
	}

	pre, err := svc.GetPrecuenta(sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(pre.Lines) != 1 {
		t.Fatalf("esperaba 1 línea (el combo, no sus componentes), got %d", len(pre.Lines))
	}
	line := pre.Lines[0]
	if line.ProductName != "Combo Familiar" {
		t.Errorf("nombre = %q, want Combo Familiar", line.ProductName)
	}
	if line.UnitPrice != 18 || line.Quantity != 2 {
		t.Errorf("esperaba 2 x 18.00, got %g x %.2f", line.Quantity, line.UnitPrice)
	}
	if pre.Total != 36 {
		t.Errorf("total = %.2f, want 36.00", pre.Total)
	}
	// Los componentes se listan como detalle de la línea, no como ítems cobrables.
	if !strings.Contains(line.ModifiersJSON, "Pollo a la brasa") ||
		!strings.Contains(line.ModifiersJSON, "Agua mineral") {
		t.Errorf("esperaba los componentes en el detalle de la línea, got %s", line.ModifiersJSON)
	}
}

// TestComandasToBillLines_IdenticalCombosMerge: dos combos iguales pedidos en rondas
// distintas se funden en una línea, como el resto del flujo.
func TestComandasToBillLines_IdenticalCombosMerge(t *testing.T) {
	db, table := setupComboOrderTestDB(t)
	f := seedComboFamiliar(t, db)
	svc := New(db)

	sess, _ := svc.OpenTableExtended(openInput(table.ID))
	comboID := f.Combo.ID
	sel := comboSelectionJSON(t, f.BebidaG.ID, f.Agua.ID, 1)
	for i := 0; i < 2; i++ {
		if _, err := svc.AddOrder(sess.ID, nil, 1, []NewOrderItem{{
			ProductID: &comboID, Quantity: 1, ComboJSON: sel,
		}}, ""); err != nil {
			t.Fatalf("AddOrder %d: %v", i, err)
		}
	}

	var comandas []database.TenantComanda
	db.Where("session_id = ?", sess.ID).Order("id ASC").Find(&comandas)
	if len(comandas) != 4 {
		t.Fatalf("esperaba 4 comandas (2 combos x 2 componentes), got %d", len(comandas))
	}

	lines := comandasToBillLines(comandas)
	if len(lines) != 2 {
		t.Fatalf("esperaba 2 líneas (una por combo pedido), got %d", len(lines))
	}
	if lines[0].Key != lines[1].Key {
		t.Errorf("combos idénticos deben compartir clave para fundirse: %q vs %q", lines[0].Key, lines[1].Key)
	}
	for _, l := range lines {
		if !l.IsCombo || l.Quantity != 1 || l.UnitPrice != 18 {
			t.Errorf("línea inesperada: combo=%v qty=%g precio=%.2f", l.IsCombo, l.Quantity, l.UnitPrice)
		}
	}
}

// TestComandasToBillLines_DifferentSelectionsDoNotMerge: agua y gaseosa son combos distintos.
func TestComandasToBillLines_DifferentSelectionsDoNotMerge(t *testing.T) {
	db, table := setupComboOrderTestDB(t)
	f := seedComboFamiliar(t, db)
	svc := New(db)

	sess, _ := svc.OpenTableExtended(openInput(table.ID))
	comboID := f.Combo.ID
	if _, err := svc.AddOrder(sess.ID, nil, 1, []NewOrderItem{
		{ProductID: &comboID, Quantity: 1, ComboJSON: comboSelectionJSON(t, f.BebidaG.ID, f.Agua.ID, 1)},
		{ProductID: &comboID, Quantity: 1, ComboJSON: comboSelectionJSON(t, f.BebidaG.ID, f.Gaseosa.ID, 1)},
	}, ""); err != nil {
		t.Fatalf("AddOrder: %v", err)
	}

	var comandas []database.TenantComanda
	db.Where("session_id = ?", sess.ID).Order("id ASC").Find(&comandas)
	lines := comandasToBillLines(comandas)
	if len(lines) != 2 {
		t.Fatalf("esperaba 2 líneas, got %d", len(lines))
	}
	if lines[0].Key == lines[1].Key {
		t.Error("combos con distinta bebida no deben compartir línea")
	}
}

// TestComandasToBillLines_MixedComboAndPlain: un combo y un producto suelto conviven,
// y el orden de las líneas se conserva.
func TestComandasToBillLines_MixedComboAndPlain(t *testing.T) {
	db, table := setupComboOrderTestDB(t)
	f := seedComboFamiliar(t, db)
	svc := New(db)

	sess, _ := svc.OpenTableExtended(openInput(table.ID))
	comboID, papasID := f.Combo.ID, f.Papas.ID
	if _, err := svc.AddOrder(sess.ID, nil, 1, []NewOrderItem{
		{ProductID: &comboID, Quantity: 1, ComboJSON: comboSelectionJSON(t, f.BebidaG.ID, f.Agua.ID, 1)},
		{ProductID: &papasID, Quantity: 2},
	}, ""); err != nil {
		t.Fatalf("AddOrder: %v", err)
	}

	var comandas []database.TenantComanda
	db.Where("session_id = ?", sess.ID).Order("id ASC").Find(&comandas)
	lines := comandasToBillLines(comandas)
	if len(lines) != 2 {
		t.Fatalf("esperaba 2 líneas (combo + papas), got %d", len(lines))
	}
	if !lines[0].IsCombo || lines[0].Name != "Combo Familiar" {
		t.Errorf("la primera línea debe ser el combo, got %q", lines[0].Name)
	}
	if lines[1].IsCombo || lines[1].Name != "Papas fritas" || lines[1].UnitPrice != 6 {
		t.Errorf("la segunda debe ser el producto suelto a 6.00, got %q a %.2f", lines[1].Name, lines[1].UnitPrice)
	}

	// El total de la sesión: 18 del combo + 12 de las papas.
	var after database.TenantTableSession
	db.First(&after, sess.ID)
	if after.TotalAmount != 30 {
		t.Errorf("total = %.2f, want 30.00", after.TotalAmount)
	}
}

// fakeStockRecorder captura los movimientos de kardex sin tocar el inventario real.
type fakeStockRecorder struct {
	moves []invsvc.MovementInput
}

func (f *fakeStockRecorder) RecordMovementTx(_ *gorm.DB, in invsvc.MovementInput) error {
	f.moves = append(f.moves, in)
	return nil
}

// TestRecordComboComponentStock_OnlyComponentsWithManageStock: el combo no mueve stock propio;
// mueve el de cada componente, y solo si ese componente tiene control de stock activado.
func TestRecordComboComponentStock_OnlyComponentsWithManageStock(t *testing.T) {
	db, table := setupComboOrderTestDB(t)
	f := seedComboFamiliar(t, db)
	svc := New(db)

	// El pollo controla stock; el agua no.
	db.Model(&database.TenantProduct{}).Where("id = ?", f.Pollo.ID).Update("manage_stock", true)
	db.Model(&database.TenantProduct{}).Where("id = ?", f.Agua.ID).Update("manage_stock", false)

	sess, _ := svc.OpenTableExtended(openInput(table.ID))
	comboID := f.Combo.ID
	if _, err := svc.AddOrder(sess.ID, nil, 1, []NewOrderItem{{
		ProductID: &comboID, Quantity: 2,
		ComboJSON: comboSelectionJSON(t, f.BebidaG.ID, f.Agua.ID, 1),
	}}, ""); err != nil {
		t.Fatalf("AddOrder: %v", err)
	}

	var comandas []database.TenantComanda
	db.Where("session_id = ?", sess.ID).Order("id ASC").Find(&comandas)

	rec := &fakeStockRecorder{}
	err := recordComboComponentStock(db, rec, comandas, comboStockContext{
		BranchID: 1, Reference: "VENTA/B001-00000001", UserID: 1,
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(rec.moves) != 1 {
		t.Fatalf("esperaba 1 movimiento (solo el pollo), got %d", len(rec.moves))
	}
	mv := rec.moves[0]
	if mv.ProductID != f.Pollo.ID {
		t.Errorf("esperaba mover el stock del pollo (%d), got producto %d", f.Pollo.ID, mv.ProductID)
	}
	if mv.Quantity != 2 {
		t.Errorf("2 combos = 2 pollos, got %g", mv.Quantity)
	}
	if mv.Type != "out" || mv.OperationCode != "SALE" {
		t.Errorf("movimiento inesperado: type=%s op=%s", mv.Type, mv.OperationCode)
	}
	for _, m := range rec.moves {
		if m.ProductID == f.Agua.ID {
			t.Error("el agua no controla stock: no debe tocar el kardex")
		}
		if m.ProductID == f.Combo.ID {
			t.Error("el combo no tiene stock propio: no debe tocar el kardex")
		}
	}
}

// TestRecordComboComponentStock_AccumulatesAcrossCombos: el mismo componente en varios combos
// deja un solo asiento con la cantidad sumada.
func TestRecordComboComponentStock_AccumulatesAcrossCombos(t *testing.T) {
	db, table := setupComboOrderTestDB(t)
	f := seedComboFamiliar(t, db)
	svc := New(db)

	db.Model(&database.TenantProduct{}).Where("id IN ?", []uint{f.Pollo.ID, f.Agua.ID}).Update("manage_stock", true)

	sess, _ := svc.OpenTableExtended(openInput(table.ID))
	comboID := f.Combo.ID
	sel := comboSelectionJSON(t, f.BebidaG.ID, f.Agua.ID, 1)
	for i := 0; i < 2; i++ {
		if _, err := svc.AddOrder(sess.ID, nil, 1, []NewOrderItem{{
			ProductID: &comboID, Quantity: 1, ComboJSON: sel,
		}}, ""); err != nil {
			t.Fatalf("AddOrder %d: %v", i, err)
		}
	}

	var comandas []database.TenantComanda
	db.Where("session_id = ?", sess.ID).Order("id ASC").Find(&comandas)

	rec := &fakeStockRecorder{}
	if err := recordComboComponentStock(db, rec, comandas, comboStockContext{BranchID: 1, UserID: 1}); err != nil {
		t.Fatal(err)
	}
	if len(rec.moves) != 2 {
		t.Fatalf("esperaba 1 asiento por producto (pollo y agua), got %d", len(rec.moves))
	}
	for _, m := range rec.moves {
		if m.Quantity != 2 {
			t.Errorf("producto %d: esperaba cantidad acumulada 2, got %g", m.ProductID, m.Quantity)
		}
	}
}

// TestRecordComboComponentStock_IgnoresPlainComandas: las líneas sueltas ya las descuenta el
// flujo normal por saleItems; contarlas aquí duplicaría la salida.
func TestRecordComboComponentStock_IgnoresPlainComandas(t *testing.T) {
	db, table := setupComboOrderTestDB(t)
	f := seedComboFamiliar(t, db)
	svc := New(db)

	db.Model(&database.TenantProduct{}).Where("id = ?", f.Papas.ID).Update("manage_stock", true)

	sess, _ := svc.OpenTableExtended(openInput(table.ID))
	papasID := f.Papas.ID
	if _, err := svc.AddOrder(sess.ID, nil, 1, []NewOrderItem{{ProductID: &papasID, Quantity: 2}}, ""); err != nil {
		t.Fatalf("AddOrder: %v", err)
	}

	var comandas []database.TenantComanda
	db.Where("session_id = ?", sess.ID).Find(&comandas)

	rec := &fakeStockRecorder{}
	if err := recordComboComponentStock(db, rec, comandas, comboStockContext{BranchID: 1, UserID: 1}); err != nil {
		t.Fatal(err)
	}
	if len(rec.moves) != 0 {
		t.Fatalf("una comanda suelta no debe moverse aquí, got %d movimientos", len(rec.moves))
	}
}

// TestComandasToBillLines_CorruptComboJSONDegrades: si el snapshot es ilegible, la comanda
// se cobra como línea suelta en vez de desaparecer de la cuenta.
func TestComandasToBillLines_CorruptComboJSONDegrades(t *testing.T) {
	comandas := []database.TenantComanda{{
		ID: 1, ProductName: "Pollo", Quantity: 1, UnitPrice: 20,
		ComboParentKey: "abc", ComboJSON: "{roto",
	}}
	lines := comandasToBillLines(comandas)
	if len(lines) != 1 {
		t.Fatalf("esperaba 1 línea, got %d", len(lines))
	}
	if lines[0].IsCombo {
		t.Error("con combo_json ilegible la línea no debe tratarse como combo")
	}
	if lines[0].UnitPrice != 20 {
		t.Errorf("esperaba degradar al precio de la comanda (20.00), got %.2f", lines[0].UnitPrice)
	}
}
