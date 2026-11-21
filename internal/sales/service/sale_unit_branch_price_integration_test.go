package service

import (
	"testing"
	"time"

	"tukifac/pkg/database"
	"tukifac/pkg/saleunit"
	"tukifac/pkg/tax"

	"gorm.io/gorm"
)

// seedAguaConCaja crea un producto con una SaleUnit "Caja x12" (Price1=34) y dos sucursales,
// para probar la resolución de precio por sucursal en el flujo real de venta.
func seedAguaConCaja(t *testing.T, db *gorm.DB) (product database.TenantProduct, caja database.TenantProductSaleUnit, branchA, branchB database.TenantBranch) {
	t.Helper()
	product = database.TenantProduct{
		Code: "AGUA", Name: "Agua", Type: "product", Unit: "NIU", SalePrice: 3,
		ManageStock: false, IgvAffectationType: "10", PriceIncludesIgv: true, Active: true,
	}
	if err := db.Create(&product).Error; err != nil {
		t.Fatal(err)
	}
	caja = database.TenantProductSaleUnit{
		ProductID: product.ID, Name: "Caja x12", ConversionFactor: 12, AllowFraction: true,
		Price1: 34, Active: true,
	}
	if err := db.Create(&caja).Error; err != nil {
		t.Fatal(err)
	}
	branchA = database.TenantBranch{Name: "Sucursal A", Active: true}
	if err := db.Create(&branchA).Error; err != nil {
		t.Fatal(err)
	}
	branchB = database.TenantBranch{Name: "Sucursal B", Active: true}
	if err := db.Create(&branchB).Error; err != nil {
		t.Fatal(err)
	}
	openCashSessionForBranch(t, db, branchA.ID)
	openCashSessionForBranch(t, db, branchB.ID)
	return product, caja, branchA, branchB
}

// openCashSessionForBranch: toda venta exige una sesión de caja abierta del usuario en esa
// sucursal (setupSaleUnitIntegrationDB solo abre una para branch_id=1 por defecto).
func openCashSessionForBranch(t *testing.T, db *gorm.DB, branchID uint) {
	t.Helper()
	if err := db.Create(&database.TenantCashSession{
		BranchID: branchID, UserID: 1, OpenedBy: 1, Status: "open", OpenedAt: time.Now(),
	}).Error; err != nil {
		t.Fatal(err)
	}
}

func createSUAtBranch(db *gorm.DB, branchID uint, series database.TenantDocumentSeries, item SaleItemInput) (*database.TenantSale, error) {
	amount := item.UnitPrice * item.Quantity
	return NewSaleService(db).Create(CreateSaleInput{
		BranchID: branchID, UserID: 1, SeriesID: series.ID, DocType: "00",
		IssueDate: time.Now(), Currency: "PEN", TaxConfig: tax.DefaultConfig(),
		Payments: []PaymentInput{{Method: "cash", Amount: amount}},
		Items:    []SaleItemInput{item},
	})
}

