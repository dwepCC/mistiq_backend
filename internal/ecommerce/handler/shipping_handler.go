package handler

import (
	"strconv"
	"strings"
	"time"

	"tukifac/internal/ecommerce/service"
	"tukifac/pkg/tax"

	"github.com/gofiber/fiber/v3"
)

// ── Transportistas ───────────────────────────────────────────────────

type carrierBody struct {
	Name               *string `json:"name"`
	Kind               *string `json:"kind"`
	Active             *bool   `json:"active"`
	DispatchDays       *string `json:"dispatch_days"`
	PickupDeadlineDays *int    `json:"pickup_deadline_days"`
	WarnDaysBefore     *int    `json:"warn_days_before"`
	TrackingURL        *string `json:"tracking_url"`
	ShowInStore        *bool   `json:"show_in_store"`
	SortOrder          *int    `json:"sort_order"`
}

func (b carrierBody) input() service.CarrierInput {
	return service.CarrierInput{
		Name: b.Name, Kind: b.Kind, Active: b.Active, DispatchDays: b.DispatchDays,
		PickupDeadlineDays: b.PickupDeadlineDays, WarnDaysBefore: b.WarnDaysBefore,
		TrackingURL: b.TrackingURL, ShowInStore: b.ShowInStore, SortOrder: b.SortOrder,
	}
}

// ListCarriersAPI GET /api/ecommerce/carriers?active=1
func (h *EcommerceHandler) ListCarriersAPI(c fiber.Ctx) error {
	rows, err := service.NewEcommerceService(db(c)).ListCarriers(c.Query("active") == "1", false)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": rows})
}

func (h *EcommerceHandler) CreateCarrierAPI(c fiber.Ctx) error {
	var body carrierBody
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "datos inválidos"})
	}
	row, err := service.NewEcommerceService(db(c)).CreateCarrier(body.input())
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	return c.Status(201).JSON(fiber.Map{"data": row})
}

func (h *EcommerceHandler) UpdateCarrierAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "ID inválido"})
	}
	var body carrierBody
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "datos inválidos"})
	}
	row, err := service.NewEcommerceService(db(c)).UpdateCarrier(uint(id), body.input())
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": row})
}

func (h *EcommerceHandler) DeleteCarrierAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "ID inválido"})
	}
	if err := service.NewEcommerceService(db(c)).DeleteCarrier(uint(id)); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true})
}

// PublicCarriersAPI GET /api/public/ecommerce/carriers — empresas de envío que el cliente puede
// elegir en el checkout. Solo datos que el cliente necesita ver.
func (h *EcommerceHandler) PublicCarriersAPI(c fiber.Ctx) error {
	rows, err := service.NewEcommerceService(db(c)).ListCarriers(true, true)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	out := make([]fiber.Map, 0, len(rows))
	for _, r := range rows {
		out = append(out, fiber.Map{
			"id": r.ID, "name": r.Name, "kind": r.Kind, "pickup_deadline_days": r.PickupDeadlineDays,
		})
	}
	return c.JSON(fiber.Map{"data": out})
}

// ── Tablero / lista de envíos ────────────────────────────────────────

// ShippingBoardAPI GET /api/ecommerce/shipping/board — pedidos con su etapa de envío, más el conteo
// por etapa. Filtros: stage, carrier_id, branch_id, q, department (2 dígitos), date_from, date_to,
// limit, offset.
func (h *EcommerceHandler) ShippingBoardAPI(c fiber.Ctx) error {
	p := service.ShippingBoardParams{
		Stage:      c.Query("stage"),
		Query:      c.Query("q"),
		Department: c.Query("department"),
	}
	if v, err := strconv.ParseUint(c.Query("carrier_id"), 10, 32); err == nil {
		p.CarrierID = uint(v)
	}
	if v, err := strconv.ParseUint(c.Query("branch_id"), 10, 32); err == nil {
		p.BranchID = uint(v)
	}
	if v, err := strconv.Atoi(c.Query("limit")); err == nil {
		p.Limit = v
	}
	if v, err := strconv.Atoi(c.Query("offset")); err == nil {
		p.Offset = v
	}
	if from := strings.TrimSpace(c.Query("date_from")); from != "" {
		if t, err := time.Parse("2006-01-02", from); err == nil {
			p.DateFrom = &t
		}
	}
	if to := strings.TrimSpace(c.Query("date_to")); to != "" {
		if t, err := time.Parse("2006-01-02", to); err == nil {
			end := t.Add(24*time.Hour - time.Second)
			p.DateTo = &end
		}
	}
	svc := service.NewEcommerceService(db(c))
	rows, total, summary, err := svc.ListShippingBoard(p)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": rows, "total": total, "summary": summary, "settings": svc.ShippingSettings()})
}

