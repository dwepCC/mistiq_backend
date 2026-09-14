package tenantmigrations

import (
	"fmt"

	"gorm.io/gorm"
)

type v133TenantContact struct {
	ID              uint `gorm:"primaryKey"`
	IsDefaultWalkIn bool `gorm:"column:is_default_walkin;default:false;index"`
}

func (v133TenantContact) TableName() string { return "tenant_contacts" }

// V133TenantContactIsDefaultWalkIn agrega tenant_contacts.is_default_walkin (aditiva, default
// false) — mismo patrón que V129CashBankMovementContactID.
//
// NOTA DE INCIDENTE (2026-09-13): el campo IsDefaultWalkIn se agregó al struct TenantContact
// (pkg/database/migrations.go) y lo empezaron a usar tenant_contact_defaults.go,
// tenant_provision_seed.go y el backfill V035MergeDuplicateContacts sin la migración versionada
// correspondiente — este proyecto no usa AutoMigrate por reflection en producción (ver nota en
// V129), así que agregar el campo al struct Go no crea la columna en tenants ya provisionados.
// Resultado: "Error 1054 Unknown column 'd.is_default_walkin' in 'where clause'" en el backfill
// V035 para cualquier tenant provisionado antes de este campo. Cualquier cambio de schema DEBE ir
// acompañado de su migración en este paquete ANTES de desplegar el binario que la usa.
type V133TenantContactIsDefaultWalkIn struct{}

func (V133TenantContactIsDefaultWalkIn) Version() int { return 133 }
func (V133TenantContactIsDefaultWalkIn) Name() string { return "tenant_contact_is_default_walkin" }

func (V133TenantContactIsDefaultWalkIn) Up(db *gorm.DB) error {
	mig := db.Migrator()

	contact := &v133TenantContact{}
	if !mig.HasTable(contact) {
		return nil
	}
	if !mig.HasColumn(contact, "IsDefaultWalkIn") {
		if err := mig.AddColumn(contact, "IsDefaultWalkIn"); err != nil {
			return fmt.Errorf("add tenant_contacts.is_default_walkin: %w", err)
		}
	}

	return nil
}
