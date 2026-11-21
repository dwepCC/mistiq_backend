package handler

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"tukifac/pkg/branchscope"

	"tukifac/config"
	billingsvc "tukifac/internal/billing/service"
	fiscalsvc "tukifac/internal/fiscal/service"
	"tukifac/pkg/database"
	"tukifac/pkg/fiscaldedup"
	"tukifac/pkg/tenantcache"

	"github.com/gofiber/fiber/v3"
)

type StatusHandler struct {
	sync *fiscalsvc.SyncService
}

func NewStatusHandler() *StatusHandler {
	return &StatusHandler{sync: fiscalsvc.NewSyncService()}
}

func authorizeInternal(c fiber.Ctx) bool {
	cfg := config.AppConfig
	if cfg == nil {
		return false
	}
	if cfg.IsProd() && cfg.InternalAPIKey == "" {
		return false
	}
	if cfg.InternalAPIKey == "" {
		return true
	}
	if c.Get("X-Internal-Key") != cfg.InternalAPIKey {
		return false
	}
	sig := c.Get("X-Fiscal-Signature")
	if sig == "" {
		return true
	}
	if !strings.HasPrefix(sig, "sha256=") {
		return false
	}
	body := c.Body()
	mac := hmac.New(sha256.New, []byte(cfg.InternalAPIKey))
	mac.Write(body)
	expected := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(sig))
}

// PostStatus POST /api/internal/fiscal/status — webhook facturador → ERP.
func (h *StatusHandler) PostStatus(c fiber.Ctx) error {
	if !authorizeInternal(c) {
		return c.Status(fiber.StatusUnauthorized).JSON(fiber.Map{"error": "unauthorized"})
	}

	var payload fiscalsvc.StatusWebhookPayload
	if err := c.Bind().JSON(&payload); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "JSON inválido"})
	}
	if payload.TenantSlug == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "tenant_slug requerido"})
	}

	eventID := payload.EventID
	if eventID == "" {
		eventID = payload.DocumentUUID + ":" + payload.Status
	}
	if !fiscaldedup.TryMarkProcessed(eventID) {
		return c.JSON(fiber.Map{"ok": true, "deduplicated": true})
	}

	if payload.TenantID > 0 {
		tenant, err := tenantcache.LookupTenantBySlug(payload.TenantSlug)
		if err == nil && tenant != nil && tenant.ID != payload.TenantID {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "tenant_id mismatch"})
		}
	}

	tenant, err := tenantcache.LookupTenantBySlug(payload.TenantSlug)
	if err != nil || tenant == nil {
		return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "tenant no encontrado"})
	}

	db, err := database.GetTenantDB(tenant.DBName)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "tenant db"})
	}

	// Webhook del facturador (sin usuario ni sucursal): acceso interno identificado.
	db = branchscope.System(db, "fiscal: webhook de estado del facturador")
	if err := h.sync.ApplyStatus(db, &payload); err != nil {
		fiscaldedup.Release(eventID)
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}

	billingsvc.NotifyFromWebhookPayload(tenant.ID, &payload)

	return c.JSON(fiber.Map{"ok": true, "sale_id": payload.SaleID, "status": payload.Status})
}
