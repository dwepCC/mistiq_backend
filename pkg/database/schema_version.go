package database

// Versiones de esquema tenant (Migration System v3).
// 0 = BD vacía; la primera migración registrada es V001 (baseline_schema).
const TenantSchemaBaselineVersion = 0

var tenantSchemaTargetVersion = 64

// SetTenantSchemaTargetVersion sincroniza el objetivo con tenantmigrations.MaxVersion() al arranque.
func SetTenantSchemaTargetVersion(v int) {
	if v > 0 {
		tenantSchemaTargetVersion = v
	}
}

// TenantSchemaTargetVersion versión objetivo del binario (registry incremental).
func TenantSchemaTargetVersion() int {
	return tenantSchemaTargetVersion
}
