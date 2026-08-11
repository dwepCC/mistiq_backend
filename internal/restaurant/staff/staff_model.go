package staff

import (
	"strings"

	"tukifac/pkg/database"
)

func normalizeType(et string) string {
	return strings.TrimSpace(strings.ToLower(et))
}

func toLegacyRestaurantRole(st *database.TenantRestaurantStaff) string {
	switch normalizeType(st.EmployeeType) {
	case "admin", "supervisor":
		return "admin"
	case "cashier":
		return "vendedor"
	case "waiter", "driver":
		return "mozo"
	case "cook":
		return "cocinero"
	default:
		return st.EmployeeType
	}
}

func matchesStation(st *database.TenantRestaurantStaff, allowedTypes []string) bool {
	et := normalizeType(st.EmployeeType)
	for _, a := range allowedTypes {
		if strings.EqualFold(et, a) {
			return true
		}
		if a == "vendedor" && (et == "cashier" || et == "cajero") {
			return true
		}
		if a == "cashier" && (et == "cajero" || et == "vendedor") {
			return true
		}
		if a == "mozo" && (et == "waiter" || et == "driver") {
			return true
		}
		if a == "cocinero" && et == "cook" {
			return true
		}
	}
	if st.KitchenAccess && contains(allowedTypes, "cocinero") {
		return true
	}
	if st.DeliveryAccess && contains(allowedTypes, "driver") {
		return true
	}
	return false
}

func contains(ss []string, v string) bool {
	for _, s := range ss {
		if strings.EqualFold(s, v) {
			return true
		}
	}
	return false
}

func staffFlags(st *database.TenantRestaurantStaff) struct {
	CanCharge, CanDiscount, CanOpenTable, KitchenAccess, DeliveryAccess bool
} {
	return struct {
		CanCharge, CanDiscount, CanOpenTable, KitchenAccess, DeliveryAccess bool
	}{st.CanCharge, st.CanDiscount, st.CanOpenTable, st.KitchenAccess, st.DeliveryAccess}
}
