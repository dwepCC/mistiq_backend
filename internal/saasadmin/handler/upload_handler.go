package handler

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"tukifac/pkg/saas"
	"tukifac/pkg/storagepaths"
	"tukifac/pkg/uploadlimits"

	"github.com/gofiber/fiber/v3"
)

// UploadQR POST /api/superadmin/saas-settings/upload-qr (multipart: kind=yape|plin, file)
func (h *SettingsHandler) UploadQR(c fiber.Ctx) error {
	kind := strings.ToLower(strings.TrimSpace(c.FormValue("kind")))
	if kind != "yape" && kind != "plin" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "kind debe ser yape o plin"})
	}
	file, err := c.FormFile("file")
	if err != nil || file == nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "archivo requerido"})
	}
	ext := strings.ToLower(filepath.Ext(file.Filename))
	allowed := map[string]bool{".jpg": true, ".jpeg": true, ".png": true, ".webp": true}
	if !allowed[ext] {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "formato no permitido (jpg, png, webp)"})
	}
	if file.Size > uploadlimits.MaxFileBytes {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "archivo máximo 10 MB"})
	}

	dir := storagepaths.SaasDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fmt.Sprintf("no se pudo crear carpeta %s: %v", dir, err),
		})
	}
	removeSaasQrFiles(dir, kind)

	// Nombre único por subida: evita caché del navegador/CDN al recargar (misma ruta = imagen vieja).
	filename := fmt.Sprintf("qr_%s_%d%s", kind, time.Now().Unix(), ext)
	savePath := filepath.Join(dir, filename)
	if err := c.SaveFile(file, savePath); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fmt.Sprintf("error guardando QR en %s: %v", savePath, err),
		})
	}

	publicURL := "/storage/saas/" + filename
	cfg, err := saas.LoadSettings()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	if kind == "yape" {
		cfg.YapeQRURL = publicURL
	} else {
		cfg.PlinQRURL = publicURL
	}
	if err := saas.SaveSettings(cfg); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{
		"success": true,
		"kind":    kind,
		"url":     publicURL,
	})
}

// removeSaasQrFiles elimina QR anteriores del mismo tipo (yape/plin) en storage/saas.
func removeSaasQrFiles(dir, kind string) {
	prefix := "qr_" + kind
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, prefix) {
			_ = os.Remove(filepath.Join(dir, name))
		}
	}
}
