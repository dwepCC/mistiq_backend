package tenantmigrations

import (
	"tukifac/pkg/branchscope"
	"tukifac/pkg/database"

	"gorm.io/gorm"
)

// V198AccountTransfers: cuentas bancarias con saldo en su propia moneda + transferencias entre caja y cuentas.
//   - tenant_bank_movements.account_amount: importe que cada movimiento suma al saldo de su cuenta (en la moneda de la cuenta).
//     Se rellena para lo existente con el equivalente en soles (las cuentas existentes se llevan en soles).
//   - transfer_id en movimientos de caja y banco, y la tabla tenant_account_transfers.
//
// Idempotente: solo toca filas con account_amount NULL; por tramos de id para no bloquear cobros concurrentes.
type V198AccountTransfers struct{}

func (V198AccountTransfers) Version() int { return 198 }
func (V198AccountTransfers) Name() string { return "account_transfers" }

func (V198AccountTransfers) Up(db *gorm.DB) error {
	if err := db.AutoMigrate(&v198BankMovement{}, &v198CashMovement{}, &database.TenantAccountTransfer{}); err != nil {
		return err
	}
	sys := branchscope.System(db, "V198 backfill de importe en moneda de la cuenta")
	var maxID uint64
	if err := sys.Table("tenant_bank_movements").Select("COALESCE(MAX(id), 0)").Scan(&maxID).Error; err != nil {
		return err
	}
	const chunk = 5000
	for lo := uint64(1); lo <= maxID; lo += chunk {
		// Cuentas en soles (o sin moneda / etiquetas históricas distintas de USD): el saldo se llevó en soles.
		if err := sys.Exec(`UPDATE tenant_bank_movements SET account_amount = COALESCE(amount_pen, amount)
			WHERE id BETWEEN ? AND ? AND account_amount IS NULL
			  AND bank_account_id IN (SELECT id FROM tenant_bank_accounts WHERE currency IS NULL OR TRIM(currency) = '' OR UPPER(TRIM(currency)) <> 'USD')
			  AND NOT (currency = 'USD' AND amount_pen IS NULL)`, lo, lo+chunk-1).Error; err != nil {
			return err
		}
		// Cuentas en dólares existentes (no hay en producción): el importe de un movimiento en USD ya está en dólares.
		if err := sys.Exec(`UPDATE tenant_bank_movements SET account_amount = amount
			WHERE id BETWEEN ? AND ? AND account_amount IS NULL AND currency = 'USD'
			  AND bank_account_id IN (SELECT id FROM tenant_bank_accounts WHERE UPPER(TRIM(currency)) = 'USD')`, lo, lo+chunk-1).Error; err != nil {
			return err
		}
	}
	return nil
}
