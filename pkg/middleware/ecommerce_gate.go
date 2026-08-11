package middleware

import (
	"tukifac/pkg/database"
	"tukifac/pkg/saas"
	"tukifac/pkg/tenantctx"

	"github.com/gofiber/fiber/v3"
	"gorm.io/gorm"
)

const ecommerceModuleKey = "ecommerce"

// RequireEcommerceAvailable gate para las rutas PÚBLICAS del Catálogo Digital (sin JWT).
// La tienda pública solo responde si: (1) el tenant tiene el módulo "ecommerce" habilitado en su
// plan, (2) el tenant tiene la tienda activada en sus propios ajustes, y (3) su suscripción está
// operativa — si el tenant está bloqueado/suspendido, la tienda se cae igual que el resto del ERP.
func RequireEcommerceAvailable() fiber.Handler {
	return func(c fiber.Ctx) error {
		notAvailable := func() error {
			return c.Status(fiber.StatusNotFound).JSON(fiber.Map{"error": "Tienda no disponible"})
		}

		tenant, ok := tenantctx.Tenant(c)
		if !ok || tenant == nil {
			return notAvailable()
		}

		var tm database.TenantModule
		err := database.CentralDB.
			Where("tenant_id = ? AND module_key = ? AND enabled = ?", tenant.ID, ecommerceModuleKey, true).
			First(&tm).Error
		if err != nil {
			return notAvailable()
		}

		view, err := saas.GetTenantView(tenant.ID)
		if err != nil || !view.CanOperate {
			return notAvailable()
		}

		tdb, ok := c.Locals("tenantDB").(*gorm.DB)
		if !ok || tdb == nil {
			return notAvailable()
		}
		var settings database.TenantEcommerceSettings
		if err := tdb.First(&settings, 1).Error; err != nil || !settings.Enabled {
			return notAvailable()
		}

		return c.Next()
	}
}
