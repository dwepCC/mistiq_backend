package handler

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"tukifac/pkg/database"
	"tukifac/pkg/fiscaladmin"
	"tukifac/pkg/middleware"

	"github.com/gofiber/fiber/v3"
)

// requiredPermissionForFiscalAction — ÚNICA fuente de verdad del mapeo action → permiso RBAC
// para las rutas fiscales con :action dinámico (DocumentActionAPI individual y BulkActionAPI
// masivo). No duplicar este mapeo en ningún otro lugar.
//
// Acciones reales confirmadas contra el switch existente y docs/FISCAL-OPERATIONS.md,
// docs/FISCAL-CENTRAL-PANEL.md — nunca inventadas:
//   - retry, send, email, poll → fiscal.retry (individual). Las cuatro reencolan al mismo
//     pipeline de emisión/consulta hacia SUNAT (misma mecánica de cola Redis, confirmado con el
//     usuario tras revisar la documentación disponible — no hay visibilidad de código fuente de
//     facturador_lycet, que vive en otro repositorio).
//   - cancel → fiscal.cancel (individual; no existe en bulk).
//   - send, retry, email, poll (bulk) → fiscal.bulk — permiso paraguas ya sembrado en el
//     catálogo ("Acciones masivas sobre comprobantes fiscales"), separado del individual: tener
//     fiscal.retry NO concede operaciones bulk.
//   - force (individual y bulk) → SIN permiso asignado, deliberadamente. Sin visibilidad del
//     código real (vive en facturador_lycet, repo externo) no se puede confirmar si "forzar"
//     ignora validaciones — decisión confirmada con el usuario: queda EXACTAMENTE en el mismo
//     estado que tenía antes de este grupo (solo autenticación vía SuperAdminAuthAPI, sin gate
//     adicional), igual que el resto de operaciones "pendientes" del rollout
//     (tenants/migrate-all, backfills/run-all, check-expirations) — no es un bloqueo nuevo para
//     nadie, tampoco una apertura nueva.
//
// hasPermission=false NO significa "acción inválida" — el whitelist de qué valores de :action
// son sintácticamente soportados lo siguen validando, sin cambios, los switch de
// DocumentActionAPI/BulkActionAPI antes de llegar aquí.
func requiredPermissionForFiscalAction(action string, bulk bool) (permission string, hasPermission bool) {
	if bulk {
		switch action {
		case "send", "retry", "email", "poll":
			return "fiscal.bulk", true
		default: // "force"
			return "", false
		}
	}
	switch action {
	case "retry", "send", "email", "poll":
		return "fiscal.retry", true
	case "cancel":
		return "fiscal.cancel", true
	default: // "force"
		return "", false
	}
}

// checkFiscalActionPermission resuelve el permiso vía requiredPermissionForFiscalAction y lo
// verifica ANTES de que el caller ejecute cualquier efecto (proxy a facturador_lycet, envío a
// SUNAT/OSE, encolado).
//
// Devuelve (true, nil) si la request puede continuar. Devuelve (false, err) si NO — en ese caso
// la respuesta 403 YA fue escrita en c; el caller DEBE hacer `return err` de inmediato sin
// ejecutar nada más (err normalmente es nil, que es el resultado esperado de un c.JSON()
// exitoso — lo que importa es el bool, no confundir con "no hubo error de autorización").
func checkFiscalActionPermission(c fiber.Ctx, action string, bulk bool) (bool, error) {
	permission, hasPermission := requiredPermissionForFiscalAction(action, bulk)
	if !hasPermission {
		return true, nil // "pendiente" (hoy solo "force") — sin gate adicional, ver comentario arriba
	}
	claims, _ := c.Locals("sa_claims").(*middleware.SuperAdminClaims)
	if !middleware.HasSAPermission(claims, permission) {
		return false, c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error":      "No tienes permiso para esta acción",
			"permission": permission,
		})
	}
	return true, nil
}

// logFiscalActionAudit registra la ejecución de una acción fiscal — nunca contenido del
// documento, credenciales SUNAT/OSE ni tokens (el proxy no los expone en ningún caso: solo
// reenvía UUID/acción/resultado).
func logFiscalActionAudit(c fiber.Ctx, action, uuid string, resultOK bool) {
	if database.CentralDB == nil {
		return
	}
	saUserID, _ := c.Locals("sa_user_id").(uint)
	database.WriteAuditLog(&database.AuditLog{
		UserID:    saUserID,
		Action:    "fiscal_document_" + action,
		Entity:    "fiscal_document",
		Payload:   fmt.Sprintf(`{"uuid":%q,"result_ok":%v}`, uuid, resultOK),
		IPAddress: c.IP(),
	})
}