// A/B: SaleUnit con precio global, Branch A con override, Branch B sin override.
func TestBranchPrice_OverrideVsGlobal(t *testing.T) {
	db := setupSaleUnitIntegrationDB(t)
	p, caja, branchA, branchB := seedAguaConCaja(t, db)
	seriesA := database.TenantDocumentSeries{BranchID: branchA.ID, DocType: "Nota de Venta", SunatCode: "00", Series: "NVA1", Correlative: 1, Active: true}
	db.Create(&seriesA)
	seriesB := database.TenantDocumentSeries{BranchID: branchB.ID, DocType: "Nota de Venta", SunatCode: "00", Series: "NVB1", Correlative: 1, Active: true}
	db.Create(&seriesB)

	if err := db.Create(&database.TenantProductSaleUnitBranchPrice{
		SaleUnitID: caja.ID, BranchID: branchA.ID, Price1: 36, Active: true,
	}).Error; err != nil {
		t.Fatal(err)
	}

	pid, suid := p.ID, caja.ID

	// Branch A: usa el override (36), no el global (34).
	saleA, err := createSUAtBranch(db, branchA.ID, seriesA, SaleItemInput{
		ProductID: &pid, SaleUnitID: &suid, Quantity: 1, UnitPrice: 36,
		Unit: "NIU", IgvAffectationType: "10", PriceIncludesIgv: true,
	})
	if err != nil {
		t.Fatalf("Branch A con override 36 debe aceptar unit_price=36: %v", err)
	}
	if saleA.Total != 36 {
		t.Errorf("Branch A: total = %.2f, want 36", saleA.Total)
	}

	// Branch A rechaza el precio global (34) porque ahí SÍ hay override.
	if _, err := createSUAtBranch(db, branchA.ID, seriesA, SaleItemInput{
		ProductID: &pid, SaleUnitID: &suid, Quantity: 1, UnitPrice: 34,
		Unit: "NIU", IgvAffectationType: "10", PriceIncludesIgv: true,
	}); err == nil {
		t.Error("Branch A no debería aceptar el precio global (34) habiendo un override (36)")
	}

	// Branch B: sin override, usa el precio global (34).
	saleB, err := createSUAtBranch(db, branchB.ID, seriesB, SaleItemInput{
		ProductID: &pid, SaleUnitID: &suid, Quantity: 1, UnitPrice: 34,
		Unit: "NIU", IgvAffectationType: "10", PriceIncludesIgv: true,
	})
	if err != nil {
		t.Fatalf("Branch B sin override debe usar el precio global (34): %v", err)
	}
	if saleB.Total != 34 {
		t.Errorf("Branch B: total = %.2f, want 34", saleB.Total)
	}
}

// B: distintas sucursales con distintos overrides, y una tercera sin override usando el global.
func TestBranchPrice_MultipleBranchesDifferentOverrides(t *testing.T) {
	db := setupSaleUnitIntegrationDB(t)
	p, caja, branchA, branchB := seedAguaConCaja(t, db)
	if err := db.Model(&caja).Update("price1", 30).Error; err != nil {
		t.Fatal(err)
	}
	branchC := database.TenantBranch{Name: "Sucursal C", Active: true}
	db.Create(&branchC)
	openCashSessionForBranch(t, db, branchC.ID)

	db.Create(&database.TenantProductSaleUnitBranchPrice{SaleUnitID: caja.ID, BranchID: branchA.ID, Price1: 32, Active: true})
	db.Create(&database.TenantProductSaleUnitBranchPrice{SaleUnitID: caja.ID, BranchID: branchB.ID, Price1: 35, Active: true})

	pid, suid := p.ID, caja.ID
	seedSeries := func(branchID uint, code string) database.TenantDocumentSeries {
		s := database.TenantDocumentSeries{BranchID: branchID, DocType: "Nota de Venta", SunatCode: "00", Series: code, Correlative: 1, Active: true}
		db.Create(&s)
		return s
	}

	cases := []struct {
		branch database.TenantBranch
		series database.TenantDocumentSeries
		want   float64
	}{
		{branchA, seedSeries(branchA.ID, "SA1"), 32},
		{branchB, seedSeries(branchB.ID, "SB1"), 35},
		{branchC, seedSeries(branchC.ID, "SC1"), 30}, // sin override -> global
	}
	for _, tc := range cases {
		sale, err := createSUAtBranch(db, tc.branch.ID, tc.series, SaleItemInput{
			ProductID: &pid, SaleUnitID: &suid, Quantity: 1, UnitPrice: tc.want,
			Unit: "NIU", IgvAffectationType: "10", PriceIncludesIgv: true,
		})
		if err != nil {
			t.Fatalf("%s: %v", tc.branch.Name, err)
		}
		if sale.Total != tc.want {
			t.Errorf("%s: total = %.2f, want %.2f", tc.branch.Name, sale.Total, tc.want)
		}
	}
}

