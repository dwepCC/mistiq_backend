package tenantmigrations

import (
	"tukifac/pkg/database"

	"gorm.io/gorm"
)

// V188CashbankFinePermissions separa en permisos propios las pantallas y acciones de Caja y bancos
// que hasta ahora colgaban de cashbank.view / cashbank.manage.
//
// Problema: cashbank.open, cashbank.close y cashbank.movements implican cashbank.view (ver
// tenant_permissions.go), y cashbank.view abría TODO el menú Finanzas: Ingresos, Egresos, Cuentas /
// Bancos y Reporte de caja. Un vendedor que solo debía abrir/cerrar su caja veía también las cuentas
// bancarias de la empresa. Y las acciones de administración (métodos de pago, cuentas bancarias,
// borrar una caja) solo se podían dar en bloque con cashbank.manage.
//
// Permisos nuevos (cada uno se asigna por separado desde Roles):
//
//	cashbank.income_view            Ver ingresos de caja
//	cashbank.expense_view           Ver egresos de caja
//	cashbank.bank_view              Ver cuentas y bancos (pantalla y movimientos bancarios)
//	cashbank.bank_manage            Crear/editar cuentas bancarias y registrar movimientos bancarios
//	cashbank.report_view            Ver reporte de caja
//	cashbank.payment_methods_manage Gestionar métodos de pago
//	cashbank.delete_session         Eliminar una caja sin movimientos
//
// cashbank.view pasa a significar solo «Caja» (turnos y estado). cashbank.manage sigue concediendo
// todo el módulo (regla general {modulo}.manage).
//
// Backfill (nadie pierde lo que ya tenía el día del deploy, salvo lo que se pidió quitar):
//   - income_view / expense_view / report_view → todo rol con acceso efectivo a caja (view, open,
//     close, movements o manage).
//   - bank_view → igual, EXCEPTO el rol de sistema «Vendedor», que no debe ver cuentas y bancos.
//   - bank_manage / payment_methods_manage / delete_session → roles con cashbank.manage explícito
//     (que ya podían todo eso), para que la casilla aparezca marcada en Roles.
//
// Catálogo espejo: internal/users/service/role_service.go (SeedPermissions).
type V188CashbankFinePermissions struct{}

func (V188CashbankFinePermissions) Version() int { return 188 }
func (V188CashbankFinePermissions) Name() string { return "cashbank_fine_permissions" }

var v188Perms = []struct{ Action, Label string }{
	{"income_view", "Ver ingresos de caja"},
	{"expense_view", "Ver egresos de caja"},
	{"bank_view", "Ver cuentas y bancos"},
	{"bank_manage", "Crear/editar cuentas bancarias y registrar movimientos bancarios"},
	{"report_view", "Ver reporte de caja"},
	{"payment_methods_manage", "Gestionar métodos de pago"},
	{"delete_session", "Eliminar una caja sin movimientos"},
}

func (V188CashbankFinePermissions) Up(db *gorm.DB) error {
	if !db.Migrator().HasTable(&database.TenantRole{}) || !db.Migrator().HasTable(&database.TenantPermission{}) ||
		!db.Migrator().HasTable(&database.TenantRolePermission{}) {
		return nil
	}
	return db.Transaction(func(tx *gorm.DB) error {
		ids := map[string]uint{}
		for _, p := range v188Perms {
			var perm database.TenantPermission
			if err := tx.Where(database.TenantPermission{Module: "cashbank", Action: p.Action}).
				Attrs(database.TenantPermission{Label: p.Label}).
				FirstOrCreate(&perm).Error; err != nil {
				return err
			}
			ids[p.Action] = perm.ID
		}

		rolesWith := func(actions ...string) (map[uint]bool, error) {
			var roleIDs []uint
			if err := tx.Model(&database.TenantRolePermission{}).
				Joins("JOIN tenant_permissions ON tenant_permissions.id = tenant_role_permissions.permission_id").
				Where("tenant_permissions.module = ? AND tenant_permissions.action IN ?", "cashbank", actions).
				Distinct().Pluck("tenant_role_permissions.role_id", &roleIDs).Error; err != nil {
				return nil, err
			}
			out := make(map[uint]bool, len(roleIDs))
			for _, id := range roleIDs {
				out[id] = true
			}
			return out, nil
		}
		hasAccess, err := rolesWith("view", "open", "close", "movements", "manage")
		if err != nil {
			return err
		}
		hasManage, err := rolesWith("manage")
		if err != nil {
			return err
		}

		var roles []database.TenantRole
		if err := tx.Find(&roles).Error; err != nil {
			return err
		}
		for _, role := range roles {
			if hasAccess[role.ID] {
				for _, a := range []string{"income_view", "expense_view", "report_view"} {
					if err := grantIfMissing(tx, role.ID, ids[a]); err != nil {
						return err
					}
				}
				if !(role.IsSystem && role.Name == "Vendedor") {
					if err := grantIfMissing(tx, role.ID, ids["bank_view"]); err != nil {
						return err
					}
				}
			}
			if hasManage[role.ID] {
				for _, a := range []string{"bank_manage", "payment_methods_manage", "delete_session"} {
					if err := grantIfMissing(tx, role.ID, ids[a]); err != nil {
						return err
					}
				}
			}
		}
		return nil
	})
}
