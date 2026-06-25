package handler

import (
	"tukifac/internal/consulta/service"

	"github.com/gofiber/fiber/v3"
)

type ConsultaHandler struct {
	svc *service.ConsultaService
}

func NewConsultaHandler() *ConsultaHandler {
	return &ConsultaHandler{svc: service.NewConsultaService()}
}

// POST /api/consulta/dni — body: { "dni": "12345678" }. Solo panel central (superadmin).
func (h *ConsultaHandler) ConsultaDNIAPI(c fiber.Ctx) error {
	var body struct {
		DNI string `json:"dni"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "JSON inválido"})
	}
	result, err := h.svc.ConsultaDNI(body.DNI)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(result)
}

// POST /api/consulta/ruc — body: { "ruc": "20123456789" }. Solo panel central (superadmin).
func (h *ConsultaHandler) ConsultaRUCAPI(c fiber.Ctx) error {
	var body struct {
		RUC string `json:"ruc"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "JSON inválido"})
	}
	result, err := h.svc.ConsultaRUC(body.RUC)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(result)
}

// POST /api/consulta/dni (público) — body: { "dni": "12345678", "tenant_ruc": "20100443688" }.
// Valida que tenant_ruc esté registrado y activo en la central antes de llamar a apiperu.
func (h *ConsultaHandler) PublicConsultaDNIAPI(c fiber.Ctx) error {
	var body struct {
		DNI       string `json:"dni"`
		TenantRUC string `json:"tenant_ruc"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "JSON inválido"})
	}
	if body.TenantRUC == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Se requiere tenant_ruc (RUC de la empresa)"})
	}
	if err := h.svc.ValidateTenantByRUC(body.TenantRUC); err != nil {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": err.Error()})
	}
	result, err := h.svc.ConsultaDNI(body.DNI)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(result)
}

// POST /api/consulta/ruc (público) — body: { "ruc": "20123456789", "tenant_ruc": "20100443688" }.
// Valida que tenant_ruc esté registrado y activo en la central antes de llamar a apiperu.
func (h *ConsultaHandler) PublicConsultaRUCAPI(c fiber.Ctx) error {
	var body struct {
		RUC       string `json:"ruc"`
		TenantRUC string `json:"tenant_ruc"`
	}
	if err := c.Bind().JSON(&body); err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "JSON inválido"})
	}
	if body.TenantRUC == "" {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "Se requiere tenant_ruc (RUC de la empresa)"})
	}
	if err := h.svc.ValidateTenantByRUC(body.TenantRUC); err != nil {
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": err.Error()})
	}
	result, err := h.svc.ConsultaRUC(body.RUC)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(result)
}

// GET /api/consulta/tipo-cambio?fecha=YYYY-MM-DD — TC SUNAT (tenant autenticado).
func (h *ConsultaHandler) TipoCambioAPI(c fiber.Ctx) error {
	fecha := c.Query("fecha")
	result, err := h.svc.ConsultaTipoCambio(fecha)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(result)
}
