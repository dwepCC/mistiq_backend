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

// setupSaleUnitIntegrationDB monta el esquema necesario para probar la integración de
// TenantProductSaleUnit con ventas/stock/Kardex.
func setupSaleUnitIntegrationDB(t *testing.T) *gorm.DB {
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
		&database.TenantInventoryOperationType{}, &database.TenantProductSerial{},
		&database.TenantProductSaleUnit{}, &database.TenantProductSaleUnitBranchPrice{}, &database.TenantProductBranchPrice{}, &database.TenantProductAttribute{}, &database.TenantCashMovement{},
		&database.TenantBankMovement{}, &database.TenantBankAccount{},
		&database.TenantModifierGroup{}, &database.TenantModifierOption{}, &database.TenantProductModifierGroup{},
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

func seedArroz(t *testing.T, db *gorm.DB, stock float64) (product database.TenantProduct, saco100 database.TenantProductSaleUnit) {
	t.Helper()
	product = database.TenantProduct{
		Code: "ARR-KG", Name: "Arroz Superior", Type: "product", Unit: "KGM", SalePrice: 4.5,
		IgvAffectationType: "10", PriceIncludesIgv: true, ManageStock: true, BranchID: 1, Active: true,
	}
	if err := db.Create(&product).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.TenantProductStock{ProductID: product.ID, BranchID: 1, Quantity: stock}).Error; err != nil {
		t.Fatal(err)
	}
	saco100 = database.TenantProductSaleUnit{
		ProductID: product.ID, Name: "Saco 100 KG", ConversionFactor: 100, AllowFraction: true,
		Price1: 450, Active: true,
	}
	if err := db.Create(&saco100).Error; err != nil {
		t.Fatal(err)
	}
	return product, saco100
}

func seedNVSeriesSU(t *testing.T, db *gorm.DB) database.TenantDocumentSeries {
	t.Helper()
	s := database.TenantDocumentSeries{
		BranchID: 1, DocType: "Nota de Venta", SunatCode: "00", Series: "NV01", Correlative: 1, Active: true,
	}
	if err := db.Create(&s).Error; err != nil {
		t.Fatal(err)
	}
	return s
}

func createSU(db *gorm.DB, series database.TenantDocumentSeries, item SaleItemInput) (*database.TenantSale, error) {
	amount := item.UnitPrice * item.Quantity
	return NewSaleService(db).Create(CreateSaleInput{
		BranchID: 1, UserID: 1, SeriesID: series.ID, DocType: "00",
		IssueDate: time.Now(), Currency: "PEN", TaxConfig: tax.DefaultConfig(),
		Payments: []PaymentInput{{Method: "cash", Amount: amount}},
		Items:    []SaleItemInput{item},
	})
}

