package database

import (
	"tukifac/pkg/tenantrubro"

	"gorm.io/gorm"
)

func IsGastronomicRubro(rubro string) bool {
	return tenantrubro.IsGastronomico(rubro)
}

// EnableTenantModule activa un módulo ERP para un tenant en BD central.
func EnableTenantModule(central *gorm.DB, tenantID uint, moduleKey string) error {
	if central == nil || tenantID == 0 || moduleKey == "" {
		return nil
	}
	cfg := "{}"
	var tm TenantModule
	err := central.Where("tenant_id = ? AND module_key = ?", tenantID, moduleKey).First(&tm).Error
	if err != nil {
		return central.Create(&TenantModule{
			TenantID: tenantID, ModuleKey: moduleKey, Enabled: true, ConfigJSON: &cfg,
		}).Error
	}
	return central.Model(&tm).Update("enabled", true).Error
}
