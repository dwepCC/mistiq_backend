package main

import (
	"log/slog"
	"os"
	"runtime/debug"

	"tukifac/config"
	"tukifac/pkg/cmd"
	"tukifac/pkg/cron"
	"tukifac/pkg/logger"
	"tukifac/pkg/runtime"
	"tukifac/routes"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/recover"
)

func main() {
	if err := config.Load(); err != nil {
		panic(err)
	}
	cfg := config.AppConfig
	logger.Init(cfg)

	args := os.Args[1:]
	if len(args) > 0 {
		os.Exit(cmd.Execute(args))
	}

	runServer(cfg)
}

func runServer(cfg *config.Config) {
	if err := runtime.Init(cfg); err != nil {
		logger.L.Error("startup_failed", slog.String("step", "runtime_init"), slog.Any("error", err))
		panic(err)
	}
	defer runtime.Shutdown()
	runtime.ListenShutdown()

	if err := cmd.AutoMigrateDev(); err != nil {
		logger.L.Error("auto_migrate_dev_failed", slog.Any("error", err))
		panic(err)
	}

	// Cron de vencimientos espera esquema central (deploy migrate-central o AutoMigrateDev en dev).
	// Evaluación diaria respeta gracia y marca expired dentro de RunLimaDailyEvaluation.
	cron.StartSaasScheduler()
	cron.StartExchangeRateScheduler()
	cron.StartFiscalReconcileWorker()
	cron.StartPickupReminderWorker()
	cron.StartFiscalTenantSnapshotWorker()

	app := fiber.New(fiber.Config{
		AppName:      "Mistiq SaaS ERP",
		ServerHeader: "",
		BodyLimit:    cfg.BodyLimitBytes,
		// El JWT lleva permisos y módulos (un Administrador ronda los 3 KB) y Cloudflare/el proxy
		// suman sus cabeceras: con el búfer por defecto (4096) esas peticiones se rechazaban con
		// 431 "Request Header Fields Too Large" antes de llegar a cualquier middleware.
		ReadBufferSize: 16384,
		ReadTimeout:    cfg.ReadTimeout,
		WriteTimeout:   cfg.WriteTimeout,
		IdleTimeout:    cfg.IdleTimeout,
		ProxyHeader:    fiber.HeaderXForwardedFor,
		TrustProxy:     true,
		TrustProxyConfig: fiber.TrustProxyConfig{
			Loopback:  true,
			Private:   true,
			LinkLocal: true,
		},
		ErrorHandler: func(c fiber.Ctx, err error) error {
			code := fiber.StatusInternalServerError
			if e, ok := err.(*fiber.Error); ok {
				code = e.Code
			}
			if cfg.IsProd() && code >= 500 {
				return c.Status(code).JSON(fiber.Map{"error": "error interno del servidor"})
			}
			return c.Status(code).JSON(fiber.Map{"error": err.Error()})
		},
	})

	// Un panic en un handler lo captura recover y el ErrorHandler responde 500 genérico. El logger de peticiones va
	// por dentro de recover, así que el panic lo atraviesa sin quedar registrado: sin este handler no hay ningún rastro
	// en producción (guía T001-5 de 20615174638, 20/11/2026: 500 en GET /api/sales/:id y cero líneas de log).
	app.Use(recover.New(recover.Config{
		EnableStackTrace: true,
		StackTraceHandler: func(c fiber.Ctx, e any) {
			attrs := []any{
				slog.String("method", c.Method()),
				slog.String("route", c.Path()),
				slog.Any("panic", e),
				slog.String("stack", string(debug.Stack())),
			}
			if slug, ok := c.Locals("tenant_slug").(string); ok && slug != "" {
				attrs = append(attrs, slog.String("tenant", slug))
			}
			if uid, ok := c.Locals("user_id").(uint); ok && uid > 0 {
				attrs = append(attrs, slog.Uint64("user_id", uint64(uid)))
			}
			logger.L.Error("panic_recovered", attrs...)
		},
	}))

	routes.Setup(app)

	addr := ":" + cfg.ServerPort
	logger.L.Info("server_starting",
		slog.String("env", cfg.AppEnv),
		slog.String("addr", addr),
		slog.Bool("auto_migrate_dev", os.Getenv("AUTO_MIGRATE_DEV") == "true" || os.Getenv("AUTO_MIGRATE_DEV") == "1"),
		slog.String("migrations", "manual: ./tukifac-api migrate"),
	)
	if err := app.Listen(addr); err != nil {
		logger.L.Error("server_stopped", slog.Any("error", err))
		os.Exit(1)
	}
}