// logFiscalBulkActionAudit ídem, para el lote masivo: actor, acción, alcance y cantidad
// (document_uuids explícitos o filtros + max) — nunca los UUIDs completos si son muchos, solo la
// cantidad.
func logFiscalBulkActionAudit(c fiber.Ctx, action string, count int, byFilter bool, resultOK bool) {
	if database.CentralDB == nil {
		return
	}
	saUserID, _ := c.Locals("sa_user_id").(uint)
	scope := "selection"
	if byFilter {
		scope = "filters"
	}
	database.WriteAuditLog(&database.AuditLog{
		UserID:    saUserID,
		Action:    "fiscal_bulk_" + action,
		Entity:    "fiscal_document",
		Payload:   fmt.Sprintf(`{"scope":%q,"count":%d,"result_ok":%v}`, scope, count, resultOK),
		IPAddress: c.IP(),
	})
}

// FiscalHandler BFF superadmin → facturador_lycet (sin BD fiscal local).
type FiscalHandler struct{}

func NewFiscalHandler() *FiscalHandler {
	return &FiscalHandler{}
}

func (h *FiscalHandler) ensureConfigured(c fiber.Ctx) error {
	if !fiscaladmin.Enabled() {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
			"error": "Facturador fiscal no configurado (FACTURADOR_BASE_URL / FACTURADOR_TOKEN)",
		})
	}
	return nil
}

func (h *FiscalHandler) proxyError(c fiber.Ctx, err error, raw json.RawMessage, status int) error {
	if len(raw) > 0 {
		c.Status(status)
		c.Set("Content-Type", "application/json")
		return c.Send(raw)
	}
	if status == 0 {
		status = fiber.StatusBadGateway
	}
	return c.Status(status).JSON(fiber.Map{"error": err.Error()})
}

func collectQuery(c fiber.Ctx) url.Values {
	q := url.Values{}
	c.Request().URI().QueryArgs().VisitAll(func(k, v []byte) {
		q.Set(string(k), string(v))
	})
	return q
}

// GET /api/superadmin/fiscal/stats
func (h *FiscalHandler) StatsAPI(c fiber.Ctx) error {
	if err := h.ensureConfigured(c); err != nil {
		return err
	}
	raw, status, err := fiscaladmin.GetJSON("/api/v1/fiscal/stats", collectQuery(c))
	if err != nil {
		return h.proxyError(c, err, raw, status)
	}
	c.Set("Content-Type", "application/json")
	return c.Send(raw)
}

// GET /api/superadmin/fiscal/documents
func (h *FiscalHandler) ListDocumentsAPI(c fiber.Ctx) error {
	if err := h.ensureConfigured(c); err != nil {
		return err
	}
	raw, status, err := fiscaladmin.GetJSON("/api/v1/fiscal/documents", collectQuery(c))
	if err != nil {
		return h.proxyError(c, err, raw, status)
	}
	c.Set("Content-Type", "application/json")
	return c.Send(raw)
}

// GET /api/superadmin/fiscal/documents/:uuid
func (h *FiscalHandler) DocumentDetailAPI(c fiber.Ctx) error {
	if err := h.ensureConfigured(c); err != nil {
		return err
	}
	uuid := c.Params("uuid")
	raw, status, err := fiscaladmin.GetJSON("/api/v1/fiscal/documents/"+url.PathEscape(uuid), nil)
	if err != nil {
		return h.proxyError(c, err, raw, status)
	}
	c.Set("Content-Type", "application/json")
	return c.Send(raw)
}

// GET /api/superadmin/fiscal/documents/:uuid/download/:type
func (h *FiscalHandler) DownloadAPI(c fiber.Ctx) error {
	if err := h.ensureConfigured(c); err != nil {
		return err
	}
	uuid := c.Params("uuid")
	typ := c.Params("type")
	path := "/api/v1/fiscal/documents/" + url.PathEscape(uuid) + "/download/" + url.PathEscape(typ)
	data, ct, status, err := fiscaladmin.Download(path)
	if err != nil {
		if status >= 400 && len(data) > 0 {
			c.Status(status)
			return c.Send(data)
		}
		return c.Status(fiber.StatusBadGateway).JSON(fiber.Map{"error": err.Error()})
	}
	if ct != "" {
		c.Set("Content-Type", ct)
	}
	c.Set("Content-Disposition", "attachment")
	return c.Send(data)
}

type bulkBody struct {
	DocumentUUIDs []string               `json:"document_uuids"`
	Filters       map[string]interface{} `json:"filters"`
	Max           int                    `json:"max"`
}

func (h *FiscalHandler) bulkAction(c fiber.Ctx, action string) error {
	// Autorización PRIMERO — antes de tocar configuración, parsear el body, o reenviar nada a
	// facturador_lycet (que a su vez encola hacia SUNAT/OSE).
	if ok, err := checkFiscalActionPermission(c, action, true); !ok {
		return err
	}
	if err := h.ensureConfigured(c); err != nil {
		return err
	}
	var body bulkBody
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "JSON inválido"})
	}
	payload := map[string]interface{}{"max": body.Max}
	byFilter := false
	count := len(body.DocumentUUIDs)
	if len(body.DocumentUUIDs) > 0 {
		payload["document_uuids"] = body.DocumentUUIDs
	} else if len(body.Filters) > 0 {
		payload["filters"] = body.Filters
		byFilter = true
		count = body.Max
	} else {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "document_uuids o filters requerido"})
	}
	raw, status, err := fiscaladmin.PostJSON("/api/v1/fiscal/documents/bulk/"+action, payload)
	logFiscalBulkActionAudit(c, action, count, byFilter, err == nil)
	if err != nil {
		return h.proxyError(c, err, raw, status)
	}
	c.Status(fiber.StatusAccepted)
	c.Set("Content-Type", "application/json")
	return c.Send(raw)
}

