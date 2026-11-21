package branchlogo

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"tukifac/pkg/tenantstorage"
)

// DataURLMaxBytes: por encima de esto no se embebe. Un logo de comprobante pesa unos pocos KB;
// si alguien sube una foto enorme, mejor que el cliente la baje por su cuenta que inflar la
// config/el PDF en cada impresión.
const DataURLMaxBytes = 512 * 1024

var logoMimeByExt = map[string]string{
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".png":  "image/png",
	".webp": "image/webp",
}

// BranchSubdir carpeta de uploads del logo propio de una sucursal.
func BranchSubdir(branchID uint) string {
	return fmt.Sprintf("company/branches/%d", branchID)
}

// ResolveCompanyDataURL embebe como data: URL el logo global de la empresa (subdir "company").
func ResolveCompanyDataURL(ruc, rawURL string) string {
	return ResolveDataURL(ruc, "company", rawURL)
}

// ResolveBranchDataURL embebe como data: URL el logo propio de una sucursal, si la tiene.
func ResolveBranchDataURL(ruc string, branchID uint, rawURL string) string {
	return ResolveDataURL(ruc, BranchSubdir(branchID), rawURL)
}

// ResolveDataURL embebe como data: URL el archivo guardado en
// uploads/tenants/{ruc}/{subdir}/{filename tomado de rawURL}. Único punto que lee el logo del
// disco y lo convierte a base64 — usado tanto por la config de empresa (sesión/pantalla de
// Ajustes) como por la generación de comprobantes (print_data.go de ventas/cotizaciones), para
// que ambos caminos sean igual de confiables: el PDF nunca necesita un fetch en vivo a
// /uploads/* (que en producción puede no estar enrutado igual que /api/*, o fallar por CORS) —
// el logo viaja embebido en la misma respuesta.
//
// Silencioso a propósito ("" si algo falla): nunca debe romper la config ni la impresión, el
// llamador sigue teniendo la URL cruda como respaldo (ver branchlogo.ResolveURL).
func ResolveDataURL(ruc, subdir, rawURL string) string {
	raw := strings.TrimSpace(rawURL)
	if raw == "" {
		return ""
	}
	// Ya viene embebido (config antigua o llamador que ya resolvió): se devuelve tal cual.
	if strings.HasPrefix(raw, "data:") {
		return raw
	}
	if ruc == "" {
		return ""
	}

	filename := logoFilenameFromURL(raw)
	if filename == "" {
		return ""
	}
	mime, ok := logoMimeByExt[strings.ToLower(filepath.Ext(filename))]
	if !ok {
		return ""
	}

	path := filepath.Join(tenantstorage.TenantUploadDir(ruc, subdir), filename)
	info, err := os.Stat(path)
	if err != nil || info.IsDir() || info.Size() == 0 || info.Size() > DataURLMaxBytes {
		return ""
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return ""
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)
}

// logoFilenameFromURL extrae el nombre de archivo de la URL pública, sin el ?v= que se le añade
// para romper la caché del navegador. Solo acepta un nombre plano: la URL viene de la BD y no
// debe poder salirse de la carpeta del tenant.
func logoFilenameFromURL(raw string) string {
	if i := strings.IndexAny(raw, "?#"); i >= 0 {
		raw = raw[:i]
	}
	raw = strings.TrimRight(raw, "/")
	name := pathBase(raw)
	if name == "" || name == "." || name == "/" {
		return ""
	}
	if strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") {
		return ""
	}
	return name
}

// pathBase evita depender de path/filepath para URLs (separador siempre '/').
func pathBase(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}
