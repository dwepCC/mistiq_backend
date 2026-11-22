package handler

import (
	"strconv"

	"tukifac/internal/equipos/service"

	"github.com/gofiber/fiber/v3"
)

// ── Retornos ───────────────────────────────────────────────────────────────

func (h *Handler) ListReturns(c fiber.Ctx) error {
	rows, err := svc().ListReturns(c.Query("status"))
	if err != nil {
		return fail(c, err)
	}
	return c.JSON(fiber.Map{"data": rows})
}

func (h *Handler) CreateReturn(c fiber.Ctx) error {
	if !requirePin(c) { // mismo mecanismo que anular pedido: cabecera X-Security-Pin
		return nil
	}
	var in service.ReturnInput
	if err := c.Bind().JSON(&in); err != nil {
		return badBody(c)
	}
	v, err := svc().CreateReturn(in, userID(c))
	if err != nil {
		return fail(c, err)
	}
	audit(c, "equip_return_created", "equip_return", v.ID, fiber.Map{"input": in, "pin_verified": true})
	return c.Status(fiber.StatusCreated).JSON(fiber.Map{"data": v})
}

func (h *Handler) UpdateReturn(c fiber.Ctx) error {
	id, ok := idParam(c, "id")
	if !ok {
		return nil
	}
	var in service.ReturnUpdate
	if err := c.Bind().JSON(&in); err != nil {
		return badBody(c)
	}
	v, err := svc().UpdateReturn(id, in, userID(c))
	if err != nil {
		return fail(c, err)
	}
	audit(c, "equip_return_updated", "equip_return", id, in)
	return c.JSON(fiber.Map{"data": v})
}

func (h *Handler) ReshipReturn(c fiber.Ctx) error {
	id, ok := idParam(c, "id")
	if !ok {
		return nil
	}
	res, err := svc().Reship(id, userID(c))
	if err != nil {
		return fail(c, err)
	}
	audit(c, "equip_return_reshipped", "equip_return", id, nil)
	trimOrder(c, res.Order)
	return c.JSON(fiber.Map{"data": res.Order})
}

// CancelReturn POST /equipos/returns/:id/cancel {reason} — pide el PIN de seguridad (X-Security-Pin).
func (h *Handler) CancelReturn(c fiber.Ctx) error {
	id, ok := idParam(c, "id")
	if !ok {
		return nil
	}
	if !requirePin(c) {
		return nil
	}
	var b notesBody
	_ = c.Bind().JSON(&b)
	v, err := svc().CancelReturn(id, b.Reason, userID(c))
	if err != nil {
		return fail(c, err)
	}
	audit(c, "equip_return_cancelled", "equip_return", id, b)
	return c.JSON(fiber.Map{"data": v})
}

// SetReturnGuide PATCH /equipos/returns/:id/guide {guide_number, code} — guarda la guía y, si es de Shalom, la vincula.
func (h *Handler) SetReturnGuide(c fiber.Ctx) error {
	id, ok := idParam(c, "id")
	if !ok {
		return nil
	}
	var b struct {
		GuideNumber string `json:"guide_number"`
		Code        string `json:"code"`
	}
	if err := c.Bind().JSON(&b); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Datos inválidos"})
	}
	ctx, cancel := providerCtx()
	defer cancel()
	v, warnings, err := svc().SetReturnGuide(ctx, id, b.GuideNumber, b.Code)
	if err != nil {
		return fail(c, err)
	}
	audit(c, "equip_return_guide", "equip_return", id, b)
	return c.JSON(fiber.Map{"data": v, "warnings": warnings})
}

// RefreshReturnTracking POST /equipos/returns/:id/provider/refresh — «Actualizar desde Shalom».
func (h *Handler) RefreshReturnTracking(c fiber.Ctx) error {
	id, ok := idParam(c, "id")
	if !ok {
		return nil
	}
	ctx, cancel := providerCtx()
	defer cancel()
	v, err := svc().RefreshReturnFromProvider(ctx, id)
	if err != nil {
		return fail(c, err)
	}
	return c.JSON(fiber.Map{"data": v})
}