// POST /api/superadmin/fiscal/documents/bulk/:action
func (h *FiscalHandler) BulkActionAPI(c fiber.Ctx) error {
	action := strings.TrimSpace(c.Params("action"))
	switch action {
	case "send", "retry", "force", "email", "poll":
		return h.bulkAction(c, action)
	default:
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "acción bulk no soportada"})
	}
}

// POST /api/superadmin/fiscal/documents/:uuid/:action
func (h *FiscalHandler) DocumentActionAPI(c fiber.Ctx) error {
	uuid := c.Params("uuid")
	action := strings.TrimSpace(c.Params("action"))
	switch action {
	case "send", "retry", "force", "email", "poll", "cancel":
	default:
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "acción no soportada"})
	}
	// Autorización PRIMERO — antes de configuración, y antes de reenviar nada a facturador_lycet.
	if ok, err := checkFiscalActionPermission(c, action, false); !ok {
		return err
	}
	if err := h.ensureConfigured(c); err != nil {
		return err
	}
	path := "/api/v1/fiscal/documents/" + url.PathEscape(uuid) + "/" + action
	raw, status, err := fiscaladmin.PostJSON(path, nil)
	logFiscalActionAudit(c, action, uuid, err == nil)
	if err != nil {
		return h.proxyError(c, err, raw, status)
	}
	if action == "cancel" {
		c.Status(fiber.StatusOK)
	} else {
		c.Status(fiber.StatusAccepted)
	}
	c.Set("Content-Type", "application/json")
	return c.Send(raw)
}

// GET /api/superadmin/fiscal/health
func (h *FiscalHandler) HealthAPI(c fiber.Ctx) error {
	if err := h.ensureConfigured(c); err != nil {
		return err
	}
	raw, status, err := fiscaladmin.GetJSON("/api/v1/fiscal/health", nil)
	if err != nil {
		return h.proxyError(c, err, raw, status)
	}
	c.Set("Content-Type", "application/json")
	return c.Send(raw)
}

// GET /api/superadmin/fiscal/operations/summary
func (h *FiscalHandler) OperationsSummaryAPI(c fiber.Ctx) error {
	if err := h.ensureConfigured(c); err != nil {
		return err
	}
	raw, status, err := fiscaladmin.GetJSON("/api/v1/fiscal/operations/summary", collectQuery(c))
	if err != nil {
		return h.proxyError(c, err, raw, status)
	}
	c.Set("Content-Type", "application/json")
	return c.Send(raw)
}

// GET /api/superadmin/fiscal/operations/tenants
func (h *FiscalHandler) OperationsTenantsAPI(c fiber.Ctx) error {
	if err := h.ensureConfigured(c); err != nil {
		return err
	}
	raw, status, err := fiscaladmin.GetJSON("/api/v1/fiscal/operations/tenants", collectQuery(c))
	if err != nil {
		return h.proxyError(c, err, raw, status)
	}
	c.Set("Content-Type", "application/json")
	return c.Send(raw)
}

// GET /api/superadmin/fiscal/operations/queue
func (h *FiscalHandler) OperationsQueueAPI(c fiber.Ctx) error {
	if err := h.ensureConfigured(c); err != nil {
		return err
	}
	raw, status, err := fiscaladmin.GetJSON("/api/v1/fiscal/operations/queue", nil)
	if err != nil {
		return h.proxyError(c, err, raw, status)
	}
	c.Set("Content-Type", "application/json")
	return c.Send(raw)
}

// GET /api/superadmin/fiscal/alerts
func (h *FiscalHandler) AlertsAPI(c fiber.Ctx) error {
	if err := h.ensureConfigured(c); err != nil {
		return err
	}
	raw, status, err := fiscaladmin.GetJSON("/api/v1/fiscal/alerts", nil)
	if err != nil {
		return h.proxyError(c, err, raw, status)
	}
	c.Set("Content-Type", "application/json")
	return c.Send(raw)
}

// GET /api/superadmin/fiscal/documents/:uuid/audit-timeline
func (h *FiscalHandler) AuditTimelineAPI(c fiber.Ctx) error {
	if err := h.ensureConfigured(c); err != nil {
		return err
	}
	uuid := c.Params("uuid")
	raw, status, err := fiscaladmin.GetJSON("/api/v1/fiscal/documents/"+url.PathEscape(uuid)+"/audit-timeline", nil)
	if err != nil {
		return h.proxyError(c, err, raw, status)
	}
	c.Set("Content-Type", "application/json")
	return c.Send(raw)
}
