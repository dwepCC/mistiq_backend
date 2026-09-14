package saas

import (
	"errors"
	"fmt"
	"time"

	"tukifac/pkg/database"

	"gorm.io/gorm"
)

// ProvisionInitialSubscription crea suscripción activa + ciclo de facturación inicial.
// startDate opcional: la empresa se registra hoy, pero su suscripción (y primer cobro) puede
// arrancar unos días después — nil = arranca hoy, como antes. Debe ser hoy o futuro (validado en
// extendSubscriptionTx).
//
// El descuento del cobro inicial NO se recibe del llamador: sale de PlanCycleDiscount(plan.ID,
// months) — la misma tabla de ciclos configurados que ya usa el autoservicio del tenant
// (SubmitRenewalRequest) y la aprobación de una renovación sin ciclo. Antes el panel central
// dejaba que el admin escribiera el tipo/valor a mano al dar de alta una empresa, lo cual podía
// no coincidir con lo que el plan tiene pactado para esa cantidad de meses.
func ProvisionInitialSubscription(tenantID uint, planName string, months int, notes string, startDate *time.Time) (*database.SaasSubscription, error) {
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
	discount := PlanCycleDiscount(plan.ID, months)
	sub, err := ExtendSubscription(tenantID, plan.ID, months, notes, startDate, discount)
	if err != nil {
		return nil, err
	}
	var cycle database.SaasBillingCycle
	if err := database.CentralDB.Where("subscription_id = ?", sub.ID).Order("id DESC").First(&cycle).Error; err != nil {
		return nil, fmt.Errorf("ciclo de facturación inicial: %w", err)
	}
	return sub, nil
}
