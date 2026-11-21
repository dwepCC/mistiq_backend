package middleware

import (
	"log/slog"
	"time"

	"tukifac/pkg/logger"

	"github.com/gofiber/fiber/v3"
)

// RequestLogger emite un log estructurado por request (compatible con Docker logs / Loki).
func RequestLogger() fiber.Handler {
	return func(c fiber.Ctx) error {
		start := time.Now()
		err := c.Next()

		status := c.Response().StatusCode()
		if status == 0 {
			status = fiber.StatusOK
		}
		if err != nil {
			if e, ok := err.(*fiber.Error); ok {
				status = e.Code
			}
		}

		attrs := []any{
			slog.String("request_id", GetRequestID(c)),
			slog.String("method", c.Method()),
			slog.String("route", c.Path()),
			slog.Int("status", status),
			slog.Int64("latency_ms", time.Since(start).Milliseconds()),
			slog.String("ip", c.IP()),
		}
		// IP real del cliente (CF-Connecting-IP solo si el par es Cloudflare): es la que usa el rate limit. "ip" sigue siendo
		// la del último salto (el edge de Cloudflare) para no cambiar los registros existentes. NO se registra en la
		// telemetría de rendimiento (/api/public/rum): esa petición no debe dejar la IP del usuario en ningún log.
		if c.Path() != "/api/public/rum" {
			attrs = append(attrs, slog.String("client_ip", ClientIP(c)))
		}

		if slug, ok := c.Locals("tenant_slug").(string); ok && slug != "" {
			attrs = append(attrs, slog.String("tenant", slug))
		}
		if uid, ok := c.Locals("user_id").(uint); ok && uid > 0 {
			attrs = append(attrs, slog.Uint64("user_id", uint64(uid)))
		}
		if saUID, ok := c.Locals("sa_user_id").(uint); ok && saUID > 0 {
			attrs = append(attrs, slog.Uint64("sa_user_id", uint64(saUID)))
		}

		msg := "http_request"
		switch {
		case status >= 500:
			if err != nil {
				attrs = append(attrs, slog.String("error", err.Error()))
			}
			logger.L.Error(msg, attrs...)
		case status >= 400:
			logger.L.Warn(msg, attrs...)
		default:
			logger.L.Info(msg, attrs...)
		}
		return err
	}
}
