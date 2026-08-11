package service

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"tukifac/internal/catalog/combos"
	"tukifac/pkg/database"
	"tukifac/pkg/tax"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func setupSaleCombosDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	models := []interface{}{
		&database.TenantCompanyConfig{}, &database.TenantDocumentSeries{}, &database.TenantContact{},
		&database.TenantSale{}, &database.TenantSaleItem{}, &database.TenantSalePayment{},
		&database.TenantCashSession{}, &database.TenantPaymentMethod{}, &database.TenantProduct{},
		&database.TenantBranch{}, &database.TenantProductStock{}, &database.TenantStockMovement{},
		&database.TenantInventoryOperationType{},
		&database.TenantComboGroup{}, &database.TenantComboGroupItem{},
	}
	for _, m := range models {
		if err := db.AutoMigrate(m); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Create(&database.TenantCompanyConfig{ID: 1, SunatEnabled: true, TaxRate: 18}).Error; err != nil {
		t.Fatal(err)
	}
	if err := database.SeedInventoryOperationTypes(db); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.TenantPaymentMethod{Code: "cash", Name: "Efectivo", IsSystem: true, Active: true}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.TenantCashSession{
		BranchID: 1, UserID: 1, OpenedBy: 1, Status: "open", OpenedAt: time.Now(),
	}).Error; err != nil {
		t.Fatal(err)
	}
	return db
}