// ── Envío del pedido ─────────────────────────────────────────────────

// UpdateOrderShippingAPI PUT /api/ecommerce/orders/:id/shipping
func (h *EcommerceHandler) UpdateOrderShippingAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "ID inválido"})
	}
	var body struct {
		CarrierID          *uint    `json:"carrier_id"`
		DestinationUbigeo  *string  `json:"destination_ubigeo"`
		DestinationAgency  *string  `json:"destination_agency"`
		DestinationAddress *string  `json:"destination_address"`
		RecipientName      *string  `json:"recipient_name"`
		RecipientDocType   *string  `json:"recipient_doc_type"`
		RecipientDocNumber *string  `json:"recipient_doc_number"`
		RecipientPhone     *string  `json:"recipient_phone"`
		ShippingCost       *float64 `json:"shipping_cost"`
		PackageCount       *int     `json:"package_count"`
		WeightKg           *float64 `json:"weight_kg"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "datos inválidos"})
	}
	order, err := service.NewEcommerceService(db(c)).UpdateOrderShipping(uint(id), service.OrderShippingInput{
		CarrierID: body.CarrierID, DestinationUbigeo: body.DestinationUbigeo,
		DestinationAgency: body.DestinationAgency, DestinationAddress: body.DestinationAddress,
		RecipientName: body.RecipientName, RecipientDocType: body.RecipientDocType,
		RecipientDocNumber: body.RecipientDocNumber, RecipientPhone: body.RecipientPhone,
		ShippingCost: body.ShippingCost, PackageCount: body.PackageCount, WeightKg: body.WeightKg,
	})
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": order})
}

// BatchDispatchAPI POST /api/ecommerce/dispatches/batch — despacha varios pedidos con su guía.
// Responde 200 con el resultado de cada pedido (algunos pueden haber fallado).
func (h *EcommerceHandler) BatchDispatchAPI(c fiber.Ctx) error {
	var body struct {
		Items []struct {
			OrderID      uint     `json:"order_id"`
			TrackingCode *string  `json:"tracking_code"`
			PackageCount *int     `json:"package_count"`
			WeightKg     *float64 `json:"weight_kg"`
		} `json:"items"`
		// EmitSale: emitir también la venta de cada pedido despachado (requiere permiso de convertir).
		EmitSale bool `json:"emit_sale"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "datos inválidos"})
	}
	items := make([]service.BatchDispatchItem, 0, len(body.Items))
	for _, it := range body.Items {
		items = append(items, service.BatchDispatchItem{
			OrderID: it.OrderID, TrackingCode: it.TrackingCode, PackageCount: it.PackageCount, WeightKg: it.WeightKg,
		})
	}
	var emit *service.EmitSaleInput
	if body.EmitSale {
		if !hasEcommercePermission(c, service.PermOrdersConvert) {
			return c.Status(403).JSON(fiber.Map{"error": "no tienes permiso para emitir ventas", "permission": service.PermOrdersConvert})
		}
		emit = &service.EmitSaleInput{CentralTenant: orderCentralTenantID(c), TaxConfig: tax.LoadFromDB(db(c))}
	}
	results, err := service.NewEcommerceService(db(c)).BatchDispatch(items, orderUserID(c), emit)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"results": results})
}

// BulkLabelsAPI GET /api/ecommerce/orders/labels?ids=1,2,3 — datos de rótulo de varios pedidos.
func (h *EcommerceHandler) BulkLabelsAPI(c fiber.Ctx) error {
	var ids []uint
	for _, part := range strings.Split(c.Query("ids"), ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		n, err := strconv.ParseUint(part, 10, 32)
		if err != nil {
			return c.Status(400).JSON(fiber.Map{"error": "ids inválidos"})
		}
		ids = append(ids, uint(n))
	}
	results, err := service.BuildLabelsForOrders(db(c), ids)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"labels": results})
}