// Caso principal: 1.5 sacos de 100 KG a S/450/saco → subtotal comercial correcto, stock -150 KG,
// kardex con snapshot histórico completo, un solo movimiento.
func TestSaleUnitIntegration_SellBySaco_ConvertsToBaseStock(t *testing.T) {
	db := setupSaleUnitIntegrationDB(t)
	p, saco := seedArroz(t, db, 1000)
	series := seedNVSeriesSU(t, db)

	pid, suid := p.ID, saco.ID
	sale, err := createSU(db, series, SaleItemInput{
		ProductID: &pid, SaleUnitID: &suid, Quantity: 1.5, UnitPrice: 450,
		Unit: "NIU", IgvAffectationType: "10", PriceIncludesIgv: true,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if sale.Total != 675 {
		t.Errorf("total = %.2f, want 675 (1.5 × 450, cantidad y precio COMERCIALES)", sale.Total)
	}

	var item database.TenantSaleItem
	if err := db.Where("sale_id = ?", sale.ID).First(&item).Error; err != nil {
		t.Fatal(err)
	}
	if item.Quantity != 1.5 || item.UnitPrice != 450 {
		t.Errorf("TenantSaleItem debe conservar cantidad/precio comercial: quantity=%.2f unit_price=%.2f", item.Quantity, item.UnitPrice)
	}
	if item.SaleUnitID == nil || *item.SaleUnitID != suid {
		t.Errorf("TenantSaleItem.SaleUnitID no persistido: %v", item.SaleUnitID)
	}

	var stock database.TenantProductStock
	if err := db.Where("product_id = ? AND branch_id = ?", p.ID, 1).First(&stock).Error; err != nil {
		t.Fatal(err)
	}
	if stock.Quantity != 850 {
		t.Errorf("stock = %g, want 850 (1000 - 150)", stock.Quantity)
	}

	var movements []database.TenantStockMovement
	if err := db.Where("product_id = ?", p.ID).Find(&movements).Error; err != nil {
		t.Fatal(err)
	}
	if len(movements) != 1 {
		t.Fatalf("esperaba exactamente 1 movimiento de kardex (no duplicar), got %d", len(movements))
	}
	mv := movements[0]
	if mv.Quantity != 150 {
		t.Errorf("Kardex.Quantity = %g, want 150 (unidad base, no 1.5 ni 675 ni 450)", mv.Quantity)
	}
	if mv.Balance != 850 {
		t.Errorf("Kardex.Balance = %g, want 850", mv.Balance)
	}
	if mv.SaleUnitID == nil || *mv.SaleUnitID != suid {
		t.Errorf("Kardex.SaleUnitID no persistido: %v", mv.SaleUnitID)
	}
	if mv.SaleUnitQuantity == nil || *mv.SaleUnitQuantity != 1.5 {
		t.Errorf("Kardex.SaleUnitQuantity = %v, want 1.5", mv.SaleUnitQuantity)
	}
	if mv.ConversionFactor == nil || *mv.ConversionFactor != 100 {
		t.Errorf("Kardex.ConversionFactor = %v, want 100", mv.ConversionFactor)
	}
}

// Snapshot histórico: cambiar el factor de la SaleUnit DESPUÉS de la venta no debe reinterpretar
// el movimiento ya persistido.
func TestSaleUnitIntegration_HistoricalSnapshot_SurvivesFactorChange(t *testing.T) {
	db := setupSaleUnitIntegrationDB(t)
	p, saco := seedArroz(t, db, 1000)
	series := seedNVSeriesSU(t, db)
	pid, suid := p.ID, saco.ID

	if _, err := createSU(db, series, SaleItemInput{
		ProductID: &pid, SaleUnitID: &suid, Quantity: 1.5, UnitPrice: 450,
		Unit: "NIU", IgvAffectationType: "10", PriceIncludesIgv: true,
	}); err != nil {
		t.Fatal(err)
	}

	// El saco ahora "pesa" 80 KG en vez de 100.
	if err := db.Model(&database.TenantProductSaleUnit{}).Where("id = ?", suid).
		Update("conversion_factor", 80).Error; err != nil {
		t.Fatal(err)
	}

	var mv database.TenantStockMovement
	if err := db.Where("product_id = ?", p.ID).First(&mv).Error; err != nil {
		t.Fatal(err)
	}
	if mv.Quantity != 150 {
		t.Errorf("el movimiento histórico debe seguir en 150 KG, no reinterpretarse a 120 (1.5×80): got %g", mv.Quantity)
	}
	if mv.ConversionFactor == nil || *mv.ConversionFactor != 100 {
		t.Errorf("el snapshot de factor debe conservar el valor usado en el momento de la venta (100), got %v", mv.ConversionFactor)
	}
}

// SaleUnit inactiva → rechazada.
func TestSaleUnitIntegration_InactiveSaleUnit_Rejected(t *testing.T) {
	db := setupSaleUnitIntegrationDB(t)
	p, saco := seedArroz(t, db, 1000)
	series := seedNVSeriesSU(t, db)
	if err := db.Model(&database.TenantProductSaleUnit{}).Where("id = ?", saco.ID).Update("active", false).Error; err != nil {
		t.Fatal(err)
	}
	pid, suid := p.ID, saco.ID
	if _, err := createSU(db, series, SaleItemInput{
		ProductID: &pid, SaleUnitID: &suid, Quantity: 1, UnitPrice: 450,
		Unit: "NIU", IgvAffectationType: "10", PriceIncludesIgv: true,
	}); err == nil {
		t.Fatal("esperaba rechazo: unidad de venta desactivada")
	}
}

// SaleUnit de otro producto → rechazada (no confiar en el ID del cliente).
func TestSaleUnitIntegration_SaleUnitFromOtherProduct_Rejected(t *testing.T) {
	db := setupSaleUnitIntegrationDB(t)
	p1, _ := seedArroz(t, db, 1000)
	p2, saco2 := seedArroz(t, db, 1000) // segundo producto con su propia sale unit
	series := seedNVSeriesSU(t, db)

	p1id, suOfP2 := p1.ID, saco2.ID
	if _, err := createSU(db, series, SaleItemInput{
		ProductID: &p1id, SaleUnitID: &suOfP2, Quantity: 1, UnitPrice: 450,
		Unit: "NIU", IgvAffectationType: "10", PriceIncludesIgv: true,
	}); err == nil {
		t.Fatalf("esperaba rechazo: la SaleUnit %d pertenece al producto %d, no a %d", suOfP2, p2.ID, p1id)
	}
}

// AllowFraction=false rechaza cantidades decimales y acepta enteras.
func TestSaleUnitIntegration_AllowFraction_EnforcedServerSide(t *testing.T) {
	db := setupSaleUnitIntegrationDB(t)
	p := database.TenantProduct{
		Code: "GAS", Name: "Gaseosa", Type: "product", Unit: "NIU", SalePrice: 3, ManageStock: true,
		IgvAffectationType: "10", PriceIncludesIgv: true, BranchID: 1, Active: true,
	}
	if err := db.Create(&p).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.TenantProductStock{ProductID: p.ID, BranchID: 1, Quantity: 1000}).Error; err != nil {
		t.Fatal(err)
	}
	caja := database.TenantProductSaleUnit{
		ProductID: p.ID, Name: "Caja x24", ConversionFactor: 24, AllowFraction: false, Price1: 60, Active: true,
	}
	if err := db.Create(&caja).Error; err != nil {
		t.Fatal(err)
	}
	series := seedNVSeriesSU(t, db)
	pid, cajaID := p.ID, caja.ID

	for _, tc := range []struct {
		name     string
		quantity float64
		wantOK   bool
	}{
		{"1 caja entera", 1, true},
		{"2 cajas enteras", 2, true},
		{"1.5 cajas fraccionadas, rechazado", 1.5, false},
		{"0.5 cajas fraccionadas, rechazado", 0.5, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := createSU(db, series, SaleItemInput{
				ProductID: &pid, SaleUnitID: &cajaID, Quantity: tc.quantity, UnitPrice: 60,
				Unit: "NIU", IgvAffectationType: "10", PriceIncludesIgv: true,
			})
			if tc.wantOK && err != nil {
				t.Fatalf("esperaba OK: %v", err)
			}
			if !tc.wantOK && err == nil {
				t.Fatal("esperaba rechazo por AllowFraction=false")
			}
		})
	}
}