// seedPromocionVerano arma el caso real: polo (S/ 10) + pantalón (S/ 30) vendidos juntos
// como «Promoción verano» a S/ 20.
func seedPromocionVerano(t *testing.T, db *gorm.DB, manageStock bool) (combo, polo, pantalon database.TenantProduct) {
	t.Helper()
	newProduct := func(name, code string, price float64) database.TenantProduct {
		p := database.TenantProduct{
			Code: code, Name: name, Type: "product", Unit: "NIU", SalePrice: price,
			IgvAffectationType: "10", PriceIncludesIgv: true, ManageStock: manageStock,
			BranchID: 1, Active: true,
		}
		if err := db.Create(&p).Error; err != nil {
			t.Fatal(err)
		}
		return p
	}
	polo = newProduct("Polo", "POLO", 10)
	pantalon = newProduct("Pantalón", "PANT", 30)

	combo = database.TenantProduct{
		Code: "PROMO-VERANO", Name: "Promoción verano", Type: "product", Unit: "NIU",
		SalePrice: 20, IgvAffectationType: "10", PriceIncludesIgv: true,
		BranchID: 1, HasCombo: true, Active: true,
	}
	if err := db.Create(&combo).Error; err != nil {
		t.Fatal(err)
	}
	for i, comp := range []database.TenantProduct{polo, pantalon} {
		g := database.TenantComboGroup{
			ProductID: combo.ID, Name: comp.Name, SelectionType: database.ComboSelectionFixed,
			MinSelect: 1, MaxSelect: 1, SortOrder: i, Active: true,
		}
		if err := db.Create(&g).Error; err != nil {
			t.Fatal(err)
		}
		if err := db.Create(&database.TenantComboGroupItem{
			GroupID: g.ID, ProductID: comp.ID, DefaultQuantity: 1, MaxQuantity: 1, Active: true,
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	return combo, polo, pantalon
}

// TestSaleCombo_PrecioDelGrupoNoSumaDeComponentes: el corazón de la promoción. La venta
// debe cobrar los S/ 20 del combo, no los S/ 40 que suman los productos por separado.
func TestSaleCombo_PrecioDelGrupoNoSumaDeComponentes(t *testing.T) {
	db := setupSaleCombosDB(t)
	combo, _, _ := seedPromocionVerano(t, db, false)

	comboID := combo.ID
	items, extra, err := resolveComboItems(db, []SaleItemInput{{
		ProductID: &comboID, Quantity: 1, UnitPrice: 0,
	}})
	if err != nil {
		t.Fatalf("resolveComboItems: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("el combo es una sola línea, got %d", len(items))
	}
	if items[0].UnitPrice != 20 {
		t.Errorf("precio = %.2f, want 20.00 (no 40 = 10 + 30)", items[0].UnitPrice)
	}
	if items[0].Description != "Promoción verano" {
		t.Errorf("descripción = %q", items[0].Description)
	}
	// Los componentes viajan como detalle de la línea.
	if !strings.Contains(items[0].ModifiersJSON, "Polo") ||
		!strings.Contains(items[0].ModifiersJSON, "Pantalón") {
		t.Errorf("esperaba los componentes en el detalle, got %s", items[0].ModifiersJSON)
	}
	// Se proponen los movimientos de ambos componentes; quién controla stock lo filtra
	// después SaleService, que es quien tiene el producto a mano al escribir el kardex.
	if len(extra) != 2 {
		t.Errorf("esperaba proponer los 2 componentes, got %d", len(extra))
	}
}

// TestSaleCombo_StockSaleDeLosComponentes: el combo no tiene stock propio.
func TestSaleCombo_StockSaleDeLosComponentes(t *testing.T) {
	db := setupSaleCombosDB(t)
	combo, polo, pantalon := seedPromocionVerano(t, db, true)

	comboID := combo.ID
	_, extra, err := resolveComboItems(db, []SaleItemInput{{ProductID: &comboID, Quantity: 3}})
	if err != nil {
		t.Fatal(err)
	}
	byProduct := map[uint]float64{}
	for _, mv := range extra {
		byProduct[mv.ProductID] = mv.Quantity
	}
	if byProduct[polo.ID] != 3 || byProduct[pantalon.ID] != 3 {
		t.Errorf("3 promociones = 3 polos y 3 pantalones, got %+v", byProduct)
	}
	if _, ok := byProduct[comboID]; ok {
		t.Error("el combo no tiene stock propio: no debe generar kardex")
	}
}

// TestSaleCombo_ProductoNormalNoSeToca: una venta sin combos pasa intacta.
func TestSaleCombo_ProductoNormalNoSeToca(t *testing.T) {
	db := setupSaleCombosDB(t)
	_, polo, _ := seedPromocionVerano(t, db, false)

	poloID := polo.ID
	original := []SaleItemInput{{ProductID: &poloID, Quantity: 2, UnitPrice: 10, Description: "Polo"}}
	items, extra, err := resolveComboItems(db, original)
	if err != nil {
		t.Fatal(err)
	}
	if items[0].UnitPrice != 10 || items[0].ModifiersJSON != "" {
		t.Errorf("un producto normal no debe tocarse: %+v", items[0])
	}
	if len(extra) != 0 {
		t.Errorf("sin combos no hay kardex extra, got %d", len(extra))
	}
}

// TestSaleCombo_ValidaLaSeleccion: la validación del grupo sigue viva en el camino del ERP.
func TestSaleCombo_ValidaLaSeleccion(t *testing.T) {
	db := setupSaleCombosDB(t)
	combo, polo, pantalon := seedPromocionVerano(t, db, false)

	// Un grupo donde el cliente elige, sin enviar elección → debe rechazar.
	g := database.TenantComboGroup{
		ProductID: combo.ID, Name: "Elige color", SelectionType: database.ComboSelectionSingle,
		MinSelect: 1, MaxSelect: 1, SortOrder: 9, Active: true,
	}
	if err := db.Create(&g).Error; err != nil {
		t.Fatal(err)
	}
	for _, p := range []database.TenantProduct{polo, pantalon} {
		if err := db.Create(&database.TenantComboGroupItem{
			GroupID: g.ID, ProductID: p.ID, DefaultQuantity: 1, MaxQuantity: 1, Active: true,
		}).Error; err != nil {
			t.Fatal(err)
		}
	}

	comboID := combo.ID
	_, _, err := resolveComboItems(db, []SaleItemInput{{ProductID: &comboID, Quantity: 1}})
	if err == nil || !strings.Contains(err.Error(), "debe elegir una opción") {
		t.Fatalf("esperaba rechazo por no elegir, got %v", err)
	}

	// Con la elección enviada, pasa.
	sel, _ := json.Marshal([]combos.Selection{{
		GroupID: g.ID, Items: []combos.SelectionItem{{ProductID: polo.ID, Quantity: 1}},
	}})
	if _, _, err := resolveComboItems(db, []SaleItemInput{{
		ProductID: &comboID, Quantity: 1, ComboJSON: string(sel),
	}}); err != nil {
		t.Fatalf("con la elección enviada debe pasar: %v", err)
	}
}

// TestSaleCombo_EmiteVentaCompleta: end-to-end por SaleService, el camino que usa Tukifac.
func TestSaleCombo_EmiteVentaCompleta(t *testing.T) {
	db := setupSaleCombosDB(t)
	combo, polo, pantalon := seedPromocionVerano(t, db, true)

	series := database.TenantDocumentSeries{
		BranchID: 1, DocType: "Nota de Venta", SunatCode: "00", Series: "NV01", Correlative: 1, Active: true,
	}
	if err := db.Create(&series).Error; err != nil {
		t.Fatal(err)
	}
	// Stock de ambos componentes: los dos controlan stock y los dos salen al vender.
	for _, p := range []database.TenantProduct{polo, pantalon} {
		if err := db.Create(&database.TenantProductStock{ProductID: p.ID, BranchID: 1, Quantity: 10}).Error; err != nil {
			t.Fatal(err)
		}
	}

	comboID := combo.ID
	sale, err := NewSaleService(db).Create(CreateSaleInput{
		BranchID: 1, UserID: 1, SeriesID: series.ID, DocType: "00",
		IssueDate: time.Now(), Currency: "PEN", TaxConfig: tax.DefaultConfig(),
		Payments: []PaymentInput{{Method: "cash", Amount: 20}},
		Items:    []SaleItemInput{{ProductID: &comboID, Quantity: 1, Unit: "NIU", IgvAffectationType: "10", PriceIncludesIgv: true}},
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if sale.Total != 20 {
		t.Errorf("total de la venta = %.2f, want 20.00", sale.Total)
	}

	var items []database.TenantSaleItem
	db.Where("sale_id = ?", sale.ID).Find(&items)
	if len(items) != 1 {
		t.Fatalf("el combo es una sola línea facturable, got %d", len(items))
	}

	// El stock del componente bajó, aunque la línea vendida sea el combo.
	var stock database.TenantProductStock
	db.Where("product_id = ? AND branch_id = ?", polo.ID, 1).First(&stock)
	if stock.Quantity != 9 {
		t.Errorf("stock del polo = %g, want 9 (10 - 1 promoción)", stock.Quantity)
	}
}
