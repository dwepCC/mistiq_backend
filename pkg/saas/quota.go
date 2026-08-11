package saas

import (
	"fmt"

	"tukifac/pkg/database"

	"gorm.io/gorm"
)

// PlanLimits: cuotas del plan vigente del tenant. 0 = ilimitado (grandfathering: superar el tope
// no borra datos; solo bloquea crear nuevos).
type PlanLimits struct {
	MaxUsers           int  `json:"max_users"`
	MaxBranches        int  `json:"max_branches"`
	MaxProducts        int  `json:"max_products"`
	MaxDocuments       int  `json:"max_documents"`
	UnlimitedDocuments bool `json:"unlimited_documents"`
}

// TenantPlanLimits devuelve los límites del plan de la suscripción vigente del tenant.
// Sin suscripción/plan → todo 0 (ilimitado), para no bloquear cuentas sin plan configurado.
func TenantPlanLimits(tenantID uint) PlanLimits {
	var limits PlanLimits
	if database.CentralDB == nil || tenantID == 0 {
		return limits
	}
	var sub database.SaasSubscription
	if err := database.CentralDB.Where("tenant_id = ?", tenantID).
		Where("status NOT IN ?", []string{database.SaasSubCancelled}).
		Order("created_at desc").First(&sub).Error; err != nil {
		return limits
	}
	var plan database.SaasPlan
	if err := database.CentralDB.First(&plan, sub.PlanID).Error; err != nil {
		return limits
	}
	return PlanLimits{
		MaxUsers:           plan.MaxUsers,
		MaxBranches:        plan.MaxBranches,
		MaxProducts:        plan.MaxProducts,
		MaxDocuments:       plan.MonthlyDocumentsLimit,
		UnlimitedDocuments: plan.IsUnlimitedDocuments,
	}
}

// QuotaExceeded indica si crear un registro más superaría el tope. max<=0 = ilimitado.
func QuotaExceeded(current, max int) bool {
	return max > 0 && current >= max
}

// QuotaResource identifica la cuota a validar.
type QuotaResource string

const (
	QuotaUsers    QuotaResource = "users"
	QuotaBranches QuotaResource = "branches"
	QuotaProducts QuotaResource = "products"
)

// QuotaError: crear un registro más superaría el tope del plan. El handler lo traduce a HTTP 402
// con code PLAN_LIMIT_EXCEEDED para que el frontend muestre el upsell.
type QuotaError struct {
	Resource QuotaResource
	Used     int64
	Max      int
}

func (e *QuotaError) Error() string {
	label := map[QuotaResource]string{
		QuotaUsers: "usuarios", QuotaBranches: "sucursales", QuotaProducts: "productos",
	}[e.Resource]
	if label == "" {
		label = string(e.Resource)
	}
	return fmt.Sprintf("Alcanzaste el límite de %s de tu plan (%d/%d). Mejora tu plan para agregar más.", label, e.Used, e.Max)
}

// CheckCreateQuota devuelve *QuotaError si crear un registro más de `res` superaría el tope del
// plan del tenant. nil = permitido (incluye tope 0 = ilimitado). No borra ni afecta datos
// existentes: solo bloquea el alta (grandfathering).
func CheckCreateQuota(tenantDB *gorm.DB, tenantID uint, res QuotaResource) *QuotaError {
	if tenantDB == nil || tenantID == 0 {
		return nil
	}
	limits := TenantPlanLimits(tenantID)
	var max int
	var used int64
	switch res {
	case QuotaUsers:
		max = limits.MaxUsers
		if max <= 0 {
			return nil
		}
		tenantDB.Model(&database.TenantUser{}).Where("active = ?", true).Count(&used)
	case QuotaBranches:
		max = limits.MaxBranches
		if max <= 0 {
			return nil
		}
		tenantDB.Model(&database.TenantBranch{}).Where("active = ?", true).Count(&used)
	case QuotaProducts:
		max = limits.MaxProducts
		if max <= 0 {
			return nil
		}
		// Solo bienes de inventario; los servicios no cuentan contra el tope de productos.
		tenantDB.Model(&database.TenantProduct{}).Where("type != ?", "service").Count(&used)
	default:
		return nil
	}
	if used >= int64(max) {
		return &QuotaError{Resource: res, Used: used, Max: max}
	}
	return nil
}