// D: fallback por nivel (Fase 6.1, corrección 3) — cubre los DOS escalones del fallback de
// Price2/Price3 documentados en pkg/saleunit.ResolvePrice, sin modificar el resolver: solo
// verifica el comportamiento que ya implementa.
//
//  1. Branch Price2 inexistente, Global Price2 existente → usa el Global Price2.
//  2. Branch Price2 inexistente Y Global Price2 inexistente → cae hasta Global Price1 (el
//     escalón más profundo, no cubierto por ningún test anterior a esta corrección).
//
// Aunque hoy nada selecciona Price2/Price3 en un flujo real (ver sale_price_authorization.go,
// saleUnitPriceLevel=1), el resolver compartido sí debe implementar el fallback correctamente
// para cuando exista esa selección — esta es la razón de ser de este test, no una funcionalidad
// nueva.
func TestBranchPrice_FallbackByLevel_NoAutomaticSelectionYet(t *testing.T) {
	t.Run("branch price2 ausente, global price2 presente -> usa global price2", func(t *testing.T) {
		db := setupSaleUnitIntegrationDB(t)
		p, caja, branchA, _ := seedAguaConCaja(t, db)
		globalP2 := 32.0
		if err := db.Model(&caja).Updates(map[string]interface{}{"price2": globalP2}).Error; err != nil {
			t.Fatal(err)
		}
		// Override de sucursal SOLO con price1 (price2 queda nil).
		db.Create(&database.TenantProductSaleUnitBranchPrice{SaleUnitID: caja.ID, BranchID: branchA.ID, Price1: 36, Active: true})

		got, err := saleunit.ResolvePrice(db, p.ID, caja.ID, branchA.ID, 2)
		if err != nil {
			t.Fatal(err)
		}
		if got != globalP2 {
			t.Errorf("nivel 2 sin override de sucursal debe caer al global (32), got %.2f", got)
		}
	})

	t.Run("branch price2 ausente y global price2 ausente -> cae hasta global price1", func(t *testing.T) {
		db := setupSaleUnitIntegrationDB(t)
		p, caja, branchA, _ := seedAguaConCaja(t, db)
		// caja.Price2 queda nil (seedAguaConCaja solo setea Price1=34). Override de sucursal
		// también SOLO con price1 (price2 queda nil).
		db.Create(&database.TenantProductSaleUnitBranchPrice{SaleUnitID: caja.ID, BranchID: branchA.ID, Price1: 36, Active: true})

		got, err := saleunit.ResolvePrice(db, p.ID, caja.ID, branchA.ID, 2)
		if err != nil {
			t.Fatal(err)
		}
		if got != 34 {
			t.Errorf("nivel 2 sin override ni global debe caer hasta Price1 global (34), got %.2f", got)
		}
	})
}

// Defensa adicional de Fase 6.1: ResolvePrice rechaza una SaleUnit que no pertenece al producto
// indicado, aunque exista y sea válida para otro producto del mismo tenant.
func TestBranchPrice_ResolvePrice_RejectsSaleUnitFromOtherProduct(t *testing.T) {
	db := setupSaleUnitIntegrationDB(t)
	p1, caja1, branchA, _ := seedAguaConCaja(t, db)
	p2, _, _, _ := seedAguaConCaja(t, db)

	if _, err := saleunit.ResolvePrice(db, p2.ID, caja1.ID, branchA.ID, 1); err == nil {
		t.Fatalf("esperaba rechazo: la SaleUnit %d pertenece al producto %d, no a %d", caja1.ID, p1.ID, p2.ID)
	}
	// Con el productID correcto, sigue resolviendo con normalidad.
	if _, err := saleunit.ResolvePrice(db, p1.ID, caja1.ID, branchA.ID, 1); err != nil {
		t.Fatalf("con el productID correcto no debería fallar: %v", err)
	}
}

