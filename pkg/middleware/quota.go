package middleware

import (
	"errors"

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
	return writeQuotaError(c, q)
}

// RespondQuotaError traduce un *saas.QuotaError devuelto por un servicio (alta o reactivación
// validada dentro de su transacción) a la misma respuesta 402 PLAN_LIMIT_EXCEEDED. Devuelve false
// si err no es un error de cuota, para que el handler siga con su manejo normal.
func RespondQuotaError(c fiber.Ctx, err error) bool {
	var q *saas.QuotaError
	if !errors.As(err, &q) {
		return false
	}
	return writeQuotaError(c, q)
}

// TenantIDFromClaims devuelve el tenant del JWT de la petición (0 si no hay sesión de tenant).
func TenantIDFromClaims(c fiber.Ctx) uint {
	if claims, _ := c.Locals("tenant_claims").(*TenantClaims); claims != nil {
		return claims.TenantID
	}
	return 0
}

func writeQuotaError(c fiber.Ctx, q *saas.QuotaError) bool {
	_ = c.Status(fiber.StatusPaymentRequired).JSON(fiber.Map{
		"error":    q.Error(),
		"code":     "PLAN_LIMIT_EXCEEDED",
		"resource": string(q.Resource),
		"used":     q.Used,
		"max":      q.Max,
	})
	return true
}