// Precio manipulado usando SaleUnit → rechazada (extiende el hardening de Fase 0). Precio
// correcto (Price1 de la SaleUnit) → OK.
func TestSaleUnitIntegration_PriceAuthorization_UsesSaleUnitPrice(t *testing.T) {
	db := setupSaleUnitIntegrationDB(t)
	p, saco := seedArroz(t, db, 1000)
	series := seedNVSeriesSU(t, db)
	pid, suid := p.ID, saco.ID

	if _, err := createSU(db, series, SaleItemInput{
		ProductID: &pid, SaleUnitID: &suid, Quantity: 1, UnitPrice: 100, // 100 != Price1 (450), ni SalePrice (4.5)
		Unit: "NIU", IgvAffectationType: "10", PriceIncludesIgv: true,
	}); err == nil {
		t.Fatal("esperaba rechazo: unit_price no coincide con SaleUnit.Price1 (450)")
	}
	if _, err := createSU(db, series, SaleItemInput{
		ProductID: &pid, SaleUnitID: &suid, Quantity: 1, UnitPrice: 450,
		Unit: "NIU", IgvAffectationType: "10", PriceIncludesIgv: true,
	}); err != nil {
		t.Fatalf("precio correcto (Price1=450 de la SaleUnit) debe pasar: %v", err)
	}
}

