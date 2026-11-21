package service

import (
	"strings"
	"sync"
	"testing"

	"tukifac/pkg/database"
)

// Fase 6.1, corrección 4: el error de duplicado debe ser un mensaje de negocio legible, tanto en
// el camino normal (chequeo previo) como bajo una carrera real de dos requests concurrentes.

// isDuplicateBranchPriceError debe reconocer el error real que devuelve el driver ante una
// violación del índice único idx_sale_unit_branch_price (probado contra el mensaje real de
// SQLite, que es el driver que usan estos tests; el mismo detector también reconoce "1062", el
// código de MySQL en producción).
func TestIsDuplicateBranchPriceError_RecognizesRealConstraintMessage(t *testing.T) {
	sqliteMsg := "constraint failed: UNIQUE constraint failed: tenant_product_sale_unit_branch_prices.sale_unit_id, tenant_product_sale_unit_branch_prices.branch_id (2067)"
	if !isDuplicateBranchPriceError(errString(sqliteMsg)) {
		t.Fatal("esperaba reconocer el mensaje real de violación de unique constraint de SQLite")
	}
	mysqlMsg := "Error 1062: Duplicate entry '1-1' for key 'idx_sale_unit_branch_price'"
	if !isDuplicateBranchPriceError(errString(mysqlMsg)) {
		t.Fatal("esperaba reconocer el mensaje real de MySQL (1062)")
	}
	if isDuplicateBranchPriceError(errString("sucursal no encontrada")) {
		t.Fatal("no debería confundir un error de negocio cualquiera con un duplicado")
	}
}

type errString string

func (e errString) Error() string { return string(e) }

// Bajo una carrera real (dos goroutines creando el mismo par SaleUnit+Branch concurrentemente),
// exactamente una debe tener éxito y la otra debe recibir el mensaje de negocio legible — nunca
// el error crudo del driver, y nunca deben quedar dos filas activas para el mismo par.
func TestSaleUnitBranchPrice_ConcurrentCreate_OnlyOneSucceedsWithFriendlyError(t *testing.T) {
	db := setupProductServiceTestDB(t)
	svc := NewProductService(db)
	p := database.TenantProduct{Code: "AGUA", Name: "Agua", Type: "product", Unit: "NIU", SalePrice: 3, Active: true}
	db.Create(&p)
	caja := database.TenantProductSaleUnit{ProductID: p.ID, Name: "Caja x12", ConversionFactor: 12, Price1: 34, Active: true}
	db.Create(&caja)
	branchA := database.TenantBranch{Name: "Sucursal A", Active: true}
	db.Create(&branchA)

	var wg sync.WaitGroup
	errs := make([]error, 2)
	start := make(chan struct{})
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, err := svc.CreateSaleUnitBranchPrice(p.ID, caja.ID, branchA.ID, SaleUnitBranchPriceInput{
				Price1: 36, Active: true,
			})
			errs[i] = err
		}(i)
	}
	close(start)
	wg.Wait()

	successes, friendlyRejections := 0, 0
	for _, err := range errs {
		if err == nil {
			successes++
			continue
		}
		if strings.Contains(err.Error(), "ya existe un precio configurado") {
			friendlyRejections++
			continue
		}
		t.Errorf("error inesperado (no es ni éxito ni el mensaje de negocio esperado): %v", err)
	}
	if successes != 1 {
		t.Errorf("esperaba exactamente 1 creación exitosa, got %d", successes)
	}
	if friendlyRejections != 1 {
		t.Errorf("esperaba exactamente 1 rechazo con mensaje de negocio legible, got %d", friendlyRejections)
	}

	var count int64
	db.Model(&database.TenantProductSaleUnitBranchPrice{}).
		Where("sale_unit_id = ? AND branch_id = ?", caja.ID, branchA.ID).Count(&count)
	if count != 1 {
		t.Errorf("debe quedar exactamente 1 fila para el par (SaleUnit, Branch), got %d", count)
	}
}

