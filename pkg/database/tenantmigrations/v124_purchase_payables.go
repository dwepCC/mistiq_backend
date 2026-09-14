package tenantmigrations

import (
	"fmt"
	"time"

	"gorm.io/gorm"
)

type v124PurchasePayable struct {
	ID             uint      `gorm:"primaryKey"`
	PurchaseID     uint      `gorm:"not null;uniqueIndex"`
	OriginalAmount float64   `gorm:"type:decimal(15,2);not null"`
	PaidAmount     float64   `gorm:"type:decimal(15,2);default:0"`
	Status         string    `gorm:"size:20;default:pending"`
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

func (v124PurchasePayable) TableName() string { return "tenant_purchase_payables" }

type v124PurchasePayment struct {
	ID            uint    `gorm:"primaryKey"`
	PurchaseID    uint    `gorm:"not null;index"`
	Method        string  `gorm:"size:50;not null"`
	Amount        float64 `gorm:"type:decimal(15,2);not null"`
	Reference     string  `gorm:"size:100"`
	Notes         string  `gorm:"size:255"`
	CashSessionID *uint   `gorm:"index"`
	CreatedAt     time.Time
}

func (v124PurchasePayment) TableName() string { return "tenant_purchase_payments" }

// V124PurchasePayables — Fase 2: CxP (cuentas por pagar). Crea dos tablas nuevas, mismo patrón
// que V092SaleCreditInstallments (crear tabla) — ninguna migración de columna sobre tablas
// existentes, así que no hay riesgo de tocar datos de tenant_purchases.
//
//   - tenant_purchase_payables: una fila por compra a crédito (PaymentMethod=""), con el saldo
//     corriente (original/pagado/estado). Análoga a tenant_sale_credit_installments, pero sin
//     cronograma de cuotas — las compras no tienen ese concepto hoy (una sola DueDate en la
//     propia compra, ya reutilizada, no duplicada aquí).
//   - tenant_purchase_payments: una fila por pago a proveedor contra una compra, con su propia
//     cash_session_id (la Caja donde ocurrió ESE pago) — análoga a tenant_sale_payments.
//
// Aditivas, sin tocar ninguna tabla existente. No se ejecuta ningún backfill de compras a
// crédito históricas: no hay evidencia inequívoca para reconstruir cuánto se pagó de cada una
// antes de esta fase (mismo criterio ya aplicado en V123 para tenant_sale_payments).
type V124PurchasePayables struct{}

func (V124PurchasePayables) Version() int { return 124 }
func (V124PurchasePayables) Name() string { return "purchase_payables" }

func (V124PurchasePayables) Up(db *gorm.DB) error {
	mig := db.Migrator()

	if !mig.HasTable(&v124PurchasePayable{}) {
		if err := mig.CreateTable(&v124PurchasePayable{}); err != nil {
			return fmt.Errorf("create tenant_purchase_payables: %w", err)
		}
	}
	if !mig.HasTable(&v124PurchasePayment{}) {
		if err := mig.CreateTable(&v124PurchasePayment{}); err != nil {
			return fmt.Errorf("create tenant_purchase_payments: %w", err)
		}
	}

	return nil
}
