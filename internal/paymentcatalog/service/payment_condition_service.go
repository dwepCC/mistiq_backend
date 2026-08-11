package service

import (
	"errors"

	"tukifac/pkg/database"

	"gorm.io/gorm"
)

type PaymentConditionService struct {
	db *gorm.DB
}

func NewPaymentConditionService(db *gorm.DB) *PaymentConditionService {
	return &PaymentConditionService{db: db}
}

func (s *PaymentConditionService) List(activeOnly bool) ([]database.TenantPaymentCondition, error) {
	var list []database.TenantPaymentCondition
	q := s.db.Order("id ASC")
	if activeOnly {
		q = q.Where("active = ?", true)
	}
	err := q.Find(&list).Error
	return list, err
}

func (s *PaymentConditionService) GetByCode(code string) (*database.TenantPaymentCondition, error) {
	var row database.TenantPaymentCondition
	if err := s.db.Where("code = ? AND active = ?", code, true).First(&row).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &row, nil
}
