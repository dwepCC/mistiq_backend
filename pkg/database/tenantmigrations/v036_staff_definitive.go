package tenantmigrations

import (
	"fmt"
	"strings"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

// V036StaffDefinitive migra waiters → staff, staff_id en sesiones, activa staff v2.
type V036StaffDefinitive struct{}

func (V036StaffDefinitive) Version() int { return 36 }
func (V036StaffDefinitive) Name() string { return "restaurant_staff_definitive" }

type v036SessionStaff struct {
	StaffID *uint `gorm:"index"`
}

func (v036SessionStaff) TableName() string { return "tenant_table_sessions" }

type v036OrderStaff struct {
	StaffID *uint `gorm:"index"`
}

func (v036OrderStaff) TableName() string { return "tenant_table_orders" }

func (V036StaffDefinitive) Up(db *gorm.DB) error {
	mig := db.Migrator()
	sess := &v036SessionStaff{}
	if mig.HasTable(sess) && !mig.HasColumn(sess, "StaffID") {
		if err := mig.AddColumn(sess, "StaffID"); err != nil {
			return fmt.Errorf("tenant_table_sessions.staff_id: %w", err)
		}
	}
	ord := &v036OrderStaff{}
	if mig.HasTable(ord) && !mig.HasColumn(ord, "StaffID") {
		if err := mig.AddColumn(ord, "StaffID"); err != nil {
			return fmt.Errorf("tenant_table_orders.staff_id: %w", err)
		}
	}
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

type v036Waiter struct {
	ID     uint
	UserID *uint
	Name   string
	Code   string
	Active bool
}

func (v036Waiter) TableName() string { return "tenant_waiters" }

func migrateWaitersToStaff(db *gorm.DB) error {
	if !db.Migrator().HasTable(&v036Waiter{}) || !db.Migrator().HasTable(&v035Staff{}) {
		return nil
	}
	var waiters []v036Waiter
	if err := db.Find(&waiters).Error; err != nil {
		return err
	}
	for _, w := range waiters {
		var n int64
		db.Model(&v035Staff{}).Where("legacy_waiter_id = ?", w.ID).Count(&n)
		if n > 0 {
			continue
		}
		userID := uint(0)
		if w.UserID != nil && *w.UserID > 0 {
			userID = *w.UserID
		} else {
			uid, err := ensureInternalUserForWaiter(db, w)
			if err != nil {
				return err
			}
			userID = uid
		}
		var existing int64
		db.Model(&v035Staff{}).Where("user_id = ?", userID).Count(&existing)
		if existing > 0 {
			_ = db.Model(&v035Staff{}).Where("user_id = ?", userID).Updates(map[string]interface{}{
				"legacy_waiter_id": w.ID,
				"display_name":     w.Name,
				"staff_code":       w.Code,
				"employee_type":    "waiter",
				"is_active":        w.Active,
			}).Error
			continue
		}
		lid := w.ID
		row := v035Staff{
			UserID: userID, EmployeeType: "waiter", StaffCode: w.Code,
			DisplayName: w.Name, IsActive: w.Active, CanOpenTable: true,
			LegacyWaiterID: &lid,
		}
		if err := db.Create(&row).Error; err != nil {
			return err
		}
	}
	return nil
}

func ensureInternalUserForWaiter(db *gorm.DB, w v036Waiter) (uint, error) {
	email := fmt.Sprintf("waiter-%d@internal.tukichef", w.ID)
	type tu struct {
		ID uint
	}
	var u tu
	if err := db.Table("tenant_users").Where("email = ?", email).First(&u).Error; err == nil {
		return u.ID, nil
	}
	var roleID uint
	db.Table("tenant_roles").Select("id").Order("id ASC").Limit(1).Scan(&roleID)
	if roleID == 0 {
		roleID = 1
	}
	hash, _ := bcrypt.GenerateFromPassword([]byte("changeme-internal"), bcrypt.DefaultCost)
	res := db.Exec(`INSERT INTO tenant_users (name, email, password, role_id, active, created_at, updated_at)
		VALUES (?, ?, ?, ?, 1, NOW(), NOW())`, w.Name, email, string(hash), roleID)
	if res.Error != nil {
		return 0, res.Error
	}
	var id uint
	db.Table("tenant_users").Where("email = ?", email).Select("id").Scan(&id)
	return id, nil
}

func backfillStaffFromLegacyRolesV036(db *gorm.DB) error {
	if !db.Migrator().HasTable(&v035Staff{}) {
		return nil
	}
	type legacyRole struct {
		UserID uint
		Role   string
	}
	var roles []legacyRole
	if err := db.Table("tenant_user_restaurant_roles").Find(&roles).Error; err != nil {
		return nil
	}
	roleToType := map[string]string{
		"admin": "admin", "vendedor": "cashier", "mozo": "waiter", "cocinero": "cook",
	}
	for _, r := range roles {
		et, ok := roleToType[strings.TrimSpace(r.Role)]
		if !ok {
			continue
		}
		var n int64
		db.Model(&v035Staff{}).Where("user_id = ?", r.UserID).Count(&n)
		if n > 0 {
			_ = db.Model(&v035Staff{}).Where("user_id = ?", r.UserID).Update("employee_type", et).Error
			continue
		}
		canCharge := et == "cashier" || et == "admin"
		kitchen := et == "cook" || et == "admin"
		row := v035Staff{
			UserID: r.UserID, EmployeeType: et, IsActive: true,
			CanCharge: canCharge, CanOpenTable: true, KitchenAccess: kitchen,
		}
		if err := db.Create(&row).Error; err != nil {
			return err
		}
	}
	return nil
}

func remapStaffIDsFromWaiters(db *gorm.DB) error {
	sessCol := &v036SessionStaff{}
	if !db.Migrator().HasTable(sessCol) || !db.Migrator().HasColumn(sessCol, "StaffID") {
		return nil
	}
	// sessions.waiter_id → staff_id via legacy_waiter_id
	_ = db.Exec(`
		UPDATE tenant_table_sessions s
		INNER JOIN tenant_restaurant_staff st ON st.legacy_waiter_id = s.waiter_id
		SET s.staff_id = st.id
		WHERE s.waiter_id IS NOT NULL AND (s.staff_id IS NULL OR s.staff_id = 0)
	`).Error
	_ = db.Exec(`
		UPDATE tenant_table_orders o
		INNER JOIN tenant_restaurant_staff st ON st.legacy_waiter_id = o.waiter_id
		SET o.staff_id = st.id
		WHERE o.waiter_id IS NOT NULL AND (o.staff_id IS NULL OR o.staff_id = 0)
	`).Error
	// pedidos sin waiter: heredar staff de la sesión
	_ = db.Exec(`
		UPDATE tenant_table_orders o
		INNER JOIN tenant_table_sessions s ON s.id = o.session_id
		SET o.staff_id = s.staff_id
		WHERE (o.staff_id IS NULL OR o.staff_id = 0) AND s.staff_id IS NOT NULL
	`).Error
	return nil
}
