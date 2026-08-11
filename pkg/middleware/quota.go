package middleware

import (
	"tukifac/pkg/saas"

	"github.com/gofiber/fiber/v3"
	"gorm.io/gorm"
)

// EnforceCreateQuota valida la cuota del plan del tenant antes de crear un registro de `res`.
// Si se supera, escribe una respuesta 402 con code PLAN_LIMIT_EXCEEDED (para que el frontend
// muestre el upsell) y devuelve true; el handler debe entonces `return nil`. No afecta datos
// existentes: solo bloquea el alta (grandfathering).
func EnforceCreateQuota(c fiber.Ctx, tenantDB *gorm.DB, res saas.QuotaResource) bool {
	claims, _ := c.Locals("tenant_claims").(*TenantClaims)
	if claims == nil {
		return false
	}
	q := saas.CheckCreateQuota(tenantDB, claims.TenantID, res)
	if q == nil {
		return false
	}
	_ = c.Status(fiber.StatusPaymentRequired).JSON(fiber.Map{
		"error":    q.Error(),
		"code":     "PLAN_LIMIT_EXCEEDED",
		"resource": string(q.Resource),
		"used":     q.Used,
		"max":      q.Max,
	})
	return true
}
