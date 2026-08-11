package handler

import (
	"strings"

	"tukifac/internal/exchangerate"
	"tukifac/pkg/saas"

	"github.com/gofiber/fiber/v3"
)

type ExchangeRateHandler struct {
	svc *exchangerate.CacheService
}

func NewExchangeRateHandler() *ExchangeRateHandler {
	return &ExchangeRateHandler{svc: exchangerate.DefaultService()}
}

// TodayAPI GET /api/superadmin/exchange-rates/today
func (h *ExchangeRateHandler) TodayAPI(c fiber.Ctx) error {
	res, row, err := h.svc.GetTodayStatus()
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	out := fiber.Map{
		"rate": res,
	}
	if row != nil {
		out["record"] = row
	}
	return c.JSON(out)
}

// RefreshAPI POST /api/superadmin/exchange-rates/refresh?fecha=YYYY-MM-DD
func (h *ExchangeRateHandler) RefreshAPI(c fiber.Ctx) error {
	fecha := strings.TrimSpace(c.Query("fecha"))
	if fecha == "" {
		fecha = strings.TrimSpace(c.FormValue("fecha"))
	}
	if fecha == "" {
		fecha = saas.NowLima().Format("2006-01-02")
	}
	res, err := h.svc.ForceRefresh(fecha)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": err.Error()})
	}
	return c.JSON(res)
}
