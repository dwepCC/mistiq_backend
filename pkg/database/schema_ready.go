package database

// IsCentralSchemaReady indica si el esquema central mínimo para jobs de suscripciones
// (cron de vencimientos) está aplicado. No sustituye migrate-tenants.
func IsCentralSchemaReady() bool {
	if CentralDB == nil {
		return false
	}
	m := CentralDB.Migrator()
	if !m.HasTable(&SaasSubscription{}) {
		return false
	}
	if !m.HasColumn(&SaasSubscription{}, "Status") {
		return false
	}
	if !m.HasColumn(&SaasSubscription{}, "EndDate") {
		return false
	}
	return true
}
