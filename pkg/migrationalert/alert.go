package migrationalert

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/smtp"
	"strings"
	"time"

	"tukifac/config"
	"tukifac/pkg/logger"
)

// TenantFailureContext datos para alerta.
type TenantFailureContext struct {
	TenantSlug string
	TenantName string
	Version    int
	Attempts   int
	Error      string
}

// NotifyCircuitBreakerOpen alerta cuando el fleet se pausa por fallos consecutivos.
func NotifyCircuitBreakerOpen(reason string, threshold int) {
	logger.L.Error("fleet_circuit_breaker_alert",
		slog.String("reason", reason),
		slog.Int("threshold", threshold),
	)
	cfg := config.AppConfig
	if cfg == nil {
		return
	}
	body := fmt.Sprintf("Fleet migration CIRCUIT BREAKER OPEN\n\nThreshold: %d consecutive failures\nReason:\n%s\n\nResume: POST /api/superadmin/migrations/resume-fleet or ./tukifac-api migrate-fleet-resume\n",
		threshold, reason)
	if cfg.MigrationAlertWebhook != "" {
		_ = postWebhook(cfg.MigrationAlertWebhook, map[string]interface{}{
			"text":      body,
			"alert":     "fleet_circuit_breaker",
			"threshold": threshold,
			"reason":    reason,
		})
	}
	if cfg.MigrationAlertEmail != "" && cfg.SMTPHost != "" {
		_ = sendEmail(cfg.MigrationAlertEmail, "[Mistiq] Fleet circuit breaker OPEN", body, cfg)
	}
}

// NotifyMigrationFailure alerta básica si attempts > 3 o status failed (no bloquea).
func NotifyMigrationFailure(ctx TenantFailureContext) {
	if ctx.Attempts < 3 && ctx.Error == "" {
		return
	}
	logger.L.Warn("migration_alert",
		slog.String("tenant", ctx.TenantSlug),
		slog.Int("version", ctx.Version),
		slog.Int("attempts", ctx.Attempts),
		slog.String("error", ctx.Error),
	)
	cfg := config.AppConfig
	if cfg == nil {
		return
	}
	body := fmt.Sprintf("Migration failed\n\nTenant: %s (%s)\nVersion: %d\nAttempts: %d\nError:\n%s\n",
		ctx.TenantName, ctx.TenantSlug, ctx.Version, ctx.Attempts, ctx.Error)
	if cfg.MigrationAlertWebhook != "" {
		_ = postWebhook(cfg.MigrationAlertWebhook, map[string]interface{}{
			"text": body,
			"tenant_slug": ctx.TenantSlug,
			"version":     ctx.Version,
			"attempts":    ctx.Attempts,
			"error":       ctx.Error,
		})
	}
	if cfg.MigrationAlertEmail != "" && cfg.SMTPHost != "" {
		_ = sendEmail(cfg.MigrationAlertEmail, "[Mistiq] Migration failed: "+ctx.TenantSlug, body, cfg)
	}
}

func postWebhook(url string, payload map[string]interface{}) error {
	b, _ := json.Marshal(payload)
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}

func sendEmail(to, subject, body string, cfg *config.Config) error {
	addr := fmt.Sprintf("%s:%d", cfg.SMTPHost, cfg.SMTPPort)
	msg := strings.Join([]string{
		"To: " + to,
		"Subject: " + subject,
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=UTF-8",
		"",
		body,
	}, "\r\n")
	var auth smtp.Auth
	if cfg.SMTPUser != "" {
		auth = smtp.PlainAuth("", cfg.SMTPUser, cfg.SMTPPassword, cfg.SMTPHost)
	}
	return smtp.SendMail(addr, auth, cfg.SMTPFrom, []string{to}, []byte(msg))
}
