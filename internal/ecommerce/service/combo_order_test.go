package service

import (
	"encoding/json"
	"strings"
	"testing"

	"tukifac/pkg/database"
	"tukifac/pkg/stockreserve"

	"gorm.io/gorm"
)

// setupComboOrderDB universo de la conversión (ventas + pedido) más las tablas del combo.
func setupComboOrderDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := setupConvertTestDB(t)
	for _, m := range []interface{}{
		&database.TenantComboGroup{}, &database.TenantComboGroupItem{},
		&database.TenantProductPresentation{}, &database.TenantProductPresentationStock{},
		&database.TenantEcommerceCustomerAccount{}, &database.TenantEcommerceCustomerAddress{},
	} {
		if err := db.AutoMigrate(m); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func stockedComponent(t *testing.T, db *gorm.DB, code string, qty float64) database.TenantProduct {
	t.Helper()
	p := database.TenantProduct{
		Code: code, Name: "Comp " + code, Type: "product", Unit: "NIU", SalePrice: 10,
		IgvAffectationType: "10", PriceIncludesIgv: true, ManageStock: true, BranchID: 1, Active: true,
	}
	if err := db.Create(&p).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.TenantProductStock{ProductID: p.ID, BranchID: 1, Quantity: qty}).Error; err != nil {
		t.Fatal(err)
	}
	return p
}

