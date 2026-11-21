package service

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"tukifac/pkg/database"
	"tukifac/pkg/tax"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// setupPriceHardeningDB extiende setupSaleCombosDB con las tablas de presentación/modificadores
// necesarias para probar validateAuthorizedPrices contra ese mecanismo de precio.
func setupPriceHardeningDB(t *testing.T) *gorm.DB {
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
		&database.TenantProductPresentation{}, &database.TenantProductPresentationStock{},
		&database.TenantModifierGroup{}, &database.TenantModifierOption{}, &database.TenantProductModifierGroup{},
		&database.TenantProductBranchPrice{},
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

func seedNVSeries(t *testing.T, db *gorm.DB) database.TenantDocumentSeries {
	t.Helper()
	s := database.TenantDocumentSeries{
		BranchID: 1, DocType: "Nota de Venta", SunatCode: "00", Series: "NV01", Correlative: 1, Active: true,
	}
	if err := db.Create(&s).Error; err != nil {
		t.Fatal(err)
	}
	return s
}

func createOK(t *testing.T, db *gorm.DB, series database.TenantDocumentSeries, item SaleItemInput) (*database.TenantSale, error) {
	t.Helper()
	return NewSaleService(db).Create(CreateSaleInput{
		BranchID: 1, UserID: 1, SeriesID: series.ID, DocType: "00",
		IssueDate: time.Now(), Currency: "PEN", TaxConfig: tax.DefaultConfig(),
		Payments: []PaymentInput{{Method: "cash", Amount: item.UnitPrice * item.Quantity}},
		Items:    []SaleItemInput{item},
	})
}

// ---- Precio ----

// Caso 1/12: venta normal con precio correcto → OK.
func TestPriceHardening_PlainProduct_CatalogPrice_OK(t *testing.T) {
	db := setupPriceHardeningDB(t)
	series := seedNVSeries(t, db)
	p := database.TenantProduct{
		Code: "GAS500", Name: "Gaseosa 500ml", Type: "product", Unit: "NIU", SalePrice: 3.5,
		IgvAffectationType: "10", PriceIncludesIgv: true, ManageStock: false, BranchID: 1, Active: true,
	}
	if err := db.Create(&p).Error; err != nil {
		t.Fatal(err)
	}
	pid := p.ID
	_, err := createOK(t, db, series, SaleItemInput{
		ProductID: &pid, Quantity: 1, UnitPrice: 3.5, Unit: "NIU", IgvAffectationType: "10", PriceIncludesIgv: true,
	})
	if err != nil {
		t.Fatalf("precio real de catálogo debe pasar: %v", err)
	}
}

// Caso 2: venta normal con precio manipulado → RECHAZADA.
func TestPriceHardening_PlainProduct_ManipulatedPrice_Rejected(t *testing.T) {
	db := setupPriceHardeningDB(t)
	series := seedNVSeries(t, db)
	p := database.TenantProduct{
		Code: "GAS500", Name: "Gaseosa 500ml", Type: "product", Unit: "NIU", SalePrice: 3.5,
		IgvAffectationType: "10", PriceIncludesIgv: true, ManageStock: false, BranchID: 1, Active: true,
	}
	if err := db.Create(&p).Error; err != nil {
		t.Fatal(err)
	}
	pid := p.ID
	_, err := createOK(t, db, series, SaleItemInput{
		ProductID: &pid, Quantity: 1, UnitPrice: 0.10, Unit: "NIU", IgvAffectationType: "10", PriceIncludesIgv: true,
	})
	if err == nil {
		t.Fatal("esperaba rechazo por precio manipulado (0.10 en vez de 3.50), no hubo error")
	}
	if !strings.Contains(err.Error(), "precio autorizado") {
		t.Errorf("mensaje inesperado: %v", err)
	}
}

// Caso 2b (sales.override_price, incidente 2026-09-17): el mismo precio manipulado del caso 2,
// pero con UserCanOverridePrice=true (como lo resolvería el handler contra el permiso del JWT del
// usuario) → la venta se acepta, sin reevaluar el precio contra el catálogo.
func TestPriceHardening_PlainProduct_ManipulatedPrice_AcceptedWithOverridePermission(t *testing.T) {
	db := setupPriceHardeningDB(t)
	series := seedNVSeries(t, db)
	p := database.TenantProduct{
		Code: "GAS500", Name: "Gaseosa 500ml", Type: "product", Unit: "NIU", SalePrice: 3.5,
		IgvAffectationType: "10", PriceIncludesIgv: true, ManageStock: false, BranchID: 1, Active: true,
	}
	if err := db.Create(&p).Error; err != nil {
		t.Fatal(err)
	}
	pid := p.ID
	item := SaleItemInput{
		ProductID: &pid, Quantity: 1, UnitPrice: 0.10, Unit: "NIU", IgvAffectationType: "10", PriceIncludesIgv: true,
	}
	_, err := NewSaleService(db).Create(CreateSaleInput{
		BranchID: 1, UserID: 1, SeriesID: series.ID, DocType: "00",
		IssueDate: time.Now(), Currency: "PEN", TaxConfig: tax.DefaultConfig(),
		Payments:             []PaymentInput{{Method: "cash", Amount: item.UnitPrice * item.Quantity}},
		Items:                []SaleItemInput{item},
		UserCanOverridePrice: true,
	})
	if err != nil {
		t.Fatalf("con sales.override_price el precio pactado (0.10) debe aceptarse, got err: %v", err)
	}
}

// Caso 3: venta con presentación y precio correcto → OK.
func TestPriceHardening_Presentation_CorrectPrice_OK(t *testing.T) {
	db := setupPriceHardeningDB(t)
	series := seedNVSeries(t, db)
	p := database.TenantProduct{
		Code: "POLO", Name: "Polo", Type: "product", Unit: "NIU", SalePrice: 25,
		IgvAffectationType: "10", PriceIncludesIgv: true, ManageStock: false, HasVariants: true,
		BranchID: 1, Active: true,
	}
	if err := db.Create(&p).Error; err != nil {
		t.Fatal(err)
	}
	pres := database.TenantProductPresentation{ProductID: p.ID, Name: "Rojo/M", SalePrice: 28, Active: true}
	if err := db.Create(&pres).Error; err != nil {
		t.Fatal(err)
	}
	pid, presID := p.ID, pres.ID
	_, err := createOK(t, db, series, SaleItemInput{
		ProductID: &pid, PresentationID: &presID, Quantity: 1, UnitPrice: 28,
		Unit: "NIU", IgvAffectationType: "10", PriceIncludesIgv: true,
	})
	if err != nil {
		t.Fatalf("precio real de la presentación debe pasar: %v", err)
	}
}

// Caso 4: venta con presentación y precio manipulado → RECHAZADA.
func TestPriceHardening_Presentation_ManipulatedPrice_Rejected(t *testing.T) {
	db := setupPriceHardeningDB(t)
	series := seedNVSeries(t, db)
	p := database.TenantProduct{
		Code: "POLO", Name: "Polo", Type: "product", Unit: "NIU", SalePrice: 25,
		IgvAffectationType: "10", PriceIncludesIgv: true, ManageStock: false, HasVariants: true,
		BranchID: 1, Active: true,
	}
	if err := db.Create(&p).Error; err != nil {
		t.Fatal(err)
	}
	pres := database.TenantProductPresentation{ProductID: p.ID, Name: "Rojo/M", SalePrice: 28, Active: true}
	if err := db.Create(&pres).Error; err != nil {
		t.Fatal(err)
	}
	pid, presID := p.ID, pres.ID
	// Manda el precio base del producto (25) en vez del de la presentación elegida (28).
	_, err := createOK(t, db, series, SaleItemInput{
		ProductID: &pid, PresentationID: &presID, Quantity: 1, UnitPrice: 25,
		Unit: "NIU", IgvAffectationType: "10", PriceIncludesIgv: true,
	})
	if err == nil {
		t.Fatal("esperaba rechazo: el precio de la presentación (28) no coincide con el enviado (25)")
	}
}

// seedPoloConExtra arma un producto con presentación (variante) + un grupo de extra (kind=extra)
// vinculado, para probar que validateAuthorizedPrices suma correctamente presentación + extras
// leyendo siempre el ExtraPrice real de BD, nunca el que venga en modifiers_json.
func seedPoloConExtra(t *testing.T, db *gorm.DB) (product database.TenantProduct, presentation database.TenantProductPresentation, option database.TenantModifierOption) {
	t.Helper()
	product = database.TenantProduct{
		Code: "POLO", Name: "Polo", Type: "product", Unit: "NIU", SalePrice: 25,
		IgvAffectationType: "10", PriceIncludesIgv: true, ManageStock: false, HasVariants: true,
		BranchID: 1, Active: true,
	}
	if err := db.Create(&product).Error; err != nil {
		t.Fatal(err)
	}
	presentation = database.TenantProductPresentation{ProductID: product.ID, Name: "Rojo/M", SalePrice: 28, Active: true}
	if err := db.Create(&presentation).Error; err != nil {
		t.Fatal(err)
	}
	group := database.TenantModifierGroup{Name: "Personalización", Kind: "extra", Active: true}
	if err := db.Create(&group).Error; err != nil {
		t.Fatal(err)
	}
	option = database.TenantModifierOption{GroupID: group.ID, Name: "Bordado", ExtraPrice: 5, Active: true}
	if err := db.Create(&option).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.TenantProductModifierGroup{ProductID: product.ID, GroupID: group.ID}).Error; err != nil {
		t.Fatal(err)
	}
	return product, presentation, option
}

