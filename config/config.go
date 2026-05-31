package config

import (
	"context"
	"os"
	"strconv"
	"strings"
	"time"

	"tukifac/pkg/domains"

	"github.com/joho/godotenv"
)

// QueryClass clasifica operaciones DB para timeouts.
type QueryClass int

const (
	QueryRead QueryClass = iota
	QueryWrite
	QueryBilling
	QueryReport
)

type Config struct {
	// Base de datos central
	DBHost        string
	DBPort        string
	DBUser        string
	DBPassword    string
	CentralDBName string

	// Pool MySQL (central + por cada BD tenant en TenantDBManager)
	DBCentralMaxOpen  int
	DBCentralMaxIdle  int
	DBTenantMaxOpen   int
	DBTenantMaxIdle   int
	DBConnMaxLifetime time.Duration
	DBConnMaxIdleTime time.Duration
	DBReadTimeout     time.Duration
	DBWriteTimeout    time.Duration
	DBBillingTimeout  time.Duration
	DBReportTimeout   time.Duration

	// Tenant metadata cache (Redis + fallback central DB)
	TenantMetadataTTL      time.Duration
	TenantCacheNegativeTTL time.Duration
	TenantCacheMaxStale    time.Duration

	// TenantDBManager
	TenantPoolIdleTTL       time.Duration
	TenantPoolMaxActive     int
	TenantPoolEvictInterval time.Duration

	// Redis (resuelto en Load desde REDIS_URL / REDIS_ADDR / REDIS_HOST+PORT)
	Redis            RedisSettings
	RedisURL         string // URL canónica (compatibilidad)
	RedisPoolSize    int
	RedisMinIdleConns int
	RedisMaxRetries  int

	// Billing async queue (Redis LIST)
	BillingAsyncEnabled   bool
	BillingQueueWorkers   int
	BillingMaxRetries     int
	BillingRetryBaseDelay time.Duration

	// Migraciones CLI (lotes)
	MigrationBatchSize  int
	MigrationBatchPause time.Duration
	MigrationAlertWebhook string
	MigrationAlertEmail   string
	FleetFailedThreshold          int
	FleetCircuitBreakerThreshold  int
	InternalAPIKey                string

	// SMTP opcional (alertas migración)
	SMTPHost     string
	SMTPPort     int
	SMTPUser     string
	SMTPPassword string
	SMTPFrom     string

	// Dominio raíz de tenants: empresa1.APP_DOMAIN (ej. bendey.cloud).
	// Alias env: ROOT_DOMAIN (tiene prioridad sobre APP_DOMAIN).
	AppDomain string

	// URL pública del API (CORS). Ej: https://api.bendey.cloud
	APIPublicURL string
	// Host del API sin esquema (alternativa a API_PUBLIC_URL). Ej: api.bendey.cloud
	APIHost string

	// Subdominios reservados (no son tenants): api, app, www, admin, central + extras en .env
	ReservedSubdomains []string

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
	FacturadorBaseURL         string
	FacturadorToken           string
	FiscalQueueWorkers        int
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

// DevTenantAPIOrigin origen del API tenant en local (mismo proceso que la API central).
func (c *Config) DevTenantAPIOrigin() string {
	if u := strings.TrimSpace(c.APIPublicURL); u != "" {
		return strings.TrimRight(u, "/")
	}
	port := strings.TrimSpace(c.ServerPort)
	if port == "" {
		port = "3000"
	}
	return "http://localhost:" + port
}

func (c *Config) IsProd() bool {
	return c.AppEnv == "production"
}

// DBContext devuelve contexto con timeout según clase de query.
func (c *Config) DBContext(class QueryClass) (context.Context, context.CancelFunc) {
	var d time.Duration
	switch class {
	case QueryWrite:
		d = c.DBWriteTimeout
	case QueryBilling:
		d = c.DBBillingTimeout
	case QueryReport:
		d = c.DBReportTimeout
	default:
		d = c.DBReadTimeout
	}
	return context.WithTimeout(context.Background(), d)
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

		DBCentralMaxOpen:  getEnvInt("DB_CENTRAL_MAX_OPEN", 25),
		DBCentralMaxIdle:  getEnvInt("DB_CENTRAL_MAX_IDLE", 10),
		DBTenantMaxOpen:   getEnvInt("DB_TENANT_MAX_OPEN", 2),
		DBTenantMaxIdle:   getEnvInt("DB_TENANT_MAX_IDLE", 1),
		DBConnMaxLifetime: getEnvDuration("DB_CONN_MAX_LIFETIME", "30m"),
		DBConnMaxIdleTime: getEnvDuration("DB_CONN_MAX_IDLE_TIME", "5m"),
		DBReadTimeout:     getEnvDuration("DB_READ_TIMEOUT", "3s"),
		DBWriteTimeout:    getEnvDuration("DB_WRITE_TIMEOUT", "5s"),
		DBBillingTimeout:  getEnvDuration("DB_BILLING_TIMEOUT", "45s"),
		DBReportTimeout:   getEnvDuration("DB_REPORT_TIMEOUT", "15s"),

		TenantMetadataTTL:      getEnvDuration("TENANT_METADATA_TTL", "10m"),
		TenantCacheNegativeTTL: getEnvDuration("TENANT_CACHE_NEGATIVE_TTL", "60s"),
		TenantCacheMaxStale:    getEnvDuration("TENANT_CACHE_MAX_STALE", "2m"),

		TenantPoolIdleTTL:       getEnvDuration("TENANT_POOL_IDLE_TTL", "25m"),
		TenantPoolMaxActive:     getEnvInt("TENANT_POOL_MAX_ACTIVE", 200),
		TenantPoolEvictInterval: getEnvDuration("TENANT_POOL_EVICT_INTERVAL", "2m"),

		BillingAsyncEnabled:   getEnvBool("BILLING_ASYNC_ENABLED", true),
		BillingQueueWorkers:   getEnvInt("BILLING_QUEUE_WORKERS", 4),
		BillingMaxRetries:     getEnvInt("BILLING_MAX_RETRIES", 5),
		BillingRetryBaseDelay: getEnvDuration("BILLING_RETRY_BASE_DELAY", "30s"),
		MigrationBatchSize:    getEnvInt("MIGRATION_BATCH_SIZE", 50),
		MigrationBatchPause:   getEnvDuration("MIGRATION_BATCH_PAUSE", "2s"),
		MigrationAlertWebhook: getEnv("MIGRATION_ALERT_WEBHOOK", ""),
		MigrationAlertEmail:   getEnv("MIGRATION_ALERT_EMAIL", ""),
		FleetFailedThreshold:         getEnvInt("FLEET_FAILED_THRESHOLD", 25),
		FleetCircuitBreakerThreshold: getEnvInt("FLEET_CIRCUIT_BREAKER_THRESHOLD", 10),
		InternalAPIKey:               getEnv("INTERNAL_API_KEY", ""),

		SMTPHost:     getEnv("SMTP_HOST", ""),
		SMTPPort:     getEnvInt("SMTP_PORT", 587),
		SMTPUser:     getEnv("SMTP_USER", ""),
		SMTPPassword: getEnv("SMTP_PASSWORD", ""),
		SMTPFrom:     getEnv("SMTP_FROM", "noreply@bendey.cloud"),

		AppDomain:          resolveRootDomain(),
		APIPublicURL:       strings.TrimSpace(getEnv("API_PUBLIC_URL", "")),
		APIHost:            domains.NormalizeHost(getEnv("API_HOST", "")),
		ReservedSubdomains: domains.MergeReserved(splitEnvList(getEnv("RESERVED_SUBDOMAINS", ""))),
		AppEnv:             appEnv,

		JWTSecret:   getEnv("JWT_SECRET", "tenant-secret-change-in-production"),
		SAJWTSecret: getEnv("SA_JWT_SECRET", "superadmin-secret-change-in-production"),

		ServerPort:     getEnv("PORT", "3000"),
		BodyLimitBytes: getEnvInt("BODY_LIMIT_BYTES", 12*1024*1024),
		ReadTimeout:    getEnvDuration("HTTP_READ_TIMEOUT", "30s"),
		WriteTimeout:   getEnvDuration("HTTP_WRITE_TIMEOUT", "120s"),
		IdleTimeout:    getEnvDuration("HTTP_IDLE_TIMEOUT", "120s"),

		FacturadorBaseURL:          getEnv("FACTURADOR_BASE_URL", ""),
		FacturadorToken:            getEnv("FACTURADOR_TOKEN", ""),
		FiscalQueueWorkers:         getEnvInt("FISCAL_QUEUE_WORKERS", 4),
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

	if AppConfig.APIPublicURL == "" && AppConfig.APIHost != "" {
		AppConfig.APIPublicURL = domains.OriginFromHost(AppConfig.APIHost)
	}
	if AppConfig.APIPublicURL == "" {
		AppConfig.APIPublicURL = strings.TrimSpace(getEnv("API_PUBLIC_URL", ""))
	}

	redis := ResolveRedisSettings(appEnv)
	AppConfig.Redis = redis
	AppConfig.RedisURL = redis.URL
	AppConfig.RedisPoolSize = redis.PoolSize
	AppConfig.RedisMinIdleConns = redis.MinIdleConns
	AppConfig.RedisMaxRetries = redis.MaxRetries

	return nil
}

// resolveRootDomain: ROOT_DOMAIN o APP_DOMAIN (sin protocolo).
func resolveRootDomain() string {
	root := strings.TrimSpace(getEnv("ROOT_DOMAIN", ""))
	if root == "" {
		root = getEnv("APP_DOMAIN", "localhost")
	}
	return domains.NormalizeRootDomain(root)
}

// IsReservedSubdomain indica si el slug del host no debe tratarse como tenant.
func (c *Config) IsReservedSubdomain(slug string) bool {
	return domains.IsReserved(slug, c.ReservedSubdomains)
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
