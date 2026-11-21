package service

import (
	"errors"
	"strings"

	"tukifac/pkg/database"
)

// Catálogo de bancos y opciones de métodos de pago (Fase D).

// ListBanks devuelve el catálogo de bancos (activos primero, por nombre).
func (s *CashBankService) ListBanks(all bool) ([]database.TenantBank, error) {
	var rows []database.TenantBank
	q := s.db.Order("name ASC")
	if !all {
		q = q.Where("active = ?", true)
	}
	return rows, q.Find(&rows).Error
}

// CreateBank agrega un banco al catálogo (el nombre es único sin distinguir mayúsculas).
func (s *CashBankService) CreateBank(name string) (*database.TenantBank, error) {
	name = strings.ToUpper(strings.TrimSpace(name))
	if name == "" {
		return nil, errors.New("el nombre del banco es requerido")
	}
	var exists int64
	s.db.Model(&database.TenantBank{}).Where("UPPER(name) = ?", name).Count(&exists)
	if exists > 0 {
		return nil, errors.New("ya existe un banco con ese nombre")
	}
	b := &database.TenantBank{Name: name, Active: true}
	return b, s.db.Create(b).Error
}

// UpdateBank renombra o activa/desactiva un banco. El nombre de las cuentas ya enlazadas se actualiza.
func (s *CashBankService) UpdateBank(id uint, name string, active bool) error {
	var b database.TenantBank
	if err := s.db.First(&b, id).Error; err != nil {
		return errors.New("banco no encontrado")
	}
	name = strings.ToUpper(strings.TrimSpace(name))
	if name == "" {
		return errors.New("el nombre del banco es requerido")
	}
	var dup int64
	s.db.Model(&database.TenantBank{}).Where("UPPER(name) = ? AND id <> ?", name, id).Count(&dup)
	if dup > 0 {
		return errors.New("ya existe un banco con ese nombre")
	}
	b.Name, b.Active = name, active
	if err := s.db.Save(&b).Error; err != nil {
		return err
	}
	return s.db.Model(&database.TenantBankAccount{}).Where("bank_id = ?", id).Update("bank_name", name).Error
}

// SetBankAccountBank enlaza una cuenta con un banco del catálogo (bankID nil = sin banco): el nombre del banco queda en bank_name
// para que la impresión y los datos antiguos sigan funcionando.
func (s *CashBankService) SetBankAccountBank(accountID uint, bankID *uint) error {
	if bankID == nil || *bankID == 0 {
		return s.db.Model(&database.TenantBankAccount{}).Where("id = ?", accountID).Update("bank_id", nil).Error
	}
	var b database.TenantBank
	if err := s.db.First(&b, *bankID).Error; err != nil {
		return errors.New("el banco elegido no existe")
	}
	return s.db.Model(&database.TenantBankAccount{}).Where("id = ?", accountID).
		Updates(map[string]interface{}{"bank_id": b.ID, "bank_name": b.Name}).Error
}

// SetPaymentMethodOptions guarda para qué sentido sirve el método (income|expense|both) y si exige referencia.
func (s *CashBankService) SetPaymentMethodOptions(id uint, appliesTo string, requiresReference bool) error {
	appliesTo = strings.ToLower(strings.TrimSpace(appliesTo))
	if appliesTo == "" {
		appliesTo = "both"
	}
	if appliesTo != "income" && appliesTo != "expense" && appliesTo != "both" {
		return errors.New("applies_to debe ser income, expense o both")
	}
	var pm database.TenantPaymentMethod
	if err := s.db.First(&pm, id).Error; err != nil {
		return errors.New("método de pago no encontrado")
	}
	if pm.IsSystem && appliesTo != "both" {
		return errors.New("el método de sistema (Efectivo) sirve para cobros y pagos")
	}
	return s.db.Model(&pm).Updates(map[string]interface{}{"applies_to": appliesTo, "requires_reference": requiresReference}).Error
}

// PaymentRefLine línea de pago a validar: método y la referencia que escribió el cajero.
type PaymentRefLine struct {
	Method    string
	Reference string
}

// ValidatePaymentReferences rechaza una línea cuyo método exige referencia (N° de operación) y no la trae. La validación vive en el
// servidor: antes solo existía en la pantalla.
func (s *CashBankService) ValidatePaymentReferences(lines []PaymentRefLine) error {
	for _, l := range lines {
		if strings.TrimSpace(l.Method) == "" || strings.TrimSpace(l.Reference) != "" {
			continue
		}
		pm, err := s.GetPaymentMethodByCode(l.Method)
		if err != nil || pm == nil {
			continue
		}
		if pm.RequiresReference {
			return errors.New("el método «" + pm.Name + "» exige el N° de operación o referencia")
		}
	}
	return nil
}
