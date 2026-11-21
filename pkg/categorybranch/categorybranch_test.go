package categorybranch

import (
	"fmt"
	"strings"
	"testing"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func newDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&database.TenantCategory{}, &database.TenantBranch{}, &database.TenantProduct{}, &database.TenantProductBranch{}); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"Principal", "Surco"} {
		if err := db.Create(&database.TenantBranch{Name: n, Active: true}).Error; err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func u(v uint) *uint { return &v }

func cat(t *testing.T, db *gorm.DB, name string, branch *uint) database.TenantCategory {
	t.Helper()
	c := database.TenantCategory{Name: name, Active: true, BranchID: branch}
	if err := db.Create(&c).Error; err != nil {
		t.Fatal(err)
	}
	return c
}

func product(t *testing.T, db *gorm.DB, name string, categoryID uint, branches ...uint) database.TenantProduct {
	t.Helper()
	p := database.TenantProduct{Name: name, Code: name, CategoryID: &categoryID, Active: true, SalePrice: 1}
	if err := db.Create(&p).Error; err != nil {
		t.Fatal(err)
	}
	for _, b := range branches {
		if err := db.Create(&database.TenantProductBranch{ProductID: p.ID, BranchID: b}).Error; err != nil {
			t.Fatal(err)
		}
	}
	return p
}

func names(cs []database.TenantCategory) string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Name
	}
	return strings.Join(out, ",")
}

func TestVisibleIn_TodasMasLasDeLaSucursal(t *testing.T) {
	db := newDB(t)
	cat(t, db, "Todas", nil)
	cat(t, db, "SoloPrincipal", u(1))
	cat(t, db, "SoloSurco", u(2))

	var got []database.TenantCategory
	db.Scopes(VisibleIn(db, 1)).Order("id").Find(&got)
	if names(got) != "Todas,SoloPrincipal" {
		t.Fatalf("sucursal 1 ve %q", names(got))
	}
	db.Scopes(VisibleIn(db, 2)).Order("id").Find(&got)
	if names(got) != "Todas,SoloSurco" {
		t.Fatalf("sucursal 2 ve %q", names(got))
	}
	db.Scopes(VisibleIn(db, 0)).Order("id").Find(&got)
	if len(got) != 3 {
		t.Fatalf("sin sucursal (administración) debe ver las 3, ve %d", len(got))
	}
	// Sin sucursal resuelta (fail-closed): solo las de todas.
	db.Scopes(VisibleIn(db, 1<<32-1)).Order("id").Find(&got)
	if names(got) != "Todas" {
		t.Fatalf("sucursal inexistente debe ver solo las de todas, ve %q", names(got))
	}
}

func TestEnsureNameFree(t *testing.T) {
	db := newDB(t)
	todas := cat(t, db, "Bebidas", nil)
	cat(t, db, "Postres", u(1))

	cases := []struct {
		name    string
		branch  *uint
		exclude uint
		wantErr bool
	}{
		{"Bebidas", nil, 0, true},         // mismo nombre, ambas de todas
		{"bebidas", nil, 0, true},         // sin distinguir mayúsculas
		{"Bebidas", u(1), 0, true},        // de sucursal con una de todas del mismo nombre
		{"Postres", u(1), 0, true},        // misma sucursal
		{"Postres", nil, 0, true},         // de todas con una de sucursal del mismo nombre
		{"Postres", u(2), 0, false},       // otra sucursal: permitido (el caso de uso)
		{"Nueva", u(1), 0, false},         // nombre libre
		{"Bebidas", nil, todas.ID, false}, // editarse a sí misma
	}
	for _, c := range cases {
		err := EnsureNameFree(db, c.name, c.branch, c.exclude)
		if (err != nil) != c.wantErr {
			t.Errorf("EnsureNameFree(%q, %v, excl %d) err=%v, wantErr=%v", c.name, c.branch, c.exclude, err, c.wantErr)
		}
	}
}

func TestCheckProductCompatible(t *testing.T) {
	db := newDB(t)
	global := cat(t, db, "Todas", nil)
	s1 := cat(t, db, "Sucursal1", u(1))

	if err := CheckProductCompatible(db, &global.ID, nil); err != nil {
		t.Errorf("categoría de todas admite cualquier producto: %v", err)
	}
	if err := CheckProductCompatible(db, nil, nil); err != nil {
		t.Errorf("sin categoría siempre válido: %v", err)
	}
	if err := CheckProductCompatible(db, &s1.ID, []uint{1}); err != nil {
		t.Errorf("producto solo en la sucursal 1 con su categoría debía pasar: %v", err)
	}
	if err := CheckProductCompatible(db, &s1.ID, nil); err == nil || !strings.Contains(err.Error(), "Principal") {
		t.Errorf("producto de todas las sucursales debía rechazarse nombrando la sucursal, got %v", err)
	}
	if err := CheckProductCompatible(db, &s1.ID, []uint{1, 2}); err == nil {
		t.Error("producto también vendido en la sucursal 2 debía rechazarse")
	}
	if err := CheckProductCompatible(db, &s1.ID, []uint{2}); err == nil {
		t.Error("producto de otra sucursal debía rechazarse")
	}
}