// Stock insuficiente en unidad base (no en cantidad comercial) → rechazada.
func TestSaleUnitIntegration_InsufficientBaseStock_Rejected(t *testing.T) {
	db := setupSaleUnitIntegrationDB(t)
	// Solo 100 KG en stock: alcanza para 1 saco pero no para 1.5.
	p, saco := seedArroz(t, db, 100)
	series := seedNVSeriesSU(t, db)
	pid, suid := p.ID, saco.ID

	if _, err := createSU(db, series, SaleItemInput{
		ProductID: &pid, SaleUnitID: &suid, Quantity: 1.5, UnitPrice: 450,
		Unit: "NIU", IgvAffectationType: "10", PriceIncludesIgv: true,
	}); err == nil {
		t.Fatal("esperaba rechazo: 1.5 sacos = 150 KG, stock real es 100 KG")
	} else if !strings.Contains(err.Error(), "stock insuficiente") {
		t.Errorf("mensaje inesperado: %v", err)
	}

	// 1 saco (100 KG) sí alcanza exacto.
	if _, err := createSU(db, series, SaleItemInput{
		ProductID: &pid, SaleUnitID: &suid, Quantity: 1, UnitPrice: 450,
		Unit: "NIU", IgvAffectationType: "10", PriceIncludesIgv: true,
	}); err != nil {
		t.Fatalf("1 saco (100 KG) con 100 KG en stock debería pasar: %v", err)
	}
}

// Producto legacy (sin SaleUnitID en la línea) sigue funcionando exactamente igual.
func TestSaleUnitIntegration_LegacyProduct_Unaffected(t *testing.T) {
	db := setupSaleUnitIntegrationDB(t)
	p := database.TenantProduct{
		Code: "SVC", Name: "Producto legacy", Type: "product", Unit: "NIU", SalePrice: 25,
		ManageStock: true, IgvAffectationType: "10", PriceIncludesIgv: true, BranchID: 1, Active: true,
	}
	if err := db.Create(&p).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.TenantProductStock{ProductID: p.ID, BranchID: 1, Quantity: 10}).Error; err != nil {
		t.Fatal(err)
	}
	series := seedNVSeriesSU(t, db)
	pid := p.ID

	if _, err := createSU(db, series, SaleItemInput{
		ProductID: &pid, Quantity: 2, UnitPrice: 25, Unit: "NIU", IgvAffectationType: "10", PriceIncludesIgv: true,
	}); err != nil {
		t.Fatalf("venta legacy sin SaleUnit debe seguir funcionando: %v", err)
	}
	var stock database.TenantProductStock
	db.Where("product_id = ? AND branch_id = ?", p.ID, 1).First(&stock)
	if stock.Quantity != 8 {
		t.Errorf("stock = %g, want 8 (10 - 2, sin conversión)", stock.Quantity)
	}
	var mv database.TenantStockMovement
	db.Where("product_id = ?", p.ID).First(&mv)
	if mv.SaleUnitID != nil {
		t.Errorf("una venta legacy no debe tener SaleUnitID en el kardex, got %v", mv.SaleUnitID)
	}
}