// limaLocation huso horario de Perú (las fechas «de un día» se interpretan en hora local del negocio).
func limaLocation() *time.Location {
	loc, err := time.LoadLocation("America/Lima")
	if err != nil || loc == nil {
		return time.Local
	}
	return loc
}

// parseOptionalDay "YYYY-MM-DD" → mediodía de ese día en hora de Lima; nil si viene vacío o inválido.
func parseOptionalDay(day string) *time.Time {
	day = strings.TrimSpace(day)
	if day == "" {
		return nil
	}
	t, err := time.ParseInLocation("2006-01-02", day, limaLocation())
	if err != nil {
		return nil
	}
	t = time.Date(t.Year(), t.Month(), t.Day(), 12, 0, 0, 0, t.Location())
	return &t
}

// ── Abonos ───────────────────────────────────────────────────────────

// ListOrderPaymentsAPI GET /api/ecommerce/orders/:id/payments
func (h *EcommerceHandler) ListOrderPaymentsAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "ID inválido"})
	}
	rows, sum, err := service.NewEcommerceService(db(c)).ListOrderPayments(uint(id))
	if err != nil {
		return c.Status(404).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": rows, "summary": sum})
}

// AddOrderPaymentAPI POST /api/ecommerce/orders/:id/payments
func (h *EcommerceHandler) AddOrderPaymentAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "ID inválido"})
	}
	var body struct {
		Amount     float64 `json:"amount"`
		Method     string  `json:"method"`
		Reference  string  `json:"reference"`
		Note       string  `json:"note"`
		VoucherURL string  `json:"voucher_url"`
		PaidAt     string  `json:"paid_at"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "datos inválidos"})
	}
	svc := service.NewEcommerceService(db(c))
	row, err := svc.AddOrderPayment(uint(id), service.AddPaymentInput{
		Amount: body.Amount, Method: body.Method, Reference: body.Reference, Note: body.Note,
		VoucherURL: body.VoucherURL, PaidAt: parseOptionalDay(body.PaidAt), UserID: orderUserID(c),
	})
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	_, sum, _ := svc.ListOrderPayments(uint(id))
	return c.Status(201).JSON(fiber.Map{"data": row, "summary": sum})
}

// ReviewOrderPaymentAPI PUT /api/ecommerce/payments/:id/review — {approve: bool, reason}
func (h *EcommerceHandler) ReviewOrderPaymentAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "ID inválido"})
	}
	var body struct {
		Approve bool   `json:"approve"`
		Reason  string `json:"reason"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "datos inválidos"})
	}
	row, err := service.NewEcommerceService(db(c)).ReviewOrderPayment(uint(id), service.ReviewPaymentInput{
		Approve: body.Approve, Reason: body.Reason, UserID: orderUserID(c),
	})
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": row})
}

// DeleteOrderPaymentAPI DELETE /api/ecommerce/payments/:id
func (h *EcommerceHandler) DeleteOrderPaymentAPI(c fiber.Ctx) error {
	id, err := strconv.ParseUint(c.Params("id"), 10, 32)
	if err != nil {
		return c.Status(400).JSON(fiber.Map{"error": "ID inválido"})
	}
	if err := service.NewEcommerceService(db(c)).DeleteOrderPayment(uint(id)); err != nil {
		return c.Status(400).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"success": true})
}

// PublicTrackAPI GET /api/public/ecommerce/track?order=PW-152&phone=951700442 — seguimiento para el
// cliente final: se identifica con el número del pedido y el celular con el que pidió.
func (h *EcommerceHandler) PublicTrackAPI(c fiber.Ctx) error {
	view, err := service.NewEcommerceService(db(c)).TrackOrder(c.Query("order"), c.Query("phone"))
	if err != nil {
		if err == service.ErrTrackNotFound {
			return c.Status(404).JSON(fiber.Map{"error": err.Error()})
		}
		return c.Status(500).JSON(fiber.Map{"error": "no se pudo consultar el pedido"})
	}
	return c.JSON(fiber.Map{"data": view})
}
