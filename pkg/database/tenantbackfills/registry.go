package tenantbackfills

// TenantBackfills backfills ordenados por versión.
var TenantBackfills = []TenantBackfill{
	V031BranchBackfill{},
	V032RestaurantOrdersBackfill{},
	V033FinalizeOrphanTableOrders{},
	V034ProductCodes{},
	V035MergeDuplicateContacts{},
	V036SalePaymentCashSessionBackfill{},
}

// ByVersion mapa versión → backfill.
func ByVersion() map[int]TenantBackfill {
	m := make(map[int]TenantBackfill, len(TenantBackfills))
	for _, b := range TenantBackfills {
		m[b.Version()] = b
	}
	return m
}
