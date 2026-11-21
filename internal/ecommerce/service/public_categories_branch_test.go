package service

import (
	"fmt"
	"testing"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// La tienda virtual es de todo el negocio: muestra las categorías de todas las sucursales, sin filtrar por sucursal, y unifica por
// nombre las repetidas (la «Bebidas» de cada local sale una sola vez). Filtrar por ella trae los productos de todas.
func TestPublicCategories_UnificaPorNombreSinMirarSucursal(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&database.TenantProduct{}, &database.TenantCategory{}); err != nil {
		t.Fatal(err)
	}
	b1, b2 := uint(1), uint(2)
	bebidas1 := database.TenantCategory{Name: "Bebidas", Active: true, BranchID: &b1}
	bebidas2 := database.TenantCategory{Name: "bebidas", Active: true, BranchID: &b2, ImageURL: "/u/b2.jpg"}
	postres := database.TenantCategory{Name: "Postres", Active: true} // de todas
	solo2 := database.TenantCategory{Name: "Solo S2", Active: true, BranchID: &b2}
	vacia := database.TenantCategory{Name: "Sin productos", Active: true}
	for _, c := range []*database.TenantCategory{&bebidas1, &bebidas2, &postres, &solo2, &vacia} {
		if err := db.Create(c).Error; err != nil {
			t.Fatal(err)
		}
	}
	mk := func(code string, cat uint) {
		p := database.TenantProduct{Code: code, Name: "Prod " + code, Type: "product", Unit: "NIU", SalePrice: 10, Active: true, ShowInDigitalCatalog: true, CategoryID: &cat}
		if err := db.Create(&p).Error; err != nil {
			t.Fatal(err)
		}
	}
	mk("A", bebidas1.ID)
	mk("B", bebidas2.ID)
	mk("C", postres.ID)
	mk("D", solo2.ID)

	svc := NewEcommerceService(db)
	cats, err := svc.PublicCategories()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, c := range cats {
		got[c.Name] = true
	}
	if len(cats) != 3 || !got["Bebidas"] || !got["Postres"] || !got["Solo S2"] {
		t.Fatalf("categorías públicas = %+v, quería Bebidas (una sola), Postres y Solo S2", cats)
	}
	for _, c := range cats {
		if c.Name == "Bebidas" && c.ImageURL != "/u/b2.jpg" {
			t.Errorf("la categoría unificada debía conservar la imagen de la que la tiene: %q", c.ImageURL)
		}
	}
	// Filtrar por la unificada trae los productos de las dos «Bebidas».
	var bebidasID uint
	for _, c := range cats {
		if c.Name == "Bebidas" {
			bebidasID = c.ID
		}
	}
	items, total, err := svc.PublicProducts("", bebidasID, nil, nil, 1, 50, true)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(items) != 2 {
		t.Fatalf("filtrando por «Bebidas» debía traer 2 productos (A y B), trajo %d/%d", len(items), total)
	}
}