// E: producto legacy (sin SaleUnit) sigue usando Product.SalePrice, sin que la sucursal influya.
func TestBranchPrice_LegacyProduct_UsesSalePriceRegardlessOfBranch(t *testing.T) {
	db := setupSaleUnitIntegrationDB(t)
	p := database.TenantProduct{
		Code: "LEG", Name: "Producto legacy", Type: "product", Unit: "NIU", SalePrice: 10,
		ManageStock: false, IgvAffectationType: "10", PriceIncludesIgv: true, Active: true,
	}
	db.Create(&p)
	branchA := database.TenantBranch{Name: "Sucursal A", Active: true}
	db.Create(&branchA)
	series := database.TenantDocumentSeries{BranchID: branchA.ID, DocType: "Nota de Venta", SunatCode: "00", Series: "NV01", Correlative: 1, Active: true}
	db.Create(&series)

	pid := p.ID
	sale, err := createSUAtBranch(db, branchA.ID, series, SaleItemInput{
		ProductID: &pid, Quantity: 1, UnitPrice: 10, Unit: "NIU", IgvAffectationType: "10", PriceIncludesIgv: true,
	})
	if err != nil {
		t.Fatalf("producto legacy debe seguir vendiéndose a SalePrice: %v", err)
	}
	if sale.Total != 10 {
		t.Errorf("total = %.2f, want 10 (SalePrice, sin ningún override de sucursal)", sale.Total)
	}
}

// F: manipulación de precio vía HTTP — un precio distinto del resuelto (override o global) se
// rechaza, la protección de Fase 0 sigue intacta con la nueva resolución de Fase 4.
func TestBranchPrice_PriceManipulation_StillRejected(t *testing.T) {
	db := setupSaleUnitIntegrationDB(t)
	p, caja, branchA, _ := seedAguaConCaja(t, db)
	db.Create(&database.TenantProductSaleUnitBranchPrice{SaleUnitID: caja.ID, BranchID: branchA.ID, Price1: 36, Active: true})
	series := database.TenantDocumentSeries{BranchID: branchA.ID, DocType: "Nota de Venta", SunatCode: "00", Series: "NV01", Correlative: 1, Active: true}
	db.Create(&series)

	pid, suid := p.ID, caja.ID
	if _, err := createSUAtBranch(db, branchA.ID, series, SaleItemInput{
		ProductID: &pid, SaleUnitID: &suid, Quantity: 1, UnitPrice: 0.01,
		Unit: "NIU", IgvAffectationType: "10", PriceIncludesIgv: true,
	}); err == nil {
		t.Fatal("esperaba rechazo: unit_price=0.01 no es ni el override (36) ni el global (34)")
	}
}

