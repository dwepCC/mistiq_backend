package service

import (
	"strings"
	"sync"
	"time"

	"tukifac/pkg/database"

	"golang.org/x/crypto/bcrypt"
)

const (
	pinMaxFails = 5
	pinLockTime = 5 * time.Minute
)

type pinAttempts struct {
	fails int
	until time.Time
}

var (
	pinMu      sync.Mutex
	pinTracker = map[uint]*pinAttempts{}
)

func validPinFormat(pin string) error {
	if len(pin) < 4 || len(pin) > 6 {
		return invalid("el PIN debe tener entre 4 y 6 dígitos")
	}
	if !onlyDigits(pin) {
		return invalid("el PIN solo puede contener dígitos")
	}
	return nil
}

// HasSecurityPin indica si ya hay un PIN de seguridad configurado para el módulo.
func (s *Service) HasSecurityPin() (bool, error) {
	st, err := s.GetSettings()
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(st.SecurityPinHash) != "", nil
}

// SetSecurityPin crea o cambia el PIN. Si ya existe, exige el PIN actual.
func (s *Service) SetSecurityPin(newPin, currentPin string, userID uint) error {
	newPin = strings.TrimSpace(newPin)
	if err := validPinFormat(newPin); err != nil {
		return err
	}
	st, err := s.GetSettings()
	if err != nil {
		return err
	}
	if strings.TrimSpace(st.SecurityPinHash) != "" {
		if err := s.VerifyPin(currentPin, userID); err != nil {
			return err
		}
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(newPin), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	return s.db.Model(&database.EquipSettings{}).Where("id = ?", st.ID).Update("security_pin_hash", string(hash)).Error
}

// VerifyPin valida el PIN de seguridad antes de una acción sensible. Tras 5 fallos seguidos bloquea al usuario 5 minutos.
func (s *Service) VerifyPin(pin string, userID uint) error {
	pin = strings.TrimSpace(pin)
	st, err := s.GetSettings()
	if err != nil {
		return err
	}
	hash := strings.TrimSpace(st.SecurityPinHash)
	if hash == "" {
		return invalid("configura primero el PIN de seguridad en Equipos → Configuración")
	}
	if pin == "" {
		return invalid("ingresa el PIN de seguridad")
	}
	pinMu.Lock()
	defer pinMu.Unlock()
	a := pinTracker[userID]
	if a == nil {
		a = &pinAttempts{}
		pinTracker[userID] = a
	}
	if time.Now().Before(a.until) {
		return invalid("demasiados intentos fallidos: espera %d min para volver a intentar", int(time.Until(a.until).Minutes())+1)
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(pin)) != nil {
		a.fails++
		if a.fails >= pinMaxFails {
			a.fails = 0
			a.until = time.Now().Add(pinLockTime)
			return invalid("PIN incorrecto: se bloqueó por %d minutos", int(pinLockTime.Minutes()))
		}
		return invalid("PIN de seguridad incorrecto (%d intentos restantes)", pinMaxFails-a.fails)
	}
	a.fails = 0
	return nil
}

// OrderStatus estado actual del pedido ("" si no existe).
func (s *Service) OrderStatus(id uint) string {
	var o struct{ Status string }
	_ = s.db.Model(&database.EquipOrder{}).Select("status").Where("id = ?", id).Scan(&o).Error
	return o.Status
}
