package config

import (
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	// Base de datos central
	DBHost        string
	DBPort        string
	DBUser        string
	DBPassword    string
	CentralDBName string

	// Pool MySQL (central + por cada BD tenant en sync.Map)
	DBCentralMaxOpen     int
	DBCentralMaxIdle     int
	DBTenantMaxOpen      int
	DBTenantMaxIdle      int
	DBConnMaxLifetime    time.Duration
	DBConnMaxIdleTime    time.Duration
	TenantMetadataTTL time.Duration

	// Migraciones CLI (lotes)
	MigrationBatchSize  int
	MigrationBatchPause time.Duration

	// Dominio de la aplicación (para subdominios)
	AppDomain string

	// Entorno: "development" | "production"
	AppEnv string

	// JWT
	JWTSecret   string
	SAJWTSecret string

	// Servidor HTTP
	ServerPort      string
	BodyLimitBytes  int
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	IdleTimeout     time.Duration

	// Facturación electrónica externa
	TukifacBaseURL            string
	TukifacAPIToken           string
	FacturadorBaseURL         string
	FacturadorToken           string
	LegacyInvoiceEndpoint     string
	InvoiceStoragePath        string
	ValidaPSEManagementBaseURL string
	ValidaPSEManagementToken   string

	// URLs de los frontends React (para CORS)
	FrontendURL        string
	CentralFrontendURL string
	// Orígenes adicionales separados por coma (opcional)
	CORSExtraOrigins []string

	// Rate limiting (req / ventana 1m por clave IP o IP|tenant)
	RateLimitEnabled       bool
	RateLimitGlobal        int
	RateLimitAuth          int
	RateLimitPassword      int
	RateLimitBilling       int
	RateLimitUpload        int
	RateLimitPublicConsult int

	// Logging
	LogLevel string // debug | info | warn | error
}

func (c *Config) IsDev() bool {
	return c.AppEnv != "production"
}

func (c *Config) IsProd() bool {
	return c.AppEnv == "production"
}

var AppConfig *Config

func Load() error {
	_ = godotenv.Load()

	appEnv := getEnv("APP_ENV", "development")

	AppConfig = &Config{
		DBHost:        getEnv("DB_HOST", "127.0.0.1"),
		DBPort:        getEnv("DB_PORT", "3306"),
		DBUser:        getEnv("DB_USER", "root"),
		DBPassword:    getEnv("DB_PASSWORD", ""),
		CentralDBName: getEnv("CENTRAL_DB_NAME", "tukifac_saas"),

		DBCentralMaxOpen:  getEnvInt("DB_CENTRAL_MAX_OPEN", 40),
		DBCentralMaxIdle:  getEnvInt("DB_CENTRAL_MAX_IDLE", 15),
		DBTenantMaxOpen:   getEnvInt("DB_TENANT_MAX_OPEN", 3),
		DBTenantMaxIdle:   getEnvInt("DB_TENANT_MAX_IDLE", 2),
		DBConnMaxLifetime: getEnvDuration("DB_CONN_MAX_LIFETIME", "30m"),
		DBConnMaxIdleTime: getEnvDuration("DB_CONN_MAX_IDLE_TIME", "5m"),
		TenantMetadataTTL:   getEnvDuration("TENANT_METADATA_TTL", "5m"),
		MigrationBatchSize:  getEnvInt("MIGRATION_BATCH_SIZE", 50),
		MigrationBatchPause: getEnvDuration("MIGRATION_BATCH_PAUSE", "2s"),

		AppDomain: getEnv("APP_DOMAIN", "localhost"),
		AppEnv:    appEnv,

		JWTSecret:   getEnv("JWT_SECRET", "tenant-secret-change-in-production"),
		SAJWTSecret: getEnv("SA_JWT_SECRET", "superadmin-secret-change-in-production"),

		ServerPort:     getEnv("PORT", "3000"),
		BodyLimitBytes: getEnvInt("BODY_LIMIT_BYTES", 12*1024*1024),
		ReadTimeout:    getEnvDuration("HTTP_READ_TIMEOUT", "30s"),
		WriteTimeout:   getEnvDuration("HTTP_WRITE_TIMEOUT", "120s"),
		IdleTimeout:    getEnvDuration("HTTP_IDLE_TIMEOUT", "120s"),

		TukifacBaseURL:             getEnv("TUKIFAC_BASE_URL", ""),
		TukifacAPIToken:            getEnv("TUKIFAC_API_TOKEN", ""),
		FacturadorBaseURL:          getEnv("FACTURADOR_BASE_URL", ""),
		FacturadorToken:            getEnv("FACTURADOR_TOKEN", ""),
		LegacyInvoiceEndpoint:      getEnv("LEGACY_INVOICE_ENDPOINT", ""),
		InvoiceStoragePath:         getEnv("INVOICE_STORAGE_PATH", "./storage/invoices"),
		ValidaPSEManagementBaseURL: getEnv("VALIDAPSE_MGMT_BASE_URL", "https://app.validapse.com/api"),
		ValidaPSEManagementToken:   getEnv("VALIDAPSE_MGMT_TOKEN", ""),

		FrontendURL:        getEnv("FRONTEND_URL", "http://localhost:5173"),
		CentralFrontendURL: getEnv("CENTRAL_FRONTEND_URL", "http://localhost:5174"),
		CORSExtraOrigins:   splitEnvList(getEnv("CORS_ALLOWED_ORIGINS", "")),

		RateLimitEnabled:       getEnvBool("RATE_LIMIT_ENABLED", true),
		RateLimitGlobal:        getEnvInt("RATE_LIMIT_GLOBAL", 300),
		RateLimitAuth:          getEnvInt("RATE_LIMIT_AUTH", 10),
		RateLimitPassword:      getEnvInt("RATE_LIMIT_PASSWORD", 10),
		RateLimitBilling:       getEnvInt("RATE_LIMIT_BILLING", 60),
		RateLimitUpload:        getEnvInt("RATE_LIMIT_UPLOAD", 30),
		RateLimitPublicConsult: getEnvInt("RATE_LIMIT_PUBLIC_CONSULT", 20),

		LogLevel: getEnv("LOG_LEVEL", defaultLogLevel(appEnv)),
	}
	return nil
}

func defaultLogLevel(appEnv string) string {
	if appEnv == "production" {
		return "info"
	}
	return "debug"
}

func getEnv(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}

func getEnvInt(key string, defaultVal int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil && i > 0 {
			return i
		}
	}
	return defaultVal
}

func getEnvBool(key string, defaultVal bool) bool {
	if v := os.Getenv(key); v != "" {
		switch v {
		case "1", "true", "TRUE", "yes", "YES", "on", "ON":
			return true
		case "0", "false", "FALSE", "no", "NO", "off", "OFF":
			return false
		}
	}
	return defaultVal
}

func splitEnvList(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func getEnvDuration(key, defaultVal string) time.Duration {
	raw := getEnv(key, defaultVal)
	d, err := time.ParseDuration(raw)
	if err != nil {
		d, _ = time.ParseDuration(defaultVal)
	}
	return d
}
