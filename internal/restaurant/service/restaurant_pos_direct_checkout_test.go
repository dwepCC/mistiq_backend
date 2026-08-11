package service

import (
	"testing"

	"tukifac/pkg/database"
	"tukifac/pkg/tax"

	"gorm.io/gorm"
)

// setupDirectCheckoutDB extiende el fixture de combos con lo que necesita emitir una venta.
func setupDirectCheckoutDB(t *testing.T) (*gorm.DB, comboFixture) {
	t.Helper()
	db, _ := setupComboOrderTestDB(t)
	if err := db.AutoMigrate(
		&database.TenantBranch{},
		&database.TenantProductStock{},
		&database.TenantStockMovement{},
		&database.TenantInventoryOperationType{},
		&database.TenantCompanyConfig{},
	); err != nil {
		t.Fatal(err)
	}
	if err := database.SeedInventoryOperationTypes(db); err != nil {
		t.Fatal(err)
	}
	f := seedComboFamiliar(t, db)
	return db, f
}

func directCheckoutInput(f comboFixture, items []NewOrderItem, seriesID uint) POSCheckoutInput {
	return POSCheckoutInput{
		BranchID:  1,
		UserID:    1,
		OrderType: OrderTypeQuickSale,
		Items:     items,
		SeriesID:  seriesID,
		DocType:   "03",
		Payments:  []PaymentInput{{Method: "card", Amount: 20}},
	}
}

