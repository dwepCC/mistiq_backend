package middleware

import (
	"tukifac/pkg/restaurantperm"

	"github.com/gofiber/fiber/v3"
)

const contactsScopeLocal = "contacts_scope"

// ContactsScope indica sobre qué tipos de contacto puede actuar el usuario en la acción de la ruta
// (view/create/edit/delete). Clientes y proveedores son una misma tabla (tenant_contacts.type =
// customer | supplier | both) pero tienen permisos propios: customers.* y suppliers.*.
type ContactsScope struct {
	Customers bool
	Suppliers bool
}

// Any dice si puede actuar al menos sobre uno de los dos tipos.
func (s ContactsScope) Any() bool { return s.Customers || s.Suppliers }

// Both dice si puede actuar sobre los dos tipos.
func (s ContactsScope) Both() bool { return s.Customers && s.Suppliers }

// Allows indica si el alcance cubre un contacto del tipo dado. Un contacto "both" es cliente y
// proveedor a la vez: para leerlo basta uno de los dos permisos (needAll=false); para modificarlo
// hacen falta los dos (needAll=true), porque el cambio afecta ambos roles.
func (s ContactsScope) Allows(contactType string, needAll bool) bool {
	switch contactType {
	case "supplier":
		return s.Suppliers
	case "both":
		if needAll {
			return s.Both()
		}
		return s.Any()
	default: // customer (y cualquier valor vacío/legacy: el default de la columna es customer)
		return s.Customers
	}
}

// TypesFor lista los valores de `type` visibles con este alcance (para filtrar listas).
// nil = sin restricción (ve ambos tipos).
func (s ContactsScope) TypesFor() []string {
	switch {
	case s.Both():
		return nil
	case s.Customers:
		return []string{"customer", "both"}
	case s.Suppliers:
		return []string{"supplier", "both"}
	default:
		return []string{}
	}
}

// ContactsScopeFrom devuelve el alcance que dejó RequireContactsAccess en la petición.
func ContactsScopeFrom(c fiber.Ctx) ContactsScope {
	s, _ := c.Locals(contactsScopeLocal).(ContactsScope)
	return s
}

// contactsPermAction normaliza la acción de la ruta a la acción del permiso.
func contactsPermAction(action string) string {
	switch action {
	case "create", "edit", "delete":
		return action
	default:
		return "view"
	}
}

// contactsScopeFromPermissions calcula el alcance a partir de los permisos del rol:
// customers.<acción> / suppliers.<acción>. El permiso legacy contacts.<acción> (previo a la
// separación) se sigue respetando como "ambos" hasta que la migración v168 lo reemplaza.
func contactsScopeFromPermissions(permissions []string, action string) ContactsScope {
	a := contactsPermAction(action)
	legacy := tenantHasPermission(permissions, "contacts."+a)
	return ContactsScope{
		Customers: legacy || tenantHasPermission(permissions, "customers."+a),
		Suppliers: legacy || tenantHasPermission(permissions, "suppliers."+a),
	}
}

// RequireContactsAccess permite acceso a contactos vía permisos tenant (customers.* / suppliers.*)
// o staff restaurante (o.c — crear pedido: buscar/dar de alta un cliente es parte de armar la
// venta en el POS de Tukichef, frontend_restaurant_tenant/src/pages/{POS,Mesa,Ventas}Page.tsx).
// El staff de PIN solo cubre CLIENTES (el POS nunca maneja proveedores); el rol "Administrador"
// por PIN cubre ambos.
//
// Sin este puente, RequirePermission a secas rechaza SIEMPRE a un staff de PIN: su JWT nunca llena
// claims.Permissions (usa pkg/restaurantperm, no TenantPermission) — regresión real detectada en
// producción tras cerrar el hueco de internal/contacts/routes.go.
//
// "delete" queda fuera del puente a propósito: borrar un contacto no es una operación de POS, sigue
// exigiendo el permiso tenant (igual que antes).
//
// La ruta pasa si el usuario tiene la acción sobre AL MENOS un tipo; el alcance exacto queda en
// ContactsScopeFrom(c) para que el handler filtre o rechace según el tipo del contacto.
// Usar después de RequireModule("contacts") y LoadRestaurantPermissions().
func RequireContactsAccess(action string) fiber.Handler {
	return func(c fiber.Ctx) error {
		claims, ok := c.Locals("tenant_claims").(*TenantClaims)
		if !ok || claims == nil {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{"error": "Sin contexto de autenticación"})
		}
		scope := contactsScopeFromPermissions(claims.Permissions, action)
		if !scope.Both() && contactsPermAction(action) != "delete" && (claims.AuthMethod == "pin" || claims.EmployeeType != "") {
			if HasRestaurantPerm(c, restaurantperm.OrdersCreate) {
				scope.Customers = true
			}
			if claims.RoleName == "Administrador" {
				scope.Customers, scope.Suppliers = true, true
			}
		}
		if !scope.Any() {
			return c.Status(fiber.StatusForbidden).JSON(fiber.Map{
				"error":      "No tienes permiso para esta acción",
				"permission": "customers." + contactsPermAction(action),
			})
		}
		c.Locals(contactsScopeLocal, scope)
		return c.Next()
	}
}
