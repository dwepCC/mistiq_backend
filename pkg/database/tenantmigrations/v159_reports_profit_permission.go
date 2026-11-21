package tenantmigrations

import (
	"tukifac/pkg/database"

	"gorm.io/gorm"
)

// V159ReportsProfitPermission agrega "reports.profit" (reporte de Utilidades: ganancia por línea
// de venta = precio venta - precio compra) — mismo criterio que V158: gatea solo la PÁGINA nueva
// en frontend_tenant (/reports/profit), el endpoint que consume sigue exigiendo sales.view (el
// dueño real de los datos), sin permiso nuevo en el backend. Ver internal/users/service/
// role_service.go (SeedPermissions) para el catálogo espejo.
type V159ReportsProfitPermission struct{}

func (V159ReportsProfitPermission) Version() int { return 159 }
func (V159ReportsProfitPermission) Name() string { return "reports_profit_permission" }

func (V159ReportsProfitPermission) Up(db *gorm.DB) error {
	if !db.Migrator().HasTable(&database.TenantRole{}) || !db.Migrator().HasTable(&database.TenantPermission{}) {
		return nil
	}

	// 1. Catálogo: crear el permiso si falta.
	var perm database.TenantPermission
	if err := db.Where(database.TenantPermission{Module: "reports", Action: "profit"}).
		Attrs(database.TenantPermission{Label: "Ver reporte de utilidades"}).
		FirstOrCreate(&perm).Error; err != nil {
		return err
	}

	// 2. Espejo: igual que reports.sales_by_product en V158 — cada rol que ya tenía sales.view
	// recibe reports.profit, para no dejar a nadie sin ver el reporte nuevo el día del deploy.
	var salesPerm database.TenantPermission
	if err := db.Where("module = ? AND action = ?", "sales", "view").First(&salesPerm).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil // catálogo viejo tampoco existe (provisioning en curso)
		}
		return err
	}

	var roles []database.TenantRole
	if err := db.Find(&roles).Error; err != nil {
		return err
	}

	for _, role := range roles {
		var hasSales int64
		if err := db.Model(&database.TenantRolePermission{}).
			Where("role_id = ? AND permission_id = ?", role.ID, salesPerm.ID).
			Count(&hasSales).Error; err != nil {
			return err
		}
		if hasSales == 0 {
			continue
		}
		var hasProfit int64
		if err := db.Model(&database.TenantRolePermission{}).
			Where("role_id = ? AND permission_id = ?", role.ID, perm.ID).
			Count(&hasProfit).Error; err != nil {
			return err
		}
		if hasProfit > 0 {
			continue
		}
		if err := db.Create(&database.TenantRolePermission{RoleID: role.ID, PermissionID: perm.ID}).Error; err != nil {
			return err
		}
	}

	return nil
}