func TestCheckScopeChange(t *testing.T) {
	db := newDB(t)
	c := cat(t, db, "Bebidas", nil)
	product(t, db, "Inca Kola", c.ID, 1)    // solo sucursal 1
	product(t, db, "Chicha", c.ID, 1, 2)    // en ambas
	product(t, db, "Agua", c.ID)            // sin asignación = todas

	err := CheckScopeChange(db, c.ID, u(1))
	if err == nil || !strings.Contains(err.Error(), "2 producto(s)") {
		t.Fatalf("debía bloquear indicando 2 productos (Chicha y Agua), got %v", err)
	}
	if err := CheckScopeChange(db, c.ID, nil); err != nil {
		t.Fatalf("ampliar a todas siempre se permite: %v", err)
	}
	// Una categoría cuyos productos ya están solo en esa sucursal sí puede limitarse.
	ok := cat(t, db, "Solo1", nil)
	product(t, db, "P", ok.ID, 1)
	if err := CheckScopeChange(db, ok.ID, u(1)); err != nil {
		t.Fatalf("todos sus productos están solo en la sucursal 1: %v", err)
	}
	if err := CheckScopeChange(db, ok.ID, u(2)); err == nil {
		t.Fatal("limitar a la sucursal 2 con productos de la 1 debía bloquearse")
	}
}

func TestWidenForBranch(t *testing.T) {
	db := newDB(t)
	c := cat(t, db, "Bebidas", u(1))
	p := product(t, db, "Inca Kola", c.ID, 1)

	// Entra stock en la misma sucursal de la categoría: no cambia nada.
	if err := WidenForBranch(db, p.ID, 1); err != nil {
		t.Fatal(err)
	}
	var got database.TenantCategory
	db.First(&got, c.ID)
	if got.BranchID == nil || *got.BranchID != 1 {
		t.Fatalf("la categoría debía seguir en la sucursal 1, quedó %v", got.BranchID)
	}
	// Entra stock en OTRA sucursal: la categoría se amplía a todas.
	if err := WidenForBranch(db, p.ID, 2); err != nil {
		t.Fatal(err)
	}
	db.First(&got, c.ID)
	if got.BranchID != nil {
		t.Fatalf("la categoría debía pasar a todas (NULL), quedó %v", *got.BranchID)
	}
}

func TestFindByName_PrefiereLaDeLaSucursal(t *testing.T) {
	db := newDB(t)
	cat(t, db, "Bebidas", nil)
	s2 := cat(t, db, "Bebidas", u(2)) // permitido a nivel de BD; la regla de nombres vive en el servicio
	got, ok, err := FindByName(db, "bebidas", 2)
	if err != nil || !ok || got.ID != s2.ID {
		t.Fatalf("en la sucursal 2 debía elegir la suya: %+v ok=%v err=%v", got, ok, err)
	}
	got, ok, _ = FindByName(db, "bebidas", 1)
	if !ok || got.BranchID != nil {
		t.Fatalf("en la sucursal 1 debía elegir la de todas: %+v ok=%v", got, ok)
	}
	cat(t, db, "Solo2", u(2))
	if _, ok, _ := FindByName(db, "solo2", 1); ok {
		t.Fatal("una categoría de la sucursal 2 no debe ser visible desde la 1")
	}
}

// Tenant sin la migración V192: todo el paquete se comporta como antes (sin ámbito) y no rompe.
func TestSinMigracionV192_ComportamientoAnterior(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec(`CREATE TABLE tenant_categories (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT, deleted_at DATETIME)`).Error; err != nil {
		t.Fatal(err)
	}
	db.Exec(`INSERT INTO tenant_categories (name) VALUES ('Bebidas')`)
	if Ready(db) {
		t.Fatal("sin la columna no debe estar listo")
	}
	var got []database.TenantCategory
	if err := db.Scopes(VisibleIn(db, 1)).Find(&got).Error; err != nil || len(got) != 1 {
		t.Fatalf("sin V192 el filtro no aplica y debe listar todo: len=%d err=%v", len(got), err)
	}
	if err := EnsureNameFree(db, "Bebidas", nil, 0); err != nil {
		t.Fatalf("sin V192 el nombre queda libre como antes: %v", err)
	}
	id := uint(1)
	if err := CheckProductCompatible(db, &id, nil); err != nil {
		t.Fatalf("sin V192 no hay restricción: %v", err)
	}
}

// Los platos de la carta de Tukichef son de UNA sucursal (TenantProduct.BranchID legacy) aunque no tengan filas en
// tenant_product_branches: cuentan como «solo esa sucursal» para la compatibilidad con la categoría.
func TestEffectiveBranches_UsaLaSucursalDuenaLegacy(t *testing.T) {
	db := newDB(t)
	c := cat(t, db, "Carta", u(1))
	asignado := product(t, db, "Asignado", c.ID, 1, 2)
	plato := database.TenantProduct{Name: "Plato", Code: "PL", Active: true, SalePrice: 1, BranchID: 1}
	db.Create(&plato)
	todas := database.TenantProduct{Name: "Todas", Code: "TD", Active: true, SalePrice: 1}
	db.Create(&todas)

	if got, _ := EffectiveBranches(db, asignado.ID); len(got) != 2 {
		t.Errorf("con filas manda lo asignado: %v", got)
	}
	if got, _ := EffectiveBranches(db, plato.ID); len(got) != 1 || got[0] != 1 {
		t.Errorf("sin filas, la sucursal dueña: %v", got)
	}
	if got, _ := EffectiveBranches(db, todas.ID); len(got) != 0 {
		t.Errorf("sin filas ni dueña = todas: %v", got)
	}
	// Y el cambio de ámbito de la categoría no bloquea por un plato que ya es de esa sucursal.
	g := cat(t, db, "Otra", nil)
	db.Model(&database.TenantProduct{}).Where("id = ?", plato.ID).Update("category_id", g.ID)
	if err := CheckScopeChange(db, g.ID, u(1)); err != nil {
		t.Errorf("un plato de la sucursal 1 no impide limitar la categoría a la sucursal 1: %v", err)
	}
}
