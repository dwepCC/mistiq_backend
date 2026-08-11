package middleware

import (
	"os"
	"testing"

	"tukifac/config"
	"tukifac/pkg/logger"
)

func TestMain(m *testing.M) {
	logger.Init(&config.Config{LogLevel: "error", AppEnv: "development"})
	config.AppConfig = &config.Config{JWTSecret: "test-secret", AppEnv: "development", AppDomain: "tukifac.com"}
	os.Exit(m.Run())
}
