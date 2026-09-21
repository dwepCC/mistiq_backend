package tenantmigrations

import (
	"fmt"
	"time"

	"gorm.io/gorm"
)

// v142Dispatch espejo local de database.TenantEcommerceDispatch para CreateTable — mismo criterio
// que las migraciones anteriores de este archivo: la tabla se crea sin tags de índice (CreateTable
// solo garantiza columnas), los índices reales se agregan aparte de forma idempotente abajo, usando
// migrationHasIndex (definido en v052_series_global_unique.go).
type v142Dispatch struct {
	ID           uint    `gorm:"primaryKey"`
	OrderID      uint
	Status       string  `gorm:"size:20"`
	CarrierName  *string `gorm:"size:150"`
	TrackingCode *string `gorm:"size:100"`
	PackageCount *int
	WeightKg     *float64
	LengthCm     *float64
	WidthCm      *float64
	HeightCm     *float64
	DispatchedAt *time.Time
	DeliveredAt  *time.Time
	UserID       *uint
	Notes        string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

func (v142Dispatch) TableName() string { return "tenant_ecommerce_dispatches" }

const (
	idxDispatchOrderIDUnique = "uk_tenant_ecommerce_dispatches_order_id"
	idxDispatchStatus        = "idx_tenant_ecommerce_dispatches_status"
)

// V142EcommerceDispatch crea tenant_ecommerce_dispatches (Contrato v2 §5/§8, Fase 8): despacho
// operativo de un pedido web, 1:1 con TenantEcommerceOrder — UNIQUE(order_id) es la defensa de
// base de datos contra doble despacho (la protección primaria real es el SELECT ... FOR UPDATE
// sobre el pedido dentro de la transacción de creación, ver EcommerceService.CreateDispatch; esta
// UNIQUE es la red de seguridad final).
type V142EcommerceDispatch struct{}

func (V142EcommerceDispatch) Version() int { return 142 }
func (V142EcommerceDispatch) Name() string { return "ecommerce_dispatch" }

func (V142EcommerceDispatch) Up(db *gorm.DB) error {
	mig := db.Migrator()
	if !mig.HasTable(&v142Dispatch{}) {
		if err := mig.CreateTable(&v142Dispatch{}); err != nil {
			return fmt.Errorf("tenant_ecommerce_dispatches: %w", err)
		}
	}
	if !migrationHasIndex(db, "tenant_ecommerce_dispatches", idxDispatchOrderIDUnique) {
		if err := db.Exec(fmt.Sprintf(
			`CREATE UNIQUE INDEX %s ON tenant_ecommerce_dispatches (order_id)`, idxDispatchOrderIDUnique,
		)).Error; err != nil {
			return fmt.Errorf("crear %s: %w", idxDispatchOrderIDUnique, err)
		}
	}
	if !migrationHasIndex(db, "tenant_ecommerce_dispatches", idxDispatchStatus) {
		if err := db.Exec(fmt.Sprintf(
			`CREATE INDEX %s ON tenant_ecommerce_dispatches (status)`, idxDispatchStatus,
		)).Error; err != nil {
			return fmt.Errorf("crear %s: %w", idxDispatchStatus, err)
		}
	}
	return nil
}
