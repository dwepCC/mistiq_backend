package service

import (
	"testing"

	"tukifac/pkg/database"
)

// Corrección "Unidad Comercial Fiscal": TenantProductSaleUnit gana UnitID/Unit (unidad comercial
// SUNAT propia, ej. "Caja" → BX), siguiendo el mismo patrón que TenantProduct.UnitID/Unit
// (resolveUnitReference). Estos tests cubren la validación/resolución a nivel de catálogo — la
// resolución al vender (SaleItem.Unit) se prueba en internal/sales/service/sale_unit_unit_code_test.go.

// Una SaleUnit nueva sin UnitID se rechaza — "SaleUnits nuevas deben exigir una unidad válida".
func TestSaleUnit_Create_RequiresUnitID(t *testing.T) {
	db := setupProductServiceTestDB(t)
	svc := NewProductService(db)
	p := newSaleUnitTestProduct(t, db)

	if _, err := svc.CreateSaleUnit(p.ID, SaleUnitInput{
		Name: "Caja", ConversionFactor: 12, Price1: 45,
	}); err == nil {
		t.Fatal("una SaleUnit nueva sin UnitID debería rechazarse")
	}
}

// Un UnitID que no existe en TenantUnit se rechaza (no se acepta un ID inventado ni se cae a NIU
// silenciosamente).
func TestSaleUnit_Create_RejectsInvalidUnitID(t *testing.T) {
	db := setupProductServiceTestDB(t)
	svc := NewProductService(db)
	p := newSaleUnitTestProduct(t, db)
	bogus := uint(999999)

	if _, err := svc.CreateSaleUnit(p.ID, SaleUnitInput{
		Name: "Caja", UnitID: &bogus, ConversionFactor: 12, Price1: 45,
	}); err == nil {
		t.Fatal("un UnitID inexistente debería rechazarse")
	}
}

