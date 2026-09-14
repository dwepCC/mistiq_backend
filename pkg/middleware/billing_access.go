package middleware

import (
	"tukifac/pkg/restaurantperm"

	"github.com/gofiber/fiber/v3"
)

// RequireBillingAccess permite acceso a facturación vía permiso tenant o staff restaurante.
//
// action "send": bridgea a o.ch (OrdersCharge) — enviar/reenviar/consultar estado/ver comprobante
// es parte de cobrar una mesa en Tukichef (frontend_restaurant_tenant/src/services/billing.service.ts,
// pages/VentasPage.tsx). Regresión real detectada en producción: sin este puente, ningún cajero de
// PIN podía emitir ni consultar el comprobante de su propia venta.
// action "credit_note": bridgea a o.cx (OrdersCancel) — anular con nota de crédito, mismo permiso
// que ya usa RequireSalesAccess("cancel").
// debit_note/despatch/advanced_docs NO tienen puente: confirmado que Tukichef no los usa.
func RequireBillingAccess(tenantPerm, action string) fiber.Handler {
	restPerm := restaurantperm.OrdersCharge
	if action == "credit_note" {
		restPerm = restaurantperm.OrdersCancel
	}
	return func(c fiber.Ctx) error {
		claims, ok := c.Locals("tenant_claims").(*TenantClaims)
		if !ok || claims == nil {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "Sin contexto de autenticación"})
		}
		if tenantHasPermission(claims.Permissions, tenantPerm) {
			return c.Next()
		}
		if claims.AuthMethod == "pin" || claims.EmployeeType != "" {
			if HasRestaurantPerm(c, restPerm) {
				return c.Next()
			}
			if claims.RoleName == "Administrador" {
				return c.Next()
			}
		}
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error":      "No tienes permiso para esta acción",
			"permission": tenantPerm,
		})
	}
}
