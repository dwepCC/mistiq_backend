package tenantmigrations

import (
	"fmt"
	"time"

	"gorm.io/gorm"
)

// v143DispatchStatusHistory espejo local de database.TenantEcommerceDispatchStatusHistory — CON
// tags de tamaño en las columnas string (lección de v139/v142: un struct local SIN tags produce
// columnas TEXT/BLOB en MySQL, lo que rompe la creación de índices sin longitud de prefijo — ver
// v141_notification_reads.go para el bug real que causó y cómo se corrigió).
type v143DispatchStatusHistory struct {
	ID         uint   `gorm:"primaryKey"`
	DispatchID uint
	FromStatus string `gorm:"size:20"`
	ToStatus   string `gorm:"size:20"`
	UserID     *uint
	Notes      string
	CreatedAt  time.Time
}

func (v143DispatchStatusHistory) TableName() string { return "tenant_ecommerce_dispatch_status_histories" }

const idxDispatchStatusHistoryDispatchID = "idx_tenant_ecommerce_dispatch_status_histories_dispatch_id"

// V143DispatchStatusHistory crea tenant_ecommerce_dispatch_status_histories (Contrato v2 §9, Fase
// 9) — historial del DESPACHO, separado de tenant_ecommerce_order_status_histories (decisión
// aprobada explícitamente: la transición DESPACHADO->EN_TRANSITO no cambia Order.Status, así que
// no tiene sentido registrarla en el historial del pedido).
type V143DispatchStatusHistory struct{}

func (V143DispatchStatusHistory) Version() int { return 143 }
func (V143DispatchStatusHistory) Name() string { return "ecommerce_dispatch_status_history" }

func (V143DispatchStatusHistory) Up(db *gorm.DB) error {
	mig := db.Migrator()
	if !mig.HasTable(&v143DispatchStatusHistory{}) {
		if err := mig.CreateTable(&v143DispatchStatusHistory{}); err != nil {
			return fmt.Errorf("tenant_ecommerce_dispatch_status_histories: %w", err)
		}
	}
	if !migrationHasIndex(db, "tenant_ecommerce_dispatch_status_histories", idxDispatchStatusHistoryDispatchID) {
		if err := db.Exec(fmt.Sprintf(
			`CREATE INDEX %s ON tenant_ecommerce_dispatch_status_histories (dispatch_id)`, idxDispatchStatusHistoryDispatchID,
		)).Error; err != nil {
			return fmt.Errorf("crear %s: %w", idxDispatchStatusHistoryDispatchID, err)
		}
	}
	return nil
}
