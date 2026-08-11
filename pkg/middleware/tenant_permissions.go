package middleware

// tenantHasPermission indica si el usuario tiene el permiso requerido, incluyendo
// implicaciones habituales (p. ej. cashbank.open → consultar sesión cashbank.view).
func tenantHasPermission(permissions []string, required string) bool {
	if required == "" {
		return false
	}
	set := make(map[string]struct{}, len(permissions))
	for _, p := range permissions {
		set[p] = struct{}{}
	}
	if _, ok := set[required]; ok {
		return true
	}
	// {module}.manage concede todas las acciones del módulo.
	if mod, _, ok := splitModuleAction(required); ok {
		if _, ok := set[mod+".manage"]; ok {
			return true
		}
	}
	return tenantPermissionImplied(set, required)
}

func splitModuleAction(key string) (module, action string, ok bool) {
	for i := 0; i < len(key); i++ {
		if key[i] == '.' {
			return key[:i], key[i+1:], true
		}
	}
	return "", "", false
}

func tenantPermissionImplied(set map[string]struct{}, required string) bool {
	switch required {
	case "cashbank.view":
		for _, p := range []string{"cashbank.open", "cashbank.close", "cashbank.movements"} {
			if _, ok := set[p]; ok {
				return true
			}
		}
	case "products.view":
		for _, p := range []string{"sales.pos", "sales.create", "products.create", "products.edit", "products.delete"} {
			if _, ok := set[p]; ok {
				return true
			}
		}
	case "sales.view":
		for _, p := range []string{"sales.create", "sales.pos", "sales.edit", "sales.cancel"} {
			if _, ok := set[p]; ok {
				return true
			}
		}
	case "sales.create":
		if _, ok := set["sales.pos"]; ok {
			return true
		}
	}
	return false
}
