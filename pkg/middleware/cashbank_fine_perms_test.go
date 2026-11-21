package middleware

import "testing"

// Abrir/cerrar caja y mover efectivo dan «Caja» (cashbank.view) pero NO cuentas y bancos ni las
// acciones de administración: cada una se concede por separado (o con cashbank.manage).
func TestCashbankFinePermissionsAreNotImplied(t *testing.T) {
	turn := []string{"cashbank.open", "cashbank.close", "cashbank.movements", "cashbank.arqueo"}
	if !tenantHasPermission(turn, cashbankTenantPerm("view")) {
		t.Error("el turno de caja debe dar cashbank.view")
	}
	for _, action := range []string{"bank_view", "bank_manage", "payment_methods_manage", "delete_session"} {
		if tenantHasPermission(turn, cashbankTenantPerm(action)) {
			t.Errorf("abrir/cerrar/mover caja no debe dar %s", action)
		}
		if tenantHasPermission([]string{"cashbank.view"}, cashbankTenantPerm(action)) {
			t.Errorf("cashbank.view no debe dar %s", action)
		}
		if !tenantHasPermission([]string{cashbankTenantPerm(action)}, cashbankTenantPerm(action)) {
			t.Errorf("el permiso propio de %s debe bastar", action)
		}
		if !tenantHasPermission([]string{"cashbank.manage"}, cashbankTenantPerm(action)) {
			t.Errorf("cashbank.manage debe conceder %s", action)
		}
	}
	// Cada permiso fino es independiente de los demás.
	if tenantHasPermission([]string{"cashbank.bank_view"}, cashbankTenantPerm("bank_manage")) {
		t.Error("bank_view no debe dar bank_manage")
	}
}
