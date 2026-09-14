package tenantmigrations

import (
	"fmt"
	"testing"

	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func v132TestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&database.TenantRole{},
		&database.TenantPermission{},
		&database.TenantRolePermission{},
	); err != nil {
		t.Fatal(err)
	}
	return db
}

func v132CreatePerm(t *testing.T, db *gorm.DB, module, action string) database.TenantPermission {
	t.Helper()
	p := database.TenantPermission{Module: module, Action: action, Label: module + "." + action}
	if err := db.Create(&p).Error; err != nil {
		t.Fatal(err)
	}
	return p
}

func v132Grant(t *testing.T, db *gorm.DB, roleID, permID uint) {
	t.Helper()
	if err := db.Create(&database.TenantRolePermission{RoleID: roleID, PermissionID: permID}).Error; err != nil {
		t.Fatal(err)
	}
}

func v132HasGrant(t *testing.T, db *gorm.DB, roleID uint, module, action string) bool {
	t.Helper()
	var count int64
	err := db.Model(&database.TenantRolePermission{}).
		Joins("JOIN tenant_permissions ON tenant_permissions.id = tenant_role_permissions.permission_id").
		Where("tenant_role_permissions.role_id = ? AND tenant_permissions.module = ? AND tenant_permissions.action = ?", roleID, module, action).
		Count(&count).Error
	if err != nil {
		t.Fatal(err)
	}
	return count > 0
}

// Caso central: Vendedor (sales.view + sales.create) debe terminar con acceso a Cotizaciones y
// CxC equivalente a lo que ya tenía; Cajero (solo cashbank.open, sin nada de sales/billing) no
// debe ganar Cotizaciones/CxC/Facturación de la nada, pero SÍ debe ganar los 4 permisos "blanket"
// (nadie los necesitaba antes) y NO debe perder nada de lo que ya tenía.
func TestV132PermissionCatalogRedesign_MirrorsOldAccessExactly(t *testing.T) {
	db := v132TestDB(t)

	salesView := v132CreatePerm(t, db, "sales", "view")
	salesCreate := v132CreatePerm(t, db, "sales", "create")
	billingSend := v132CreatePerm(t, db, "billing", "send")
	purchasesView := v132CreatePerm(t, db, "purchases", "view")
	cashbankOpen := v132CreatePerm(t, db, "cashbank", "open")

	vendedor := database.TenantRole{Name: "Vendedor"}
	if err := db.Create(&vendedor).Error; err != nil {
		t.Fatal(err)
	}
	v132Grant(t, db, vendedor.ID, salesView.ID)
	v132Grant(t, db, vendedor.ID, salesCreate.ID)
	v132Grant(t, db, vendedor.ID, billingSend.ID)

	cajero := database.TenantRole{Name: "Cajero"}
	if err := db.Create(&cajero).Error; err != nil {
		t.Fatal(err)
	}
	v132Grant(t, db, cajero.ID, cashbankOpen.ID)

	contador := database.TenantRole{Name: "Contador"}
	if err := db.Create(&contador).Error; err != nil {
		t.Fatal(err)
	}
	v132Grant(t, db, contador.ID, purchasesView.ID)

	if err := (V132PermissionCatalogRedesign{}).Up(db); err != nil {
		t.Fatalf("Up: %v", err)
	}

	// Vendedor: sales.view → quotations.view + receivables.view; sales.create → el resto de
	// quotations + receivables.collect/confirm_bn; billing.send → los 4 nuevos de billing.
	for _, ma := range [][2]string{
		{"quotations", "view"}, {"quotations", "create"}, {"quotations", "edit"},
		{"quotations", "delete"}, {"quotations", "convert"},
		{"receivables", "view"}, {"receivables", "collect"}, {"receivables", "confirm_bn"},
		{"billing", "credit_note"}, {"billing", "debit_note"}, {"billing", "despatch"}, {"billing", "advanced_docs"},
		{"sales", "cancel"}, {"cashbank", "arqueo"}, {"subscription", "view"}, {"subscription", "manage"},
	} {
		if !v132HasGrant(t, db, vendedor.ID, ma[0], ma[1]) {
			t.Errorf("vendedor: esperaba %s.%s concedido, no lo tiene", ma[0], ma[1])
		}
	}
	// Vendedor NO tenía purchases.*, no debe ganar payables.
	if v132HasGrant(t, db, vendedor.ID, "payables", "view") || v132HasGrant(t, db, vendedor.ID, "payables", "pay") {
		t.Error("vendedor: no debía ganar payables.* (nunca tuvo purchases.*)")
	}

	// Cajero: no tenía sales/billing/purchases → no debe ganar quotations/receivables/payables/
	// billing nuevos, pero SÍ los 4 blanket (nadie los necesitaba antes).
	for _, ma := range [][2]string{
		{"quotations", "view"}, {"receivables", "view"}, {"payables", "view"},
		{"billing", "credit_note"},
	} {
		if v132HasGrant(t, db, cajero.ID, ma[0], ma[1]) {
			t.Errorf("cajero: no debía ganar %s.%s (no tenía el permiso viejo equivalente)", ma[0], ma[1])
		}
	}
	for _, ma := range v132BlanketGrants {
		if !v132HasGrant(t, db, cajero.ID, ma[0], ma[1]) {
			t.Errorf("cajero: esperaba %s.%s (blanket), no lo tiene", ma[0], ma[1])
		}
	}
	// Cajero no debe perder lo que ya tenía.
	if !v132HasGrant(t, db, cajero.ID, "cashbank", "open") {
		t.Error("cajero: perdió cashbank.open, no debía tocarse")
	}

	// Contador: purchases.view → payables.view, pero no payables.pay (no tenía purchases.create).
	if !v132HasGrant(t, db, contador.ID, "payables", "view") {
		t.Error("contador: esperaba payables.view (tenía purchases.view)")
	}
	if v132HasGrant(t, db, contador.ID, "payables", "pay") {
		t.Error("contador: no debía ganar payables.pay (no tenía purchases.create)")
	}
}

