package tenantmigrations

import (
	"fmt"

	"gorm.io/gorm"
)

// V037StaffSchemaRepair corrige nombre de tabla GORM y columnas staff_id faltantes.
type V037StaffSchemaRepair struct{}

func (V037StaffSchemaRepair) Version() int { return 37 }
func (V037StaffSchemaRepair) Name() string { return "restaurant_staff_schema_repair" }

type v037SessionStaff struct {
	StaffID *uint `gorm:"index"`
}

func (v037SessionStaff) TableName() string { return "tenant_table_sessions" }

type v037OrderStaff struct {
	StaffID *uint `gorm:"index"`
}

func (v037OrderStaff) TableName() string { return "tenant_table_orders" }

func (V037StaffSchemaRepair) Up(db *gorm.DB) error {
	mig := db.Migrator()

	// GORM AutoMigrate pudo crear tenant_restaurant_staffs (plural incorrecto).
	if mig.HasTable("tenant_restaurant_staffs") && !mig.HasTable(&v035Staff{}) {
		if err := db.Exec("RENAME TABLE tenant_restaurant_staffs TO tenant_restaurant_staff").Error; err != nil {
			return fmt.Errorf("rename tenant_restaurant_staffs: %w", err)
		}
	} else if mig.HasTable("tenant_restaurant_staffs") {
		_ = db.Exec("DROP TABLE IF EXISTS tenant_restaurant_staffs").Error
	}

	staff := &v035Staff{}
	if !mig.HasTable(staff) {
		if err := mig.CreateTable(staff); err != nil {
			return fmt.Errorf("create tenant_restaurant_staff: %w", err)
		}
	}

	sess := &v037SessionStaff{}
	if mig.HasTable(sess) {
		if !mig.HasColumn(sess, "StaffID") {
			if err := mig.AddColumn(sess, "StaffID"); err != nil {
				return fmt.Errorf("tenant_table_sessions.staff_id: %w", err)
			}
		}
	}

	ord := &v037OrderStaff{}
	if mig.HasTable(ord) {
		if !mig.HasColumn(ord, "StaffID") {
			if err := mig.AddColumn(ord, "StaffID"); err != nil {
				return fmt.Errorf("tenant_table_orders.staff_id: %w", err)
			}
		}
	}

	// Backfill staff desde roles/waiters si la tabla quedó vacía.
	if err := migrateWaitersToStaff(db); err != nil {
		return err
	}
	if err := backfillStaffFromLegacyRolesV036(db); err != nil {
		return err
	}
	if err := remapStaffIDsFromWaiters(db); err != nil {
		return err
	}

	if mig.HasTable("tenant_restaurant_settings") {
		_ = db.Exec("UPDATE tenant_restaurant_settings SET staff_v2_enabled = 1").Error
	}
	return nil
}