// Caso 3b: venta con presentación + extra, precio correcto (28 + 5 = 33) → OK. El precio del
// extra se suma sobre el de la presentación, y siempre se lee de BD (TenantModifierOption),
// nunca del modifiers_json del cliente.
func TestPriceHardening_PresentationPlusExtra_CorrectPrice_OK(t *testing.T) {
	db := setupPriceHardeningDB(t)
	series := seedNVSeries(t, db)
	p, pres, opt := seedPoloConExtra(t, db)
	pid, presID := p.ID, pres.ID
	modifiersJSON := fmt.Sprintf(`[{"type":"modifier","option_id":%d}]`, opt.ID)
	_, err := createOK(t, db, series, SaleItemInput{
		ProductID: &pid, PresentationID: &presID, Quantity: 1, UnitPrice: 33,
		Unit: "NIU", IgvAffectationType: "10", PriceIncludesIgv: true, ModifiersJSON: modifiersJSON,
	})
	if err != nil {
		t.Fatalf("presentación (28) + extra real (5) = 33 debe pasar: %v", err)
	}
}

// Caso 3c: mismo caso, pero el cliente manda un ExtraPrice inflado dentro del propio
// modifiers_json (10 en vez de 5) y ajusta unit_price para que "cuadre" con ese valor falso
// (38). La validación debe ignorar el ExtraPrice del JSON y usar el real de BD (5) → RECHAZADA.
func TestPriceHardening_PresentationPlusExtra_ManipulatedExtraPriceInJSON_Rejected(t *testing.T) {
	db := setupPriceHardeningDB(t)
	series := seedNVSeries(t, db)
	p, pres, opt := seedPoloConExtra(t, db)
	pid, presID := p.ID, pres.ID
	// extra_price:10 es puro adorno: sumModifierExtras solo mira type/option_id y relee de BD.
	modifiersJSON := fmt.Sprintf(`[{"type":"modifier","option_id":%d,"extra_price":10}]`, opt.ID)
	_, err := createOK(t, db, series, SaleItemInput{
		ProductID: &pid, PresentationID: &presID, Quantity: 1, UnitPrice: 38,
		Unit: "NIU", IgvAffectationType: "10", PriceIncludesIgv: true, ModifiersJSON: modifiersJSON,
	})
	if err == nil {
		t.Fatal("esperaba rechazo: el ExtraPrice real en BD es 5, no 10 — el total autorizado es 33, no 38")
	}
}