// ReceiveReturn POST /equipos/returns/:id/receive {condition} — casilla «Recibido» (reingresa el stock si está en buen estado).
func (h *Handler) ReceiveReturn(c fiber.Ctx) error {
	id, ok := idParam(c, "id")
	if !ok {
		return nil
	}
	var b struct {
		Condition string `json:"condition"`
	}
	_ = c.Bind().JSON(&b)
	v, err := svc().ReceiveReturn(id, b.Condition, userID(c))
	if err != nil {
		return fail(c, err)
	}
	audit(c, "equip_return_received", "equip_return", id, b)
	return c.JSON(fiber.Map{"data": v})
}

// ── Panel y alertas ────────────────────────────────────────────────────────

func (h *Handler) Dashboard(c fiber.Ctx) error {
	d, err := svc().Dashboard(c.Query("period"), canViewStock(c))
	if err != nil {
		return fail(c, err)
	}
	if !hasPerm(c, "equipos.payments_view") {
		d.SalesTotal, d.Collected, d.Receivable, d.ReturnUnpaid = 0, 0, 0, 0
	}
	return c.JSON(fiber.Map{"data": d, "money_visible": hasPerm(c, "equipos.payments_view")})
}

func (h *Handler) Alerts(c fiber.Ctx) error {
	rows, err := svc().Alerts()
	if err != nil {
		return fail(c, err)
	}
	if !hasPerm(c, "equipos.payments_view") {
		for i := range rows {
			rows[i].Balance = 0
		}
	}
	return c.JSON(fiber.Map{"data": rows})
}

// ── Reportes y cierre ──────────────────────────────────────────────────────

func (h *Handler) ReportSummary(c fiber.Ctx) error {
	v, err := svc().Summary(c.Query("period"))
	if err != nil {
		return fail(c, err)
	}
	return c.JSON(fiber.Map{"data": v})
}

func (h *Handler) ReportSales(c fiber.Ctx) error {
	v, err := svc().SalesByProduct(c.Query("period"))
	if err != nil {
		return fail(c, err)
	}
	return c.JSON(fiber.Map{"data": v})
}

func (h *Handler) ReportCollections(c fiber.Ctx) error {
	v, err := svc().Collections()
	if err != nil {
		return fail(c, err)
	}
	return c.JSON(fiber.Map{"data": v})
}

func (h *Handler) ReportReplenishment(c fiber.Ctx) error {
	v, err := svc().Replenishment()
	if err != nil {
		return fail(c, err)
	}
	return c.JSON(fiber.Map{"data": v})
}

func (h *Handler) ReportProfit(c fiber.Ctx) error {
	v, err := svc().Profitability(c.Query("period"))
	if err != nil {
		return fail(c, err)
	}
	return c.JSON(fiber.Map{"data": v})
}

func (h *Handler) ClosedPeriods(c fiber.Ctx) error {
	v, err := svc().ClosedPeriods()
	if err != nil {
		return fail(c, err)
	}
	return c.JSON(fiber.Map{"data": v})
}

func (h *Handler) ClosePeriod(c fiber.Ctx) error {
	if !requirePin(c) {
		return nil
	}
	period := c.Params("period")
	v, err := svc().ClosePeriod(period, userID(c))
	if err != nil {
		return fail(c, err)
	}
	audit(c, "equip_period_closed", "equip_period", 0, fiber.Map{"period": period})
	return c.JSON(fiber.Map{"data": v})
}

func (h *Handler) ReopenPeriod(c fiber.Ctx) error {
	if !requirePin(c) {
		return nil
	}
	period := c.Params("period")
	if err := svc().ReopenPeriod(period); err != nil {
		return fail(c, err)
	}
	audit(c, "equip_period_reopened", "equip_period", 0, fiber.Map{"period": period})
	return c.JSON(fiber.Map{"success": true})
}

var _ = strconv.Itoa
