package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"math/rand"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"tukifac/pkg/database"
	"tukifac/pkg/tenantstorage"

	"github.com/gofiber/fiber/v3"
	"gorm.io/gorm"
)

// heavyTestJPEG genera un JPEG grande con alta entropía (como una foto real de celular) — un
// patrón liso comprimiría casi igual sin importar el tamaño, lo que no prueba nada.
func heavyTestJPEG(t *testing.T) []byte {
	t.Helper()
	w, h := 2400, 1600
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	r := rand.New(rand.NewSource(7))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(r.Intn(256)), uint8(r.Intn(256)), uint8(r.Intn(256)), 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 97}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// newProductImageTestApp monta ProductHandler.UploadImageAPI directo — mismo patrón que
// newProductTestApp (arriba, en product_handler_test.go) pero sin active_branch_id porque
// UploadImageAPI no lo necesita para un producto no-restaurante.
func newProductImageTestApp(db *gorm.DB) *fiber.App {
	h := &ProductHandler{}
	app := fiber.New(fiber.Config{BodyLimit: 20 << 20}) // el JPEG de prueba pesa varios MB
	app.Use(func(c fiber.Ctx) error {
		c.Locals("tenantDB", db)
		c.Locals("user_id", uint(1))
		c.Locals("user_email", "test@tukifac.com")
		return c.Next()
	})
	app.Post("/products/:id/image", h.UploadImageAPI)
	return app
}

// uploadImageMultipart arma y envía el POST multipart/form-data tal como lo hace
// productsService.uploadImage en el frontend (campo "image").
func uploadImageMultipart(t *testing.T, app *fiber.App, productID uint, data []byte, filename string) *http.Response {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, err := w.CreateFormFile("image", filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/products/%d/image", productID), &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	resp, err := app.Test(req)
	if err != nil {
		t.Fatalf("request error: %v", err)
	}
	return resp
}

// diskPathForPublicURL replica cómo UploadsHandler resuelve una URL /uploads/... a una ruta de
// disco relativa al CWD del proceso (igual que tenantstorage.TenantUploadDir/UploadsRoot).
func diskPathForPublicURL(t *testing.T, publicURL string) string {
	t.Helper()
	rel := publicURL
	if len(rel) > 0 && rel[0] == '/' {
		rel = rel[1:]
	}
	return filepath.Join(".", filepath.FromSlash(rel))
}

// TestUploadImageAPI_OptimizesAndCleansUpPrevious cubre las dos correcciones de esta sesión:
//  1. la imagen subida se reduce de peso/dimensiones antes de guardarse en disco (no queda tal
//     cual una foto de celular en alta resolución) — pedido explícito del usuario (lentitud en
//     POS/catálogo con imágenes pesadas).
//  2. al reemplazar la imagen de un producto, el archivo anterior se borra del disco — antes
//     quedaba huérfano para siempre en cada reemplazo.
func TestUploadImageAPI_OptimizesAndCleansUpPrevious(t *testing.T) {
	db := setupProductHandlerTestDB(t)
	ruc := "20610686991"
	if err := db.Create(&database.TenantCompanyConfig{RUC: ruc, BusinessName: "Empresa de prueba"}).Error; err != nil {
		t.Fatal(err)
	}
	db.Create(&database.TenantBranch{Name: "Principal", IsMain: true, Active: true})

	p := &database.TenantProduct{
		Code:      "IMG001",
		Name:      "Producto con imagen pesada",
		SalePrice: 10,
		Active:    true,
	}
	if err := db.Create(p).Error; err != nil {
		t.Fatal(err)
	}

	app := newProductImageTestApp(db)
	t.Cleanup(func() {
		_ = os.RemoveAll(filepath.Join(".", tenantstorage.UploadsRoot, "tenants", ruc))
	})

	heavy := heavyTestJPEG(t)

	// --- Primera subida: debe optimizarse y quedar más liviana en disco ---
	resp1 := uploadImageMultipart(t, app, p.ID, heavy, "foto-celular.jpg")
	if resp1.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp1.StatusCode)
	}
	var out1 struct {
		ImageURL string `json:"image_url"`
	}
	if err := json.NewDecoder(resp1.Body).Decode(&out1); err != nil {
		t.Fatal(err)
	}
	if out1.ImageURL == "" {
		t.Fatal("image_url vacío")
	}
	path1 := diskPathForPublicURL(t, out1.ImageURL)
	info1, err := os.Stat(path1)
	if err != nil {
		t.Fatalf("no se encontró el archivo guardado en %s: %v", path1, err)
	}
	if info1.Size() >= int64(len(heavy)) {
		t.Errorf("la imagen guardada no quedó más liviana: original=%d guardada=%d", len(heavy), info1.Size())
	}

	// --- Segunda subida: reemplaza — el archivo de la primera debe desaparecer del disco ---
	resp2 := uploadImageMultipart(t, app, p.ID, heavy, "otra-foto.jpg")
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp2.StatusCode)
	}
	var out2 struct {
		ImageURL string `json:"image_url"`
	}
	if err := json.NewDecoder(resp2.Body).Decode(&out2); err != nil {
		t.Fatal(err)
	}
	if out2.ImageURL == out1.ImageURL {
		t.Fatal("la segunda subida debería generar una URL distinta (nombre con uuid+timestamp)")
	}
	if _, err := os.Stat(path1); !os.IsNotExist(err) {
		t.Errorf("el archivo de la imagen anterior (%s) debía borrarse al reemplazarla, sigue existiendo", path1)
	}
	path2 := diskPathForPublicURL(t, out2.ImageURL)
	if _, err := os.Stat(path2); err != nil {
		t.Errorf("no se encontró el archivo de la segunda subida en %s: %v", path2, err)
	}

	var updated database.TenantProduct
	if err := db.First(&updated, p.ID).Error; err != nil {
		t.Fatal(err)
	}
	if updated.ImageURL != out2.ImageURL {
		t.Errorf("product.image_url = %q, want %q", updated.ImageURL, out2.ImageURL)
	}
}
