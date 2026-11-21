package tenantmigrations

import (
	"tukifac/pkg/database"

	"gorm.io/gorm"
)

// V186BillingViewPermission agrega billing.view ("Ver consulta de comprobantes", solo lectura).
//
// Hasta ahora ver la lista de comprobantes exigía billing.send, que también permite enviar y reenviar a
// SUNAT: no se podía dar "solo consultar". Los endpoints de lectura aceptan ahora billing.view O
// billing.send, y los roles que ya tenían billing.send reciben billing.view para que el catálogo de
// Roles refleje lo que realmente pueden hacer (nadie gana ni pierde acceso el día del deploy).
// Los demás roles (Vendedor, Cajero…) lo reciben solo si el dueño lo marca en Roles.
//
// Catálogo espejo: internal/users/service/role_service.go (SeedPermissions). Idempotente.
type V186BillingViewPermission struct{}

func (V186BillingViewPermission) Version() int { return 186 }
func (V186BillingViewPermission) Name() string  { return "billing_view_permission" }

func (V186BillingViewPermission) Up(db *gorm.DB) error {
	if !db.Migrator().HasTable(&database.TenantRole{}) || !db.Migrator().HasTable(&database.TenantPermission{}) ||
		!db.Migrator().HasTable(&database.TenantRolePermission{}) {
		return nil
	}
	return db.Transaction(func(tx *gorm.DB) error {
		var view database.TenantPermission
		if err := tx.Where(database.TenantPermission{Module: "billing", Action: "view"}).
			Attrs(database.TenantPermission{Label: "Ver consulta de comprobantes (solo lectura)"}).
			FirstOrCreate(&view).Error; err != nil {
			return err
		}
		var send database.TenantPermission
		if err := tx.Where("module = ? AND action = ?", "billing", "send").First(&send).Error; err != nil {
			return nil // catálogo sin billing.send: nada que heredar
		}
		var roleIDs []uint
		if err := tx.Model(&database.TenantRolePermission{}).
			Where("permission_id = ?", send.ID).Pluck("role_id", &roleIDs).Error; err != nil {
			return err
		}
		for _, roleID := range roleIDs {
			if err := grantIfMissing(tx, roleID, view.ID); err != nil {
				return err
			}
		}
		return nil
	})
}
