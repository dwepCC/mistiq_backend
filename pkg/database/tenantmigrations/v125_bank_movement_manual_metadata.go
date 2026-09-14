package tenantmigrations

import (
	"fmt"

	"gorm.io/gorm"
)

type v125BankMovement struct {
	ID       uint   `gorm:"primaryKey"`
	Category string `gorm:"column:category;size:100"`
	Notes    string `gorm:"column:notes;type:text"`
}

func (v125BankMovement) TableName() string { return "tenant_bank_movements" }

// V125BankMovementManualMetadata agrega category y notes (aditivas) a tenant_bank_movements —
// mismo patrón que V122FinancialSessionTraceability.
//
// AddMovement (ingreso/egreso manual de Caja) corrigió su duplicación: un método con cuenta
// bancaria asociada (Yape/Plin/Tarjeta/Transferencia) ya no crea también un TenantCashMovement
// "impostado" solo para cargar category/notes — ahora crea EXCLUSIVAMENTE el TenantBankMovement
// real. Pero ese registro no tenía dónde guardar esos dos datos (antes vivían solo en la fila
// de tenant_cash_movements que se descartó): sin esta columna, un ingreso/egreso manual no
// efectivo perdería su categoría (para mostrarla con la misma etiqueta que uno en efectivo) y
// las notas del usuario quedarían descartadas en silencio.
//
// Aditiva y compatible: ambas columnas nullable/string vacío por defecto, sin tocar filas
// existentes (quedan '' — un movimiento de venta/compra vinculado nunca las usa, solo lee
// Description/Reference como siempre). No cambia ningún cálculo de saldo ni de arqueo.
type V125BankMovementManualMetadata struct{}

func (V125BankMovementManualMetadata) Version() int { return 125 }
func (V125BankMovementManualMetadata) Name() string { return "bank_movement_manual_metadata" }

func (V125BankMovementManualMetadata) Up(db *gorm.DB) error {
	mig := db.Migrator()
	bank := &v125BankMovement{}

	if mig.HasTable(bank) {
		if !mig.HasColumn(bank, "Category") {
			if err := mig.AddColumn(bank, "Category"); err != nil {
				return fmt.Errorf("add tenant_bank_movements.category: %w", err)
			}
		}
		if !mig.HasColumn(bank, "Notes") {
			if err := mig.AddColumn(bank, "Notes"); err != nil {
				return fmt.Errorf("add tenant_bank_movements.notes: %w", err)
			}
		}
	}

	return nil
}
