package tenantmigrations

import (
	"tukifac/pkg/database"

	"gorm.io/gorm"
)

// V187ClearWalkInCompanyContact quita del cliente "Clientes Varios" (is_default_walk_in) el correo y el
// teléfono cuando son los de la propia empresa.
//
// Al crear el tenant, el alta copiaba el correo y teléfono de la empresa al contacto por defecto del POS.
// Resultado: en toda venta a "Clientes Varios" el envío del comprobante por correo sugería la dirección de
// la empresa, y el comprobante terminaba enviándose a la empresa en vez de al cliente.
//
// Solo se limpia el valor que COINCIDE con el de tenant_company_configs: si alguien puso a propósito otro
// correo o teléfono en ese contacto, se respeta. Idempotente.
type V187ClearWalkInCompanyContact struct{}

func (V187ClearWalkInCompanyContact) Version() int { return 187 }
func (V187ClearWalkInCompanyContact) Name() string { return "clear_walkin_company_contact" }

func (V187ClearWalkInCompanyContact) Up(db *gorm.DB) error {
	if !db.Migrator().HasTable(&database.TenantContact{}) || !db.Migrator().HasTable(&database.TenantCompanyConfig{}) {
		return nil
	}
	return db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Exec(`
			UPDATE tenant_contacts SET email = ''
			WHERE is_default_walk_in = ? AND TRIM(COALESCE(email, '')) <> ''
			  AND LOWER(TRIM(email)) IN (
			    SELECT LOWER(TRIM(email)) FROM tenant_company_configs WHERE TRIM(COALESCE(email, '')) <> '')`, true).Error; err != nil {
			return err
		}
		return tx.Exec(`
			UPDATE tenant_contacts SET phone = ''
			WHERE is_default_walk_in = ? AND TRIM(COALESCE(phone, '')) <> ''
			  AND TRIM(phone) IN (
			    SELECT TRIM(phone) FROM tenant_company_configs WHERE TRIM(COALESCE(phone, '')) <> '')`, true).Error
	})
}
