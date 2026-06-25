package storagepaths

import (
	"os"
	"path/filepath"
)

// Root directorio base de archivos públicos (/storage/*).
// En Docker producción: INVOICE_STORAGE_PATH=/app/storage/invoices → /app/storage.
func Root() string {
	if inv := os.Getenv("INVOICE_STORAGE_PATH"); inv != "" {
		return filepath.Dir(filepath.Clean(inv))
	}
	return "storage"
}

// SaasDir carpeta QR Yape/Plin (debe estar bajo el volumen montado en producción).
func SaasDir() string {
	return filepath.Join(Root(), "saas")
}

// FilePath ruta absoluta o relativa al cwd para servir/guardar bajo Root.
func FilePath(rel string) string {
	rel = filepath.Clean(filepath.FromSlash(rel))
	if rel == "." || rel == ".." {
		return Root()
	}
	return filepath.Join(Root(), rel)
}
