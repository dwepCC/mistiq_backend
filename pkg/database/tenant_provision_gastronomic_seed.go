package database

import (
	"errors"
	"fmt"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

// DefaultGastronomicAdminPIN PIN inicial del administrador Tukichef al crear tenant gastronómico.
const DefaultGastronomicAdminPIN = "7410"

var defaultPeruDeliveryCompanies = []string{
	"PedidosYa",
	"Rappi",
	"Uber Eats",
	"Chazki",
	"Ya Vámonos",
}

// seedGastronomicDefaults crea piso, mesas y empresas de delivery para tenants gastronómicos.
func seedGastronomicDefaults(tx *gorm.DB, branchID uint) error {
	var floorCount int64
	if err := tx.Model(&TenantRestaurantFloor{}).Count(&floorCount).Error; err != nil {
		return err
	}
	if floorCount == 0 {
		floor := TenantRestaurantFloor{
			BranchID:  branchID,
			Name:      "Piso 1",
			SortOrder: 1,
			Active:    true,
		}
		if err := tx.Create(&floor).Error; err != nil {
			return fmt.Errorf("piso restaurante: %w", err)
		}
		tables := make([]TenantRestaurantTable, 0, 10)
		for i := 1; i <= 10; i++ {
			tables = append(tables, TenantRestaurantTable{
				BranchID: branchID,
				FloorID:  floor.ID,
				Name:     fmt.Sprintf("M%d", i),
				Capacity: 4,
				Status:   "libre",
				Active:   true,
			})
		}
		if err := tx.Create(&tables).Error; err != nil {
			return fmt.Errorf("mesas restaurante: %w", err)
		}
	}

	var companyCount int64
	if err := tx.Model(&TenantDeliveryCompany{}).Count(&companyCount).Error; err != nil {
		return err
	}
	if companyCount == 0 {
		companies := make([]TenantDeliveryCompany, 0, len(defaultPeruDeliveryCompanies))
		for i, name := range defaultPeruDeliveryCompanies {
			companies = append(companies, TenantDeliveryCompany{
				Name:      name,
				SortOrder: i + 1,
				Active:    true,
			})
		}
		if err := tx.Create(&companies).Error; err != nil {
			return fmt.Errorf("empresas delivery: %w", err)
		}
	}

	var settingsCount int64
	if err := tx.Model(&TenantRestaurantSetting{}).Count(&settingsCount).Error; err != nil {
		return err
	}
	if settingsCount == 0 {
		if err := tx.Create(&TenantRestaurantSetting{
			StaffV2Enabled:   true,
			PermCacheVersion: 1,
		}).Error; err != nil {
			return fmt.Errorf("config restaurante: %w", err)
		}
	}
	return nil
}

// seedGastronomicAdminStaff vincula al usuario admin del tenant con perfil operativo admin + PIN por defecto.
func seedGastronomicAdminStaff(tx *gorm.DB, adminUserID uint) error {
	var existing TenantRestaurantStaff
	err := tx.Where("user_id = ?", adminUserID).First(&existing).Error
	if err == nil {
		return nil
	}
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}

	pinHash, err := bcrypt.GenerateFromPassword([]byte(DefaultGastronomicAdminPIN), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hash PIN admin restaurante: %w", err)
	}

	staff := TenantRestaurantStaff{
		UserID:         adminUserID,
		EmployeeType:   "admin",
		DisplayName:    "Administrador",
		IsActive:       true,
		CanCharge:      true,
		CanOpenTable:   true,
		KitchenAccess:  true,
		DeliveryAccess: true,
		PinHash:        string(pinHash),
	}
	if err := tx.Create(&staff).Error; err != nil {
		return fmt.Errorf("perfil admin restaurante: %w", err)
	}
	return nil
}
