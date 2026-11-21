package tenantmigrations

import (
	"fmt"
	"log/slog"

	"tukifac/pkg/branchscope"
	"tukifac/pkg/database"

	"gorm.io/gorm"
)

// V194SingleMainBranchAndOrphans deja al tenant con EXACTAMENTE UNA sucursal principal y vincula a ella los registros huérfanos.
//
//  1. Sucursal principal única: si hay varias marcadas `is_main`, se conserva la primera (menor id, preferiblemente activa) y
//     las demás dejan de serlo; si no hay ninguna, la primera sucursal activa pasa a ser la principal. (Producción: `doriconta`
//     tenía dos principales; la causa era CompanyService.CreateBranch, que intentaba quitar la marca con un UPDATE sin WHERE
//     —rechazado por GORM— y no revisaba el error.)
//  2. Registros huérfanos: toda fila con `branch_id = 0` o NULL en las entidades con sucursal propia se vincula a la sucursal
//     principal (decisión del dueño, 10-nov-2026; hoy son 5 comprobantes de retención en 3 tenants). Solo toca filas huérfanas.
//     Si una fila no se puede vincular por chocar con un índice único (p. ej. ya existe el mismo registro en la principal), se
//     omite y se deja constancia en el log para revisión manual: la migración nunca falla por eso ni borra datos.
//
// Idempotente. Cumple la regla "aditiva": no borra ni renombra nada.
type V194SingleMainBranchAndOrphans struct{}

func (V194SingleMainBranchAndOrphans) Version() int { return 194 }
func (V194SingleMainBranchAndOrphans) Name() string { return "single_main_branch_and_orphans" }

func (V194SingleMainBranchAndOrphans) Up(db *gorm.DB) error {
	if !db.Migrator().HasTable(&database.TenantBranch{}) {
		return nil
	}
	return db.Transaction(func(tx *gorm.DB) error {
		mainID, err := ensureSingleMainBranch(tx)
		if err != nil || mainID == 0 {
			return err
		}
		return linkOrphansToBranch(tx, mainID)
	})
}

// ensureSingleMainBranch normaliza `is_main` y devuelve el id de la sucursal principal (0 si el tenant no tiene sucursales).
func ensureSingleMainBranch(tx *gorm.DB) (uint, error) {
	var mains []database.TenantBranch
	if err := tx.Where("is_main = ?", true).Order("id ASC").Find(&mains).Error; err != nil {
		return 0, err
	}
	var keep uint
	switch {
	case len(mains) == 0:
		var first database.TenantBranch
		if err := tx.Where("active = ?", true).Order("id ASC").First(&first).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				return 0, nil
			}
			return 0, err
		}
		keep = first.ID
	default:
		keep = mains[0].ID
		for _, m := range mains {
			if m.Active {
				keep = m.ID
				break
			}
		}
	}
	if err := tx.Model(&database.TenantBranch{}).Where("id != ? AND is_main = ?", keep, true).Update("is_main", false).Error; err != nil {
		return 0, err
	}
	if err := tx.Model(&database.TenantBranch{}).Where("id = ?", keep).Update("is_main", true).Error; err != nil {
		return 0, err
	}
	return keep, nil
}

func linkOrphansToBranch(tx *gorm.DB, mainID uint) error {
	// Proceso interno de migración sobre todo el tenant: acceso declarado (sin ruido de auditoría del scope de sucursal).
	tx = branchscope.System(tx, "migración V194: vincular huérfanos a la sucursal principal")
	ignore := "UPDATE IGNORE"
	if tx.Dialector.Name() == "sqlite" {
		ignore = "UPDATE OR IGNORE"
	}
	for _, m := range database.BranchScopedModels() {
		stmt := &gorm.Statement{DB: tx}
		if err := stmt.Parse(m); err != nil {
			return err
		}
		table := stmt.Schema.Table
		if !tx.Migrator().HasTable(table) || !tx.Migrator().HasColumn(m, "BranchID") {
			continue
		}
		res := tx.Exec(fmt.Sprintf("%s `%s` SET branch_id = ? WHERE branch_id = 0 OR branch_id IS NULL", ignore, table), mainID)
		if res.Error != nil {
			return fmt.Errorf("vinculando huérfanos de %s a la sucursal principal: %w", table, res.Error)
		}
		var left int64
		if err := tx.Table(table).Where("branch_id = 0 OR branch_id IS NULL").Count(&left).Error; err == nil && left > 0 {
			slog.Warn("V194: registros sin sucursal que no se pudieron vincular (índice único); revisar a mano",
				slog.String("table", table), slog.Int64("rows", left))
		}
	}
	return nil
}
