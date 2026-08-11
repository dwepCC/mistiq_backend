package middleware

import "testing"

func TestTenantHasPermission_cashbankOpenImpliesView(t *testing.T) {
	perms := []string{"cashbank.open"}
	if !tenantHasPermission(perms, "cashbank.view") {
		t.Fatal("cashbank.open debe permitir cashbank.view")
	}
	if !tenantHasPermission(perms, "cashbank.open") {
		t.Fatal("cashbank.open debe permitir cashbank.open")
	}
	if tenantHasPermission(perms, "cashbank.close") {
		t.Fatal("cashbank.open no debe implicar cashbank.close")
	}
}

func TestTenantHasPermission_salesPosImpliesCreateAndProductsView(t *testing.T) {
	perms := []string{"sales.pos"}
	if !tenantHasPermission(perms, "sales.create") {
		t.Fatal("sales.pos debe permitir sales.create")
	}
	if !tenantHasPermission(perms, "products.view") {
		t.Fatal("sales.pos debe permitir products.view")
	}
}

func TestTenantHasPermission_manageGrantsModule(t *testing.T) {
	perms := []string{"cashbank.manage"}
	if !tenantHasPermission(perms, "cashbank.view") {
		t.Fatal("cashbank.manage debe permitir cashbank.view")
	}
	if !tenantHasPermission(perms, "cashbank.open") {
		t.Fatal("cashbank.manage debe permitir cashbank.open")
	}
}
