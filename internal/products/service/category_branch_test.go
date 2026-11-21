package service

import (
	"fmt"
	"strings"
	"testing"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func categoryBranchDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&database.TenantCategory{}, &database.TenantBranch{}, &database.TenantProduct{}, &database.TenantProductBranch{}); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"Principal", "Surco"} {
		db.Create(&database.TenantBranch{Name: n, Active: true})
	}
	return db
}

// El alta sin sucursal sigue siendo «todas» (NULL): es lo que hace cualquier camino antiguo que no indica sucursal.
func TestCreateCategory_SinSucursalEsParaTodas(t *testing.T) {
	svc := NewProductService(categoryBranchDB(t))
	c, err := svc.CreateCategory("Bebidas", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if c.BranchID != nil {
		t.Fatalf("sin sucursal debía quedar NULL, quedó %v", *c.BranchID)
	}
}

func TestCreateCategoryInScope_ReglasDeNombreYSucursal(t *testing.T) {
	svc := NewProductService(categoryBranchDB(t))
	if _, err := svc.CreateCategoryInScope("Bebidas", "", nil, uptr(1)); err != nil {
		t.Fatalf("categoría de la sucursal 1: %v", err)
	}
	// El mismo nombre en OTRA sucursal es justo el caso de uso: permitido.
	if _, err := svc.CreateCategoryInScope("Bebidas", "", nil, uptr(2)); err != nil {
		t.Fatalf("mismo nombre en otra sucursal debía permitirse: %v", err)
	}
	// Repetido en la misma sucursal, o como categoría de todas: bloqueado.
	if _, err := svc.CreateCategoryInScope("bebidas", "", nil, uptr(1)); err == nil {
		t.Error("nombre repetido en la misma sucursal debía bloquearse")
	}
	if _, err := svc.CreateCategoryInScope("Bebidas", "", nil, nil); err == nil {
		t.Error("categoría de todas con el nombre de una de sucursal debía bloquearse")
	}
	if _, err := svc.CreateCategoryInScope("Otra", "", nil, uptr(99)); err == nil {
		t.Error("sucursal inexistente debía rechazarse")
	}
}

func TestUpdateCategoryInScope_CambiarAmbito(t *testing.T) {
	db := categoryBranchDB(t)
	svc := NewProductService(db)
	c, _ := svc.CreateCategory("Bebidas", "", nil)
	p := database.TenantProduct{Name: "Chicha", Code: "C1", CategoryID: &c.ID, Active: true, SalePrice: 1}
	db.Create(&p)
	db.Create(&database.TenantProductBranch{ProductID: p.ID, BranchID: 1})
	db.Create(&database.TenantProductBranch{ProductID: p.ID, BranchID: 2})

	// Limitar a la sucursal 1 con un producto que también se vende en la 2: bloqueado, con el motivo.
	_, err := svc.UpdateCategoryInScope(c.ID, "Bebidas", "", 0, true, uptr(1))
	if err == nil || !strings.Contains(err.Error(), "1 producto(s)") {
		t.Fatalf("debía bloquear indicando el producto, got %v", err)
	}
	// Dejar el producto solo en la sucursal 1 y reintentar.
	db.Where("product_id = ? AND branch_id = ?", p.ID, 2).Delete(&database.TenantProductBranch{})
	got, err := svc.UpdateCategoryInScope(c.ID, "Bebidas", "", 0, true, uptr(1))
	if err != nil || got.BranchID == nil || *got.BranchID != 1 {
		t.Fatalf("limitar a la sucursal 1: %+v err=%v", got, err)
	}
	// Sin setBranch conserva el ámbito (editar solo el orden/nombre no lo pisa).
	got, err = svc.UpdateCategoryInScope(c.ID, "Bebidas 2", "", 5, false, nil)
	if err != nil || got.BranchID == nil || *got.BranchID != 1 || got.SortOrder != 5 {
		t.Fatalf("editar sin tocar el ámbito: %+v err=%v", got, err)
	}
	// Ampliar a todas siempre se permite.
	got, err = svc.UpdateCategoryInScope(c.ID, "Bebidas 2", "", 5, true, nil)
	if err != nil || got.BranchID != nil {
		t.Fatalf("ampliar a todas: %+v err=%v", got, err)
	}
}

// Editar una categoría vieja con nombre repetido (permitido antes de V192) no debe bloquearse si no se toca nombre ni ámbito.
func TestUpdateCategoryInScope_NoRevalidaNombreSiNoCambia(t *testing.T) {
	db := categoryBranchDB(t)
	db.Create(&database.TenantCategory{Name: "Bebidas", Active: true})
	b := database.TenantCategory{Name: "Bebidas", Active: true}
	db.Create(&b)
	svc := NewProductService(db)
	if _, err := svc.UpdateCategoryInScope(b.ID, "Bebidas", "x", 3, false, nil); err != nil {
		t.Fatalf("editar el orden de un duplicado antiguo no debía bloquearse: %v", err)
	}
}

func TestListCategoriesScoped_PorSucursal(t *testing.T) {
	db := categoryBranchDB(t)
	svc := NewProductService(db)
	svc.CreateCategoryInScope("Todas", "", nil, nil)
	svc.CreateCategoryInScope("S1", "", nil, uptr(1))
	svc.CreateCategoryInScope("S2", "", nil, uptr(2))

	names := func(scope func(*gorm.DB) *gorm.DB) string {
		cs, err := svc.ListCategoriesScoped(scope)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, c := range cs {
			out = append(out, c.Name)
		}
		return strings.Join(out, ",")
	}
	if got := names(nil); got != "Todas,S1,S2" {
		t.Errorf("sin filtro: %q", got)
	}
	items, err := svc.ListCategoriesWithCountsScoped(nil)
	if err != nil || len(items) != 3 {
		t.Fatalf("con conteos: %d err=%v", len(items), err)
	}
}
