package main

import (
	"log/slog"
	"os"

	"tukifac/config"
	"tukifac/pkg/cmd"
	"tukifac/pkg/cron"
	"tukifac/pkg/database"
	"tukifac/pkg/logger"
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
	if err := database.ConnectCentral(); err != nil {
		logger.L.Error("startup_failed", slog.String("step", "connect_central"), slog.Any("error", err))
		panic(err)
	}

	if err := cmd.AutoMigrateDev(); err != nil {
		logger.L.Error("auto_migrate_dev_failed", slog.Any("error", err))
		panic(err)
	}

	// Cron de vencimientos solo tras esquema central (migrate-central en entrypoint o AutoMigrateDev).
	cron.StartExpirationChecker()

	app := fiber.New(fiber.Config{
		AppName:      "Tukifac SaaS ERP",
		ServerHeader: "",
		BodyLimit:    cfg.BodyLimitBytes,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
		IdleTimeout:  cfg.IdleTimeout,
		ProxyHeader:  fiber.HeaderXForwardedFor,
		TrustProxy:   true,
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

	app.Use(recover.New(recover.Config{
		EnableStackTrace: cfg.IsDev(),
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
