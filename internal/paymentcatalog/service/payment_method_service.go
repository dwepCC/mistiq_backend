package service

import (
	"errors"
	"strings"

	"tukifac/pkg/database"
	"tukifac/pkg/paymentmethod"

	"gorm.io/gorm"
)

type PaymentMethodService struct {
	db *gorm.DB
}

func NewPaymentMethodService(db *gorm.DB) *PaymentMethodService {
	return &PaymentMethodService{db: db}
}

func (s *PaymentMethodService) List(activeOnly bool) ([]database.TenantPaymentMethod, error) {
	var list []database.TenantPaymentMethod
	q := s.db.Order("sort_order ASC, id ASC")
	if activeOnly {
		q = q.Where("active = ?", true)
	}
	err := q.Find(&list).Error
	return list, err
}

func (s *PaymentMethodService) GetByID(id uint) (*database.TenantPaymentMethod, error) {
	var pm database.TenantPaymentMethod
	if err := s.db.First(&pm, id).Error; err != nil {
		return nil, err
	}
	return &pm, nil
}

func (s *PaymentMethodService) GetByCode(code string) (*database.TenantPaymentMethod, error) {
	code = strings.TrimSpace(strings.ToLower(code))
	var pm database.TenantPaymentMethod
	if err := s.db.Where("code = ? AND active = ?", code, true).First(&pm).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &pm, nil
}

func (s *PaymentMethodService) Create(name, code string, bankAccountID *uint) (*database.TenantPaymentMethod, error) {
	code = strings.TrimSpace(strings.ToLower(code))
	if name == "" || code == "" {
		return nil, errors.New("nombre y código son requeridos")
	}
	if code == "cash" && bankAccountID != nil && *bankAccountID > 0 {
		return nil, errors.New("efectivo no lleva cuenta bancaria")
	}
	if code != "cash" && (bankAccountID == nil || *bankAccountID == 0) {
		return nil, errors.New("debe indicar cuenta bancaria para este medio")
	}
	var existing database.TenantPaymentMethod
	if err := s.db.Where("code = ?", code).First(&existing).Error; err == nil {
		return nil, errors.New("ya existe un método con ese código")
	}
	var maxOrder int
	s.db.Model(&database.TenantPaymentMethod{}).Select("COALESCE(MAX(sort_order), 0)").Scan(&maxOrder)
	pm := &database.TenantPaymentMethod{
		Name:            name,
		Code:            code,
		DestinationType: paymentmethod.DestinationForCode(code),
		BankAccountID:   bankAccountID,
		SortOrder:       maxOrder + 1,
		Active:          true,
	}
	if code == "cash" {
		pm.IsSystem = true
	}
	return pm, s.db.Create(pm).Error
}

func (s *PaymentMethodService) Update(id uint, name, code string, bankAccountID *uint, active bool) error {
	var pm database.TenantPaymentMethod
	if err := s.db.First(&pm, id).Error; err != nil {
		return err
	}
	if pm.IsSystem {
		if code != "" && strings.ToLower(code) != pm.Code {
			return errors.New("no se puede cambiar el código de efectivo")
		}
	}
	if name != "" {
		pm.Name = name
	}
	if code != "" && !pm.IsSystem {
		pm.Code = strings.ToLower(strings.TrimSpace(code))
		pm.DestinationType = paymentmethod.DestinationForCode(pm.Code)
	}
	if pm.Code != "cash" {
		if bankAccountID != nil {
			pm.BankAccountID = bankAccountID
		}
	} else {
		pm.BankAccountID = nil
	}
	pm.Active = active
	return s.db.Save(&pm).Error
}

func (s *PaymentMethodService) Delete(id uint) error {
	var pm database.TenantPaymentMethod
	if err := s.db.First(&pm, id).Error; err != nil {
		return err
	}
	if pm.IsSystem {
		return errors.New("no se puede eliminar un método del sistema")
	}
	return s.db.Delete(&pm).Error
}

// DestinationType devuelve cash | bank_account para tesorería.
func DestinationType(pm *database.TenantPaymentMethod) string {
	if pm == nil {
		return "cash"
	}
	if strings.TrimSpace(pm.DestinationType) != "" {
		return pm.DestinationType
	}
	return paymentmethod.DestinationForCode(pm.Code)
}
