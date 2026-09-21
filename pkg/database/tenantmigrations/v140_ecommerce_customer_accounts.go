package tenantmigrations

import (
	"fmt"
	"time"

	"gorm.io/gorm"
)

type v140CustomerAccount struct {
	ID           uint    `gorm:"primaryKey"`
	Name         string  `gorm:"size:150;not null"`
	Phone        string  `gorm:"size:30;not null;uniqueIndex"`
	Email        *string `gorm:"size:255;uniqueIndex"`
	PasswordHash string  `gorm:"size:255;not null"`
	ContactID    *uint   `gorm:"index"`
	Active       bool    `gorm:"default:true"`
	CreatedAt    time.Time
	UpdatedAt    time.Time
	DeletedAt    gorm.DeletedAt `gorm:"index"`
}

func (v140CustomerAccount) TableName() string { return "tenant_ecommerce_customer_accounts" }

type v140CustomerAddress struct {
	ID                uint `gorm:"primaryKey"`
	CustomerAccountID uint `gorm:"not null;index"`
	Label             string
	AddressLine       string `gorm:"not null"`
	Reference         string
	Ubigeo            string `gorm:"size:6"`
	Phone             string `gorm:"size:30"`
	IsDefault         bool   `gorm:"default:false"`
	CreatedAt         time.Time
	UpdatedAt         time.Time
}

func (v140CustomerAddress) TableName() string { return "tenant_ecommerce_customer_addresses" }

// V140EcommerceCustomerAccounts crea tenant_ecommerce_customer_accounts y
// tenant_ecommerce_customer_addresses (Contrato ecommerce v2 §1.4/§1.5, Fase 4). Identidad de
// LOGIN del comprador final, separada de TenantUser (staff) y de TenantContact (facturación) — ver
// comentario del struct en pkg/database/migrations.go.
type V140EcommerceCustomerAccounts struct{}

func (V140EcommerceCustomerAccounts) Version() int { return 140 }
func (V140EcommerceCustomerAccounts) Name() string { return "ecommerce_customer_accounts" }

func (V140EcommerceCustomerAccounts) Up(db *gorm.DB) error {
	mig := db.Migrator()
	if !mig.HasTable(&v140CustomerAccount{}) {
		if err := mig.CreateTable(&v140CustomerAccount{}); err != nil {
			return fmt.Errorf("tenant_ecommerce_customer_accounts: %w", err)
		}
	}
	if !mig.HasTable(&v140CustomerAddress{}) {
		if err := mig.CreateTable(&v140CustomerAddress{}); err != nil {
			return fmt.Errorf("tenant_ecommerce_customer_addresses: %w", err)
		}
	}
	return nil
}
