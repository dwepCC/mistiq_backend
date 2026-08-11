package saas

import (
	"errors"
	"fmt"

	"tukifac/pkg/database"

	"gorm.io/gorm"
)

// ProvisionInitialSubscription crea suscripción activa + ciclo de facturación inicial.
func ProvisionInitialSubscription(tenantID uint, planName string, months int, notes string, discount ...Discount) (*database.SaasSubscription, error) {
	if tenantID == 0 {
		return nil, errors.New("tenant_id requerido")
	}
	if months <= 0 {
		months = 1
	}
	var plan database.SaasPlan
	if err := database.CentralDB.Where("LOWER(name) = LOWER(?) AND active = ?", planName, true).First(&plan).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("plan %q no encontrado o inactivo en catálogo SaaS", planName)
		}
		return nil, err
	}
	sub, err := ExtendSubscription(tenantID, plan.ID, months, notes, discount...)
	if err != nil {
		return nil, err
	}
	var cycle database.SaasBillingCycle
	if err := database.CentralDB.Where("subscription_id = ?", sub.ID).Order("id DESC").First(&cycle).Error; err != nil {
		return nil, fmt.Errorf("ciclo de facturación inicial: %w", err)
	}
	return sub, nil
}
