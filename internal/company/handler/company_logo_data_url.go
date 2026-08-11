package handler

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"

	"tukifac/pkg/database"
	"tukifac/pkg/tenantstorage"
)

// logoDataURLMaxBytes: por encima de esto no se embebe. Un logo de comprobante pesa unos
// pocos KB; si alguien sube una foto enorme, mejor que el cliente la baje por su cuenta que
// inflar la config en cada arranque de sesión.
const logoDataURLMaxBytes = 512 * 1024

var logoMimeByExt = map[string]string{
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".png":  "image/png",
	".webp": "image/webp",
}

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
	raw := strings.TrimSpace(cfg.LogoURL)
	if raw == "" {
		return
	}
	// Config antigua con el logo ya embebido: se devuelve tal cual.
	if strings.HasPrefix(raw, "data:") {
		cfg.LogoDataURL = raw
		return
	}
	if ruc == "" {
		return
	}

	filename := logoFilenameFromURL(raw)
	if filename == "" {
		return
	}
	mime, ok := logoMimeByExt[strings.ToLower(filepath.Ext(filename))]
	if !ok {
		return
	}

	path := filepath.Join(tenantstorage.TenantUploadDir(ruc, "company"), filename)
	info, err := os.Stat(path)
	if err != nil || info.IsDir() || info.Size() == 0 || info.Size() > logoDataURLMaxBytes {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil || len(data) == 0 {
		return
	}
	cfg.LogoDataURL = "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)
}

// logoFilenameFromURL extrae el nombre de archivo de la URL pública, sin el ?v= que se le
// añade para romper la caché del navegador. Solo acepta un nombre plano: la URL viene de la
// BD y no debe poder salirse de la carpeta del tenant.
func logoFilenameFromURL(raw string) string {
	if i := strings.IndexAny(raw, "?#"); i >= 0 {
		raw = raw[:i]
	}
	raw = strings.TrimRight(raw, "/")
	name := path_Base(raw)
	if name == "" || name == "." || name == "/" {
		return ""
	}
	if strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") {
		return ""
	}
	return name
}

// path_Base evita depender de path/filepath para URLs (separador siempre '/').
func path_Base(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}