func TestIsDirectSaleCheckout(t *testing.T) {
	sessionID := uint(7)
	cases := []struct {
		name string
		in   POSCheckoutInput
		want bool
	}{
		{"venta rápida sin sesión", POSCheckoutInput{OrderType: OrderTypeQuickSale}, true},
		{"sin order_type y sin mesa: se normaliza a venta rápida", POSCheckoutInput{}, true},
		{"venta rápida con sesión previa (borrador POS)", POSCheckoutInput{OrderType: OrderTypeQuickSale, SessionID: &sessionID}, false},
		{"para llevar: puede mandar a cocina", POSCheckoutInput{OrderType: OrderTypeTakeaway}, false},
		{"delivery: puede mandar a cocina", POSCheckoutInput{OrderType: OrderTypeDelivery}, false},
		{"en mesa", POSCheckoutInput{OrderType: OrderTypeDineIn}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isDirectSaleCheckout(tc.in); got != tc.want {
				t.Errorf("isDirectSaleCheckout = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestResolveDirectSaleItems_PlainProducts: el carrito se traduce a líneas de venta con el
// precio del catálogo, sin tocar sesiones ni comandas.
func TestResolveDirectSaleItems_PlainProducts(t *testing.T) {
	db, f := setupDirectCheckoutDB(t)
	svc := NewRestaurantPOSCheckoutService(db)

	polloID, aguaID := f.Pollo.ID, f.Agua.ID
	items, extra, err := svc.resolveDirectSaleItems([]NewOrderItem{
		{ProductID: &polloID, Quantity: 2},
		{ProductID: &aguaID, Quantity: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("esperaba 2 líneas, got %d", len(items))
	}
	if items[0].Description != "Pollo a la brasa" || items[0].UnitPrice != 20 || items[0].Quantity != 2 {
		t.Errorf("línea del pollo inesperada: %+v", items[0])
	}
	if items[0].IgvAffectationType != "10" {
		t.Errorf("esperaba afectación 10, got %q", items[0].IgvAffectationType)
	}
	if len(extra) != 0 {
		t.Errorf("sin combos no debe haber kardex extra, got %d", len(extra))
	}
}

// TestResolveDirectSaleItems_ComboJSONLlegaASaleService: el combo ya no se resuelve aquí.
// Esta capa solo traduce el carrito y propaga la elección; el precio, la validación y el
// stock de componentes los resuelve SaleService, el mismo punto que usa el ERP. Así no hay
// dos motores de precio que puedan divergir.
func TestResolveDirectSaleItems_ComboJSONLlegaASaleService(t *testing.T) {
	db, f := setupDirectCheckoutDB(t)
	svc := NewRestaurantPOSCheckoutService(db)

	comboID := f.Combo.ID
	sel := comboSelectionJSON(t, f.BebidaG.ID, f.Agua.ID, 1)
	items, extra, err := svc.resolveDirectSaleItems([]NewOrderItem{{
		ProductID: &comboID, Quantity: 2, ComboJSON: sel,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("esperaba 1 línea, got %d", len(items))
	}
	if items[0].ComboJSON != sel {
		t.Errorf("la elección del combo debe llegar intacta a SaleService, got %q", items[0].ComboJSON)
	}
	if len(extra) != 0 {
		t.Errorf("el kardex de componentes lo produce SaleService, no esta capa: got %d", len(extra))
	}
}


// TestCheckoutDirect_ComboAloneDeductsComponentStock: regresión. Una venta directa de un combo
// SOLO (sin ninguna otra línea) debe descontar el stock de sus componentes. El bug: el descuento
// de ExtraStockMovements vivía dentro del bucle de ítems, después del `continue` del combo, así
// que con un combo solo no corría nunca y el stock de los componentes quedaba intacto.
func TestCheckoutDirect_ComboAloneDeductsComponentStock(t *testing.T) {
	db, f := setupDirectCheckoutDB(t)
	if err := db.AutoMigrate(&database.TenantSale{}, &database.TenantSaleItem{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.TenantCompanyConfig{SunatEnabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	series := database.TenantDocumentSeries{
		BranchID: 1, DocType: "Boleta", SunatCode: "03", Series: "B001", Correlative: 1, Active: true,
	}
	if err := db.Create(&series).Error; err != nil {
		t.Fatal(err)
	}

	// Los dos componentes controlan stock y arrancan con 10 en la sucursal.
	db.Model(&database.TenantProduct{}).Where("id IN ?", []uint{f.Pollo.ID, f.Agua.ID}).Update("manage_stock", true)
	for _, pid := range []uint{f.Pollo.ID, f.Agua.ID} {
		if err := db.Create(&database.TenantProductStock{ProductID: pid, BranchID: 1, Quantity: 10}).Error; err != nil {
			t.Fatal(err)
		}
	}

	svc := NewRestaurantPOSCheckoutService(db)
	comboID := f.Combo.ID
	in := POSCheckoutInput{
		BranchID: 1, UserID: 1, OrderType: OrderTypeQuickSale, SeriesID: series.ID, DocType: "03",
		Items: []NewOrderItem{{
			ProductID: &comboID, Quantity: 2,
			ComboJSON: comboSelectionJSON(t, f.BebidaG.ID, f.Agua.ID, 1),
		}},
		Payments: []PaymentInput{{Method: "card", Amount: 40}},
	}

	sale, err := svc.Checkout(in, tax.DefaultConfig())
	if err != nil {
		t.Fatalf("Checkout: %v", err)
	}

	// 2 combos = 2 pollos + 2 aguas: el stock de cada componente baja de 10 a 8.
	stockOf := func(pid uint) float64 {
		var s database.TenantProductStock
		db.Where("product_id = ? AND branch_id = ?", pid, 1).First(&s)
		return s.Quantity
	}
	if got := stockOf(f.Pollo.ID); got != 8 {
		t.Errorf("stock del pollo = %g, want 8 (10 - 2 combos)", got)
	}
	if got := stockOf(f.Agua.ID); got != 8 {
		t.Errorf("stock del agua = %g, want 8 (10 - 2 combos)", got)
	}

	// El combo no tiene stock propio: no debe existir un asiento de kardex a su nombre.
	var comboMoves int64
	db.Model(&database.TenantStockMovement{}).Where("product_id = ?", comboID).Count(&comboMoves)
	if comboMoves != 0 {
		t.Errorf("el combo no debe generar kardex propio, got %d movimientos", comboMoves)
	}
	if sale == nil || sale.ID == 0 {
		t.Fatal("esperaba una venta creada")
	}
}

// TestCheckoutDirect_CreatesNoSessionNorComandas: el corazón del cambio. Una venta directa
// no debe dejar sesión, pedido ni comandas: era trabajo que se creaba para borrarlo.
func TestCheckoutDirect_CreatesNoSessionNorComandas(t *testing.T) {
	db, f := setupDirectCheckoutDB(t)
	if err := db.AutoMigrate(&database.TenantSale{}, &database.TenantSaleItem{}); err != nil {
		t.Fatal(err)
	}
	// La caja y los métodos de pago ya vienen sembrados por el fixture de mesas.
	// SaleService exige facturación electrónica habilitada para emitir boleta (03).
	if err := db.Create(&database.TenantCompanyConfig{SunatEnabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	series := database.TenantDocumentSeries{
		BranchID: 1, DocType: "Boleta", SunatCode: "03", Series: "B001", Correlative: 1, Active: true,
	}
	if err := db.Create(&series).Error; err != nil {
		t.Fatal(err)
	}

	svc := NewRestaurantPOSCheckoutService(db)
	polloID := f.Pollo.ID
	in := directCheckoutInput(f, []NewOrderItem{{ProductID: &polloID, Quantity: 1}}, series.ID)

	if !isDirectSaleCheckout(in) {
		t.Fatal("el fixture debe ser una venta directa")
	}

	sale, err := svc.Checkout(in, tax.DefaultConfig())
	if err != nil {
		t.Fatalf("Checkout: %v", err)
	}
	if sale == nil || sale.ID == 0 {
		t.Fatal("esperaba una venta creada")
	}
	if sale.Number == "" {
		t.Error("la venta debe llevar número de comprobante")
	}

	// Lo que motivó el cambio: nada de este andamiaje se crea ya.
	var sessions, orders, comandas int64
	db.Model(&database.TenantTableSession{}).Count(&sessions)
	db.Model(&database.TenantTableOrder{}).Count(&orders)
	db.Model(&database.TenantComanda{}).Count(&comandas)
	if sessions != 0 {
		t.Errorf("una venta directa no debe crear sesión de mesa, got %d", sessions)
	}
	if orders != 0 {
		t.Errorf("una venta directa no debe crear pedido, got %d", orders)
	}
	if comandas != 0 {
		t.Errorf("una venta directa no debe crear comandas, got %d", comandas)
	}

	var itemCount int64
	db.Model(&database.TenantSaleItem{}).Where("sale_id = ?", sale.ID).Count(&itemCount)
	if itemCount != 1 {
		t.Errorf("esperaba 1 línea de venta, got %d", itemCount)
	}
}