// PresentationID + SaleUnitID combinados en la misma línea → rechazada.
func TestSaleUnitIntegration_PresentationPlusSaleUnit_Rejected(t *testing.T) {
	db := setupSaleUnitIntegrationDB(t)
	if err := db.AutoMigrate(&database.TenantProductPresentation{}); err != nil {
		t.Fatal(err)
	}
	p, saco := seedArroz(t, db, 1000)
	if err := db.Model(&p).Update("has_variants", true).Error; err != nil {
		t.Fatal(err)
	}
	pres := database.TenantProductPresentation{ProductID: p.ID, Name: "Bolsa", SalePrice: 5, Active: true}
	if err := db.Create(&pres).Error; err != nil {
		t.Fatal(err)
	}
	series := seedNVSeriesSU(t, db)
	pid, suid, presID := p.ID, saco.ID, pres.ID

	if _, err := createSU(db, series, SaleItemInput{
		ProductID: &pid, SaleUnitID: &suid, PresentationID: &presID, Quantity: 1, UnitPrice: 450,
		Unit: "NIU", IgvAffectationType: "10", PriceIncludesIgv: true,
	}); err == nil {
		t.Fatal("esperaba rechazo: no se puede combinar presentación y unidad de venta")
	}
}

// ManageSeries + SaleUnitID → rechazada (fuera de alcance de esta fase).
func TestSaleUnitIntegration_ManageSeriesPlusSaleUnit_Rejected(t *testing.T) {
	db := setupSaleUnitIntegrationDB(t)
	p := database.TenantProduct{
		Code: "SERIAL", Name: "Con series", Type: "product", Unit: "NIU", SalePrice: 100,
		ManageStock: true, ManageSeries: true, IgvAffectationType: "10", PriceIncludesIgv: true,
		BranchID: 1, Active: true,
	}
	if err := db.Create(&p).Error; err != nil {
		t.Fatal(err)
	}
	su := database.TenantProductSaleUnit{ProductID: p.ID, Name: "Caja", ConversionFactor: 2, Price1: 200, Active: true}
	if err := db.Create(&su).Error; err != nil {
		t.Fatal(err)
	}
	series := seedNVSeriesSU(t, db)
	pid, suid := p.ID, su.ID

	if _, err := createSU(db, series, SaleItemInput{
		ProductID: &pid, SaleUnitID: &suid, Quantity: 1, UnitPrice: 200,
		Unit: "NIU", IgvAffectationType: "10", PriceIncludesIgv: true,
	}); err == nil {
		t.Fatal("esperaba rechazo: ManageSeries + SaleUnit no es compatible todavía")
	}
}