// Caso 4b: presentación + extra, pero el cliente omite el extra en unit_price (manda solo 28,
// el precio de la presentación sola) aunque sí eligió el extra en modifiers_json → RECHAZADA.
func TestPriceHardening_PresentationPlusExtra_MissingExtraInPrice_Rejected(t *testing.T) {
	db := setupPriceHardeningDB(t)
	series := seedNVSeries(t, db)
	p, pres, opt := seedPoloConExtra(t, db)
	pid, presID := p.ID, pres.ID
	modifiersJSON := fmt.Sprintf(`[{"type":"modifier","option_id":%d}]`, opt.ID)
	_, err := createOK(t, db, series, SaleItemInput{
		ProductID: &pid, PresentationID: &presID, Quantity: 1, UnitPrice: 28,
		Unit: "NIU", IgvAffectationType: "10", PriceIncludesIgv: true, ModifiersJSON: modifiersJSON,
	})
	if err == nil {
		t.Fatal("esperaba rechazo: falta sumar el extra (5) elegido en modifiers_json")
	}
}

// Caso 5: el combo sigue funcionando exactamente igual (precio fijo del grupo, no el de catálogo
// del producto combo) — el mecanismo legítimo de precio especial existente no se rompe.
func TestPriceHardening_Combo_KeepsWorking(t *testing.T) {
	db := setupSaleCombosDB(t)
	combo, polo, pantalon := seedPromocionVerano(t, db, true)
	series := seedNVSeries(t, db)
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
		t.Fatalf("el combo debe seguir emitiendo venta con el hardening activo: %v", err)
	}
	if sale.Total != 20 {
		t.Errorf("total = %.2f, want 20 (precio fijo del combo, no 40 ni el SalePrice del producto combo)", sale.Total)
	}
}

