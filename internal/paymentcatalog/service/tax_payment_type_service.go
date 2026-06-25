package service

import (
	"errors"

	"tukifac/pkg/database"

	"gorm.io/gorm"
)

type TaxPaymentTypeService struct {
	db *gorm.DB
}

func NewTaxPaymentTypeService(db *gorm.DB) *TaxPaymentTypeService {
	return &TaxPaymentTypeService{db: db}
}

func (s *TaxPaymentTypeService) List(activeOnly bool) ([]database.TenantTaxPaymentType, error) {
	var list []database.TenantTaxPaymentType
	q := s.db.Order("id ASC")
	if activeOnly {
		q = q.Where("active = ?", true)
	}
	err := q.Find(&list).Error
	return list, err
}

func (s *TaxPaymentTypeService) GetByCode(code string) (*database.TenantTaxPaymentType, error) {
	var row database.TenantTaxPaymentType
	if err := s.db.Where("code = ? AND active = ?", code, true).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &row, nil
}
