package tenantmigrations

import (
	"fmt"

	"gorm.io/gorm"
)

// V059UserBranches tabla N:N usuario ↔ sucursal + backfill desde home_branch_id.
type V059UserBranches struct{}

func (V059UserBranches) Version() int { return 59 }
func (V059UserBranches) Name() string { return "user_branches" }

type v059UserBranch struct {
	UserID   uint `gorm:"primaryKey"`
	BranchID uint `gorm:"primaryKey;index"`
}

func (v059UserBranch) TableName() string { return "tenant_user_branches" }

func v059HasHomeBranchID(db *gorm.DB) bool {
	return db.Migrator().HasColumn(&v059User{HomeBranchID: nil}, "HomeBranchID")
}

type v059User struct {
	ID           uint
	HomeBranchID *uint `gorm:"column:home_branch_id"`
	BranchID     *uint `gorm:"column:branch_id"`
}

func (v059User) TableName() string { return "tenant_users" }

func (V059UserBranches) Up(db *gorm.DB) error {
	mig := db.Migrator()
	st := &v059UserBranch{}
	if !mig.HasTable(st) {
		if err := mig.CreateTable(st); err != nil {
			return fmt.Errorf("create tenant_user_branches: %w", err)
		}
	}

	if !v059HasHomeBranchID(db) {
		return nil
	}

	var users []struct {
		ID           uint
		HomeBranchID *uint
		BranchID     *uint
	}
	if err := db.Table("tenant_users").
		Select("id, home_branch_id, branch_id").
		Find(&users).Error; err != nil {
		return err
	}

	for _, u := range users {
		var n int64
		db.Model(st).Where("user_id = ?", u.ID).Count(&n)
		if n > 0 {
			continue
		}
		bid := uint(0)
		if u.HomeBranchID != nil && *u.HomeBranchID > 0 {
			bid = *u.HomeBranchID
		} else if u.BranchID != nil && *u.BranchID > 0 {
			bid = *u.BranchID
		}
		if bid == 0 {
			continue
		}
		var active int64
		db.Table("tenant_branches").Where("id = ? AND active = ?", bid, true).Count(&active)
		if active == 0 {
			continue
		}
		if err := db.Create(&v059UserBranch{UserID: u.ID, BranchID: bid}).Error; err != nil {
			return fmt.Errorf("backfill user %d branch %d: %w", u.ID, bid, err)
		}
	}

	return nil
}
