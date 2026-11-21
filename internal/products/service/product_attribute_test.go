package service

import (
	"fmt"
	"strings"
	"testing"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func newAttributeTestProduct(t *testing.T, db *gorm.DB) database.TenantProduct {
	t.Helper()
	p := database.TenantProduct{
		Code: "POLO", Name: "Polo", Type: "product", Unit: "NIU", SalePrice: 25, Active: true,
	}
	if err := db.Create(&p).Error; err != nil {
		t.Fatal(err)
	}
	return p
}

// A: ciclo completo Create/List/Get/Update/Delete.
func TestProductAttribute_CRUD_FullCycle(t *testing.T) {
	db := setupProductServiceTestDB(t)
	svc := NewProductService(db)
	p := newAttributeTestProduct(t, db)

	created, err := svc.CreateProductAttribute(p.ID, ProductAttributeInput{Name: "Color", Value: "Rojo"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.Name != "Color" || created.Value != "Rojo" || !created.Active {
		t.Errorf("atributo creado inesperado: %+v", created)
	}

	list, err := svc.ListProductAttributes(p.ID)
	if err != nil || len(list) != 1 {
		t.Fatalf("List: %v, len=%d", err, len(list))
	}

	got, err := svc.GetProductAttribute(p.ID, created.ID)
	if err != nil || got.Value != "Rojo" {
		t.Fatalf("Get: %v, %+v", err, got)
	}

	updated, err := svc.UpdateProductAttribute(p.ID, created.ID, ProductAttributeInput{Name: "Color", Value: "Azul", Active: true})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Value != "Azul" {
		t.Errorf("update no aplicado: %+v", updated)
	}

	if err := svc.DeleteProductAttribute(p.ID, created.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := svc.GetProductAttribute(p.ID, created.ID); err == nil {
		t.Error("esperaba que el atributo ya no exista tras Delete (borrado físico)")
	}
}

// B: validaciones de nombre/valor vacíos y longitud.
func TestProductAttribute_Validations(t *testing.T) {
	db := setupProductServiceTestDB(t)
	svc := NewProductService(db)
	p := newAttributeTestProduct(t, db)

	if _, err := svc.CreateProductAttribute(p.ID, ProductAttributeInput{Name: "", Value: "Rojo"}); err == nil {
		t.Error("nombre vacío debería rechazarse")
	}
	if _, err := svc.CreateProductAttribute(p.ID, ProductAttributeInput{Name: "Color", Value: "   "}); err == nil {
		t.Error("valor vacío/en blanco debería rechazarse")
	}
	if _, err := svc.CreateProductAttribute(p.ID, ProductAttributeInput{Name: strings.Repeat("A", 101), Value: "X"}); err == nil {
		t.Error("nombre de más de 100 caracteres debería rechazarse")
	}
	if _, err := svc.CreateProductAttribute(p.ID, ProductAttributeInput{Name: "Descripción", Value: strings.Repeat("A", 256)}); err == nil {
		t.Error("valor de más de 255 caracteres debería rechazarse")
	}
}

// C: producto inexistente → rechazado.
func TestProductAttribute_RejectsNonExistentProduct(t *testing.T) {
	db := setupProductServiceTestDB(t)
	svc := NewProductService(db)
	if _, err := svc.CreateProductAttribute(99999, ProductAttributeInput{Name: "Color", Value: "Rojo"}); err == nil {
		t.Fatal("esperaba rechazo: producto inexistente")
	}
}

// D: aislación de tenant — misma arquitectura estructural que SaleUnit/BranchPrice (una base de
// datos física por tenant); un atributo creado en la BD del tenant A es inalcanzable desde la BD
// del tenant B, aunque se use el mismo ID.
func TestProductAttribute_TenantIsolation(t *testing.T) {
	dsnA := fmt.Sprintf("file:%s-A?mode=memory&cache=shared", t.Name())
	dbA, err := gorm.Open(sqlite.Open(dsnA), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	dsnB := fmt.Sprintf("file:%s-B?mode=memory&cache=shared", t.Name())
	dbB, err := gorm.Open(sqlite.Open(dsnB), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, db := range []*gorm.DB{dbA, dbB} {
		if err := db.AutoMigrate(&database.TenantProduct{}, &database.TenantProductAttribute{}); err != nil {
			t.Fatal(err)
		}
	}
	svcA, svcB := NewProductService(dbA), NewProductService(dbB)
	pA := newAttributeTestProduct(t, dbA)
	attrA, err := svcA.CreateProductAttribute(pA.ID, ProductAttributeInput{Name: "Color", Value: "Rojo"})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := svcB.GetProductAttribute(pA.ID, attrA.ID); err == nil {
		t.Fatal("tenant B no debería poder leer un atributo que solo existe en la BD del tenant A")
	}
	if _, err := svcB.UpdateProductAttribute(pA.ID, attrA.ID, ProductAttributeInput{Name: "Color", Value: "Hackeado", Active: true}); err == nil {
		t.Fatal("tenant B no debería poder modificar un atributo del tenant A")
	}
	if err := svcB.DeleteProductAttribute(pA.ID, attrA.ID); err == nil {
		t.Fatal("tenant B no debería poder eliminar un atributo del tenant A")
	}
}

// E: duplicados — mismo nombre+valor se rechaza; mismo nombre con valor distinto se permite.
func TestProductAttribute_Duplicates(t *testing.T) {
	db := setupProductServiceTestDB(t)
	svc := NewProductService(db)
	p := newAttributeTestProduct(t, db)

	if _, err := svc.CreateProductAttribute(p.ID, ProductAttributeInput{Name: "Color", Value: "Rojo"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateProductAttribute(p.ID, ProductAttributeInput{Name: "color", Value: "rojo"}); err == nil {
		t.Fatal("esperaba rechazo: 'Color=Rojo' ya existe (comparación insensible a mayúsculas)")
	}
	if _, err := svc.CreateProductAttribute(p.ID, ProductAttributeInput{Name: "Color", Value: "Azul"}); err != nil {
		t.Fatalf("Color=Azul debe permitirse junto a Color=Rojo: %v", err)
	}
}

// F: múltiples atributos distintos coexisten correctamente.
func TestProductAttribute_MultipleAttributesCoexist(t *testing.T) {
	db := setupProductServiceTestDB(t)
	svc := NewProductService(db)
	p := newAttributeTestProduct(t, db)

	for _, kv := range [][2]string{{"Color", "Rojo"}, {"Talla", "L"}, {"Material", "Algodón"}} {
		if _, err := svc.CreateProductAttribute(p.ID, ProductAttributeInput{Name: kv[0], Value: kv[1]}); err != nil {
			t.Fatalf("crear %s=%s: %v", kv[0], kv[1], err)
		}
	}
	list, err := svc.ListProductAttributes(p.ID)
	if err != nil || len(list) != 3 {
		t.Fatalf("esperaba 3 atributos, got %d (%v)", len(list), err)
	}
}

// G: crear un atributo NO crea ni modifica ninguna SaleUnit.
func TestProductAttribute_DoesNotCreateSaleUnit(t *testing.T) {
	db := setupProductServiceTestDB(t)
	svc := NewProductService(db)
	p := newAttributeTestProduct(t, db)
	unitID := newSaleUnitTestUnit(t, db)
	if _, err := svc.CreateSaleUnit(p.ID, SaleUnitInput{Name: "Unidad", UnitID: &unitID, ConversionFactor: 1, Price1: 25}); err != nil {
		t.Fatal(err)
	}

	if _, err := svc.CreateProductAttribute(p.ID, ProductAttributeInput{Name: "Color", Value: "Rojo"}); err != nil {
		t.Fatal(err)
	}

	units, err := svc.ListSaleUnits(p.ID)
	if err != nil || len(units) != 1 {
		t.Fatalf("crear un atributo no debe alterar las SaleUnits del producto, got %d (%v)", len(units), err)
	}
}

// H: crear un atributo NO crea stock ni Kardex.
func TestProductAttribute_DoesNotCreateStockOrKardex(t *testing.T) {
	db := setupProductServiceTestDB(t)
	svc := NewProductService(db)
	p := newAttributeTestProduct(t, db)
	if err := db.Create(&database.TenantProductStock{ProductID: p.ID, BranchID: 1, Quantity: 1000}).Error; err != nil {
		t.Fatal(err)
	}

	if _, err := svc.CreateProductAttribute(p.ID, ProductAttributeInput{Name: "Color", Value: "Rojo"}); err != nil {
		t.Fatal(err)
	}

	var stock database.TenantProductStock
	db.Where("product_id = ? AND branch_id = ?", p.ID, 1).First(&stock)
	if stock.Quantity != 1000 {
		t.Errorf("crear un atributo no debe tocar el stock, quedó en %g", stock.Quantity)
	}
	var movements int64
	db.Model(&database.TenantStockMovement{}).Count(&movements)
	if movements != 0 {
		t.Errorf("crear un atributo no debe generar Kardex, got %d movimientos", movements)
	}
}

// I: crear/editar atributos NO cambia ningún precio (Product.SalePrice, SaleUnit.Price1/2/3,
// BranchPrice).
func TestProductAttribute_DoesNotChangeAnyPrice(t *testing.T) {
	db := setupProductServiceTestDB(t)
	svc := NewProductService(db)
	p := newAttributeTestProduct(t, db)
	unitID := newSaleUnitTestUnit(t, db)
	su, err := svc.CreateSaleUnit(p.ID, SaleUnitInput{Name: "Caja x12", UnitID: &unitID, ConversionFactor: 12, Price1: 34, Active: true})
	if err != nil {
		t.Fatal(err)
	}
	branch := database.TenantBranch{Name: "Sucursal A", Active: true}
	db.Create(&branch)
	if _, err := svc.CreateSaleUnitBranchPrice(p.ID, su.ID, branch.ID, SaleUnitBranchPriceInput{Price1: 36, Active: true}); err != nil {
		t.Fatal(err)
	}

	attr, err := svc.CreateProductAttribute(p.ID, ProductAttributeInput{Name: "Color", Value: "Rojo"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpdateProductAttribute(p.ID, attr.ID, ProductAttributeInput{Name: "Color", Value: "Azul", Active: true}); err != nil {
		t.Fatal(err)
	}

	var reloadedProduct database.TenantProduct
	db.First(&reloadedProduct, p.ID)
	if reloadedProduct.SalePrice != 25 {
		t.Errorf("Product.SalePrice cambió: got %.2f, want 25", reloadedProduct.SalePrice)
	}
	var reloadedSU database.TenantProductSaleUnit
	db.First(&reloadedSU, su.ID)
	if reloadedSU.Price1 != 34 {
		t.Errorf("SaleUnit.Price1 cambió: got %.2f, want 34", reloadedSU.Price1)
	}
	bp, err := svc.GetSaleUnitBranchPrice(p.ID, su.ID, branch.ID)
	if err != nil || bp.Price1 != 36 {
		t.Errorf("BranchPrice cambió o desapareció: %v, %+v", err, bp)
	}
}
