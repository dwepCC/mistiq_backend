package service

import (
	"errors"
	"strings"
	"time"

	"tukifac/pkg/database"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// isDuplicateCounterRowError detecta la violación del índice único ux_branch_daily_comanda_counter
// (mismo patrón que isDuplicateOpenSessionError en table_session_sync.go).
func isDuplicateCounterRowError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "duplicate") ||
		strings.Contains(msg, "1062") ||
		strings.Contains(msg, "ux_branch_daily_comanda_counter")
}

// reserveDailyComandaNumber asigna el próximo "Pedido #N" de la sucursal para el día de negocio
// actual (hora local) y lo incrementa de forma atómica. Debe llamarse dentro de una transacción
// abierta (tx), igual que docseries.ReserveNext.
//
// A diferencia del MAX(order_number)+1 por sesión de mesa que reemplaza, este contador es
// independiente de la mesa: una fila por sucursal+día, bloqueada con SELECT...FOR UPDATE. Esto
// importa porque el lock de AddOrder es sobre la fila de TenantTableSession (una mesa
// específica) — dos mesas distintas pidiendo al mismo instante no compiten por ese lock, así que
// un MAX() recalculado sobre toda la sucursal sí podría duplicar números bajo concurrencia real
// (varias mesas pidiendo a la vez es el caso normal de un restaurante).
func reserveDailyComandaNumber(tx *gorm.DB, branchID uint, at time.Time) (int, error) {
	day := at.Format("20060102")

	var counter database.TenantBranchDailyComandaCounter
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("branch_id = ? AND business_date = ?", branchID, day).
		First(&counter).Error

	if errors.Is(err, gorm.ErrRecordNotFound) {
		counter = database.TenantBranchDailyComandaCounter{BranchID: branchID, BusinessDate: day, LastNumber: 0}
		if createErr := tx.Create(&counter).Error; createErr != nil {
			if !isDuplicateCounterRowError(createErr) {
				return 0, createErr
			}
			// Carrera: otra transacción concurrente ya insertó la fila del día para esta
			// sucursal (su INSERT bloqueó el nuestro hasta que confirmó). Reintentar el lock,
			// ahora sí encuentra la fila.
			if lockErr := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
				Where("branch_id = ? AND business_date = ?", branchID, day).
				First(&counter).Error; lockErr != nil {
				return 0, lockErr
			}
		}
	} else if err != nil {
		return 0, err
	}

	next := counter.LastNumber + 1
	if err := tx.Model(&counter).Update("last_number", next).Error; err != nil {
		return 0, err
	}
	return next, nil
}
