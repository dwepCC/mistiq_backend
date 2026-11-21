package service

import (
	"fmt"
	"testing"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestPublicProductGallery_principalPrimeroYSoloPublicados(t *testing.T) {
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&database.TenantProduct{}, &database.TenantProductImage{}, &database.TenantCategory{}); err != nil {
		t.Fatal(err)
	}
	pub := database.TenantProduct{Code: "P", Name: "Publicado", Type: "product", Unit: "NIU", Active: true, ShowInDigitalCatalog: true, ImageURL: "/u/main.jpg"}
	hidden := database.TenantProduct{Code: "H", Name: "Oculto", Type: "product", Unit: "NIU", Active: true, ShowInDigitalCatalog: false, ImageURL: "/u/hidden.jpg"}
	db.Create(&pub)
	db.Create(&hidden)
	db.Create(&database.TenantProductImage{ProductID: pub.ID, URL: "/u/b.jpg", SortOrder: 2})
	db.Create(&database.TenantProductImage{ProductID: pub.ID, URL: "/u/a.jpg", SortOrder: 1})
	db.Create(&database.TenantProductImage{ProductID: hidden.ID, URL: "/u/h2.jpg", SortOrder: 1})
	svc := NewEcommerceService(db)

	got, err := svc.PublicProductGallery(pub.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/u/main.jpg", "/u/a.jpg", "/u/b.jpg"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
	if _, err := svc.PublicProductGallery(hidden.ID); err == nil {
		t.Fatal("un producto no publicado no debe exponer su galería")
	}
	// Una categoría con imagen aparece con su image_url en la lista pública.
	cat := database.TenantCategory{Name: "Bebidas", ImageURL: "/u/cat.jpg", Active: true}
	db.Create(&cat)
	db.Model(&database.TenantProduct{}).Where("id = ?", pub.ID).Update("category_id", cat.ID)
	cats, err := svc.PublicCategories()
	if err != nil || len(cats) != 1 || cats[0].ImageURL != "/u/cat.jpg" {
		t.Fatalf("PublicCategories = %+v, %v", cats, err)
	}
}
