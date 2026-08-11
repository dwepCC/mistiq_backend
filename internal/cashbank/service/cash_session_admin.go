package service

import (
	"errors"
	"fmt"

	"tukifac/pkg/database"

	"gorm.io/gorm"
)

// SessionUsage qué hay colgando de una sesión de caja.
type SessionUsage struct {
	Movements int64 `json:"movements"`
	Sales     int64 `json:"sales"`
}

// Empty sesión sin nada registrado: se puede borrar sin perder información.
func (u SessionUsage) Empty() bool { return u.Movements == 0 && u.Sales == 0 }

// SessionUsageOf cuenta lo que referencia a la sesión.
//
// Son las dos únicas tablas que apuntan a una caja: los movimientos y las ventas cobradas en
// ella. Si ambas están vacías, la sesión no respalda ningún dato contable.
func (s *CashBankService) SessionUsageOf(sessionID uint) (SessionUsage, error) {
	var usage SessionUsage
	if err := s.db.Model(&database.TenantCashMovement{}).
		Where("cash_session_id = ?", sessionID).
		Count(&usage.Movements).Error; err != nil {
		return usage, err
	}
	if err := s.db.Model(&database.TenantSale{}).
		Where("cash_session_id = ?", sessionID).
		Count(&usage.Sales).Error; err != nil {
		return usage, err
	}
	return usage, nil
}

// DeleteEmptySession borra físicamente una sesión que no registró nada.
//
// Es un borrado real y no lógico a propósito: una caja abierta por error no es historial, es
// ruido en el listado. Solo se permite cuando no hay ni un movimiento ni una venta; si los hay,
// la sesión respalda dinero contado y se cierra, no se borra.
func (s *CashBankService) DeleteEmptySession(sessionID uint) error {
	var session database.TenantCashSession
	if err := s.db.First(&session, sessionID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errors.New("sesión de caja no encontrada")
		}
		return err
	}

	usage, err := s.SessionUsageOf(sessionID)
	if err != nil {
		return err
	}
	if !usage.Empty() {
		return fmt.Errorf(
			"la caja tiene %d movimiento(s) y %d venta(s): no se puede eliminar",
			usage.Movements, usage.Sales,
		)
	}

	// Se vuelve a comprobar dentro del borrado por si entra una venta entremedio: la condición
	// viaja en el WHERE, así que una caja que dejó de estar vacía no se borra.
	res := s.db.Unscoped().
		Where("id = ?", sessionID).
		Where("NOT EXISTS (SELECT 1 FROM tenant_cash_movements m WHERE m.cash_session_id = tenant_cash_sessions.id)").
		Where("NOT EXISTS (SELECT 1 FROM tenant_sales v WHERE v.cash_session_id = tenant_cash_sessions.id)").
		Delete(&database.TenantCashSession{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return errors.New("la caja dejó de estar vacía: recargue el listado")
	}
	return nil
}

// UpdateOpeningBalance corrige el monto con el que se abrió la caja.
//
// El error típico es tipear mal el monto inicial al abrir, y hasta ahora no había forma de
// arreglarlo: quedaba arrastrado en el saldo de todo el turno.
//
// En una caja ya cerrada se recalculan el esperado y la diferencia, porque ambos se derivan del
// monto de apertura (esperado = apertura + ingresos − egresos). Dejarlos sin tocar guardaría un
// arqueo que no cuadra con sus propios números.
func (s *CashBankService) UpdateOpeningBalance(sessionID uint, amount float64) (*database.TenantCashSession, error) {
	if amount < 0 {
		return nil, errors.New("el monto de apertura no puede ser negativo")
	}
	var session database.TenantCashSession
	if err := s.db.First(&session, sessionID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errors.New("sesión de caja no encontrada")
		}
		return nil, err
	}
	if session.OpeningBalance == amount {
		return &session, nil
	}

	updates := map[string]interface{}{"opening_balance": amount}
	if session.Status == "closed" {
		delta := amount - session.OpeningBalance
		if session.ExpectedBalance != nil {
			expected := *session.ExpectedBalance + delta
			updates["expected_balance"] = expected
			if session.ClosingBalance != nil {
				updates["difference"] = *session.ClosingBalance - expected
			}
		}
	}
	if err := s.db.Model(&session).Updates(updates).Error; err != nil {
		return nil, err
	}
	return s.GetSessionByID(sessionID)
}
