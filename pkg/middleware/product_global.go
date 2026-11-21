package middleware

import (
	"sync"
	"time"

	"tukifac/pkg/database"

	"github.com/gofiber/fiber/v3"
	"gorm.io/gorm"
)

// PermProductsManageGlobal protege la información GLOBAL del catálogo: datos maestros del producto (nombre, código,
// categoría, marca, unidad, impuestos, precio global, imágenes, galería, unidades de venta, atributos), su alta/baja y
// las categorías/marcas/unidades. La información comercial POR SUCURSAL (precio por sucursal, visibilidad y stock)
// sigue regida por products.edit y similares.
const PermProductsManageGlobal = "products.manage_global"

func productsGlobalCatalogReady(db *gorm.DB) bool {
	if db == nil {
		return false
	}
	return database.CachedSchemaProbe(db, "perm:"+PermProductsManageGlobal, func() bool {
		var n int64
		db.Model(&database.TenantPermission{}).Where("module = ? AND action = ?", "products", "manage_global").Count(&n)
		return n > 0
	})
}

// RequireGlobalCatalog exige products.manage_global (además del permiso de acción de la ruta). Mientras el tenant no
// tenga la migración V177 (permiso no sembrado) no restringe, para no bloquear a nadie en un despliegue escalonado.
func RequireGlobalCatalog() fiber.Handler {
	return func(c fiber.Ctx) error {
		tdb, _ := c.Locals("tenantDB").(*gorm.DB)
		if !productsGlobalCatalogReady(tdb) {
			return c.Next()
		}
		if canManageGlobalCatalog(c, tdb) {
			return c.Next()
		}
		return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
			"error":      "No tiene permiso para modificar el catálogo global de productos",
			"permission": PermProductsManageGlobal,
		})
	}
}

// CanManageGlobalCatalog: variante booleana para handlers que deciden según el body (p. ej. actualización masiva de
// precios: con sucursal es comercial, sin sucursal es el precio global).
func CanManageGlobalCatalog(c fiber.Ctx) bool {
	tdb, _ := c.Locals("tenantDB").(*gorm.DB)
	if !productsGlobalCatalogReady(tdb) {
		return true
	}
	return canManageGlobalCatalog(c, tdb)
}

type globalCatKey struct {
	pool   interface{}
	roleID uint
}

type globalCatEntry struct {
	val bool
	exp time.Time
}

var globalCatCache sync.Map

const globalCatTTL = 10 * time.Second

// canManageGlobalCatalog decide con los permisos del ROL en BD (caché de 10 s), no con los del JWT. Un token emitido antes
// de la migración V177 no trae products.manage_global aunque el rol ya lo tenga (V177 se lo dio a quien editaba productos):
// con el JWT, los administradores con sesión anterior al deploy recibían 403 al crear o editar productos hasta renovar el token
// (incidente del 11-nov-2026). products.manage también lo implica. Quitar el permiso al rol surte efecto en segundos.
func canManageGlobalCatalog(c fiber.Ctx, tdb *gorm.DB) bool {
	claims, _ := c.Locals("tenant_claims").(*TenantClaims)
	if claims == nil || claims.RoleID == 0 || tdb == nil || tdb.Config == nil {
		return false
	}
	key := globalCatKey{pool: tdb.Config.ConnPool, roleID: claims.RoleID}
	if v, ok := globalCatCache.Load(key); ok {
		if e := v.(globalCatEntry); time.Now().Before(e.exp) {
			return e.val
		}
	}
	var n int64
	tdb.Table("tenant_role_permissions").
		Joins("JOIN tenant_permissions ON tenant_permissions.id = tenant_role_permissions.permission_id").
		Where("tenant_role_permissions.role_id = ? AND tenant_permissions.module = ? AND tenant_permissions.action IN ?",
			claims.RoleID, "products", []string{"manage_global", "manage"}).
		Count(&n)
	val := n > 0
	globalCatCache.Store(key, globalCatEntry{val: val, exp: time.Now().Add(globalCatTTL)})
	return val
}