// seedStoreCombo combo publicado: un grupo fijo (polo) + un grupo «elige uno» (pantalón o short, short +5).
func seedStoreCombo(t *testing.T, db *gorm.DB, polo, pantalon, short database.TenantProduct) (combo database.TenantProduct, fixedGroup, chooseGroup database.TenantComboGroup) {
	t.Helper()
	combo = database.TenantProduct{
		Code: "COMBO-T", Name: "Combo verano", Type: "product", Unit: "NIU", SalePrice: 40,
		IgvAffectationType: "10", PriceIncludesIgv: true, BranchID: 1, HasCombo: true,
		Active: true, ShowInDigitalCatalog: true,
	}
	if err := db.Create(&combo).Error; err != nil {
		t.Fatal(err)
	}
	fixedGroup = database.TenantComboGroup{ProductID: combo.ID, Name: "Incluye", SelectionType: database.ComboSelectionFixed, MinSelect: 1, MaxSelect: 1, Active: true}
	chooseGroup = database.TenantComboGroup{ProductID: combo.ID, Name: "Elige", SelectionType: database.ComboSelectionSingle, MinSelect: 1, MaxSelect: 1, SortOrder: 1, Active: true}
	for _, g := range []*database.TenantComboGroup{&fixedGroup, &chooseGroup} {
		if err := db.Create(g).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, it := range []database.TenantComboGroupItem{
		{GroupID: fixedGroup.ID, ProductID: polo.ID, DefaultQuantity: 1, MaxQuantity: 1, Active: true},
		{GroupID: chooseGroup.ID, ProductID: pantalon.ID, DefaultQuantity: 1, MaxQuantity: 1, IsDefault: true, Active: true},
		{GroupID: chooseGroup.ID, ProductID: short.ID, DefaultQuantity: 1, MaxQuantity: 1, ExtraPrice: 5, Active: true},
	} {
		if err := db.Create(&it).Error; err != nil {
			t.Fatal(err)
		}
	}
	return combo, fixedGroup, chooseGroup
}

func comboSelection(t *testing.T, groupID, productID uint) string {
	t.Helper()
	b, _ := json.Marshal([]map[string]any{{"group_id": groupID, "items": []map[string]any{{"product_id": productID, "quantity": 1}}}})
	return string(b)
}

func TestComboPedido_PrecioYComponentesSeGuardan(t *testing.T) {
	db := setupComboOrderDB(t)
	polo := stockedComponent(t, db, "POLO", 10)
	pant := stockedComponent(t, db, "PANT", 10)
	short := stockedComponent(t, db, "SHORT", 10)
	combo, _, choose := seedStoreCombo(t, db, polo, pant, short)
	svc := &EcommerceService{db: db}

	o, _, err := svc.CreateOrder(CreateOrderInput{
		CustomerName: "Ana", CustomerPhone: "999111222", DeliveryMethod: DeliveryMethodPickup,
		Items: []CreateOrderItemInput{{ProductID: combo.ID, Quantity: 2, ComboJSON: comboSelection(t, choose.ID, short.ID)}},
	})
	if err != nil {
		t.Fatalf("CreateOrder: %v", err)
	}
	var item database.TenantEcommerceOrderItem
	if err := db.Where("order_id = ?", o.ID).First(&item).Error; err != nil {
		t.Fatal(err)
	}
	if item.UnitPrice != 45 {
		t.Errorf("precio = %v, quería 45 (base 40 + 5 del short)", item.UnitPrice)
	}
	comps := stockreserve.ParseComponents(item.ComboComponentsJSON)
	if len(comps) != 2 {
		t.Fatalf("componentes = %d, quería 2: %s", len(comps), item.ComboComponentsJSON)
	}
	if o.Total != 90 {
		t.Errorf("total = %v, quería 90", o.Total)
	}
}

func TestComboPedido_EleccionInvalidaSeRechaza(t *testing.T) {
	db := setupComboOrderDB(t)
	polo := stockedComponent(t, db, "POLO", 10)
	pant := stockedComponent(t, db, "PANT", 10)
	short := stockedComponent(t, db, "SHORT", 10)
	other := stockedComponent(t, db, "OTRO", 10)
	combo, _, choose := seedStoreCombo(t, db, polo, pant, short)
	svc := &EcommerceService{db: db}
	mk := func(sel string) error {
		_, _, err := svc.CreateOrder(CreateOrderInput{
			CustomerName: "Ana", CustomerPhone: "999111222", DeliveryMethod: DeliveryMethodPickup,
			Items: []CreateOrderItemInput{{ProductID: combo.ID, Quantity: 1, ComboJSON: sel}},
		})
		return err
	}
	if err := mk(""); err == nil {
		t.Error("un combo sin elección del grupo «elige uno» debe rechazarse")
	}
	if err := mk(comboSelection(t, choose.ID, other.ID)); err == nil {
		t.Error("un producto que no pertenece al grupo debe rechazarse")
	}
	if err := mk(comboSelection(t, choose.ID, pant.ID)); err != nil {
		t.Errorf("la elección válida debe aceptarse: %v", err)
	}
}

func TestComboPedido_ReservaYBloqueoPorComponente(t *testing.T) {
	db := setupComboOrderDB(t)
	polo := stockedComponent(t, db, "POLO", 3)
	pant := stockedComponent(t, db, "PANT", 10)
	short := stockedComponent(t, db, "SHORT", 10)
	combo, _, choose := seedStoreCombo(t, db, polo, pant, short)
	svc := &EcommerceService{db: db}
	newOrder := func(qty float64) *database.TenantEcommerceOrder {
		o, _, err := svc.CreateOrder(CreateOrderInput{
			CustomerName: "Ana", CustomerPhone: "999111222", DeliveryMethod: DeliveryMethodPickup,
			Items: []CreateOrderItemInput{{ProductID: combo.ID, Quantity: qty, ComboJSON: comboSelection(t, choose.ID, pant.ID)}},
		})
		if err != nil {
			t.Fatal(err)
		}
		return o
	}

	a := newOrder(2)
	if err := confirmIn(svc, a.ID, 1); err != nil {
		t.Fatalf("2 combos con 3 polos deben confirmarse: %v", err)
	}
	if r, _ := stockreserve.ReservedFor(db, 1, 0, polo.ID, nil); r != 2 {
		t.Errorf("polos reservados = %v, quería 2", r)
	}
	if r, _ := stockreserve.ReservedFor(db, 1, 0, pant.ID, nil); r != 2 {
		t.Errorf("pantalones reservados = %v, quería 2", r)
	}
	b := newOrder(2)
	err := confirmIn(svc, b.ID, 1)
	if err == nil || !strings.Contains(err.Error(), "stock insuficiente") {
		t.Fatalf("faltan polos para otros 2 combos (queda 1): err=%v", err)
	}
	c := newOrder(1)
	if err := confirmIn(svc, c.ID, 1); err != nil {
		t.Fatalf("el último polo disponible debe alcanzar: %v", err)
	}
}

func TestComboPedido_ConvertirEnVentaDescuentaComponentes(t *testing.T) {
	db := setupComboOrderDB(t)
	polo := stockedComponent(t, db, "POLO", 5)
	pant := stockedComponent(t, db, "PANT", 5)
	short := stockedComponent(t, db, "SHORT", 5)
	combo, _, choose := seedStoreCombo(t, db, polo, pant, short)
	series := seedNotaVentaSeries(t, db)
	svc := &EcommerceService{db: db}

	o, _, err := svc.CreateOrder(CreateOrderInput{
		CustomerName: "Ana", CustomerPhone: "999111222", DeliveryMethod: DeliveryMethodPickup,
		Items: []CreateOrderItemInput{{ProductID: combo.ID, Quantity: 2, ComboJSON: comboSelection(t, choose.ID, pant.ID)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := confirmIn(svc, o.ID, 1); err != nil {
		t.Fatal(err)
	}
	conv, input := convertServiceAndInput(db, series.ID, 1)
	sale, err := conv.ConvertToSale(o.ID, input)
	if err != nil {
		t.Fatalf("ConvertToSale: %v", err)
	}
	if sale.Total != 80 {
		t.Errorf("total de la venta = %v, quería 80", sale.Total)
	}
	stockOf := func(id uint) float64 {
		var st database.TenantProductStock
		db.Where("product_id = ? AND branch_id = 1", id).First(&st)
		return st.Quantity
	}
	if got := stockOf(polo.ID); got != 3 {
		t.Errorf("stock del polo = %v, quería 3", got)
	}
	if got := stockOf(pant.ID); got != 3 {
		t.Errorf("stock del pantalón = %v, quería 3", got)
	}
	if got := stockOf(short.ID); got != 5 {
		t.Errorf("el short no se eligió y no debe tocarse: %v", got)
	}
	// Ya convertido: no debe seguir reservando.
	if r, _ := stockreserve.ReservedFor(db, 1, 0, polo.ID, nil); r != 0 {
		t.Errorf("tras la venta no debe quedar reserva, hay %v", r)
	}
}

func TestComboPublico_ExponeGruposYDisponibilidad(t *testing.T) {
	db := setupComboOrderDB(t)
	polo := stockedComponent(t, db, "POLO", 10)
	pant := stockedComponent(t, db, "PANT", 10)
	short := stockedComponent(t, db, "SHORT", 0) // sin stock
	combo, _, choose := seedStoreCombo(t, db, polo, pant, short)
	svc := &EcommerceService{db: db}

	items, _, err := svc.PublicProducts("COMBO", 0, nil, nil, 0, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	var found *PublicProduct
	for i := range items {
		if items[i].ID == combo.ID {
			found = &items[i]
		}
	}
	if found == nil || found.Combo == nil {
		t.Fatalf("el combo debe aparecer en la tienda con su configuración: %+v", items)
	}
	if len(found.Combo.Groups) != 2 || !found.Combo.Available {
		t.Fatalf("grupos = %d, available = %v", len(found.Combo.Groups), found.Combo.Available)
	}
	for _, g := range found.Combo.Groups {
		if g.ID != choose.ID {
			continue
		}
		for _, o := range g.Options {
			if o.ProductID == short.ID && o.Available {
				t.Error("el short sin stock debe verse como no disponible")
			}
			if o.ProductID == pant.ID && !o.Available {
				t.Error("el pantalón con stock debe estar disponible")
			}
		}
	}
}

func TestComboPedido_RotuloYNotaMuestranLoQueLleva(t *testing.T) {
	db := setupComboOrderDB(t)
	polo := stockedComponent(t, db, "POLO", 10)
	pant := stockedComponent(t, db, "PANT", 10)
	short := stockedComponent(t, db, "SHORT", 10)
	combo, _, choose := seedStoreCombo(t, db, polo, pant, short)
	if err := db.AutoMigrate(&database.TenantCompanyConfig{}, &database.UbiRegion{}, &database.UbiProvincia{}, &database.UbiDistrito{}); err != nil {
		t.Fatal(err)
	}
	svc := &EcommerceService{db: db}
	o, _, err := svc.CreateOrder(CreateOrderInput{
		CustomerName: "Ana", CustomerPhone: "999111222", DeliveryMethod: DeliveryMethodPickup,
		Items: []CreateOrderItemInput{{ProductID: combo.ID, Quantity: 1, ComboJSON: comboSelection(t, choose.ID, pant.ID)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	db.Model(&database.TenantEcommerceOrder{}).Where("id = ?", o.ID).Update("status", OrderStatusListoParaDespacho)
	label, err := BuildLabelDataForOrder(db, o.ID)
	if err != nil {
		t.Fatalf("rótulo: %v", err)
	}
	want := "Combo verano (Comp POLO, Comp PANT)"
	if len(label.Items) != 1 || label.Items[0].Name != want {
		t.Errorf("rótulo: %+v, esperaba %q", label.Items, want)
	}
	pd, err := BuildPrintDataForOrder(db, o.ID)
	if err != nil {
		t.Fatalf("nota: %v", err)
	}
	if len(pd.Items) != 1 || pd.Items[0].ItemNote != "Incluye: Comp POLO, Comp PANT" {
		t.Errorf("nota de la línea: %+v", pd.Items)
	}
}
