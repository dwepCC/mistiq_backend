package handler

import (
	"strconv"

	"tukifac/internal/equipos/service"

	"github.com/gofiber/fiber/v3"
)

// ── Almacenes ──────────────────────────────────────────────────────────────

func (h *Handler) ListWarehouses(c fiber.Ctx) error {
	rows, err := svc().ListWarehouses(c.Query("include_inactive") == "1")
	if err != nil {
		return fail(c, err)
	}
	return c.JSON(fiber.Map{"data": rows})
}

func (h *Handler) CreateWarehouse(c fiber.Ctx) error {
	var in service.WarehouseInput
	if err := c.Bind().JSON(&in); err != nil {
		return badBody(c)
	}
	w, err := svc().CreateWarehouse(in)
	if err != nil {
		return fail(c, err)
	}
	audit(c, "equip_warehouse_created", "equip_warehouse", w.ID, in)
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"data": w})
}

func (h *Handler) UpdateWarehouse(c fiber.Ctx) error {
	id, ok := idParam(c, "id")
	if !ok {
		return nil
	}
	var in service.WarehouseInput
	if err := c.Bind().JSON(&in); err != nil {
		return badBody(c)
	}
	w, err := svc().UpdateWarehouse(id, in)
	if err != nil {
		return fail(c, err)
	}
	audit(c, "equip_warehouse_updated", "equip_warehouse", id, in)
	return c.JSON(fiber.Map{"data": w})
}

// StockByWarehouse GET /equipos/stock/warehouses — stock actual de cada producto repartido por almacén.
func (h *Handler) StockByWarehouse(c fiber.Ctx) error {
	rows, err := svc().StockByWarehouse()
	if err != nil {
		return fail(c, err)
	}
	return c.JSON(fiber.Map{"data": rows})
}

// ── Requerimientos ─────────────────────────────────────────────────────────

func (h *Handler) ListRequirements(c fiber.Ctx) error {
	rows, err := svc().ListRequirements(c.Query("status"))
	if err != nil {
		return fail(c, err)
	}
	return c.JSON(fiber.Map{"data": rows})
}

func (h *Handler) GetRequirement(c fiber.Ctx) error {
	id, ok := idParam(c, "id")
	if !ok {
		return nil
	}
	v, err := svc().GetRequirement(id)
	if err != nil {
		return fail(c, err)
	}
	return c.JSON(fiber.Map{"data": v})
}

func (h *Handler) CreateRequirement(c fiber.Ctx) error {
	var in service.RequirementInput
	if err := c.Bind().JSON(&in); err != nil {
		return badBody(c)
	}
	v, err := svc().CreateRequirement(in, userID(c))
	if err != nil {
		return fail(c, err)
	}
	audit(c, "equip_requirement_created", "equip_requirement", v.ID, in)
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"data": v})
}

// CancelRequirement POST /equipos/requirements/:id/cancel {reason} — pide el PIN de seguridad.
func (h *Handler) CancelRequirement(c fiber.Ctx) error {
	id, ok := idParam(c, "id")
	if !ok {
		return nil
	}
	if !requirePin(c) {
		return nil
	}
	var b notesBody
	_ = c.Bind().JSON(&b)
	v, err := svc().CancelRequirement(id, b.Reason)
	if err != nil {
		return fail(c, err)
	}
	audit(c, "equip_requirement_cancelled", "equip_requirement", id, b)
	return c.JSON(fiber.Map{"data": v})
}

// ── Transferencias ─────────────────────────────────────────────────────────

func (h *Handler) ListTransfers(c fiber.Ctx) error {
	page, _ := strconv.Atoi(c.Query("page"))
	per, _ := strconv.Atoi(c.Query("per_page"))
	wh, _ := strconv.ParseUint(c.Query("warehouse_id"), 10, 32)
	prod, _ := strconv.ParseUint(c.Query("product_id"), 10, 32)
	res, err := svc().ListTransfers(service.TransferFilter{
		From: c.Query("from"), To: c.Query("to"), WarehouseID: uint(wh), ProductID: uint(prod), Status: c.Query("status"), Page: page, PerPage: per,
	})
	if err != nil {
		return fail(c, err)
	}
	return c.JSON(fiber.Map{"data": res})
}

func (h *Handler) GetTransfer(c fiber.Ctx) error {
	id, ok := idParam(c, "id")
	if !ok {
		return nil
	}
	v, err := svc().GetTransfer(id)
	if err != nil {
		return fail(c, err)
	}
	return c.JSON(fiber.Map{"data": v})
}

func (h *Handler) CreateTransfer(c fiber.Ctx) error {
	var in service.TransferInput
	if err := c.Bind().JSON(&in); err != nil {
		return badBody(c)
	}
	v, err := svc().CreateTransfer(in, userID(c))
	if err != nil {
		if handled, e := stockError(c, err); handled {
			return e
		}
		return fail(c, err)
	}
	audit(c, "equip_transfer_created", "equip_transfer", v.ID, in)
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"data": v})
}

// CancelTransfer POST /equipos/transfers/:id/cancel {reason} — pide el PIN de seguridad.
func (h *Handler) CancelTransfer(c fiber.Ctx) error {
	id, ok := idParam(c, "id")
	if !ok {
		return nil
	}
	if !requirePin(c) {
		return nil
	}
	var b notesBody
	_ = c.Bind().JSON(&b)
	v, err := svc().CancelTransfer(id, b.Reason, userID(c))
	if err != nil {
		return fail(c, err)
	}
	audit(c, "equip_transfer_cancelled", "equip_transfer", id, b)
	return c.JSON(fiber.Map{"data": v})
}
