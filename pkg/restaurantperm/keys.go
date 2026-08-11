package restaurantperm

// Claves cortas para cache Redis y JWT liviano (no lista completa en token).
const (
	TablesView      = "t.v"
	TablesOpen      = "t.o"
	OrdersCreate    = "o.c"
	OrdersCharge    = "o.ch" // cobrar / generar venta (POS, mesa, ventas)
	OrdersCancel    = "o.cx" // anular comandas / ítems (requiere PIN de operaciones)
	KitchenView     = "k.v"
	KitchenUpdate   = "k.u"
	POSUse          = "p.u"
	CashView        = "c.v"
	ProductsManage  = "g.p"
	SettingsManage  = "s.m"
	DeliveryView    = "d.v"
	DeliveryUpdate  = "d.u"
)

// AllKeys lista completa para invalidación / admin.
var AllKeys = []string{
	TablesView, TablesOpen, OrdersCreate, OrdersCharge, OrdersCancel,
	KitchenView, KitchenUpdate, POSUse, CashView,
	ProductsManage, SettingsManage, DeliveryView, DeliveryUpdate,
}

// LegacyRoleToKeys mapeo retrocompatible restaurant_role → permisos.
func LegacyRoleToKeys(role string) []string {
	switch role {
	case "admin":
		return append([]string{}, AllKeys...)
	case "vendedor":
		return []string{TablesView, TablesOpen, OrdersCreate, OrdersCharge, OrdersCancel, KitchenView, POSUse, CashView, DeliveryView}
	case "mozo":
		return []string{TablesView, TablesOpen, OrdersCreate}
	case "cocinero":
		return []string{KitchenView, KitchenUpdate}
	default:
		return nil
	}
}

// EmployeeTypeToKeys plantilla por tipo de empleado (staff v2).
func EmployeeTypeToKeys(employeeType string, flags StaffFlags) []string {
	switch employeeType {
	case "admin", "supervisor":
		keys := append([]string{}, AllKeys...)
		return keys
	case "cashier", "cajero", "vendedor":
		keys := []string{TablesView, TablesOpen, OrdersCreate, OrdersCancel, POSUse, CashView, KitchenView}
		if flags.CanCharge {
			keys = append(keys, OrdersCharge)
		}
		return keys
	case "waiter":
		keys := []string{TablesView, TablesOpen, OrdersCreate}
		return keys
	case "cook":
		return []string{KitchenView, KitchenUpdate}
	case "driver":
		return []string{DeliveryView, DeliveryUpdate}
	default:
		return nil
	}
}

// StaffFlags capacidades granulares en fila staff.
type StaffFlags struct {
	CanCharge      bool
	CanDiscount    bool
	CanOpenTable   bool
	KitchenAccess  bool
	DeliveryAccess bool
}

// StationAllowedTypes estaciones de login PIN en UI.
func StationAllowedTypes(station string) []string {
	switch station {
	case "waiter", "mozo":
		return []string{"waiter", "mozo"}
	case "cashier", "cajero":
		return []string{"cashier", "vendedor"}
	case "kitchen", "cocina":
		return []string{"cook", "cocinero"}
	case "delivery":
		return []string{"driver"}
	case "admin":
		return []string{"admin", "supervisor"}
	default:
		return nil
	}
}