// J: precio histórico — cambiar el override de sucursal DESPUÉS de una venta no debe alterar el
// UnitPrice ya persistido en esa venta.
func TestBranchPrice_HistoricalPrice_PreservedAfterChange(t *testing.T) {
	db := setupSaleUnitIntegrationDB(t)
	p, caja, branchA, _ := seedAguaConCaja(t, db)
	bp := database.TenantProductSaleUnitBranchPrice{SaleUnitID: caja.ID, BranchID: branchA.ID, Price1: 34, Active: true}
	db.Create(&bp)
	series := database.TenantDocumentSeries{BranchID: branchA.ID, DocType: "Nota de Venta", SunatCode: "00", Series: "NV01", Correlative: 1, Active: true}
	db.Create(&series)

	pid, suid := p.ID, caja.ID
	oldSale, err := createSUAtBranch(db, branchA.ID, series, SaleItemInput{
		ProductID: &pid, SaleUnitID: &suid, Quantity: 1, UnitPrice: 34,
		Unit: "NIU", IgvAffectationType: "10", PriceIncludesIgv: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Cambia el precio de sucursal después de la venta.
	if err := db.Model(&bp).Update("price1", 36).Error; err != nil {
		t.Fatal(err)
	}

	var reloadedOldItem database.TenantSaleItem
	db.Where("sale_id = ?", oldSale.ID).First(&reloadedOldItem)
	if reloadedOldItem.UnitPrice != 34 {
		t.Errorf("la venta antigua debe seguir mostrando 34, no recalcularse a 36: got %.2f", reloadedOldItem.UnitPrice)
	}

	// Una venta NUEVA sí usa el precio ya actualizado (36).
	newSale, err := createSUAtBranch(db, branchA.ID, series, SaleItemInput{
		ProductID: &pid, SaleUnitID: &suid, Quantity: 1, UnitPrice: 36,
		Unit: "NIU", IgvAffectationType: "10", PriceIncludesIgv: true,
	})
	if err != nil {
		t.Fatalf("la venta nueva debe usar el precio ya actualizado (36): %v", err)
	}
	if newSale.Total != 36 {
		t.Errorf("venta nueva: total = %.2f, want 36", newSale.Total)
	}
}

// K: el precio de la SaleUnit (global o de sucursal) NUNCA se multiplica por ConversionFactor.
func TestBranchPrice_NeverMultipliedByFactor(t *testing.T) {
	db := setupSaleUnitIntegrationDB(t)
	p, caja, branchA, _ := seedAguaConCaja(t, db) // factor 12, precio global 34
	db.Create(&database.TenantProductSaleUnitBranchPrice{SaleUnitID: caja.ID, BranchID: branchA.ID, Price1: 36, Active: true})
	series := database.TenantDocumentSeries{BranchID: branchA.ID, DocType: "Nota de Venta", SunatCode: "00", Series: "NV01", Correlative: 1, Active: true}
	db.Create(&series)

	pid, suid := p.ID, caja.ID
	// 34 × 12 = 408 no debe ser un precio válido; el precio cobrado debe ser 36 (override), no
	// ningún múltiplo del factor.
	if _, err := createSUAtBranch(db, branchA.ID, series, SaleItemInput{
		ProductID: &pid, SaleUnitID: &suid, Quantity: 1, UnitPrice: 408,
		Unit: "NIU", IgvAffectationType: "10", PriceIncludesIgv: true,
	}); err == nil {
		t.Fatal("esperaba rechazo: 408 (34×12) no es un precio válido, el factor nunca multiplica el precio")
	}
	sale, err := createSUAtBranch(db, branchA.ID, series, SaleItemInput{
		ProductID: &pid, SaleUnitID: &suid, Quantity: 1, UnitPrice: 36,
		Unit: "NIU", IgvAffectationType: "10", PriceIncludesIgv: true,
	})
	if err != nil {
		t.Fatalf("36 (override real) debe aceptarse: %v", err)
	}
	if sale.Total != 36 {
		t.Errorf("total = %.2f, want 36", sale.Total)
	}
}

// Override INACTIVO no se aplica — cae al global, igual que si no existiera.
func TestBranchPrice_InactiveOverride_FallsBackToGlobal(t *testing.T) {
	db := setupSaleUnitIntegrationDB(t)
	p, caja, branchA, _ := seedAguaConCaja(t, db)
	db.Create(&database.TenantProductSaleUnitBranchPrice{SaleUnitID: caja.ID, BranchID: branchA.ID, Price1: 36, Active: false})
	series := database.TenantDocumentSeries{BranchID: branchA.ID, DocType: "Nota de Venta", SunatCode: "00", Series: "NV01", Correlative: 1, Active: true}
	db.Create(&series)

	pid, suid := p.ID, caja.ID
	sale, err := createSUAtBranch(db, branchA.ID, series, SaleItemInput{
		ProductID: &pid, SaleUnitID: &suid, Quantity: 1, UnitPrice: 34,
		Unit: "NIU", IgvAffectationType: "10", PriceIncludesIgv: true,
	})
	if err != nil {
		t.Fatalf("override inactivo debe ignorarse, cayendo al global (34): %v", err)
	}
	if sale.Total != 34 {
		t.Errorf("total = %.2f, want 34 (override inactivo ignorado)", sale.Total)
	}
}
