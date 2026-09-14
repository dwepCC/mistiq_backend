package middleware

// Tests de CanDelegateAll — el "techo de delegación" del RBAC central (Fase 5, etapa 3, Grupo 7).
// Única implementación en todo el sistema; estos tests son la fuente de verdad de su
// comportamiento, para que ningún handler necesite (ni deba) reimplementarla. Usa
// claimsWithPermissions, definido en sa_permissions_test.go (mismo paquete).

import "testing"

// Caso base: el actor puede delegar exactamente lo que tiene.
func TestCanDelegateAll_ExactMatch(t *testing.T) {
	claims := claimsWithPermissions(1, "admin", 0, []string{"roles.manage", "usuarios_central.view"})
	if !CanDelegateAll(claims, []string{"roles.manage"}) {
		t.Fatal("debería poder delegar un permiso que tiene exactamente")
	}
	if !CanDelegateAll(claims, []string{"roles.manage", "usuarios_central.view"}) {
		t.Fatal("debería poder delegar todos los permisos que tiene")
	}
}

// El caso central del Grupo 7 (§6 del spec): roles.manage NO implica poder delegar permisos que
// el actor no tiene, aunque roles.manage sí le permita EJERCER la administración de roles.
func TestCanDelegateAll_RolesManageDoesNotImplyArbitraryDelegation(t *testing.T) {
	claims := claimsWithPermissions(1, "admin", 0, []string{"roles.manage"})
	if CanDelegateAll(claims, []string{"usuarios_central.change_role"}) {
		t.Fatal("roles.manage no debe permitir delegar usuarios_central.change_role — el actor no lo posee")
	}
}

// Un solo permiso no delegable en el conjunto basta para rechazar todo el conjunto (todo-o-nada).
func TestCanDelegateAll_OneMissingPermissionFailsTheWholeSet(t *testing.T) {
	claims := claimsWithPermissions(1, "admin", 0, []string{"empresas.view", "fiscal.view"})
	if CanDelegateAll(claims, []string{"empresas.view", "fiscal.view", "fiscal.cancel"}) {
		t.Fatal("un permiso faltante (fiscal.cancel) debe tumbar todo el conjunto")
	}
}

// Conjunto vacío: delegar "nada" siempre es válido (p. ej. un diff sin permisos agregados, o
// quitar permisos — que nunca pasa por CanDelegateAll con contenido).
func TestCanDelegateAll_EmptySetAlwaysSucceeds(t *testing.T) {
	claims := claimsWithPermissions(1, "admin", 0, []string{})
	if !CanDelegateAll(claims, nil) {
		t.Fatal("delegar un conjunto vacío debe ser siempre válido, incluso sin ningún permiso propio")
	}
	if !CanDelegateAll(claims, []string{}) {
		t.Fatal("delegar un slice vacío (no nil) también debe ser válido")
	}
}

// La expansión de .manage se hereda de HasSAPermission: un actor con documentos.manage puede
// delegar documentos.view (implícito en la allowlist), pero no documentos.approve_purchase
// (excluido a propósito, ver saManageImpliedActions).
func TestCanDelegateAll_InheritsManageExpansionFromHasSAPermission(t *testing.T) {
	claims := claimsWithPermissions(1, "admin", 0, []string{"documentos.manage"})
	if !CanDelegateAll(claims, []string{"documentos.view"}) {
		t.Fatal("documentos.manage debería poder delegar documentos.view (expansión de .manage)")
	}
	if CanDelegateAll(claims, []string{"documentos.approve_purchase"}) {
		t.Fatal("documentos.manage NO debe poder delegar documentos.approve_purchase (crítico, independiente)")
	}
}

// Wildcard "*" (el único caso real hoy es el propio bypass de superadmin, pero se prueba también
// el conjunto de permisos crudo por si algún día un rol pudiera tener "*" explícito).
func TestCanDelegateAll_WildcardPermissionDelegatesEverything(t *testing.T) {
	claims := claimsWithPermissions(1, "admin", 0, []string{"*"})
	if !CanDelegateAll(claims, []string{"roles.manage", "usuarios_central.destroy", "fiscal.cancel"}) {
		t.Fatal("un actor con permiso \"*\" debería poder delegar cualquier conjunto")
	}
}

// El bypass de superadmin real (Role == "superadmin") delega cualquier cosa, incluso con
// Permissions vacío en el JWT (así se emite hoy — ver LoginAPI/saPermissionsForUser).
func TestCanDelegateAll_SuperadminBypassDelegatesEverything(t *testing.T) {
	claims := claimsWithPermissions(1, "superadmin", 0, nil)
	if !CanDelegateAll(claims, []string{"roles.manage", "usuarios_central.destroy", "fiscal.cancel"}) {
		t.Fatal("superadmin real debe poder delegar cualquier permiso, incluso con Permissions vacío")
	}
}

// Sin contexto de autenticación (claims nil): nunca se asume ninguna capacidad de delegar, salvo
// el caso vacío (delegar "nada" no requiere ninguna capacidad).
func TestCanDelegateAll_NilClaims(t *testing.T) {
	if CanDelegateAll(nil, []string{"roles.manage"}) {
		t.Fatal("sin claims no debe poder delegar ningún permiso concreto")
	}
	if !CanDelegateAll(nil, nil) {
		t.Fatal("sin claims, delegar un conjunto vacío sigue siendo válido")
	}
}

// Un actor sin roles.manage (p. ej. solo roles.view) tampoco puede delegar permisos de otros
// módulos que no posee — CanDelegateAll no depende en absoluto de qué permisos de "roles" tenga
// el actor, solo de si POSEE cada permiso del conjunto a delegar.
func TestCanDelegateAll_UnrelatedToRolesModulePermissions(t *testing.T) {
	claims := claimsWithPermissions(1, "admin", 0, []string{"roles.view"})
	if CanDelegateAll(claims, []string{"empresas.master_access"}) {
		t.Fatal("roles.view no otorga ninguna capacidad de delegación sobre otros módulos")
	}
}