// ---- Cantidad ----

func newQtyTestProduct(t *testing.T, db *gorm.DB, manageStock bool) database.TenantProduct {
	t.Helper()
	p := database.TenantProduct{
		Code: "ARR-KG", Name: "Arroz", Type: "product", Unit: "KGM", SalePrice: 4.5,
		IgvAffectationType: "10", PriceIncludesIgv: true, ManageStock: manageStock, BranchID: 1, Active: true,
	}
	if err := db.Create(&p).Error; err != nil {
		t.Fatal(err)
	}
	return p
}

// Casos 6/7/8: quantity <= 0 rechazada, independientemente de ManageStock.
func TestPriceHardening_Quantity_MustBePositive(t *testing.T) {
	cases := []struct {
		name        string
		quantity    float64
		manageStock bool
	}{
		{"quantity=0, ManageStock=true", 0, true},
		{"quantity=-1, ManageStock=true", -1, true},
		{"quantity=-0.5, ManageStock=false", -0.5, false},
		{"quantity=0, ManageStock=false (antes de este fix pasaba)", 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := setupPriceHardeningDB(t)
			series := seedNVSeries(t, db)
			p := newQtyTestProduct(t, db, tc.manageStock)
			if tc.manageStock {
				if err := db.Create(&database.TenantProductStock{ProductID: p.ID, BranchID: 1, Quantity: 1000}).Error; err != nil {
					t.Fatal(err)
				}
			}
			pid := p.ID
			_, err := createOK(t, db, series, SaleItemInput{
				ProductID: &pid, Quantity: tc.quantity, UnitPrice: 4.5,
				Unit: "KGM", IgvAffectationType: "10", PriceIncludesIgv: true,
			})
			if err == nil {
				t.Fatalf("esperaba rechazo por cantidad inválida (%.2f)", tc.quantity)
			}
			if !strings.Contains(err.Error(), "cantidad inválida") {
				t.Errorf("mensaje inesperado: %v", err)
			}
		})
	}
}

// Caso 9: línea manual (sin product_id) con quantity <= 0 → RECHAZADA.
func TestPriceHardening_ManualLine_QuantityMustBePositive(t *testing.T) {
	db := setupPriceHardeningDB(t)
	series := seedNVSeries(t, db)
	_, err := createOK(t, db, series, SaleItemInput{
		Description: "Servicio manual", Code: "MANUAL", Unit: "NIU", Quantity: 0, UnitPrice: 15, IgvAffectationType: "10",
	})
	if err == nil {
		t.Fatal("esperaba rechazo: línea manual con cantidad 0")
	}
	if !strings.Contains(err.Error(), "cantidad inválida") {
		t.Errorf("mensaje inesperado: %v", err)
	}
}

// Casos 10/11: cantidades válidas (entera y decimal) → OK.
func TestPriceHardening_Quantity_ValidValues_OK(t *testing.T) {
	cases := []struct {
		name     string
		quantity float64
	}{
		{"quantity=1", 1},
		{"quantity=1.5 (decimal, ya soportado por decimal(15,3))", 1.5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := setupPriceHardeningDB(t)
			series := seedNVSeries(t, db)
			p := newQtyTestProduct(t, db, false)
			pid := p.ID
			_, err := createOK(t, db, series, SaleItemInput{
				ProductID: &pid, Quantity: tc.quantity, UnitPrice: 4.5,
				Unit: "KGM", IgvAffectationType: "10", PriceIncludesIgv: true,
			})
			if err != nil {
				t.Fatalf("cantidad válida (%.2f) no debería rechazarse: %v", tc.quantity, err)
			}
		})
	}
}

// ---- Transaccionalidad ----

