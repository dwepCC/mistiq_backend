package tenantmigrations

import (
	"fmt"

	"tukifac/pkg/database"

	"gorm.io/gorm"
)

// V189AccountingModule crea las tablas del módulo «accounting» (Contabilidad), siembra su plan de
// cuentas y agrega sus permisos al catálogo.
//
// El módulo lo activa el superadmin por plan o por tenant: esta migración NO otorga acceso a nadie.
// Solo los permisos accounting.* se asignan al rol de sistema «Administrador» (para que, cuando el
// tenant tenga el módulo, pueda usarlo sin pasar antes por Roles); el resto de roles los recibe el
// dueño desde Roles. Catálogo espejo: internal/users/service/role_service.go (SeedPermissions).
type V189AccountingModule struct{}

func (V189AccountingModule) Version() int { return 189 }
func (V189AccountingModule) Name() string { return "accounting_module" }

var v189Perms = []struct{ Action, Label string }{
	{"sales_register", "Contabilidad: exportar registro de ventas"},
	{"purchases_register", "Contabilidad: exportar registro de compras"},
	{"export_formats", "Contabilidad: exportar formatos a sistemas contables"},
	{"summary_report", "Contabilidad: reporte resumido de ventas"},
	{"ledger", "Contabilidad: libro mayor"},
	{"sire_view", "Contabilidad: consultar SIRE (ventas y compras)"},
	{"sire_manage", "Contabilidad: solicitar propuesta SIRE y aceptar propuesta de ventas"},
	{"settings", "Contabilidad: cuentas contables y credenciales SIRE"},
}

// v189Chart plan de cuentas del Libro Mayor (4 niveles; equivalente al del sistema anterior).
var v189Chart = []database.TenantAccountingAccount{
	{Code: "1", Name: "Activos", Kind: "asset"},
	{Code: "1.1", Name: "Activos corrientes", Kind: "asset"},
	{Code: "1.1.1", Name: "Efectivo y equivalentes de efectivo", Kind: "asset"},
	{Code: "1.1.1.1", Name: "Caja", Kind: "asset"},
	{Code: "1.1.1.2", Name: "Bancos y billeteras", Kind: "asset"},
	{Code: "1.1.2", Name: "Cuentas por cobrar comerciales", Kind: "asset"},
	{Code: "1.1.3", Name: "Inventarios", Kind: "asset"},
	{Code: "1.1.4", Name: "Otros activos corrientes", Kind: "asset"},
	{Code: "1.1.4.1", Name: "IGV crédito fiscal (compras)", Kind: "asset"},
	{Code: "2", Name: "Pasivos", Kind: "liability"},
	{Code: "2.1", Name: "Pasivos corrientes", Kind: "liability"},
	{Code: "2.1.1", Name: "Cuentas por pagar comerciales", Kind: "liability"},
	{Code: "2.1.2", Name: "Tributos por pagar", Kind: "liability"},
	{Code: "2.1.2.1", Name: "IGV por pagar (ventas)", Kind: "liability"},
	{Code: "4", Name: "Ingresos", Kind: "income"},
	{Code: "4.1", Name: "Ventas", Kind: "income"},
	{Code: "4.1.1", Name: "Ventas netas (sin IGV)", Kind: "income"},
	{Code: "5", Name: "Costos y gastos", Kind: "expense"},
	{Code: "5.1", Name: "Compras", Kind: "expense"},
	{Code: "5.1.1", Name: "Compras netas (sin IGV)", Kind: "expense"},
}

func (V189AccountingModule) Up(db *gorm.DB) error {
	if err := db.AutoMigrate(
		&database.TenantAccountingSettings{},
		&database.TenantAccountingAccount{},
		&database.TenantSireSettings{},
		&database.TenantSireTicket{},
	); err != nil {
		return fmt.Errorf("crear tablas de contabilidad: %w", err)
	}

	for _, a := range v189Chart {
		var row database.TenantAccountingAccount
		if err := db.Where(database.TenantAccountingAccount{Code: a.Code}).
			Attrs(database.TenantAccountingAccount{Name: a.Name, Kind: a.Kind, Active: true}).
			FirstOrCreate(&row).Error; err != nil {
			return err
		}
	}

	if !db.Migrator().HasTable(&database.TenantRole{}) || !db.Migrator().HasTable(&database.TenantPermission{}) ||
		!db.Migrator().HasTable(&database.TenantRolePermission{}) {
		return nil
	}
	var admins []database.TenantRole
	if err := db.Where("is_system = ? AND name = ?", true, "Administrador").Find(&admins).Error; err != nil {
		return err
	}
	for _, p := range v189Perms {
		var perm database.TenantPermission
		if err := db.Where(database.TenantPermission{Module: "accounting", Action: p.Action}).
			Attrs(database.TenantPermission{Label: p.Label}).
			FirstOrCreate(&perm).Error; err != nil {
			return err
		}
		for _, r := range admins {
			if err := grantIfMissing(db, r.ID, perm.ID); err != nil {
				return err
			}
		}
	}
	return nil
}
