package service

import "tukifac/pkg/database"

// PlanOption plan de Tukifac para elegirlo como ítem de un pedido.
type PlanOption struct {
	ID    uint    `json:"id"`
	Name  string  `json:"name"`
	Price float64 `json:"price"` // precio mensual de lista
}

// ListPlans devuelve los planes de Tukifac vigentes (ordenados por precio) para el desplegable del ítem «Plan Tukifac».
func (s *Service) ListPlans() ([]PlanOption, error) {
	out := []PlanOption{}
	err := s.db.Model(&database.SaasPlan{}).Select("id, name, price").Where("active = ?", true).Order("price ASC, name ASC").Scan(&out).Error
	return out, err
}