// Con un UnitID válido, CreateSaleUnit resuelve y persiste el código real del catálogo (TenantUnit.Code)
// — nunca lo deriva del nombre de la SaleUnit ("Caja" no implica "BX" por sí solo).
func TestSaleUnit_Create_ResolvesUnitCodeFromCatalog(t *testing.T) {
	db := setupProductServiceTestDB(t)
	svc := NewProductService(db)
	p := newSaleUnitTestProduct(t, db)
	unitID := newSaleUnitTestUnit(t, db) // TenantUnit{Code: "BX", Name: "Caja"}

	u, err := svc.CreateSaleUnit(p.ID, SaleUnitInput{
		// Nombre comercial deliberadamente distinto del nombre de la unidad SUNAT, para probar
		// que no hay ninguna derivación mágica entre ambos.
		Name: "Caja Grande", UnitID: &unitID, ConversionFactor: 12, Price1: 45,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if u.UnitID == nil || *u.UnitID != unitID {
		t.Errorf("UnitID = %v, want %d", u.UnitID, unitID)
	}
	if u.Unit != "BX" {
		t.Errorf("Unit = %q, want BX (resuelto desde TenantUnit.Code, no desde el nombre)", u.Unit)
	}
	if u.ConversionFactor != 12 {
		t.Errorf("ConversionFactor = %v, want 12 — el factor no debe derivarse ni verse afectado por el código de unidad", u.ConversionFactor)
	}
}

// UpdateSaleUnit NO exige UnitID — una SaleUnit creada antes de esta corrección (sin unidad
// configurada) debe poder seguir editándose (ej. cambiar el precio) sin verse forzada a completar
// retroactivamente su unidad SUNAT.
func TestSaleUnit_Update_UnitIDOptional_PreExistingRowKeepsWorking(t *testing.T) {
	db := setupProductServiceTestDB(t)
	svc := NewProductService(db)
	p := newSaleUnitTestProduct(t, db)
	unitID := newSaleUnitTestUnit(t, db)

	// Simula una SaleUnit creada ANTES de esta corrección (unit_id/unit vacíos), vaciándolos
	// manualmente después de crearla con CreateSaleUnit (que hoy exigiría UnitID).
	created, err := svc.CreateSaleUnit(p.ID, SaleUnitInput{Name: "Caja", UnitID: &unitID, ConversionFactor: 12, Price1: 45})
	if err != nil {
		t.Fatal(err)
	}
	// Vacía manualmente unit_id/unit para reproducir el estado "creada antes de esta fase".
	if err := db.Model(created).Updates(map[string]interface{}{"unit_id": nil, "unit": ""}).Error; err != nil {
		t.Fatal(err)
	}

	updated, err := svc.UpdateSaleUnit(p.ID, created.ID, SaleUnitInput{
		Name: "Caja", ConversionFactor: 12, Price1: 48, Active: true, // sin UnitID
	})
	if err != nil {
		t.Fatalf("Update sin UnitID no debería fallar para una fila preexistente: %v", err)
	}
	if updated.Price1 != 48 {
		t.Errorf("Price1 = %v, want 48 (el update debe aplicarse igual)", updated.Price1)
	}
	if updated.UnitID != nil || updated.Unit != "" {
		t.Errorf("UnitID/Unit deberían seguir vacíos (no se inventa nada), got UnitID=%v Unit=%q", updated.UnitID, updated.Unit)
	}
}

// Una unidad de venta fraccionaria (factor < 1) no puede declarar el mismo código SUNAT que la unidad base:
// "Gramo" (factor 0.001) con KGM haría que 250 g salgan como 250 KGM en el comprobante.
func TestSaleUnit_Create_RejectsSameUnitCodeAsBaseWhenFractional(t *testing.T) {
	db := setupProductServiceTestDB(t)
	svc := NewProductService(db)
	p := newSaleUnitTestProduct(t, db) // unidad base KGM
	kgm := database.TenantUnit{Code: "KGM", Name: "Kilos", Active: true}
	grm := database.TenantUnit{Code: "GRM", Name: "Gramos", Active: true}
	if err := db.Create(&kgm).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&grm).Error; err != nil {
		t.Fatal(err)
	}

	if _, err := svc.CreateSaleUnit(p.ID, SaleUnitInput{
		Name: "Gramo", UnitID: &kgm.ID, ConversionFactor: 0.001, Price1: 0.0045,
	}); err == nil {
		t.Fatal("Gramo con código KGM (el de la unidad base) debería rechazarse")
	}

	u, err := svc.CreateSaleUnit(p.ID, SaleUnitInput{
		Name: "Gramo", UnitID: &grm.ID, ConversionFactor: 0.001, Price1: 0.0045,
	})
	if err != nil {
		t.Fatalf("Gramo con GRM debería aceptarse: %v", err)
	}
	if u.Unit != "GRM" {
		t.Errorf("Unit = %q, want GRM", u.Unit)
	}
	if u.Price1 != 0.0045 {
		t.Errorf("Price1 = %v, want 0.0045", u.Price1)
	}
}

// Las presentaciones con factor > 1 (ej. "Caja x12" con el mismo código que la base) ya existen en
// producción: se siguen aceptando al crear y, sobre todo, al editar (por ejemplo cambiar su precio).
func TestSaleUnit_SameUnitCodeAsBaseAllowedWhenFactorGreaterThanOne(t *testing.T) {
	db := setupProductServiceTestDB(t)
	svc := NewProductService(db)
	p := newSaleUnitTestProduct(t, db) // unidad base KGM
	kgm := database.TenantUnit{Code: "KGM", Name: "Kilos", Active: true}
	if err := db.Create(&kgm).Error; err != nil {
		t.Fatal(err)
	}
	u, err := svc.CreateSaleUnit(p.ID, SaleUnitInput{Name: "Saco x25", UnitID: &kgm.ID, ConversionFactor: 25, Price1: 100, Active: true})
	if err != nil {
		t.Fatalf("factor 25 con el código de la base debe aceptarse: %v", err)
	}
	if _, err := svc.UpdateSaleUnit(p.ID, u.ID, SaleUnitInput{Name: "Saco x25", UnitID: &kgm.ID, ConversionFactor: 25, Price1: 110, Active: true}); err != nil {
		t.Fatalf("editar el precio de una presentación existente no debe bloquearse: %v", err)
	}
}
