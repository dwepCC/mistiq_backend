package handler

import (
	"context"
	"strconv"
	"time"

	"tukifac/internal/equipos/service"

	"github.com/gofiber/fiber/v3"
)

// Integración con Shalom API Perú. Las pantallas leen de la BD; la API solo se toca al sincronizar o en
// acciones explícitas sobre un envío. La API key nunca se devuelve ni se escribe en la auditoría.

func providerCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 170*time.Second)
}

// GetProvider GET /equipos/provider/shalom — configuración (sin la API key).
func (h *Handler) GetProvider(c fiber.Ctx) error {
	v, err := svc().ProviderSettingsView()
	if err != nil {
		return fail(c, err)
	}
	return c.JSON(fiber.Map{"data": v})
}

// SaveProvider PUT /equipos/provider/shalom — API key (cifrada), URL base, activación y URL del webhook.
func (h *Handler) SaveProvider(c fiber.Ctx) error {
	var in service.ProviderSettingsInput
	if err := c.Bind().JSON(&in); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "datos inválidos"})
	}
	v, err := svc().SaveProviderSettings(in)
	if err != nil {
		return fail(c, err)
	}
	audit(c, "equip_provider_settings_updated", "equip_provider", 0, fiber.Map{
		"api_key_changed": in.APIKey != nil && *in.APIKey != "", "api_key_cleared": in.ClearAPIKey,
		"pro_email": in.ProEmail, "pro_password_changed": in.ProPassword != nil && *in.ProPassword != "", "pro_cleared": in.ClearPro,
		"enabled": v.Enabled, "base_url": v.BaseURL, "webhook_url": v.WebhookURL,
	})
	return c.JSON(fiber.Map{"data": v})
}

// SyncProviderAgencies POST /equipos/provider/shalom/sync-agencies — sincronización manual del catálogo.
func (h *Handler) SyncProviderAgencies(c fiber.Ctx) error {
	ctx, cancel := providerCtx()
	defer cancel()
	res, err := svc().SyncAgencies(ctx, true)
	if err != nil {
		return fail(c, err)
	}
	audit(c, "equip_provider_agencies_synced", "equip_provider", 0, res)
	return c.JSON(fiber.Map{"data": res})
}

// ProviderDepartments GET /equipos/provider/shalom/departments — de la BD; llama a la API solo si está vacío.
func (h *Handler) ProviderDepartments(c fiber.Ctx) error {
	ctx, cancel := providerCtx()
	defer cancel()
	rows, err := svc().ProviderDepartments(ctx, c.Query("refresh") == "1")
	if err != nil {
		return fail(c, err)
	}
	return c.JSON(fiber.Map{"data": rows})
}

// ProviderProvinces GET /equipos/provider/shalom/departments/:dep/provinces — al elegir el departamento se
// sincronizan sus provincias y distritos una sola vez; después todo sale de la BD.
func (h *Handler) ProviderProvinces(c fiber.Ctx) error {
	dep, err := strconv.Atoi(c.Params("dep"))
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "departamento inválido"})
	}
	ctx, cancel := providerCtx()
	defer cancel()
	rows, err := svc().ProviderProvinces(ctx, dep, c.Query("refresh") == "1")
	if err != nil {
		return fail(c, err)
	}
	return c.JSON(fiber.Map{"data": rows})
}

// ProviderDistricts GET /equipos/provider/shalom/departments/:dep/provinces/:prov/districts — solo BD.
func (h *Handler) ProviderDistricts(c fiber.Ctx) error {
	dep, e1 := strconv.Atoi(c.Params("dep"))
	prov, e2 := strconv.Atoi(c.Params("prov"))
	if e1 != nil || e2 != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ubicación inválida"})
	}
	rows, err := svc().ProviderDistricts(dep, prov)
	if err != nil {
		return fail(c, err)
	}
	return c.JSON(fiber.Map{"data": rows})
}

// SetupProviderWebhook POST /equipos/provider/shalom/webhook — registra la URL en Shalom.
func (h *Handler) SetupProviderWebhook(c fiber.Ctx) error {
	ctx, cancel := providerCtx()
	defer cancel()
	v, err := svc().SetupWebhook(ctx)
	if err != nil {
		return fail(c, err)
	}
	audit(c, "equip_provider_webhook_registered", "equip_provider", 0, fiber.Map{"webhook_url": v.WebhookURL, "verified": v.WebhookVerified})
	return c.JSON(fiber.Map{"data": v})
}

// RotateProviderWebhook POST /equipos/provider/shalom/webhook/rotate — nuevo secreto de firma.
func (h *Handler) RotateProviderWebhook(c fiber.Ctx) error {
	ctx, cancel := providerCtx()
	defer cancel()
	v, err := svc().RotateWebhookSecret(ctx)
	if err != nil {
		return fail(c, err)
	}
	audit(c, "equip_provider_webhook_rotated", "equip_provider", 0, fiber.Map{})
	return c.JSON(fiber.Map{"data": v})
}

