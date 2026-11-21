package service

import (
	"errors"
	"testing"

	"tukifac/pkg/database"
)

func TestGallery_agregaLimitaYElimina(t *testing.T) {
	db := setupProductServiceTestDB(t)
	if err := db.AutoMigrate(&database.TenantProductImage{}); err != nil {
		t.Fatal(err)
	}
	svc := NewProductService(db)
	p := database.TenantProduct{Code: "G1", Name: "Con galería", Type: "product", Unit: "NIU", ImageURL: "/uploads/principal.jpg", Active: true}
	if err := db.Create(&p).Error; err != nil {
		t.Fatal(err)
	}

	for i := 0; i < database.MaxProductGalleryImages; i++ {
		if _, err := svc.AddGalleryImage(p.ID, "/uploads/g.jpg"); err != nil {
			t.Fatalf("imagen %d: %v", i+1, err)
		}
	}
	if _, err := svc.AddGalleryImage(p.ID, "/uploads/extra.jpg"); !errors.Is(err, ErrGalleryFull) {
		t.Fatalf("pasar del tope debe fallar con ErrGalleryFull, err=%v", err)
	}
	rows, _ := svc.ListGallery(p.ID)
	if len(rows) != database.MaxProductGalleryImages || rows[0].SortOrder != 1 || rows[len(rows)-1].SortOrder != len(rows) {
		t.Fatalf("galería en orden incorrecto: %+v", rows)
	}

	// Otro producto no ve ni puede borrar imágenes ajenas.
	if _, err := svc.DeleteGalleryImage(p.ID+99, rows[0].ID); err == nil {
		t.Fatal("no debe poder borrar una imagen de otro producto")
	}
	url, err := svc.DeleteGalleryImage(p.ID, rows[0].ID)
	if err != nil || url != "/uploads/g.jpg" {
		t.Fatalf("DeleteGalleryImage = %q, %v", url, err)
	}
	if rows, _ = svc.ListGallery(p.ID); len(rows) != database.MaxProductGalleryImages-1 {
		t.Fatalf("tras borrar quedan %d", len(rows))
	}
}

func TestPromoteGalleryImage_intercambiaConLaPrincipal(t *testing.T) {
	db := setupProductServiceTestDB(t)
	if err := db.AutoMigrate(&database.TenantProductImage{}); err != nil {
		t.Fatal(err)
	}
	svc := NewProductService(db)
	p := database.TenantProduct{Code: "G2", Name: "Producto", Type: "product", Unit: "NIU", ImageURL: "/uploads/vieja-principal.jpg", Active: true}
	db.Create(&p)
	img, _ := svc.AddGalleryImage(p.ID, "/uploads/nueva.jpg")

	main, err := svc.PromoteGalleryImage(p.ID, img.ID)
	if err != nil || main != "/uploads/nueva.jpg" {
		t.Fatalf("PromoteGalleryImage = %q, %v", main, err)
	}
	var reloaded database.TenantProduct
	db.First(&reloaded, p.ID)
	if reloaded.ImageURL != "/uploads/nueva.jpg" {
		t.Fatalf("image_url = %q", reloaded.ImageURL)
	}
	rows, _ := svc.ListGallery(p.ID)
	if len(rows) != 1 || rows[0].URL != "/uploads/vieja-principal.jpg" {
		t.Fatalf("la anterior principal debe quedar en la galería: %+v", rows)
	}

	// Sin principal: la elegida se mueve y sale de la galería (no queda duplicada).
	q := database.TenantProduct{Code: "G3", Name: "Sin principal", Type: "product", Unit: "NIU", Active: true}
	db.Create(&q)
	img2, _ := svc.AddGalleryImage(q.ID, "/uploads/solo.jpg")
	if _, err := svc.PromoteGalleryImage(q.ID, img2.ID); err != nil {
		t.Fatal(err)
	}
	if rows, _ = svc.ListGallery(q.ID); len(rows) != 0 {
		t.Fatalf("la imagen promovida no debe repetirse en la galería: %+v", rows)
	}
}

func TestSetCategoryImage_devuelveLaAnterior(t *testing.T) {
	db := setupProductServiceTestDB(t)
	svc := NewProductService(db)
	cat, err := svc.CreateCategory("Bebidas", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if prev, err := svc.SetCategoryImage(cat.ID, "/uploads/c1.jpg"); err != nil || prev != "" {
		t.Fatalf("primera: prev=%q err=%v", prev, err)
	}
	if prev, err := svc.SetCategoryImage(cat.ID, ""); err != nil || prev != "/uploads/c1.jpg" {
		t.Fatalf("quitar: prev=%q err=%v", prev, err)
	}
	if _, err := svc.SetCategoryImage(9999, "/x.jpg"); err == nil {
		t.Fatal("categoría inexistente debe fallar")
	}
}
