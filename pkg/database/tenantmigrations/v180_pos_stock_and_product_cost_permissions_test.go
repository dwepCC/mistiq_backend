package tenantmigrations

import (
	"testing"

	"tukifac/pkg/database"
)

func TestV165_StockToEveryoneCostToThoseWhoHandleIt(t *testing.T) {
	db := v132TestDB(t)
	productsView := v132CreatePerm(t, db, "products", "view")
	productsEdit := v132CreatePerm(t, db, "products", "edit")
	purchasesView := v132CreatePerm(t, db, "purchases", "view")
	salesPos := v132CreatePerm(t, db, "sales", "pos")
	invDoc := v132CreatePerm(t, db, "inventory", "create_document")
	invView := v132CreatePerm(t, db, "inventory", "view")

	mk := func(name string, perms ...database.TenantPermission) database.TenantRole {
		r := database.TenantRole{Name: name}
		if err := db.Create(&r).Error; err != nil {
			t.Fatal(err)
		}
		for _, p := range perms {
			v132Grant(t, db, r.ID, p.ID)
		}
		return r
	}
	admin := mk("Administrador")
	cajero := mk("Cajero", productsView, salesPos)
	editor := mk("Editor", productsView, productsEdit)
	compras := mk("Compras", purchasesView)
	vacio := mk("Vacio")
	ingresos := mk("Ingresos", invDoc)
	soloInv := mk("SoloInv", invView)

	for i := 0; i < 2; i++ { // idempotente
		if err := (V180POSStockAndProductCostPermissions{}).Up(db); err != nil {
			t.Fatal(err)
		}
	}

	for _, r := range []database.TenantRole{admin, cajero, editor, compras, vacio, ingresos, soloInv} {
		if !v132HasGrant(t, db, r.ID, "pos", "view_stock") {
			t.Errorf("%s debe recibir pos.view_stock", r.Name)
		}
	}
	for _, r := range []database.TenantRole{admin, editor, compras, ingresos} {
		if !v132HasGrant(t, db, r.ID, "products", "view_cost") || !v132HasGrant(t, db, r.ID, "pos", "view_cost") {
			t.Errorf("%s debe recibir products.view_cost y pos.view_cost", r.Name)
		}
	}
	for _, r := range []database.TenantRole{admin, cajero, editor, compras, vacio, ingresos, soloInv} {
		if !v132HasGrant(t, db, r.ID, "pos", "view_prices") {
			t.Errorf("%s debe recibir pos.view_prices", r.Name)
		}
	}
	for _, r := range []database.TenantRole{cajero, vacio, soloInv} {
		if v132HasGrant(t, db, r.ID, "products", "view_cost") || v132HasGrant(t, db, r.ID, "pos", "view_cost") {
			t.Errorf("%s no debe recibir costo", r.Name)
		}
	}

	var n int64
	db.Model(&database.TenantRolePermission{}).Where("role_id = ?", admin.ID).Count(&n)
	if n != 4 {
		t.Errorf("sin duplicados: admin debería tener 4 filas, tiene %d", n)
	}
}