// Una venta rechazada por precio o cantidad inválida no debe dejar ningún rastro: ni
// TenantSale, ni TenantSaleItem, ni movimiento de Kardex.
func TestPriceHardening_RejectedSale_LeavesNoTrace(t *testing.T) {
	db := setupPriceHardeningDB(t)
	series := seedNVSeries(t, db)
	p := newQtyTestProduct(t, db, true)
	if err := db.Create(&database.TenantProductStock{ProductID: p.ID, BranchID: 1, Quantity: 1000}).Error; err != nil {
		t.Fatal(err)
	}
	pid := p.ID

	// Precio manipulado.
	if _, err := createOK(t, db, series, SaleItemInput{
		ProductID: &pid, Quantity: 10, UnitPrice: 0.01, Unit: "KGM", IgvAffectationType: "10", PriceIncludesIgv: true,
	}); err == nil {
		t.Fatal("esperaba rechazo por precio manipulado")
	}
	// Cantidad inválida.
	if _, err := createOK(t, db, series, SaleItemInput{
		ProductID: &pid, Quantity: -5, UnitPrice: 4.5, Unit: "KGM", IgvAffectationType: "10", PriceIncludesIgv: true,
	}); err == nil {
		t.Fatal("esperaba rechazo por cantidad inválida")
	}

	var saleCount, itemCount, movementCount int64
	db.Model(&database.TenantSale{}).Count(&saleCount)
	db.Model(&database.TenantSaleItem{}).Count(&itemCount)
	db.Model(&database.TenantStockMovement{}).Count(&movementCount)
	if saleCount != 0 || itemCount != 0 || movementCount != 0 {
		t.Fatalf("una venta rechazada no debe dejar rastro: sales=%d items=%d movements=%d", saleCount, itemCount, movementCount)
	}
	var stock database.TenantProductStock
	db.Where("product_id = ? AND branch_id = ?", p.ID, 1).First(&stock)
	if stock.Quantity != 1000 {
		t.Fatalf("el stock no debe moverse en una venta rechazada, quedó en %g", stock.Quantity)
	}
}

// ---- No regresión: nota de venta, boleta y factura con el hardening activo ----

func TestPriceHardening_NoRegression_NV_Boleta_Factura(t *testing.T) {
	db := setupPriceHardeningDB(t)
	if err := db.Model(&database.TenantCompanyConfig{}).Where("id = 1").
		Update("taxpayer_regime", "general").Error; err != nil {
		t.Fatal(err)
	}
	p := database.TenantProduct{
		Code: "GAS500", Name: "Gaseosa 500ml", Type: "product", Unit: "NIU", SalePrice: 3.5,
		IgvAffectationType: "10", PriceIncludesIgv: true, ManageStock: false, BranchID: 1, Active: true,
	}
	if err := db.Create(&p).Error; err != nil {
		t.Fatal(err)
	}
	pid := p.ID
	contact := database.TenantContact{DocType: "6", DocNumber: "20123456789", BusinessName: "Cliente RUC"}
	if err := db.Create(&contact).Error; err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name      string
		sunatCode string
		docType   string
		contactID *uint
	}{
		{"nota de venta", "00", "00", nil},
		{"boleta electrónica", "03", "03", nil},
		{"factura electrónica", "01", "01", &contact.ID},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			series := database.TenantDocumentSeries{
				BranchID: 1, DocType: tc.name, SunatCode: tc.sunatCode,
				Series: fmt.Sprintf("S%02d", i), Correlative: 1, Active: true,
			}
			if err := db.Create(&series).Error; err != nil {
				t.Fatal(err)
			}
			sale, err := NewSaleService(db).Create(CreateSaleInput{
				BranchID: 1, UserID: 1, SeriesID: series.ID, DocType: tc.docType, ContactID: tc.contactID,
				IssueDate: time.Now(), Currency: "PEN", TaxConfig: tax.DefaultConfig(),
				Payments: []PaymentInput{{Method: "cash", Amount: 3.5}},
				Items: []SaleItemInput{{
					ProductID: &pid, Quantity: 1, UnitPrice: 3.5, Unit: "NIU",
					IgvAffectationType: "10", PriceIncludesIgv: true,
				}},
			})
			if err != nil {
				t.Fatalf("%s con precio de catálogo no debería romperse con el hardening: %v", tc.name, err)
			}
			if sale.Total != 3.5 {
				t.Errorf("%s: total = %.2f, want 3.50", tc.name, sale.Total)
			}
		})
	}
}
