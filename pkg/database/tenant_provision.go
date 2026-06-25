package database

import (
	"gorm.io/gorm"
)

// RollbackTenantProvision revierte un alta parcial (central + BD tenant + pool).
func RollbackTenantProvision(central *gorm.DB, tenantID uint, dbName string) {
	if dbName != "" {
		RemoveTenantFromPool(dbName)
		_ = DropTenantDB(dbName)
	}
	if central != nil && tenantID > 0 {
		_ = PurgeTenantCentralData(central, tenantID)
	}
}

// SeedDefaultTenantModules activa módulos base cuando no hay suscripción SaaS (fallback).
func SeedDefaultTenantModules(central *gorm.DB, tenantID uint) error {
	if central == nil || tenantID == 0 {
		return nil
	}
	baseModules := []string{"sales", "purchases", "inventory", "cashbank", "contacts", "products"}
	for _, mod := range baseModules {
		cfg := "{}"
		var tm TenantModule
		if central.Where("tenant_id = ? AND module_key = ?", tenantID, mod).First(&tm).Error != nil {
			_ = central.Create(&TenantModule{
				TenantID: tenantID, ModuleKey: mod, Enabled: true, ConfigJSON: &cfg,
			}).Error
		}
	}
	otherModules := []string{
		"billing", "restaurant", "ecommerce", "hotel", "clinic", "transport",
		"manufacturing", "memberships", "hr", "accounting", "bi", "fixedassets", "documents", "support",
	}
	for _, mod := range otherModules {
		cfg := "{}"
		var tm TenantModule
		if central.Where("tenant_id = ? AND module_key = ?", tenantID, mod).First(&tm).Error != nil {
			_ = central.Create(&TenantModule{
				TenantID: tenantID, ModuleKey: mod, Enabled: false, ConfigJSON: &cfg,
			}).Error
		}
	}
	return nil
}
