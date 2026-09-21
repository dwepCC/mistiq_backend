package tenantmigrations

import (
	"fmt"
	"time"

	"gorm.io/gorm"
)

type v139Notification struct {
	ID        uint `gorm:"primaryKey"`
	Type      string
	Title     string
	Body      string
	LinkPath  string
	ReadAt    *time.Time
	UserID    *uint
	CreatedAt time.Time
}

func (v139Notification) TableName() string { return "tenant_notifications" }

// V139TenantNotifications crea tenant_notifications (Contrato v2 §1.7, Fase 3): solo la tabla y su
// primer escritor (creación de pedido web) — el mecanismo de entrega en vivo (SSE/badge) es Fase 6.
type V139TenantNotifications struct{}

func (V139TenantNotifications) Version() int { return 139 }
func (V139TenantNotifications) Name() string { return "tenant_notifications" }

func (V139TenantNotifications) Up(db *gorm.DB) error {
	mig := db.Migrator()
	if !mig.HasTable(&v139Notification{}) {
		if err := mig.CreateTable(&v139Notification{}); err != nil {
			return fmt.Errorf("tenant_notifications: %w", err)
		}
	}
	return nil
}