// LinkShipmentTracking POST /equipos/orders/:id/provider/link {code} — vincula el seguimiento de Shalom.
func (h *Handler) LinkShipmentTracking(c fiber.Ctx) error {
	id, ok := idParam(c, "id")
	if !ok {
		return nil
	}
	var body struct {
		Code string `json:"code"`
	}
	_ = c.Bind().JSON(&body)
	ctx, cancel := providerCtx()
	defer cancel()
	res, err := svc().LinkShipmentTracking(ctx, id, body.Code)
	if err != nil {
		return fail(c, err)
	}
	audit(c, "equip_shipment_tracking_linked", "equip_order", id, fiber.Map{"code": body.Code})
	trimOrder(c, res.Order)
	return c.JSON(fiber.Map{"data": res.Order, "warnings": res.Warnings})
}

// RefreshShipmentTracking POST /equipos/orders/:id/provider/refresh — «Actualizar desde Shalom».
func (h *Handler) RefreshShipmentTracking(c fiber.Ctx) error {
	id, ok := idParam(c, "id")
	if !ok {
		return nil
	}
	ctx, cancel := providerCtx()
	defer cancel()
	res, err := svc().RefreshShipmentFromProvider(ctx, id)
	if err != nil {
		return fail(c, err)
	}
	audit(c, "equip_shipment_tracking_refreshed", "equip_order", id, fiber.Map{})
	trimOrder(c, res.Order)
	return c.JSON(fiber.Map{"data": res.Order, "warnings": res.Warnings})
}

// SyncProviderShipments POST /equipos/provider/shalom/sync-shipments — alinea de a 50 los envíos vigentes.
func (h *Handler) SyncProviderShipments(c fiber.Ctx) error {
	ctx, cancel := providerCtx()
	defer cancel()
	res, err := svc().SyncShalomShipments(ctx)
	if err != nil {
		return fail(c, err)
	}
	audit(c, "equip_provider_shipments_synced", "equip_provider", 0, res)
	return c.JSON(fiber.Map{"data": res})
}

// ShalomWebhook POST /api/internal/webhooks/shalom — entrega pública de Shalom (sin sesión). El ping se
// responde sin firma; el resto exige X-Shalom-Signature válida. Responde 2xx rápido (Shalom da 10 s).
func ShalomWebhook(c fiber.Ctx) error {
	body := append([]byte(nil), c.Body()...)
	res := svc().HandleWebhook(body, c.Get("X-Shalom-Signature"), time.Now())
	if res.Body != "" {
		return c.Status(res.Status).SendString(res.Body)
	}
	return c.Status(res.Status).JSON(fiber.Map{"ok": res.Status == 200, "note": res.Note})
}

// QuoteShipmentTariff POST /equipos/orders/:id/provider/tariff {aereo} — cotiza el flete (solo lectura, no crea guía).
func (h *Handler) QuoteShipmentTariff(c fiber.Ctx) error {
	id, ok := idParam(c, "id")
	if !ok {
		return nil
	}
	var body struct {
		Aereo bool `json:"aereo"`
	}
	_ = c.Bind().JSON(&body)
	ctx, cancel := providerCtx()
	defer cancel()
	q, err := svc().QuoteShipmentTariff(ctx, id, body.Aereo)
	if err != nil {
		return fail(c, err)
	}
	return c.JSON(fiber.Map{"data": q})
}

// ShipmentProviderEvents GET /equipos/orders/:id/provider/events — historial guardado del envío (no llama a Shalom).
func (h *Handler) ShipmentProviderEvents(c fiber.Ctx) error {
	id, ok := idParam(c, "id")
	if !ok {
		return nil
	}
	rows, err := svc().ShipmentProviderEvents(id)
	if err != nil {
		return fail(c, err)
	}
	return c.JSON(fiber.Map{"data": rows})
}

// ListNotifications GET /equipos/notifications — avisos de la campana del header.
func (h *Handler) ListNotifications(c fiber.Ctx) error {
	limit, _ := strconv.Atoi(c.Query("limit"))
	res, err := svc().ListNotifications(limit)
	if err != nil {
		return fail(c, err)
	}
	return c.JSON(fiber.Map{"data": res.Items, "unread": res.Unread})
}

// MarkNotificationsRead POST /equipos/notifications/read {ids, all}.
func (h *Handler) MarkNotificationsRead(c fiber.Ctx) error {
	var body struct {
		IDs []uint `json:"ids"`
		All bool   `json:"all"`
	}
	_ = c.Bind().JSON(&body)
	if err := svc().MarkNotificationsRead(body.IDs, body.All); err != nil {
		return fail(c, err)
	}
	return c.JSON(fiber.Map{"success": true})
}
