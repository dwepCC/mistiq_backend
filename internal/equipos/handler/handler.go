// Package handler expone la API del módulo «Gestión de Equipos» (panel central, solo dueño y roles con permiso).
package handler

import (
	"encoding/json"
	"strconv"

	"tukifac/internal/equipos/service"
	"tukifac/pkg/database"
	"tukifac/pkg/middleware"

	"github.com/gofiber/fiber/v3"
)

type Handler struct{}

func New() *Handler { return &Handler{} }

func svc() *service.Service { return service.New(database.CentralDB) }

func userID(c fiber.Ctx) uint {
	v, _ := c.Locals("sa_user_id").(uint)
	return v
}

func fail(c fiber.Ctx, err error) error {
	if service.IsValidation(err) {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "Error interno: " + err.Error()})
}

func idParam(c fiber.Ctx, name string) (uint, bool) {
	n, err := strconv.ParseUint(c.Params(name), 10, 32)
	if err != nil || n == 0 {
		_ = c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "ID inválido"})
		return 0, false
	}
	return uint(n), true
}

func audit(c fiber.Ctx, action, entity string, entityID uint, payload any) {
	b, _ := json.Marshal(payload)
	database.WriteAuditLog(&database.AuditLog{
		UserID: userID(c), Action: action, Entity: entity, EntityID: entityID, Payload: string(b), IPAddress: c.IP(),
	})
}

func canViewStock(c fiber.Ctx) bool {
	claims, _ := c.Locals("sa_claims").(*middleware.SuperAdminClaims)
	return middleware.HasSAPermission(claims, "equipos.stock_view")
}

// ── Catálogo ───────────────────────────────────────────────────────────────

func (h *Handler) ListProducts(c fiber.Ctx) error {
	rows, err := svc().ListProducts(c.Query("q"), c.Query("kind"), c.Query("include_inactive") == "1")
	if err != nil {
		return fail(c, err)
	}
	if !canViewStock(c) { // el stock solo lo ve quien tiene el permiso de stock
		for i := range rows {
			rows[i].Stock, rows[i].Semaphore = 0, ""
		}
	}
	return c.JSON(fiber.Map{"data": rows, "stock_visible": canViewStock(c)})
}

func (h *Handler) CreateProduct(c fiber.Ctx) error {
	var in service.ProductInput
	if err := c.Bind().JSON(&in); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Datos inválidos"})
	}
	p, err := svc().CreateProduct(in)
	if err != nil {
		return fail(c, err)
	}
	audit(c, "equip_product_created", "equip_product", p.ID, in)
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"data": p})
}

func (h *Handler) UpdateProduct(c fiber.Ctx) error {
	id, ok := idParam(c, "id")
	if !ok {
		return nil
	}
	var in service.ProductInput
	if err := c.Bind().JSON(&in); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Datos inválidos"})
	}
	p, err := svc().UpdateProduct(id, in)
	if err != nil {
		return fail(c, err)
	}
	audit(c, "equip_product_updated", "equip_product", p.ID, in)
	return c.JSON(fiber.Map{"data": p})
}

func (h *Handler) ListCombos(c fiber.Ctx) error {
	rows, err := svc().ListCombos(c.Query("include_inactive") == "1")
	if err != nil {
		return fail(c, err)
	}
	return c.JSON(fiber.Map{"data": rows})
}

func (h *Handler) CreateCombo(c fiber.Ctx) error {
	var in service.ComboInput
	if err := c.Bind().JSON(&in); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Datos inválidos"})
	}
	v, err := svc().CreateCombo(in)
	if err != nil {
		return fail(c, err)
	}
	audit(c, "equip_combo_created", "equip_combo", v.ID, in)
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"data": v})
}

func (h *Handler) UpdateCombo(c fiber.Ctx) error {
	id, ok := idParam(c, "id")
	if !ok {
		return nil
	}
	var in service.ComboInput
	if err := c.Bind().JSON(&in); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Datos inválidos"})
	}
	v, err := svc().UpdateCombo(id, in)
	if err != nil {
		return fail(c, err)
	}
	audit(c, "equip_combo_updated", "equip_combo", v.ID, in)
	return c.JSON(fiber.Map{"data": v})
}

// ── Transportistas y configuración ────────────────────────────────────────