// Los permisos fantasma (sales.edit, sales.delete, purchases.edit, reports.view) deben
// desaparecer del catálogo y de cualquier rol que los tuviera marcados, sin arrastrar nada más.
func TestV132PermissionCatalogRedesign_RemovesPhantomPermissions(t *testing.T) {
	db := v132TestDB(t)

	salesEdit := v132CreatePerm(t, db, "sales", "edit")
	salesView := v132CreatePerm(t, db, "sales", "view")
	reportsView := v132CreatePerm(t, db, "reports", "view")

	role := database.TenantRole{Name: "Supervisor"}
	if err := db.Create(&role).Error; err != nil {
		t.Fatal(err)
	}
	v132Grant(t, db, role.ID, salesEdit.ID)
	v132Grant(t, db, role.ID, salesView.ID)
	v132Grant(t, db, role.ID, reportsView.ID)

	if err := (V132PermissionCatalogRedesign{}).Up(db); err != nil {
		t.Fatalf("Up: %v", err)
	}

	var count int64
	db.Model(&database.TenantPermission{}).Where("module = ? AND action IN ?", "sales", []string{"edit", "delete"}).Count(&count)
	if count != 0 {
		t.Errorf("sales.edit/delete: esperaba 0 filas de catálogo, hay %d", count)
	}
	db.Model(&database.TenantPermission{}).Where("module = ? AND action = ?", "reports", "view").Count(&count)
	if count != 0 {
		t.Errorf("reports.view: esperaba 0 filas de catálogo, hay %d", count)
	}
	db.Model(&database.TenantRolePermission{}).Where("permission_id = ?", salesEdit.ID).Count(&count)
	if count != 0 {
		t.Errorf("supervisor: esperaba que se borrara el grant de sales.edit, sigue habiendo %d", count)
	}
	// No debe tocar lo que sí es real.
	if !v132HasGrant(t, db, role.ID, "sales", "view") {
		t.Error("supervisor: perdió sales.view, no debía tocarse")
	}
}

// Re-ejecutar debe ser un no-op (idempotente): ni duplica catálogo ni duplica grants.
func TestV132PermissionCatalogRedesign_Idempotent(t *testing.T) {
	db := v132TestDB(t)
	salesView := v132CreatePerm(t, db, "sales", "view")
	role := database.TenantRole{Name: "Vendedor"}
	if err := db.Create(&role).Error; err != nil {
		t.Fatal(err)
	}
	v132Grant(t, db, role.ID, salesView.ID)

	if err := (V132PermissionCatalogRedesign{}).Up(db); err != nil {
		t.Fatalf("primera ejecucion: %v", err)
	}
	var countAfterFirst int64
	db.Model(&database.TenantRolePermission{}).Where("role_id = ?", role.ID).Count(&countAfterFirst)

	if err := (V132PermissionCatalogRedesign{}).Up(db); err != nil {
		t.Fatalf("segunda ejecucion: %v", err)
	}
	var countAfterSecond int64
	db.Model(&database.TenantRolePermission{}).Where("role_id = ?", role.ID).Count(&countAfterSecond)

	if countAfterFirst != countAfterSecond {
		t.Fatalf("no fue idempotente: primera=%d segunda=%d", countAfterFirst, countAfterSecond)
	}

	var catalogCount int64
	db.Model(&database.TenantPermission{}).Where("module = ? AND action = ?", "quotations", "view").Count(&catalogCount)
	if catalogCount != 1 {
		t.Fatalf("catálogo duplicado: quotations.view tiene %d filas", catalogCount)
	}
}
