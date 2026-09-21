package service

import (
	"testing"

	"tukifac/pkg/database"
)

// permissionKeysForRole module.action de cada permiso asignado al rol, para comparar contra la
// matriz aprobada del Contrato ecommerce v2 §7.3 (docs/ECOMMERCE-EVOLUTION-CONTRACT.md).
func permissionKeysForRole(t *testing.T, svc *RoleService, roleID uint) map[string]bool {
	t.Helper()
	ids, err := svc.RolePermissions(roleID)
	if err != nil {
		t.Fatal(err)
	}
	idSet := make(map[uint]bool, len(ids))
	for _, id := range ids {
		idSet[id] = true
	}
	all, err := svc.AllPermissions()
	if err != nil {
		t.Fatal(err)
	}
	out := make(map[string]bool, len(ids))
	for _, p := range all {
		if idSet[p.ID] {
			out[p.Module+"."+p.Action] = true
		}
	}
	return out
}

// TestSeedDefaultRolePermissions_EcommerceGranularRBAC verifica la matriz exacta aprobada:
// Almacenero solo ve/prepara (nunca convierte, cancela, devuelve ni despacha); Vendedor gestiona y
// convierte (nunca despacha ni devuelve); Supervisor tiene los 6 permisos, incluido orders_return
// (DEVUELTO restringido a Administrador/Supervisor, punto 4 aprobado). Regresión: antes de esta
// fase, el único permiso "ecommerce.orders" daba acceso indiferenciado a todo — este test falla
// si alguna vez se vuelve a asignar ese permiso único a un rol distinto de Administrador, o si se
// filtra "orders_return"/"orders_dispatch" hacia Almacenero/Vendedor por error.
func TestSeedDefaultRolePermissions_EcommerceGranularRBAC(t *testing.T) {
	db := setupRoleServiceTestDB(t)
	svc := NewRoleService(db)
	if err := svc.SeedPermissions(); err != nil {
		t.Fatalf("SeedPermissions: %v", err)
	}
	seedSystemRoles(t, db)
	if err := svc.SeedDefaultRolePermissions(); err != nil {
		t.Fatalf("SeedDefaultRolePermissions: %v", err)
	}

	almacenero := permissionKeysForRole(t, svc, roleByName(t, db, "Almacenero").ID)
	for _, want := range []string{"ecommerce.orders_view", "ecommerce.orders_prepare"} {
		if !almacenero[want] {
			t.Errorf("Almacenero debe tener %s", want)
		}
	}
	for _, forbidden := range []string{
		"ecommerce.orders_manage", "ecommerce.orders_convert",
		"ecommerce.orders_dispatch", "ecommerce.orders_return", "ecommerce.orders",
	} {
		if almacenero[forbidden] {
			t.Errorf("Almacenero NO debe tener %s (punto 3 aprobado: sin convertir/cancelar/devolver/despachar)", forbidden)
		}
	}

	vendedor := permissionKeysForRole(t, svc, roleByName(t, db, "Vendedor").ID)
	for _, want := range []string{"ecommerce.orders_view", "ecommerce.orders_manage", "ecommerce.orders_convert"} {
		if !vendedor[want] {
			t.Errorf("Vendedor debe tener %s", want)
		}
	}
	for _, forbidden := range []string{"ecommerce.orders_dispatch", "ecommerce.orders_return", "ecommerce.orders_prepare", "ecommerce.orders"} {
		if vendedor[forbidden] {
			t.Errorf("Vendedor NO debe tener %s (sin despachar ni devolver)", forbidden)
		}
	}

	supervisor := permissionKeysForRole(t, svc, roleByName(t, db, "Supervisor").ID)
	for _, want := range []string{
		"ecommerce.orders_view", "ecommerce.orders_manage", "ecommerce.orders_prepare",
		"ecommerce.orders_convert", "ecommerce.orders_dispatch", "ecommerce.orders_return",
	} {
		if !supervisor[want] {
			t.Errorf("Supervisor debe tener %s (gestión completa)", want)
		}
	}
}

// TestSeedPermissions_DoesNotOfferLegacyEcommerceOrders: el permiso deprecado "ecommerce.orders"
// no debe volver a crearse para un tenant nuevo — solo sigue existiendo para tenants que ya lo
// tenían antes de esta fase (compatibilidad vía migración v138, no vía este seed).
func TestSeedPermissions_DoesNotOfferLegacyEcommerceOrders(t *testing.T) {
	db := setupRoleServiceTestDB(t)
	svc := NewRoleService(db)
	if err := svc.SeedPermissions(); err != nil {
		t.Fatalf("SeedPermissions: %v", err)
	}
	var count int64
	db.Model(&database.TenantPermission{}).
		Where("module = ? AND action = ?", "ecommerce", "orders").Count(&count)
	if count != 0 {
		t.Fatalf("ecommerce.orders (deprecado) no debe sembrarse para tenants nuevos, got count=%d", count)
	}
	for _, want := range []string{"orders_view", "orders_prepare", "orders_manage", "orders_convert", "orders_dispatch", "orders_return"} {
		var c int64
		db.Model(&database.TenantPermission{}).Where("module = ? AND action = ?", "ecommerce", want).Count(&c)
		if c != 1 {
			t.Errorf("ecommerce.%s debe existir exactamente una vez en el catálogo sembrado, got %d", want, c)
		}
	}
}