func TestSaleUnitBranchPrice_CRUD_FullCycle(t *testing.T) {
	db := setupProductServiceTestDB(t)
	svc := NewProductService(db)

	p := database.TenantProduct{Code: "AGUA", Name: "Agua", Type: "product", Unit: "NIU", SalePrice: 3, Active: true}
	if err := db.Create(&p).Error; err != nil {
		t.Fatal(err)
	}
	caja := database.TenantProductSaleUnit{ProductID: p.ID, Name: "Caja x12", ConversionFactor: 12, Price1: 34, Active: true}
	if err := db.Create(&caja).Error; err != nil {
		t.Fatal(err)
	}
	branchA := database.TenantBranch{Name: "Sucursal A", Active: true}
	if err := db.Create(&branchA).Error; err != nil {
		t.Fatal(err)
	}

	// Create
	created, err := svc.CreateSaleUnitBranchPrice(p.ID, caja.ID, branchA.ID, SaleUnitBranchPriceInput{
		Price1: 36, Active: true,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.Price1 != 36 {
		t.Errorf("Price1 = %.2f, want 36", created.Price1)
	}

	// List
	list, err := svc.ListSaleUnitBranchPrices(p.ID, caja.ID)
	if err != nil || len(list) != 1 {
		t.Fatalf("List: %v, len=%d", err, len(list))
	}

	// Get
	got, err := svc.GetSaleUnitBranchPrice(p.ID, caja.ID, branchA.ID)
	if err != nil || got.Price1 != 36 {
		t.Fatalf("Get: %v, %+v", err, got)
	}

	// Update
	p2 := 34.0
	updated, err := svc.UpdateSaleUnitBranchPrice(p.ID, caja.ID, branchA.ID, SaleUnitBranchPriceInput{
		Price1: 38, Price2: &p2, Active: true,
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Price1 != 38 || updated.Price2 == nil || *updated.Price2 != 34 {
		t.Errorf("update no aplicado: %+v", updated)
	}

	// Delete (físico, sin soft delete)
	if err := svc.DeleteSaleUnitBranchPrice(p.ID, caja.ID, branchA.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := svc.GetSaleUnitBranchPrice(p.ID, caja.ID, branchA.ID); err == nil {
		t.Error("esperaba que el precio de sucursal ya no exista tras Delete")
	}
	// Se puede volver a crear para el mismo par tras eliminarlo (el DELETE es físico).
	if _, err := svc.CreateSaleUnitBranchPrice(p.ID, caja.ID, branchA.ID, SaleUnitBranchPriceInput{
		Price1: 40, Active: true,
	}); err != nil {
		t.Fatalf("recrear tras Delete debe funcionar: %v", err)
	}
}

// Rechaza duplicados: no se puede crear un segundo precio para el mismo (SaleUnit, Branch).
func TestSaleUnitBranchPrice_RejectsDuplicate(t *testing.T) {
	db := setupProductServiceTestDB(t)
	svc := NewProductService(db)

	p := database.TenantProduct{Code: "AGUA", Name: "Agua", Type: "product", Unit: "NIU", SalePrice: 3, Active: true}
	db.Create(&p)
	caja := database.TenantProductSaleUnit{ProductID: p.ID, Name: "Caja x12", ConversionFactor: 12, Price1: 34, Active: true}
	db.Create(&caja)
	branchA := database.TenantBranch{Name: "Sucursal A", Active: true}
	db.Create(&branchA)

	if _, err := svc.CreateSaleUnitBranchPrice(p.ID, caja.ID, branchA.ID, SaleUnitBranchPriceInput{Price1: 36, Active: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateSaleUnitBranchPrice(p.ID, caja.ID, branchA.ID, SaleUnitBranchPriceInput{Price1: 40, Active: true}); err == nil {
		t.Fatal("esperaba rechazo: ya existe un precio para (SaleUnit, Branch)")
	}
}

// Sucursal inexistente → rechazada.
func TestSaleUnitBranchPrice_RejectsNonExistentBranch(t *testing.T) {
	db := setupProductServiceTestDB(t)
	svc := NewProductService(db)
	p := database.TenantProduct{Code: "AGUA", Name: "Agua", Type: "product", Unit: "NIU", SalePrice: 3, Active: true}
	db.Create(&p)
	caja := database.TenantProductSaleUnit{ProductID: p.ID, Name: "Caja x12", ConversionFactor: 12, Price1: 34, Active: true}
	db.Create(&caja)

	if _, err := svc.CreateSaleUnitBranchPrice(p.ID, caja.ID, 99999, SaleUnitBranchPriceInput{Price1: 36, Active: true}); err == nil {
		t.Fatal("esperaba rechazo: sucursal inexistente")
	}
}

// SaleUnit perteneciente a otro producto → rechazada.
func TestSaleUnitBranchPrice_RejectsSaleUnitFromOtherProduct(t *testing.T) {
	db := setupProductServiceTestDB(t)
	svc := NewProductService(db)
	p1 := database.TenantProduct{Code: "AGUA", Name: "Agua", Type: "product", Unit: "NIU", SalePrice: 3, Active: true}
	db.Create(&p1)
	p2 := database.TenantProduct{Code: "GAS", Name: "Gaseosa", Type: "product", Unit: "NIU", SalePrice: 3, Active: true}
	db.Create(&p2)
	cajaDeP2 := database.TenantProductSaleUnit{ProductID: p2.ID, Name: "Caja x12", ConversionFactor: 12, Price1: 34, Active: true}
	db.Create(&cajaDeP2)
	branchA := database.TenantBranch{Name: "Sucursal A", Active: true}
	db.Create(&branchA)

	if _, err := svc.CreateSaleUnitBranchPrice(p1.ID, cajaDeP2.ID, branchA.ID, SaleUnitBranchPriceInput{Price1: 36, Active: true}); err == nil {
		t.Fatal("esperaba rechazo: la SaleUnit pertenece a otro producto")
	}
}

// Price1 <= 0, o Price2/Price3 <= 0 si se especifican, se rechazan (mismas reglas que SaleUnit).
func TestSaleUnitBranchPrice_ValidatesPrices(t *testing.T) {
	db := setupProductServiceTestDB(t)
	svc := NewProductService(db)
	p := database.TenantProduct{Code: "AGUA", Name: "Agua", Type: "product", Unit: "NIU", SalePrice: 3, Active: true}
	db.Create(&p)
	caja := database.TenantProductSaleUnit{ProductID: p.ID, Name: "Caja x12", ConversionFactor: 12, Price1: 34, Active: true}
	db.Create(&caja)
	branchA := database.TenantBranch{Name: "Sucursal A", Active: true}
	db.Create(&branchA)

	if _, err := svc.CreateSaleUnitBranchPrice(p.ID, caja.ID, branchA.ID, SaleUnitBranchPriceInput{Price1: 0, Active: true}); err == nil {
		t.Error("price1=0 debería rechazarse")
	}
	bad := -1.0
	if _, err := svc.CreateSaleUnitBranchPrice(p.ID, caja.ID, branchA.ID, SaleUnitBranchPriceInput{Price1: 36, Price2: &bad, Active: true}); err == nil {
		t.Error("price2 negativo debería rechazarse")
	}
}
