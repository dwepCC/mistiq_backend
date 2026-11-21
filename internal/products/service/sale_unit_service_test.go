package service

import (
	"fmt"
	"strings"
	"testing"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// newSaleUnitTestProduct crea un producto mínimo para probar sus unidades de venta.
func newSaleUnitTestProduct(t *testing.T, db *gorm.DB) database.TenantProduct {
	t.Helper()
	p := database.TenantProduct{
		Code: "ARR-KG", Name: "Arroz", Type: "product", Unit: "KGM", SalePrice: 4.5,
		IgvAffectationType: "10", PriceIncludesIgv: true, Active: true,
	}
	if err := db.Create(&p).Error; err != nil {
		t.Fatal(err)
	}
	return p
}

// newSaleUnitTestUnit crea una fila de catálogo TenantUnit (Catálogo SUNAT N°03) para usar como
// UnitID de una SaleUnit — desde la corrección "Unidad Comercial Fiscal", CreateSaleUnit exige una
// unidad de medida válida (ver validateSaleUnitInput, requireUnit=true en Create).
func newSaleUnitTestUnit(t *testing.T, db *gorm.DB) uint {
	t.Helper()
	u := database.TenantUnit{Code: "BX", Name: "Caja", Symbol: "CAJ", Active: true}
	if err := db.Create(&u).Error; err != nil {
		t.Fatal(err)
	}
	return u.ID
}

// ---- Modelo / validaciones ----

// Caso 1: crear SaleUnit válida → OK.
func TestSaleUnit_Create_Valid(t *testing.T) {
	db := setupProductServiceTestDB(t)
	svc := NewProductService(db)
	p := newSaleUnitTestProduct(t, db)
	unitID := newSaleUnitTestUnit(t, db)

	u, err := svc.CreateSaleUnit(p.ID, SaleUnitInput{
		Name: "Saco 100 KG", UnitID: &unitID, ConversionFactor: 100, Price1: 430,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if u.ID == 0 || u.ProductID != p.ID || u.Name != "Saco 100 KG" || u.ConversionFactor != 100 || u.Price1 != 430 {
		t.Errorf("unidad creada inesperada: %+v", u)
	}
	if !u.Active {
		t.Error("una unidad recién creada debe quedar activa")
	}
}

// Caso 2: ConversionFactor <= 0 → rechazada.
func TestSaleUnit_Create_RejectsNonPositiveFactor(t *testing.T) {
	db := setupProductServiceTestDB(t)
	svc := NewProductService(db)
	p := newSaleUnitTestProduct(t, db)

	for _, factor := range []float64{0, -1, -0.5} {
		if _, err := svc.CreateSaleUnit(p.ID, SaleUnitInput{
			Name: "Saco", ConversionFactor: factor, Price1: 10,
		}); err == nil {
			t.Errorf("factor=%.2f debería rechazarse", factor)
		}
	}
}

// Caso 3: nombre vacío → rechazada.
func TestSaleUnit_Create_RejectsEmptyName(t *testing.T) {
	db := setupProductServiceTestDB(t)
	svc := NewProductService(db)
	p := newSaleUnitTestProduct(t, db)

	if _, err := svc.CreateSaleUnit(p.ID, SaleUnitInput{
		Name: "   ", ConversionFactor: 1, Price1: 10,
	}); err == nil {
		t.Error("nombre vacío/en blanco debería rechazarse")
	}
}

// Caso 4: precios válidos (price1 obligatorio >0, price2/price3 opcionales pero >0 si vienen) → OK.
func TestSaleUnit_Create_AcceptsValidPrices(t *testing.T) {
	db := setupProductServiceTestDB(t)
	svc := NewProductService(db)
	p := newSaleUnitTestProduct(t, db)
	unitID := newSaleUnitTestUnit(t, db)

	p2, p3 := 420.0, 410.0
	u, err := svc.CreateSaleUnit(p.ID, SaleUnitInput{
		Name: "Saco 100 KG", UnitID: &unitID, ConversionFactor: 100, Price1: 430, Price2: &p2, Price3: &p3,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if u.Price2 == nil || *u.Price2 != 420 || u.Price3 == nil || *u.Price3 != 410 {
		t.Errorf("price2/price3 no persistidos correctamente: %+v", u)
	}

	// price1 <= 0 se rechaza.
	if _, err := svc.CreateSaleUnit(p.ID, SaleUnitInput{Name: "KG", ConversionFactor: 1, Price1: 0}); err == nil {
		t.Error("price1=0 debería rechazarse")
	}
	// price2 <= 0 (si se especifica) se rechaza.
	bad := -5.0
	if _, err := svc.CreateSaleUnit(p.ID, SaleUnitInput{Name: "KG", ConversionFactor: 1, Price1: 4.5, Price2: &bad}); err == nil {
		t.Error("price2 negativo debería rechazarse")
	}
}

// Caso 5: comportamiento de Active — una unidad inactiva no aparece en ListSaleUnits (activas),
// pero sí en ListAllSaleUnits (administración).
func TestSaleUnit_Active_Behavior(t *testing.T) {
	db := setupProductServiceTestDB(t)
	svc := NewProductService(db)
	p := newSaleUnitTestProduct(t, db)
	unitID := newSaleUnitTestUnit(t, db)

	u, err := svc.CreateSaleUnit(p.ID, SaleUnitInput{Name: "Saco", UnitID: &unitID, ConversionFactor: 100, Price1: 430})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpdateSaleUnit(p.ID, u.ID, SaleUnitInput{
		Name: "Saco", ConversionFactor: 100, Price1: 430, Active: false,
	}); err != nil {
		t.Fatal(err)
	}

	active, err := svc.ListSaleUnits(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 0 {
		t.Errorf("una unidad desactivada no debe listarse como activa, got %d", len(active))
	}
	all, err := svc.ListAllSaleUnits(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 {
		t.Errorf("ListAllSaleUnits debe seguir viéndola (administración), got %d", len(all))
	}
}

// Caso 6: crear unidad base válida (factor 1, is_base=true) → OK.
func TestSaleUnit_Create_BaseUnit_Valid(t *testing.T) {
	db := setupProductServiceTestDB(t)
	svc := NewProductService(db)
	p := newSaleUnitTestProduct(t, db)
	unitID := newSaleUnitTestUnit(t, db)

	u, err := svc.CreateSaleUnit(p.ID, SaleUnitInput{
		Name: "KG", UnitID: &unitID, ConversionFactor: 1, IsBase: true, Price1: 4.5,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !u.IsBase {
		t.Error("esperaba IsBase=true")
	}

	// IsBase=true con factor distinto de 1 se rechaza.
	if _, err := svc.CreateSaleUnit(p.ID, SaleUnitInput{
		Name: "Otra base", UnitID: &unitID, ConversionFactor: 2, IsBase: true, Price1: 9,
	}); err == nil {
		t.Error("una unidad base con factor != 1 debería rechazarse")
	}
}

// Caso 7: impedir dos IsBase=true simultáneas para el mismo producto. Mismo criterio que
// TenantDocumentSeries.IsDefault (clearOtherDefaultSeriesTx): la más nueva "gana" y desmarca la
// anterior, no se rechaza la operación.
func TestSaleUnit_OnlyOneBasePerProduct(t *testing.T) {
	db := setupProductServiceTestDB(t)
	svc := NewProductService(db)
	p := newSaleUnitTestProduct(t, db)
	unitID := newSaleUnitTestUnit(t, db)

	kg, err := svc.CreateSaleUnit(p.ID, SaleUnitInput{Name: "KG", UnitID: &unitID, ConversionFactor: 1, IsBase: true, Price1: 4.5})
	if err != nil {
		t.Fatal(err)
	}
	libra, err := svc.CreateSaleUnit(p.ID, SaleUnitInput{Name: "Libra", UnitID: &unitID, ConversionFactor: 1, IsBase: true, Price1: 2})
	if err != nil {
		t.Fatalf("crear la segunda base no debe fallar: %v", err)
	}

	var reloadedKG database.TenantProductSaleUnit
	if err := db.First(&reloadedKG, kg.ID).Error; err != nil {
		t.Fatal(err)
	}
	if reloadedKG.IsBase {
		t.Error("la base anterior (KG) debe quedar desmarcada al crear una nueva base")
	}
	if !libra.IsBase {
		t.Error("la nueva base (Libra) debe quedar marcada")
	}

	// Lo mismo debe ocurrir vía UpdateSaleUnit.
	if _, err := svc.UpdateSaleUnit(p.ID, kg.ID, SaleUnitInput{
		Name: "KG", ConversionFactor: 1, IsBase: true, Price1: 4.5, Active: true,
	}); err != nil {
		t.Fatal(err)
	}
	var reloadedLibra database.TenantProductSaleUnit
	if err := db.First(&reloadedLibra, libra.ID).Error; err != nil {
		t.Fatal(err)
	}
	if reloadedLibra.IsBase {
		t.Error("al marcar KG como base de nuevo, Libra debe desmarcarse")
	}
}

// Caso 8: permitir múltiples SaleUnits no base para el mismo producto.
func TestSaleUnit_MultipleNonBaseAllowed(t *testing.T) {
	db := setupProductServiceTestDB(t)
	svc := NewProductService(db)
	p := newSaleUnitTestProduct(t, db)
	unitID := newSaleUnitTestUnit(t, db)

	names := []string{"Saco 50 KG", "Saco 100 KG"}
	factors := []float64{50, 100}
	for i, name := range names {
		if _, err := svc.CreateSaleUnit(p.ID, SaleUnitInput{
			Name: name, UnitID: &unitID, ConversionFactor: factors[i], Price1: factors[i] * 4,
		}); err != nil {
			t.Fatalf("crear %q: %v", name, err)
		}
	}
	units, err := svc.ListSaleUnits(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(units) != 2 {
		t.Fatalf("esperaba 2 unidades, got %d", len(units))
	}
}

// Casos 9/10: AllowFraction true/false se guarda y se lee tal cual (todavía sin efecto en ventas).
func TestSaleUnit_AllowFraction_Persisted(t *testing.T) {
	db := setupProductServiceTestDB(t)
	svc := NewProductService(db)
	p := newSaleUnitTestProduct(t, db)
	unitID := newSaleUnitTestUnit(t, db)

	saco, err := svc.CreateSaleUnit(p.ID, SaleUnitInput{
		Name: "Saco 100 KG", UnitID: &unitID, ConversionFactor: 100, AllowFraction: true, Price1: 430,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !saco.AllowFraction {
		t.Error("esperaba AllowFraction=true persistido")
	}

	caja, err := svc.CreateSaleUnit(p.ID, SaleUnitInput{
		Name: "Caja x24", UnitID: &unitID, ConversionFactor: 24, AllowFraction: false, Price1: 96,
	})
	if err != nil {
		t.Fatal(err)
	}
	if caja.AllowFraction {
		t.Error("esperaba AllowFraction=false persistido")
	}
}

// ---- Tenant isolation ----
//
// La aislación real de este backend es estructural (una base de datos física por tenant,
// resuelta por middleware antes de construir *gorm.DB — ver internal/products/handler/
// product_handler.go:30-33, db(c) lee c.Locals("tenantDB")). No hay columna tenant_id en
// TenantProduct/TenantProductSaleUnit porque no hace falta: dos tenants nunca comparten conexión.
// Estos tests reproducen esa realidad con dos *gorm.DB independientes, cada uno con su propio
// ProductService — exactamente como dos tenants distintos verían el backend.

func newIsolatedTenantDB(t *testing.T, suffix string) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s-%s?mode=memory&cache=shared", t.Name(), suffix)
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&database.TenantProduct{}, &database.TenantProductSaleUnit{}, &database.TenantUnit{}); err != nil {
		t.Fatal(err)
	}
	return db
}

// Caso 11: Tenant A no puede consultar una SaleUnit de Tenant B (ni siquiera con el ID correcto):
// son bases de datos físicamente distintas, la fila de A jamás existe en la conexión de B.
func TestSaleUnit_TenantIsolation_CannotGet(t *testing.T) {
	dbA := newIsolatedTenantDB(t, "A")
	dbB := newIsolatedTenantDB(t, "B")
	svcA, svcB := NewProductService(dbA), NewProductService(dbB)

	pA := newSaleUnitTestProduct(t, dbA)
	unitID := newSaleUnitTestUnit(t, dbA)
	uA, err := svcA.CreateSaleUnit(pA.ID, SaleUnitInput{Name: "Saco", UnitID: &unitID, ConversionFactor: 100, Price1: 430})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := svcB.GetSaleUnit(pA.ID, uA.ID); err == nil {
		t.Fatal("tenant B no debería poder leer una unidad de venta que solo existe en la BD del tenant A")
	}
}

// Caso 12: Tenant A no puede modificar una SaleUnit de Tenant B por el mismo motivo estructural.
func TestSaleUnit_TenantIsolation_CannotUpdate(t *testing.T) {
	dbA := newIsolatedTenantDB(t, "A")
	dbB := newIsolatedTenantDB(t, "B")
	svcA, svcB := NewProductService(dbA), NewProductService(dbB)

	pA := newSaleUnitTestProduct(t, dbA)
	unitID := newSaleUnitTestUnit(t, dbA)
	uA, err := svcA.CreateSaleUnit(pA.ID, SaleUnitInput{Name: "Saco", UnitID: &unitID, ConversionFactor: 100, Price1: 430})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := svcB.UpdateSaleUnit(pA.ID, uA.ID, SaleUnitInput{
		Name: "Hackeado", ConversionFactor: 1, Price1: 0.01, Active: true,
	}); err == nil {
		t.Fatal("tenant B no debería poder modificar una unidad de venta del tenant A")
	}
	// Confirma que en la BD real (tenant A) no cambió nada.
	var reloaded database.TenantProductSaleUnit
	if err := dbA.First(&reloaded, uA.ID).Error; err != nil {
		t.Fatal(err)
	}
	if reloaded.Name != "Saco" || reloaded.Price1 != 430 {
		t.Errorf("la unidad del tenant A no debe alterarse por un intento del tenant B: %+v", reloaded)
	}
}

// Caso 13: Tenant A no puede eliminar/desactivar una SaleUnit de Tenant B.
func TestSaleUnit_TenantIsolation_CannotDelete(t *testing.T) {
	dbA := newIsolatedTenantDB(t, "A")
	dbB := newIsolatedTenantDB(t, "B")
	svcA, svcB := NewProductService(dbA), NewProductService(dbB)

	pA := newSaleUnitTestProduct(t, dbA)
	unitID := newSaleUnitTestUnit(t, dbA)
	uA, err := svcA.CreateSaleUnit(pA.ID, SaleUnitInput{Name: "Saco", UnitID: &unitID, ConversionFactor: 100, Price1: 430})
	if err != nil {
		t.Fatal(err)
	}

	if err := svcB.DeleteSaleUnit(pA.ID, uA.ID); err == nil {
		t.Fatal("tenant B no debería poder eliminar una unidad de venta del tenant A")
	}
	if err := dbA.First(&database.TenantProductSaleUnit{}, uA.ID).Error; err != nil {
		t.Fatalf("la unidad del tenant A debe seguir existiendo: %v", err)
	}
}

// ---- CRUD ----

// Casos 14-17: Create/List/Get/Update, ciclo completo.
func TestSaleUnit_CRUD_FullCycle(t *testing.T) {
	db := setupProductServiceTestDB(t)
	svc := NewProductService(db)
	p := newSaleUnitTestProduct(t, db)
	unitID := newSaleUnitTestUnit(t, db)

	created, err := svc.CreateSaleUnit(p.ID, SaleUnitInput{Name: "Saco 100 KG", UnitID: &unitID, ConversionFactor: 100, Price1: 430}) // Create
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	list, err := svc.ListSaleUnits(p.ID) // List
	if err != nil || len(list) != 1 {
		t.Fatalf("List: %v, len=%d", err, len(list))
	}

	got, err := svc.GetSaleUnit(p.ID, created.ID) // Get
	if err != nil || got.Name != "Saco 100 KG" {
		t.Fatalf("Get: %v, %+v", err, got)
	}

	updated, err := svc.UpdateSaleUnit(p.ID, created.ID, SaleUnitInput{ // Update
		Name: "Saco 100 KG (renombrado)", ConversionFactor: 100, Price1: 440, Active: true,
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Name != "Saco 100 KG (renombrado)" || updated.Price1 != 440 {
		t.Errorf("update no aplicado: %+v", updated)
	}
}

// Caso 18: Deactivate/Active vía Update (mismo patrón que UpdateBrand/UpdateUnit: Active es un
// campo más del payload de actualización, no un endpoint de toggle separado).
func TestSaleUnit_CRUD_Deactivate(t *testing.T) {
	db := setupProductServiceTestDB(t)
	svc := NewProductService(db)
	p := newSaleUnitTestProduct(t, db)
	unitID := newSaleUnitTestUnit(t, db)

	u, err := svc.CreateSaleUnit(p.ID, SaleUnitInput{Name: "Saco", UnitID: &unitID, ConversionFactor: 100, Price1: 430})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpdateSaleUnit(p.ID, u.ID, SaleUnitInput{
		Name: "Saco", ConversionFactor: 100, Price1: 430, Active: false,
	}); err != nil {
		t.Fatal(err)
	}
	reloaded, err := svc.GetSaleUnit(p.ID, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Active {
		t.Error("esperaba Active=false tras desactivar")
	}
	// No es soft-delete: reactivable.
	if _, err := svc.UpdateSaleUnit(p.ID, u.ID, SaleUnitInput{
		Name: "Saco", ConversionFactor: 100, Price1: 430, Active: true,
	}); err != nil {
		t.Fatal(err)
	}
	reloaded, _ = svc.GetSaleUnit(p.ID, u.ID)
	if !reloaded.Active {
		t.Error("esperaba poder reactivar")
	}
}

// ---- Legacy ----

// Caso 19: un producto existente sin ninguna SaleUnit sigue funcionando exactamente igual — no
// hay ningún camino de código que se dispare solo por la existencia de la nueva tabla.
func TestSaleUnit_Legacy_ProductWithoutSaleUnitsUnaffected(t *testing.T) {
	db := setupProductServiceTestDB(t)
	p := newSaleUnitTestProduct(t, db)

	units, err := NewProductService(db).ListSaleUnits(p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(units) != 0 {
		t.Fatalf("un producto legacy no debe tener unidades de venta implícitas, got %d", len(units))
	}
	var reloaded database.TenantProduct
	if err := db.First(&reloaded, p.ID).Error; err != nil {
		t.Fatal(err)
	}
	if reloaded.SalePrice != 4.5 || reloaded.Unit != "KGM" {
		t.Errorf("el producto legacy no debe cambiar: %+v", reloaded)
	}
}

// Caso 20: crear/editar SaleUnits de un producto no modifica TenantProduct.SalePrice.
func TestSaleUnit_DoesNotTouchProductSalePrice(t *testing.T) {
	db := setupProductServiceTestDB(t)
	svc := NewProductService(db)
	p := newSaleUnitTestProduct(t, db)
	unitID := newSaleUnitTestUnit(t, db)

	if _, err := svc.CreateSaleUnit(p.ID, SaleUnitInput{Name: "Saco 100 KG", UnitID: &unitID, ConversionFactor: 100, Price1: 430}); err != nil {
		t.Fatal(err)
	}
	var reloaded database.TenantProduct
	if err := db.First(&reloaded, p.ID).Error; err != nil {
		t.Fatal(err)
	}
	if reloaded.SalePrice != 4.5 {
		t.Errorf("TenantProduct.SalePrice no debe alterarse al crear unidades de venta, got %.2f", reloaded.SalePrice)
	}
}

// ---- Test de no integración ----

// Crear una unidad de venta (incluso una "Saco 100 KG" con factor 100, el caso que en una fase
// futura descontaría 100 KG de stock) no debe crear stock, Kardex, ni tocar TenantProductStock,
// ventas o compras — esta fase es solo el modelo y su administración.
func TestSaleUnit_DoesNotIntegrateWithInventoryYet(t *testing.T) {
	db := setupProductServiceTestDB(t)
	if err := db.AutoMigrate(&database.TenantProductStock{}, &database.TenantStockMovement{}); err != nil {
		t.Fatal(err)
	}
	svc := NewProductService(db)
	p := newSaleUnitTestProduct(t, db)
	unitID := newSaleUnitTestUnit(t, db)
	if err := db.Create(&database.TenantProductStock{ProductID: p.ID, BranchID: 1, Quantity: 1000}).Error; err != nil {
		t.Fatal(err)
	}

	if _, err := svc.CreateSaleUnit(p.ID, SaleUnitInput{
		Name: "Saco 100 KG", UnitID: &unitID, ConversionFactor: 100, Price1: 430,
	}); err != nil {
		t.Fatal(err)
	}

	var stock database.TenantProductStock
	if err := db.Where("product_id = ? AND branch_id = ?", p.ID, 1).First(&stock).Error; err != nil {
		t.Fatal(err)
	}
	if stock.Quantity != 1000 {
		t.Errorf("crear una unidad de venta no debe tocar el stock, quedó en %g (esperaba 1000)", stock.Quantity)
	}
	var movements int64
	db.Model(&database.TenantStockMovement{}).Count(&movements)
	if movements != 0 {
		t.Errorf("crear una unidad de venta no debe generar Kardex, got %d movimientos", movements)
	}
}

// Sanity check adicional: el mensaje de error de "no encontrada" no debe filtrar si la fila
// existe en otro tenant o no existe en absoluto (mismo mensaje en ambos casos).
func TestSaleUnit_NotFoundMessage_DoesNotLeakExistence(t *testing.T) {
	db := setupProductServiceTestDB(t)
	svc := NewProductService(db)
	p := newSaleUnitTestProduct(t, db)

	_, errNonExistent := svc.GetSaleUnit(p.ID, 9999)
	_, errWrongProduct := svc.GetSaleUnit(9999, 1)
	if errNonExistent == nil || errWrongProduct == nil {
		t.Fatal("ambos casos deben fallar")
	}
	if !strings.Contains(errNonExistent.Error(), "no encontrada") || !strings.Contains(errWrongProduct.Error(), "no encontrada") {
		t.Errorf("mensajes inesperados: %v / %v", errNonExistent, errWrongProduct)
	}
}
