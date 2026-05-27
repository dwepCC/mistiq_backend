package health

import (
	"context"
	"time"

	"tukifac/pkg/database"
	"tukifac/pkg/tenantcache"

	"github.com/gofiber/fiber/v3"
)

const readinessTimeout = 2 * time.Second

// Liveness responde si el proceso HTTP está activo (sin tocar MySQL ni Redis).
// Usar en balanceadores y en clientes POS (GET /health/live) para heartbeat frecuente.
func Liveness(c fiber.Ctx) error {
	return c.JSON(fiber.Map{
		"app":    "Tukifac API",
		"status": "ok",
	})
}

// Readiness valida dependencias críticas (MySQL central).
// No abre pools de tenants ni llama al facturador (evita healthcheck pesado).
func Readiness(c fiber.Ctx) error {
	if database.CentralDB == nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
			"status": "unavailable",
			"mysql":  "not_initialized",
		})
	}

	sqlDB, err := database.CentralDB.DB()
	if err != nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
			"status": "unavailable",
			"mysql":  "pool_error",
		})
	}

	ctx, cancel := context.WithTimeout(context.Background(), readinessTimeout)
	defer cancel()

	if err := sqlDB.PingContext(ctx); err != nil {
		return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
			"status": "unavailable",
			"mysql":  "down",
		})
	}

	redisStatus := "disabled"
	if tenantcache.Connected() {
		ctxR, cancelR := context.WithTimeout(context.Background(), readinessTimeout)
		defer cancelR()
		if err := tenantcache.RDB().Ping(ctxR).Err(); err != nil {
			redisStatus = "down"
		} else {
			redisStatus = "up"
		}
	}

	return c.JSON(fiber.Map{
		"status": "ok",
		"mysql":  "up",
		"redis":  redisStatus,
	})
}
