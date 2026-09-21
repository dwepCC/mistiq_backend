package tenantmigrations

import (
	"fmt"
	"time"

	"gorm.io/gorm"
)

// v141NotificationRead espejo local (sin tags) de database.TenantNotificationRead — mismo criterio
// que v139Notification: CreateTable solo garantiza columnas/tabla, los índices se crean aparte de
// forma idempotente abajo (ver migrationHasIndex, ya definido en v052_series_global_unique.go).
type v141NotificationRead struct {
	ID             uint `gorm:"primaryKey"`
	NotificationID uint
	UserID         uint
	ReadAt         time.Time
	CreatedAt      time.Time
}

func (v141NotificationRead) TableName() string { return "tenant_notification_reads" }

const (
	idxNotificationReadsNotifUser = "uk_tenant_notification_reads_notif_user"
	idxNotificationReadsNotifID   = "idx_tenant_notification_reads_notification_id"
	idxNotificationReadsUserID    = "idx_tenant_notification_reads_user_id"
	idxNotificationsCreatedAt     = "idx_tenant_notifications_created_at"
	idxNotificationsType          = "idx_tenant_notifications_type"
	idxNotificationsUserID        = "idx_tenant_notifications_user_id"
)

// V141TenantNotificationReads crea tenant_notification_reads (Contrato v2 §1.7, Fase 6): estado de
// lectura por usuario de las notificaciones broadcast (TenantNotification.UserID = NULL) — ver
// comentario del struct en pkg/database/migrations.go. También completa los índices de
// tenant_notifications que v139 nunca creó para tenants existentes: v139Notification (el struct
// local usado por esa migración) no llevaba tags gorm, así que su CreateTable generó una tabla sin
// ningún índice real — solo los tenants creados DESPUÉS de que el struct vivo (con tags) existiera
// obtuvieron esos índices vía AutoMigrate del baseline. Esta migración los agrega de forma
// idempotente para todos.
type V141TenantNotificationReads struct{}

func (V141TenantNotificationReads) Version() int { return 141 }
func (V141TenantNotificationReads) Name() string { return "tenant_notification_reads" }

func (V141TenantNotificationReads) Up(db *gorm.DB) error {
	mig := db.Migrator()

	if !mig.HasTable(&v141NotificationRead{}) {
		if err := mig.CreateTable(&v141NotificationRead{}); err != nil {
			return fmt.Errorf("tenant_notification_reads: %w", err)
		}
	}
	if !migrationHasIndex(db, "tenant_notification_reads", idxNotificationReadsNotifUser) {
		if err := db.Exec(fmt.Sprintf(
			`CREATE UNIQUE INDEX %s ON tenant_notification_reads (notification_id, user_id)`,
			idxNotificationReadsNotifUser,
		)).Error; err != nil {
			return fmt.Errorf("crear %s: %w", idxNotificationReadsNotifUser, err)
		}
	}
	if !migrationHasIndex(db, "tenant_notification_reads", idxNotificationReadsNotifID) {
		if err := db.Exec(fmt.Sprintf(
			`CREATE INDEX %s ON tenant_notification_reads (notification_id)`,
			idxNotificationReadsNotifID,
		)).Error; err != nil {
			return fmt.Errorf("crear %s: %w", idxNotificationReadsNotifID, err)
		}
	}
	if !migrationHasIndex(db, "tenant_notification_reads", idxNotificationReadsUserID) {
		if err := db.Exec(fmt.Sprintf(
			`CREATE INDEX %s ON tenant_notification_reads (user_id)`,
			idxNotificationReadsUserID,
		)).Error; err != nil {
			return fmt.Errorf("crear %s: %w", idxNotificationReadsUserID, err)
		}
	}

	if !mig.HasTable("tenant_notifications") {
		// Tenant nuevo sin pedidos web todavía: el baseline (AutoMigrate) ya la crea con sus tags,
		// nada que completar acá.
		return nil
	}
	if !migrationHasIndex(db, "tenant_notifications", idxNotificationsType) {
		if err := db.Exec(fmt.Sprintf(
			`CREATE INDEX %s ON tenant_notifications (type)`, idxNotificationsType,
		)).Error; err != nil {
			return fmt.Errorf("crear %s: %w", idxNotificationsType, err)
		}
	}
	if !migrationHasIndex(db, "tenant_notifications", idxNotificationsUserID) {
		if err := db.Exec(fmt.Sprintf(
			`CREATE INDEX %s ON tenant_notifications (user_id)`, idxNotificationsUserID,
		)).Error; err != nil {
			return fmt.Errorf("crear %s: %w", idxNotificationsUserID, err)
		}
	}
	if !migrationHasIndex(db, "tenant_notifications", idxNotificationsCreatedAt) {
		if err := db.Exec(fmt.Sprintf(
			`CREATE INDEX %s ON tenant_notifications (created_at)`, idxNotificationsCreatedAt,
		)).Error; err != nil {
			return fmt.Errorf("crear %s: %w", idxNotificationsCreatedAt, err)
		}
	}
	return nil
}
