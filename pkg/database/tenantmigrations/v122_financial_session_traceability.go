package tenantmigrations

import (
	"fmt"

	"gorm.io/gorm"
)

type v122BankMovement struct {
	ID            uint  `gorm:"primaryKey"`
	CashSessionID *uint `gorm:"column:cash_session_id;index"`
}

func (v122BankMovement) TableName() string { return "tenant_bank_movements" }

type v122Purchase struct {
	ID            uint  `gorm:"primaryKey"`
	CashSessionID *uint `gorm:"column:cash_session_id;index"`
}

func (v122Purchase) TableName() string { return "tenant_purchases" }

// V122FinancialSessionTraceability agrega cash_session_id (nullable) a tenant_bank_movements y
// tenant_purchases — mismo patrón que V081FinancialMovementReversal (dos tablas, una columna
// aditiva cada una).
//
// tenant_sales ya tiene cash_session_id desde antes, sin importar el método de pago. Pero un
// pago no efectivo (Yape/Plin/transferencia/tarjeta) crea un TenantBankMovement, que hasta esta
// migración no tenía forma directa de saber a qué sesión pertenecía (solo indirecta, vía
// sale_id/purchase_id → tabla de origen). Y tenant_purchases no tenía sesión en absoluto: una
// compra quedaba huérfana de cualquier turno, sin importar el método de pago.
//
// Aditiva y compatible: ambas columnas nullable, sin DROP, sin tocar datos existentes (quedan
// NULL). No cambia ningún cálculo de saldo ni de arqueo — solo habilita la trazabilidad para que
// las fases siguientes (poblarla en RecordPayment/purchase_service) puedan apoyarse en ella.
type V122FinancialSessionTraceability struct{}

func (V122FinancialSessionTraceability) Version() int { return 122 }
func (V122FinancialSessionTraceability) Name() string { return "financial_session_traceability" }

func (V122FinancialSessionTraceability) Up(db *gorm.DB) error {
	mig := db.Migrator()

	bank := &v122BankMovement{}
	if mig.HasTable(bank) && !mig.HasColumn(bank, "CashSessionID") {
		if err := mig.AddColumn(bank, "CashSessionID"); err != nil {
			return fmt.Errorf("add tenant_bank_movements.cash_session_id: %w", err)
		}
	}

	purchase := &v122Purchase{}
	if mig.HasTable(purchase) && !mig.HasColumn(purchase, "CashSessionID") {
		if err := mig.AddColumn(purchase, "CashSessionID"); err != nil {
			return fmt.Errorf("add tenant_purchases.cash_session_id: %w", err)
		}
	}

	return nil
}
