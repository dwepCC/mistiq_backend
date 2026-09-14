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

// UploadQR POST /api/superadmin/saas-settings/upload-qr (multipart: kind=<key del método>, file)
//
// "kind" ya no está limitado a yape|plin: es la key de cualquier método de pago configurado en
// PaymentMethods, siempre que su Kind sea "qr". Antes el QR vivía en dos campos sueltos
// (YapeQRURL/PlinQRURL) sin relación con la lista de métodos — ver PaymentMethodConfig.QRURL.
func (h *SettingsHandler) UploadQR(c fiber.Ctx) error {
	kind := strings.ToLower(strings.TrimSpace(c.FormValue("kind")))
	if kind == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "kind (key del método) requerido"})
	}
	cfgPre, err := saas.LoadSettings()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	method := saas.PaymentMethodByKey(cfgPre.PaymentMethods, kind)
	if method == nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "método de pago no encontrado"})
	}
	if method.Kind != saas.PaymentMethodKindQR {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "este método no usa QR (es de cuenta bancaria)"})
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
	target := saas.PaymentMethodByKey(cfg.PaymentMethods, kind)
	if target == nil {
		// No debería pasar (ya lo validamos arriba con cfgPre), pero por las dudas: no perder el
		// archivo ya guardado sin dejar rastro de a qué método pertenecía.
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "método de pago ya no existe"})
	}
	target.QRURL = publicURL
	// yape/plin: mantener también los campos legacy en sync, por si algo más (fuera de este
	// backend) todavía los lee directamente — costo cero, se puede quitar cuando se confirme que
	// nada más los usa.
	if kind == "yape" {
		cfg.YapeQRURL = publicURL
	} else if kind == "plin" {
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

// removeSaasQrFiles elimina QR anteriores del mismo método (key) en storage/saas.
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

// UploadLogo POST /api/superadmin/saas-settings/upload-logo
// (multipart: target=method|bank, kind=<key del método o id de la cuenta>, file)
//
// Logo de un método de pago (target=method, cualquier Kind — qr o bank_account) o de una
// cuenta bancaria (target=bank). Reusa la MISMA carpeta storage/saas/ que los QR
// (storagepaths.SaasDir()) a propósito — no crea una carpeta aparte; solo cambia el prefijo
// del archivo ("logo_" en vez de "qr_") para no chocar con la limpieza de QR viejos.
func (h *SettingsHandler) UploadLogo(c fiber.Ctx) error {
	target := strings.ToLower(strings.TrimSpace(c.FormValue("target")))
	kind := strings.ToLower(strings.TrimSpace(c.FormValue("kind")))
	if kind == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "kind requerido"})
	}
	if target != "method" && target != "bank" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "target debe ser 'method' o 'bank'"})
	}

	cfgPre, err := saas.LoadSettings()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	if target == "method" {
		if saas.PaymentMethodByKey(cfgPre.PaymentMethods, kind) == nil {
			return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "método de pago no encontrado"})
		}
	} else if saas.BankAccountByID(cfgPre.BankAccounts, kind) == nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "cuenta bancaria no encontrada"})
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
	logoKey := target + "_" + kind
	removeSaasLogoFiles(dir, logoKey)

	// Nombre único por subida: evita caché del navegador/CDN al recargar (misma ruta = imagen vieja).
	filename := fmt.Sprintf("logo_%s_%d%s", logoKey, time.Now().Unix(), ext)
	savePath := filepath.Join(dir, filename)
	if err := c.SaveFile(file, savePath); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
			"error": fmt.Sprintf("error guardando logo en %s: %v", savePath, err),
		})
	}

	publicURL := "/storage/saas/" + filename
	cfg, err := saas.LoadSettings()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	if target == "method" {
		m := saas.PaymentMethodByKey(cfg.PaymentMethods, kind)
		if m == nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "método de pago ya no existe"})
		}
		m.LogoURL = publicURL
	} else {
		b := saas.BankAccountByID(cfg.BankAccounts, kind)
		if b == nil {
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "cuenta bancaria ya no existe"})
		}
		b.LogoURL = publicURL
	}
	if err := saas.SaveSettings(cfg); err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{
		"success": true,
		"target":  target,
		"kind":    kind,
		"url":     publicURL,
	})
}

// removeSaasLogoFiles elimina logos anteriores del mismo target+kind en storage/saas.
func removeSaasLogoFiles(dir, logoKey string) {
	prefix := "logo_" + logoKey
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
