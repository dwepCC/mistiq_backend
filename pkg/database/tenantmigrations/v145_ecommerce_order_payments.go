package tenantmigrations

import (
	"fmt"
	"time"

	"gorm.io/gorm"
)

// v145Payment espejo local de database.TenantEcommerceOrderPayment; v145Settings de las columnas
// nuevas de tenant_ecommerce_settings (mismo criterio que v142/v144).
type v145Payment struct {
	ID             uint    `gorm:"primaryKey"`
	OrderID        uint    `gorm:"not null;index"`
	Amount         float64 `gorm:"type:decimal(15,2);not null"`
	Method         string  `gorm:"size:50;not null"`
	Reference      string  `gorm:"size:100"`
	Note           string  `gorm:"size:255"`
	VoucherURL     string  `gorm:"size:255"`
	PaidAt         time.Time
	Status         string `gorm:"size:12;not null;default:'PENDIENTE';index"`
	ObservedReason string `gorm:"size:255"`
	CreatedBy      *uint
	ReviewedBy     *uint
	ReviewedAt     *time.Time
	CreatedAt      time.Time
	UpdatedAt      time.Time
	DeletedAt      gorm.DeletedAt `gorm:"index"`
}

func (v145Payment) TableName() string { return "tenant_ecommerce_order_payments" }

type v145Settings struct {
	ID                       uint   `gorm:"primaryKey"`
	RequirePaymentValidation bool   `gorm:"column:require_payment_validation;default:false"`
	EmitSaleOnDispatch       bool   `gorm:"column:emit_sale_on_dispatch;default:false"`
	EmitSaleTarget           string `gorm:"column:emit_sale_target;size:12;default:'nota_venta'"`
}

func (v145Settings) TableName() string { return "tenant_ecommerce_settings" }

// V145EcommerceOrderPayments abonos de pedidos web (varios pagos por pedido, con validación opcional)
// y los ajustes que los gobiernan: validar antes de preparar y emitir la venta al despachar.
type V145EcommerceOrderPayments struct{}

func (V145EcommerceOrderPayments) Version() int { return 145 }
func (V145EcommerceOrderPayments) Name() string { return "ecommerce_order_payments" }

func (V145EcommerceOrderPayments) Up(db *gorm.DB) error {
	mig := db.Migrator()
	if !mig.HasTable(&v145Payment{}) {
		if err := mig.CreateTable(&v145Payment{}); err != nil {
			return fmt.Errorf("tenant_ecommerce_order_payments: %w", err)
		}
	}
	if mig.HasTable(&v145Settings{}) {
		for _, col := range []string{"RequirePaymentValidation", "EmitSaleOnDispatch", "EmitSaleTarget"} {
			if mig.HasColumn(&v145Settings{}, col) {
				continue
			}
			if err := mig.AddColumn(&v145Settings{}, col); err != nil {
				return fmt.Errorf("tenant_ecommerce_settings.%s: %w", col, err)
			}
		}
	}
	return nil
}
