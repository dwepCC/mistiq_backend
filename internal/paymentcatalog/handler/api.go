package handler

import (
	pcsvc "tukifac/internal/paymentcatalog/service"

	"github.com/gofiber/fiber/v3"
	"gorm.io/gorm"
)

type PaymentCatalogHandler struct{}

func NewPaymentCatalogHandler() *PaymentCatalogHandler { return &PaymentCatalogHandler{} }

func db(c fiber.Ctx) *gorm.DB {
	v, _ := c.Locals("tenantDB").(*gorm.DB)
	return v
}

// GET /api/payment-methods
func (h *PaymentCatalogHandler) ListPaymentMethodsAPI(c fiber.Ctx) error {
	tdb := db(c)
	if tdb == nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "sin contexto de empresa"})
	}
	activeOnly := c.Query("all") != "1"
	list, err := pcsvc.NewPaymentMethodService(tdb).List(activeOnly)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": list})
}

// GET /api/payment-conditions
func (h *PaymentCatalogHandler) ListPaymentConditionsAPI(c fiber.Ctx) error {
	tdb := db(c)
	if tdb == nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "sin contexto de empresa"})
	}
	activeOnly := c.Query("all") != "1"
	list, err := pcsvc.NewPaymentConditionService(tdb).List(activeOnly)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": list})
}

// GET /api/tax-payment-types
func (h *PaymentCatalogHandler) ListTaxPaymentTypesAPI(c fiber.Ctx) error {
	tdb := db(c)
	if tdb == nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "sin contexto de empresa"})
	}
	activeOnly := c.Query("all") != "1"
	list, err := pcsvc.NewTaxPaymentTypeService(tdb).List(activeOnly)
	if err != nil {
		return c.Status(500).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(fiber.Map{"data": list})
}
