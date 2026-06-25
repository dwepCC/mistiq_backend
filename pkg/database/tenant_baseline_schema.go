package database

import (
	"fmt"

	"gorm.io/gorm"
)

// ApplyBaselineSchema crea el esquema inicial del tenant (V001). Solo invocable desde tenantmigrations.
func ApplyBaselineSchema(db *gorm.DB) error {
	if err := db.AutoMigrate(
		&TenantRole{},
		&TenantPermission{},
		&TenantRolePermission{},
		&TenantUser{},
		&TenantBranch{},
		&TenantCompanyConfig{},
		&TenantDocumentSeries{},
		&TenantContact{},
		&TenantContactPerson{},
		&TenantCategory{},
		&TenantProduct{},
		&TenantProductStock{},
		&TenantProductSerial{},
		&TenantStockMovement{},
		&TenantTransfer{},
		&TenantTransferLog{},
		&TenantProductPresentation{},
		&TenantModifierGroup{},
		&TenantModifierOption{},
		&TenantProductModifierGroup{},
		&TenantSale{},
		&TenantSaleItem{},
		&TenantInvoice{},
		&TenantSunatSummary{},
		&TenantSunatVoided{},
		&TenantDespatch{},
		&TenantRetention{},
		&TenantPerception{},
		&TenantSunatReversion{},
		&TenantPurchase{},
		&TenantPurchaseItem{},
		&TenantCashSession{},
		&TenantCashMovement{},
		&TenantPaymentMethod{},
		&TenantPaymentCondition{},
		&TenantTaxPaymentType{},
		&TenantBankAccount{},
		&TenantBankMovement{},
		&TenantExternalModule{},
		&TenantRestaurantFloor{},
		&TenantRestaurantTable{},
		&TenantWaiter{},
		&TenantTableSession{},
		&TenantTableOrder{},
		&TenantComanda{},
		&TenantDeliveryCompany{},
		&TenantDeliveryDriver{},
		&TenantRestaurantSetting{},
		&TenantRestaurantStaff{},
		&TenantUserRestaurantRole{},
		&TenantSalePayment{},
		&TenantMembership{},
		&TenantMembershipInvoice{},
		&UbiRegion{},
		&UbiProvincia{},
		&UbiDistrito{},
		&TenantSchemaPatch{},
		&TenantMigrationHistory{},
	); err != nil {
		return fmt.Errorf("baseline schema: %w", err)
	}
	if err := ensureDocumentSeriesColumns(db); err != nil {
		return fmt.Errorf("baseline document series columns: %w", err)
	}
	return nil
}
