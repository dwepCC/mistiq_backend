package tenantmigrations

// TenantMigrations migraciones ordenadas (> baseline V30). Solo versiones nuevas.
var TenantMigrations = []TenantMigration{
	V031MultiBranch{},
	V032RestaurantOrders{},
	V033DeliveryDriversTimestamps{},
	V034RestaurantSettingsDeletionPin{},
	V035RestaurantStaff{},
	V036StaffDefinitive{},
	V037StaffSchemaRepair{},
	V038CashSessionsPerUser{},
	V039FiscalLegacyCleanup{},
	V040FiscalMetadataColumns{},
	V041AutomaticSend{},
	V042DespatchSaleLink{},
	V043FiscalSaleLinks{},
	V044DeliveryCompanies{},
	V045RestaurantKitchenRounds{},
	V046TableOrderPrintedAtDatetime{},
	V047SaleAmountsSunatPrecision{},
	V048ComandaModifiersJSON{},
	V049ComandaIgvSnapshot{},
	V050SaleUniqueIndexes{},
	V051DocumentSeriesCategoryFix{},
	V052SeriesGlobalUnique{},
	V053DeliveryCompaniesTimestamps{},
	V054ModifierGroupKind{},
	V055ProductPresentations{},
	V056ProductPresentationsSoftDelete{},
	V057BranchFiscalDomicile{},
	V058CompanyAdditionalNotes{},
	V059UserBranches{},
	V060NoteSeriesBoleta{},
}

// ByVersion mapa versión → migración.
func ByVersion() map[int]TenantMigration {
	m := make(map[int]TenantMigration, len(TenantMigrations))
	for _, mig := range TenantMigrations {
		m[mig.Version()] = mig
	}
	return m
}

// MaxVersion última versión definida en el registry.
func MaxVersion() int {
	max := 0
	for _, mig := range TenantMigrations {
		if v := mig.Version(); v > max {
			max = v
		}
	}
	return max
}
