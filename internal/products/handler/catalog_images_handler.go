package handler

import (
	"fmt"
	"io"
	"mime/multipart"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"tukifac/internal/products/service"
	"tukifac/pkg/database"
	"tukifac/pkg/imageprocess"
	"tukifac/pkg/tenantstorage"

	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
)

// saveCatalogImage valida, optimiza y guarda una imagen subida en uploads/tenants/{ruc}/{subdir}/ y
// devuelve su URL pública. Mismas reglas que la imagen principal del producto (JPG/PNG/WebP, 10 MB).
func saveCatalogImage(ruc, subdir, prefix string, file *multipart.FileHeader) (string, error) {
	if file == nil {
		return "", fmt.Errorf("envía un archivo en el campo 'image'")
	}
	if file.Size > maxProductImageSize {
		return "", fmt.Errorf("la imagen no debe superar 10 MB")
	}
	ext := strings.ToLower(filepath.Ext(file.Filename))
	if ext != ".jpg" && ext != ".jpeg" && ext != ".png" && ext != ".webp" {
		return "", fmt.Errorf("formato no permitido. Usa JPG, PNG o WebP")
	}
	opened, err := file.Open()
	if err != nil {
		return "", fmt.Errorf("no se pudo leer la imagen")
	}
	data, err := io.ReadAll(opened)
	_ = opened.Close()
	if err != nil {
		return "", fmt.Errorf("no se pudo leer la imagen")
	}
	saveExt := ext
	if optimized, ok := imageprocess.Optimize(data, ext); ok {
		data = optimized.Data
		saveExt = optimized.Extension
	}
	dir := tenantstorage.TenantUploadDir(ruc, subdir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("no se pudo crear la carpeta de imágenes")
	}
	filename := fmt.Sprintf("%s_%s_%d%s", prefix, uuid.New().String()[:8], time.Now().UnixNano(), saveExt)
	if err := os.WriteFile(filepath.Join(dir, filename), data, 0644); err != nil {
		return "", fmt.Errorf("error guardando la imagen")
	}
	return tenantstorage.TenantUploadPublicURL(ruc, subdir, filename), nil
}

// GalleryListAPI GET /products/:id/gallery
func (h *ProductHandler) GalleryListAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "ID inválido"})
	}
	rows, err := service.NewProductService(db(c)).ListGallery(uint(id))
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": rows})
}

// GalleryUploadAPI POST /products/:id/gallery (multipart, campo "image"): suma una imagen a la galería.
func (h *ProductHandler) GalleryUploadAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "ID inválido"})
	}
	ruc, err := tenantstorage.ResolveTenantRUC(c)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	svc := service.NewProductService(db(c))
	p, err := svc.GetByID(uint(id))
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "producto no encontrado"})
	}
	if msg, denied := h.productBranchDenied(c, p); denied {
		return c.Status(403).JSON(fiber.Map{"error": msg})
	}
	// Se valida el tope antes de escribir en disco para no dejar archivos huérfanos.
	existing, err := svc.ListGallery(p.ID)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	if len(existing) >= database.MaxProductGalleryImages {
		return c.Status(400).JSON(fiber.Map{"error": service.ErrGalleryFull.Error()})
	}
	file, _ := c.FormFile("image")
	url, err := saveCatalogImage(ruc, "products", fmt.Sprintf("%d_g", p.ID), file)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	row, err := svc.AddGalleryImage(p.ID, url)
	if err != nil {
		_ = tenantstorage.DeleteUploadByPublicURL(url)
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	return c.Status(201).JSON(fiber.Map{"data": row})
}

// GalleryDeleteAPI DELETE /products/:id/gallery/:imageId
func (h *ProductHandler) GalleryDeleteAPI(c fiber.Ctx) error {
	id, err1 := strconv.ParseUint(c.Params("id"), 10, 32)
	imageID, err2 := strconv.ParseUint(c.Params("imageId"), 10, 32)
	if err1 != nil || err2 != nil {
		return c.Status(400).JSON(fiber.Map{"error": "ID inválido"})
	}
	svc := service.NewProductService(db(c))
	p, err := svc.GetByID(uint(id))
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "producto no encontrado"})
	}
	if msg, denied := h.productBranchDenied(c, p); denied {
		return c.Status(403).JSON(fiber.Map{"error": msg})
	}
	url, err := svc.DeleteGalleryImage(p.ID, uint(imageID))
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": err.Error()})
	}
	_ = tenantstorage.DeleteUploadByPublicURL(url)
	return c.JSON(fiber.Map{"success": true})
}

// GalleryPromoteAPI POST /products/:id/gallery/:imageId/main: la imagen elegida pasa a ser la principal.
func (h *ProductHandler) GalleryPromoteAPI(c fiber.Ctx) error {
	id, err1 := strconv.ParseUint(c.Params("id"), 10, 32)
	imageID, err2 := strconv.ParseUint(c.Params("imageId"), 10, 32)
	if err1 != nil || err2 != nil {
		return c.Status(400).JSON(fiber.Map{"error": "ID inválido"})
	}
	svc := service.NewProductService(db(c))
	p, err := svc.GetByID(uint(id))
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "producto no encontrado"})
	}
	if msg, denied := h.productBranchDenied(c, p); denied {
		return c.Status(403).JSON(fiber.Map{"error": msg})
	}
	main, err := svc.PromoteGalleryImage(p.ID, uint(imageID))
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"image_url": main})
}

// CategoryUploadImageAPI POST /categories/:id/image (multipart, campo "image").
func (h *ProductHandler) CategoryUploadImageAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "ID inválido"})
	}
	ruc, err := tenantstorage.ResolveTenantRUC(c)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	var cat database.TenantCategory
	if err := db(c).First(&cat, id).Error; err != nil {
		return c.Status(404).JSON(fiber.Map{"error": "categoría no encontrada"})
	}
	file, _ := c.FormFile("image")
	url, err := saveCatalogImage(ruc, "categories", fmt.Sprintf("%d", cat.ID), file)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	previous, err := service.NewProductService(db(c)).SetCategoryImage(cat.ID, url)
	if err != nil {
		_ = tenantstorage.DeleteUploadByPublicURL(url)
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	_ = tenantstorage.DeleteUploadByPublicURL(previous)
	return c.JSON(fiber.Map{"image_url": url})
}

// CategoryDeleteImageAPI DELETE /categories/:id/image
func (h *ProductHandler) CategoryDeleteImageAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "ID inválido"})
	}
	previous, err := service.NewProductService(db(c)).SetCategoryImage(uint(id), "")
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": err.Error()})
	}
	_ = tenantstorage.DeleteUploadByPublicURL(previous)
	return c.JSON(fiber.Map{"success": true})
}
