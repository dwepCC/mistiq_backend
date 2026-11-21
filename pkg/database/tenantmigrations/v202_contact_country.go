package tenantmigrations

import "gorm.io/gorm"

// V202ContactCountry agrega tenant_contacts.country (país ISO alfa-2 de un cliente extranjero; vacío = Perú). Solo agrega la columna: no toca datos
// existentes ni usa el struct completo del contacto (evita ALTER ... MODIFY sobre una tabla grande). Idempotente.
type V202ContactCountry struct{}

func (V202ContactCountry) Version() int { return 202 }
func (V202ContactCountry) Name() string { return "contact_country" }

func (V202ContactCountry) Up(db *gorm.DB) error {
	return db.AutoMigrate(&v202Contact{})
}
