package saas

import (
	"fmt"

	"tukifac/pkg/database"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// PlanLimits: cuotas del plan vigente del tenant. 0 = ilimitado (grandfathering: superar el tope
// no borra datos; solo bloquea crear nuevos).
type PlanLimits struct {
	PlanName           string `json:"plan_name"`
	MaxUsers          int  `json:"max_users"`
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
		PlanName:           plan.Name,
		MaxUsers:          plan.MaxUsers,
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
	PlanName string
}

func (e *QuotaError) Error() string {
	if e.Resource == QuotaUsers && e.PlanName != "" {
		return fmt.Sprintf("Tu plan %s permite hasta %d usuarios. Actualiza tu plan para agregar más.", e.PlanName, e.Max)
	}
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
		used = CountSeatUsers(tenantDB)
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
		used = CountProductSeats(tenantDB)
	default:
		return nil
	}
	if used >= int64(max) {
		return &QuotaError{Resource: res, Used: used, Max: max, PlanName: limits.PlanName}
	}
	return nil
}

// CountProductSeats cuenta los productos que ocupan un cupo del plan: bienes de inventario vivos (no eliminados); los
// servicios no cuentan. Es el MISMO criterio con el que se valida el alta y el que muestra GET /api/session/modules
// (antes el contador de pantalla contaba también los servicios y no coincidía con el bloqueo).
func CountProductSeats(tenantDB *gorm.DB) int64 {
	var n int64
	tenantDB.Model(&database.TenantProduct{}).Where("type != ?", "service").Count(&n)
	return n
}

// CountSeatUsers cuenta los usuarios que ocupan un cupo del plan: activos y no eliminados (el
// soft delete los excluye solo). Incluye al administrador principal y a los usuarios operativos
// de Tukichef (PIN), que también son filas de tenant_users. Es el mismo criterio que muestra
// GET /api/session/modules.
func CountSeatUsers(tenantDB *gorm.DB) int64 {
	var n int64
	tenantDB.Model(&database.TenantUser{}).Where("active = ?", true).Count(&n)
	return n
}

// ReserveUserSeat debe llamarse DENTRO de la transacción que crea o reactiva un usuario. Toma un
// bloqueo de fila (la del primer usuario, que siempre existe) que serializa las altas
// concurrentes del tenant y luego cuenta: así dos altas simultáneas no pueden superar el tope.
// Devuelve *QuotaError si ya no queda cupo; nil si hay cupo, si el plan es ilimitado o si no hay
// contexto de tenant (tenantID 0). No toca a los usuarios existentes.
func ReserveUserSeat(tx *gorm.DB, tenantID uint) error {
	if tx == nil || tenantID == 0 {
		return nil
	}
	if TenantPlanLimits(tenantID).MaxUsers <= 0 {
		return nil
	}
	var first uint
	if err := tx.Unscoped().Model(&database.TenantUser{}).Clauses(clause.Locking{Strength: "UPDATE"}).
		Select("id").Order("id ASC").Limit(1).Scan(&first).Error; err != nil {
		return err
	}
	if q := CheckCreateQuota(tx, tenantID, QuotaUsers); q != nil {
		return q
	}
	return nil
}