// Devolución/anulación de una venta con SaleUnit restaura el stock en unidad base, usando el
// Kardex ya persistido (no recalcula con el factor actual).
func TestSaleUnitIntegration_CancelNotaVenta_RestoresBaseStock(t *testing.T) {
	db := setupSaleUnitIntegrationDB(t)
	p, saco := seedArroz(t, db, 1000)
	series := seedNVSeriesSU(t, db)
	pid, suid := p.ID, saco.ID

	sale, err := createSU(db, series, SaleItemInput{
		ProductID: &pid, SaleUnitID: &suid, Quantity: 1.5, UnitPrice: 450,
		Unit: "NIU", IgvAffectationType: "10", PriceIncludesIgv: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	var stockAfterSale database.TenantProductStock
	db.Where("product_id = ? AND branch_id = ?", p.ID, 1).First(&stockAfterSale)
	if stockAfterSale.Quantity != 850 {
		t.Fatalf("precondición: stock tras la venta debería ser 850, got %g", stockAfterSale.Quantity)
	}

	if err := NewSaleService(db).CancelNotaVenta(sale.ID, 1, "anulación de prueba"); err != nil {
		t.Fatalf("CancelNotaVenta: %v", err)
	}

	var stockAfterCancel database.TenantProductStock
	db.Where("product_id = ? AND branch_id = ?", p.ID, 1).First(&stockAfterCancel)
	if stockAfterCancel.Quantity != 1000 {
		t.Errorf("stock tras anular = %g, want 1000 (restaurar 150 KG desde el kardex, no recalcular)", stockAfterCancel.Quantity)
	}
}

// Unidad base (IsBase=true, factor=1) se comporta igual que vender directamente en unidad base.
func TestSaleUnitIntegration_BaseSaleUnit_FactorOne(t *testing.T) {
	db := setupSaleUnitIntegrationDB(t)
	p, _ := seedArroz(t, db, 1000)
	kg := database.TenantProductSaleUnit{
		ProductID: p.ID, Name: "KG", ConversionFactor: 1, IsBase: true, AllowFraction: true, Price1: 4.5, Active: true,
	}
	if err := db.Create(&kg).Error; err != nil {
		t.Fatal(err)
	}
	series := seedNVSeriesSU(t, db)
	pid, kgID := p.ID, kg.ID

	if _, err := createSU(db, series, SaleItemInput{
		ProductID: &pid, SaleUnitID: &kgID, Quantity: 10, UnitPrice: 4.5,
		Unit: "NIU", IgvAffectationType: "10", PriceIncludesIgv: true,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	var stock database.TenantProductStock
	db.Where("product_id = ? AND branch_id = ?", p.ID, 1).First(&stock)
	if stock.Quantity != 990 {
		t.Errorf("stock = %g, want 990 (1000 - 10×1)", stock.Quantity)
	}
}

// ---- Cierre de Fase 2, punto 1: snapshot de trazabilidad en la reversión ----

// Anular una venta con SaleUnit debe conservar en el movimiento de reversión (+150 KG) el mismo
// SaleUnitID/SaleUnitQuantity/ConversionFactor que tenía la salida original — sin esto, el Kardex
// mostraba "+150 KG" sin ningún contexto comercial, aunque el stock ya quedaba correcto.
func TestSaleUnitIntegration_CancelNotaVenta_PreservesSaleUnitSnapshotOnReversal(t *testing.T) {
	db := setupSaleUnitIntegrationDB(t)
	p, saco := seedArroz(t, db, 1000)
	series := seedNVSeriesSU(t, db)
	pid, suid := p.ID, saco.ID

	sale, err := createSU(db, series, SaleItemInput{
		ProductID: &pid, SaleUnitID: &suid, Quantity: 1.5, UnitPrice: 450,
		Unit: "NIU", IgvAffectationType: "10", PriceIncludesIgv: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := NewSaleService(db).CancelNotaVenta(sale.ID, 1, "anulación de prueba"); err != nil {
		t.Fatalf("CancelNotaVenta: %v", err)
	}

	var reversal database.TenantStockMovement
	if err := db.Where("product_id = ? AND type = ?", p.ID, "in").First(&reversal).Error; err != nil {
		t.Fatal(err)
	}
	if reversal.Quantity != 150 {
		t.Errorf("Quantity de la reversión = %g, want 150 (no cambia la lógica de stock)", reversal.Quantity)
	}
	if reversal.SaleUnitID == nil || *reversal.SaleUnitID != suid {
		t.Errorf("la reversión debe conservar SaleUnitID, got %v", reversal.SaleUnitID)
	}
	if reversal.SaleUnitQuantity == nil || *reversal.SaleUnitQuantity != 1.5 {
		t.Errorf("la reversión debe conservar SaleUnitQuantity=1.5, got %v", reversal.SaleUnitQuantity)
	}
	if reversal.ConversionFactor == nil || *reversal.ConversionFactor != 100 {
		t.Errorf("la reversión debe conservar ConversionFactor=100, got %v", reversal.ConversionFactor)
	}
}

// Devolución parcial: el snapshot comercial se prorratea junto con la cantidad base.
func TestSaleUnitIntegration_PartialReturn_PreservesProportionalSaleUnitSnapshot(t *testing.T) {
	db := setupSaleUnitIntegrationDB(t)
	p, saco := seedArroz(t, db, 1000)
	series := seedNVSeriesSU(t, db)
	pid, suid := p.ID, saco.ID

	sale, err := createSU(db, series, SaleItemInput{
		ProductID: &pid, SaleUnitID: &suid, Quantity: 2, UnitPrice: 450,
		Unit: "NIU", IgvAffectationType: "10", PriceIncludesIgv: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	var originalItem database.TenantSaleItem
	if err := db.Where("sale_id = ?", sale.ID).First(&originalItem).Error; err != nil {
		t.Fatal(err)
	}

	// Nota de crédito parcial: devuelve 0.5 de los 2 sacos vendidos en esa línea (ratio 0.25).
	noteSale := database.TenantSale{Number: "NC01-1", DocType: "NOTA_CREDITO", BranchID: 1, UserID: 1}
	if err := db.Create(&noteSale).Error; err != nil {
		t.Fatal(err)
	}
	origItemID := originalItem.ID
	if err := db.Create(&database.TenantSaleItem{
		SaleID: noteSale.ID, ProductID: &pid, Description: "Devolución parcial", Unit: "NIU",
		Quantity: 0.5, UnitPrice: 450, Subtotal: 190.68, TaxAmount: 34.32, Total: 225,
		OriginalSaleItemID: &origItemID,
	}).Error; err != nil {
		t.Fatal(err)
	}

	if err := RestorePartialStockFromKardexTx(db, &noteSale, "NC/"+noteSale.Number, 1); err != nil {
		t.Fatalf("RestorePartialStockFromKardexTx: %v", err)
	}

	var reversal database.TenantStockMovement
	if err := db.Where("product_id = ? AND type = ?", p.ID, "in").First(&reversal).Error; err != nil {
		t.Fatal(err)
	}
	// 2 sacos = 200 KG de salida; devolver 0.5/2 = 25% → 50 KG base, 0.5 sacos comerciales.
	if reversal.Quantity != 50 {
		t.Errorf("Quantity de la reversión parcial = %g, want 50 (25%% de 200 KG)", reversal.Quantity)
	}
	if reversal.SaleUnitID == nil || *reversal.SaleUnitID != suid {
		t.Errorf("la reversión parcial debe conservar SaleUnitID, got %v", reversal.SaleUnitID)
	}
	if reversal.SaleUnitQuantity == nil || *reversal.SaleUnitQuantity != 0.5 {
		t.Errorf("la reversión parcial debe prorratear SaleUnitQuantity a 0.5 (25%% de 2), got %v", reversal.SaleUnitQuantity)
	}
	if reversal.ConversionFactor == nil || *reversal.ConversionFactor != 100 {
		t.Errorf("el factor snapshot no debe prorratearse, sigue siendo 100, got %v", reversal.ConversionFactor)
	}
}

// ---- Cierre de Fase 2, punto 2: SaleUnit + extras/modificadores ----

// Confirma, con código real, que NO existe hoy ningún flujo que combine SaleUnit con un extra:
// SaleUnit no existía antes de esta evolución (Fase 1), ningún frontend (Tukifac ni Tukichef)
// envía sale_unit_id todavía, y el modelo de datos no ata HasModifiers/TenantProductModifierGroup
// a TenantProductSaleUnit de ninguna forma. Por lo tanto, esta combinación no es "cerrar algo que
// el sistema ya permite": es un caso inexistente hasta ahora. Se rechaza explícitamente (en vez de
// ignorar el extra en silencio, que es lo que hacía antes de este ajuste) para que el error sea
// claro y el modifiers_json nunca quede persistido con un extra que no se cobró.
func TestSaleUnitIntegration_SaleUnitPlusExtra_ExplicitlyRejected(t *testing.T) {
	db := setupSaleUnitIntegrationDB(t)
	p, saco := seedArroz(t, db, 1000)
	group := database.TenantModifierGroup{Name: "Extras", Kind: "extra", Active: true}
	if err := db.Create(&group).Error; err != nil {
		t.Fatal(err)
	}
	option := database.TenantModifierOption{GroupID: group.ID, Name: "Empaque especial", ExtraPrice: 10, Active: true}
	if err := db.Create(&option).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.TenantProductModifierGroup{ProductID: p.ID, GroupID: group.ID}).Error; err != nil {
		t.Fatal(err)
	}
	series := seedNVSeriesSU(t, db)
	pid, suid := p.ID, saco.ID
	modifiersJSON := fmt.Sprintf(`[{"type":"modifier","option_id":%d}]`, option.ID)

	if _, err := createSU(db, series, SaleItemInput{
		ProductID: &pid, SaleUnitID: &suid, Quantity: 1, UnitPrice: 460, // 450 + 10 del extra
		Unit: "NIU", IgvAffectationType: "10", PriceIncludesIgv: true, ModifiersJSON: modifiersJSON,
	}); err == nil {
		t.Fatal("esperaba rechazo explícito: SaleUnit + extra no es una combinación soportada todavía")
	} else if !strings.Contains(err.Error(), "extras/modificadores") {
		t.Errorf("mensaje inesperado (debería explicar la causa, no un genérico de precio): %v", err)
	}
}

// Confirma el caso simétrico: una línea SIN SaleUnit sigue pudiendo combinar presentación + extra
// exactamente igual que antes de este ajuste (no se restringió el camino legacy).
func TestSaleUnitIntegration_ExtraWithoutSaleUnit_StillWorks(t *testing.T) {
	db := setupSaleUnitIntegrationDB(t)
	if err := db.AutoMigrate(&database.TenantProductPresentation{}); err != nil {
		t.Fatal(err)
	}
	p := database.TenantProduct{
		Code: "POLO", Name: "Polo", Type: "product", Unit: "NIU", SalePrice: 25, HasVariants: true,
		ManageStock: false, IgvAffectationType: "10", PriceIncludesIgv: true, BranchID: 1, Active: true,
	}
	if err := db.Create(&p).Error; err != nil {
		t.Fatal(err)
	}
	pres := database.TenantProductPresentation{ProductID: p.ID, Name: "Rojo/M", SalePrice: 28, Active: true}
	if err := db.Create(&pres).Error; err != nil {
		t.Fatal(err)
	}
	group := database.TenantModifierGroup{Name: "Extras", Kind: "extra", Active: true}
	if err := db.Create(&group).Error; err != nil {
		t.Fatal(err)
	}
	option := database.TenantModifierOption{GroupID: group.ID, Name: "Bordado", ExtraPrice: 5, Active: true}
	if err := db.Create(&option).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&database.TenantProductModifierGroup{ProductID: p.ID, GroupID: group.ID}).Error; err != nil {
		t.Fatal(err)
	}
	series := seedNVSeriesSU(t, db)
	pid, presID := p.ID, pres.ID
	modifiersJSON := fmt.Sprintf(`[{"type":"modifier","option_id":%d}]`, option.ID)

	if _, err := createSU(db, series, SaleItemInput{
		ProductID: &pid, PresentationID: &presID, Quantity: 1, UnitPrice: 33, // 28 + 5
		Unit: "NIU", IgvAffectationType: "10", PriceIncludesIgv: true, ModifiersJSON: modifiersJSON,
	}); err != nil {
		t.Fatalf("presentación + extra sin SaleUnit debe seguir funcionando: %v", err)
	}
}
