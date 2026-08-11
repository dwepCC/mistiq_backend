package handler

import (
	"tukifac/internal/superadmin/service"

	"github.com/gofiber/fiber/v3"
)

type DashboardHandler struct {
	svc *service.TenantService
}

func NewDashboardHandler() *DashboardHandler {
	return &DashboardHandler{svc: service.NewTenantService()}
}

// GET /api/superadmin/stats
func (h *DashboardHandler) StatsAPI(c fiber.Ctx) error {
	stats, err := h.svc.Stats()
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	tenants, _, err := h.svc.List(service.TenantListParams{
		Page:    1,
		PerPage: 10,
	})
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": err.Error()})
	}
	recent := tenants
	if len(recent) > 5 {
		recent = recent[:5]
	}
	return c.JSON(fiber.Map{
		"stats":          stats,
		"recent_tenants": recent,
	})
}