func (h *Handler) ListCarriers(c fiber.Ctx) error {
	rows, err := svc().ListCarriers(c.Query("include_inactive") == "1")
	if err != nil {
		return fail(c, err)
	}
	return c.JSON(fiber.Map{"data": rows})
}

func (h *Handler) CreateCarrier(c fiber.Ctx) error {
	var in service.CarrierInput
	if err := c.Bind().JSON(&in); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Datos inválidos"})
	}
	v, err := svc().CreateCarrier(in)
	if err != nil {
		return fail(c, err)
	}
	audit(c, "equip_carrier_created", "equip_carrier", v.ID, in)
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"data": v})
}

func (h *Handler) UpdateCarrier(c fiber.Ctx) error {
	id, ok := idParam(c, "id")
	if !ok {
		return nil
	}
	var in service.CarrierInput
	if err := c.Bind().JSON(&in); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Datos inválidos"})
	}
	v, err := svc().UpdateCarrier(id, in)
	if err != nil {
		return fail(c, err)
	}
	audit(c, "equip_carrier_updated", "equip_carrier", v.ID, in)
	return c.JSON(fiber.Map{"data": v})
}

func (h *Handler) GetSettings(c fiber.Ctx) error {
	st, err := svc().GetSettings()
	if err != nil {
		return fail(c, err)
	}
	hasPin, _ := svc().HasSecurityPin()
	return c.JSON(fiber.Map{"data": st, "has_security_pin": hasPin})
}

func (h *Handler) UpdateSettings(c fiber.Ctx) error {
	var in service.SettingsInput
	if err := c.Bind().JSON(&in); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Datos inválidos"})
	}
	st, err := svc().UpdateSettings(in)
	if err != nil {
		return fail(c, err)
	}
	audit(c, "equip_settings_updated", "equip_settings", 1, in)
	return c.JSON(fiber.Map{"data": st})
}

// ── Stock ──────────────────────────────────────────────────────────────────

func (h *Handler) StockReport(c fiber.Ctx) error {
	period := c.Query("period")
	if period == "" {
		period = service.CurrentPeriod()
	}
	rows, err := svc().StockReport(period)
	if err != nil {
		return fail(c, err)
	}
	return c.JSON(fiber.Map{"period": period, "data": rows})
}

func (h *Handler) ProductMovements(c fiber.Ctx) error {
	id, ok := idParam(c, "productId")
	if !ok {
		return nil
	}
	limit, _ := strconv.Atoi(c.Query("limit"))
	rows, err := svc().ProductMovements(id, limit)
	if err != nil {
		return fail(c, err)
	}
	return c.JSON(fiber.Map{"data": rows})
}

func (h *Handler) AddMovement(c fiber.Ctx) error {
	var in service.MovementInput
	if err := c.Bind().JSON(&in); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Datos inválidos"})
	}
	if (in.MovementType == "ajuste" || in.MovementType == "baja") && !requirePin(c) {
		return nil
	}
	m, err := svc().AddMovement(in, userID(c))
	if err != nil {
		return fail(c, err)
	}
	audit(c, "equip_stock_movement", "equip_stock_movement", m.ID, in)
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"data": m})
}

// ── Importación ────────────────────────────────────────────────────────────

func (h *Handler) ImportPreview(c fiber.Ctx) error {
	var payload service.ImportPayload
	if err := c.Bind().JSON(&payload); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Datos inválidos"})
	}
	pv, err := svc().ImportPreview(payload)
	if err != nil {
		return fail(c, err)
	}
	return c.JSON(fiber.Map{"data": pv})
}

func (h *Handler) ImportCommit(c fiber.Ctx) error {
	var payload service.ImportPayload
	if err := c.Bind().JSON(&payload); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Datos inválidos"})
	}
	res, err := svc().ImportCommit(payload, userID(c))
	if err != nil {
		return fail(c, err)
	}
	audit(c, "equip_import_committed", "equip_import_batch", res.Batch.ID, res.Batch)
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"data": res})
}

func (h *Handler) ImportBatches(c fiber.Ctx) error {
	rows, err := svc().ImportBatches()
	if err != nil {
		return fail(c, err)
	}
	return c.JSON(fiber.Map{"data": rows})
}
