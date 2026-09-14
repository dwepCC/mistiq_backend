package tenantmigrations

import (
	"fmt"

	"gorm.io/gorm"
)

type v129CashMovement struct {
	ID        uint  `gorm:"primaryKey"`
	ContactID *uint `gorm:"column:contact_id;index"`
}

func (v129CashMovement) TableName() string { return "tenant_cash_movements" }

type v129BankMovement struct {
	ID        uint  `gorm:"primaryKey"`
	ContactID *uint `gorm:"column:contact_id;index"`
}

func (v129BankMovement) TableName() string { return "tenant_bank_movements" }

// V129CashBankMovementContactID agrega contact_id (aditiva, nullable) a tenant_cash_movements y
// tenant_bank_movements — mismo patrón que V125BankMovementManualMetadata.
//
// AddMovement (ingreso/egreso manual de Caja, vista Ingresos/Egresos) permite vincular un egreso
// con un proveedor. Un movimiento manual vive en EXACTAMENTE una de las dos tablas según su
// método de pago (efectivo → tenant_cash_movements, Yape/Plin/tarjeta/transferencia →
// tenant_bank_movements), así que la columna hace falta en ambas.
//
// NOTA DE INCIDENTE (2026-09-09): el código Go que usa esta columna (pkg/database/migrations.go,
// internal/cashbank/service/cashbank_service.go) se desplegó ANTES de que existiera esta
// migración versionada — este proyecto ya no usa AutoMigrate por reflection en producción
// (MigrateTenant está deprecado, ver migrations.go), así que agregar el campo al struct Go no
// crea la columna en las bases reales. Resultado: "Error 1054 Unknown column 'contact_id' in
// 'field list'" en el fleet completo al primer AddMovement. Se revirtió el deploy (commit
// "Revert ... vincular egreso manual con un proveedor") mientras se preparaba esta migración.
// Cualquier cambio de schema en tenant_cash_movements/tenant_bank_movements DEBE ir acompañado
// de su migración en este paquete ANTES de desplegar el binario que la usa, nunca después.
type V129CashBankMovementContactID struct{}

func (V129CashBankMovementContactID) Version() int { return 129 }
func (V129CashBankMovementContactID) Name() string { return "cash_bank_movement_contact_id" }

func (V129CashBankMovementContactID) Up(db *gorm.DB) error {
	mig := db.Migrator()

	cash := &v129CashMovement{}
	if mig.HasTable(cash) {
		if !mig.HasColumn(cash, "ContactID") {
			if err := mig.AddColumn(cash, "ContactID"); err != nil {
				return fmt.Errorf("add tenant_cash_movements.contact_id: %w", err)
			}
		}
	}

	bank := &v129BankMovement{}
	if mig.HasTable(bank) {
		if !mig.HasColumn(bank, "ContactID") {
			if err := mig.AddColumn(bank, "ContactID"); err != nil {
				return fmt.Errorf("add tenant_bank_movements.contact_id: %w", err)
			}
		}
	}

	return nil
}
