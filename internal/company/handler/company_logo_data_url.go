package handler

import (
	"strings"

	"tukifac/pkg/branchlogo"
	"tukifac/pkg/database"
)

// attachLogoDataURL embebe el logo del tenant en la config que se devuelve al cliente.
//
// El logo se guarda en disco y su URL pública exige que cada dispositivo la descargue con
// fetch para poder imprimirla (CORS, origen, alcanzabilidad). Mandarlo ya embebido hace que
// baste con iniciar sesión: quien tenga la config tiene el logo.
//
// Silencioso a propósito: si el archivo no está o pesa de más, el cliente sigue teniendo
// logo_url como respaldo. Nunca debe impedir leer la configuración de la empresa.
func attachLogoDataURL(ruc string, cfg *database.TenantCompanyConfig) {
	if cfg == nil {
		return
	}
	// Si logo_url YA es un data: URL (logo guardado embebido en la BD) no se repite en logo_data_url:
	// la respuesta pesaba el doble (283 KB en vez de ~140). Los clientes (frontend_tenant y Tukichef)
	// ya aceptan un data: en logo_url cuando logo_data_url no viene.
	if strings.HasPrefix(strings.TrimSpace(cfg.LogoURL), "data:") {
		return
	}
	if dataURL := branchlogo.ResolveCompanyDataURL(ruc, cfg.LogoURL); dataURL != "" {
		cfg.LogoDataURL = dataURL
	}
}

// attachBranchLogoDataURL es el equivalente de attachLogoDataURL para el logo propio de una
// sucursal (guardado aparte, en company/branches/{id}/). No hace nada si la sucursal no tiene
// logo propio — el fallback al logo global lo resuelve pkg/branchlogo.ResolveURL, no esta función.
func attachBranchLogoDataURL(ruc string, b *database.TenantBranch) {
	if b == nil {
		return
	}
	if strings.HasPrefix(strings.TrimSpace(b.LogoURL), "data:") {
		return // ya embebido en logo_url: no se duplica (ver attachLogoDataURL)
	}
	if dataURL := branchlogo.ResolveBranchDataURL(ruc, b.ID, b.LogoURL); dataURL != "" {
		b.LogoDataURL = dataURL
	}
}
