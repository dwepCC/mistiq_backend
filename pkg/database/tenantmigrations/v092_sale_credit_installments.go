package tenantmigrations

import (
	"fmt"
	"time"

	"gorm.io/gorm"
)

type v092Sale struct {
	PaymentConditionCode string `gorm:"column:payment_condition_code;size:20;default:cash;index"`
}

func (v092Sale) TableName() string { return "tenant_sales" }

type v092CreditInstallment struct {
	ID            uint      `gorm:"primaryKey"`
	SaleID        uint      `gorm:"not null;index"`
	InstallmentNo int       `gorm:"not null"`
	DueDate       time.Time `gorm:"not null;index"`
	Amount        float64   `gorm:"type:decimal(15,2);not null"`
	Currency      string    `gorm:"size:10;default:PEN"`
	Status        string    `gorm:"size:20;default:pending"`
	PaidAmount    float64   `gorm:"type:decimal(15,2);default:0"`
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

func (v092CreditInstallment) TableName() string { return "tenant_sale_credit_installments" }

// V092SaleCreditInstallments condición de pago en venta y cuotas a crédito.
type V092SaleCreditInstallments struct{}

func (V092SaleCreditInstallments) Version() int { return 92 }
func (V092SaleCreditInstallments) Name() string { return "sale_credit_installments" }

func (V092SaleCreditInstallments) Up(db *gorm.DB) error {
	mig := db.Migrator()
	stSale := &v092Sale{}
	if !mig.HasColumn(stSale, "PaymentConditionCode") {
		if err := mig.AddColumn(stSale, "PaymentConditionCode"); err != nil {
			return fmt.Errorf("add tenant_sales.payment_condition_code: %w", err)
		}
	}
	if !mig.HasTable(&v092CreditInstallment{}) {
		if err := mig.CreateTable(&v092CreditInstallment{}); err != nil {
			return fmt.Errorf("create tenant_sale_credit_installments: %w", err)
		}
	}
	return nil
}
