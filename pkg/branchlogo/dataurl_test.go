package branchlogo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"tukifac/pkg/tenantstorage"
)

func TestLogoFilenameFromURL(t *testing.T) {
	cases := []struct {
		name string
		url  string
		want string
	}{
		{"ruta pública normal", "/uploads/tenants/10726187938/company/logo.png", "logo.png"},
		{"con ?v= antichaché", "/uploads/tenants/10726187938/company/logo.png?v=1736899200000", "logo.png"},
		{"con fragmento", "/uploads/tenants/1/company/logo.webp#x", "logo.webp"},
		{"absoluta", "https://api.tukifac.com/uploads/tenants/1/company/logo.jpg?v=9", "logo.jpg"},
		{"vacía", "", ""},
		{"solo barras", "///", ""},
		// La URL sale de la BD: pase lo que pase, el resultado debe ser un nombre plano que
		// no permita salir de la carpeta del tenant.
		{"traversal codificado se rechaza", "/uploads/tenants/1/company/..%2f..%2fsecret.png", ""},
		{"traversal literal se queda en el nombre base", "/uploads/tenants/1/company/../../secret.png", "secret.png"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := logoFilenameFromURL(tc.url); got != tc.want {
				t.Errorf("logoFilenameFromURL(%q) = %q, want %q", tc.url, got, tc.want)
			}
		})
	}
}

func TestResolveBranchDataURL(t *testing.T) {
	ruc := "10726187938"
	branchID := uint(7)
	dir := tenantstorage.TenantUploadDir(ruc, BranchSubdir(branchID))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	logoPath := filepath.Join(dir, "logo.png")
	png := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x01, 0x02, 0x03}
	if err := os.WriteFile(logoPath, png, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(logoPath) })

	t.Run("embebe el logo propio de la sucursal", func(t *testing.T) {
		dataURL := ResolveBranchDataURL(ruc, branchID, "/uploads/tenants/"+ruc+"/company/branches/7/logo.png?v=123")
		if !strings.HasPrefix(dataURL, "data:image/png;base64,") {
			t.Fatalf("esperaba un data URL png, got %q", dataURL)
		}
	})

	t.Run("sin logo propio no embebe nada", func(t *testing.T) {
		if got := ResolveBranchDataURL(ruc, branchID, ""); got != "" {
			t.Errorf("esperaba vacío, got %q", got)
		}
	})

	t.Run("archivo inexistente no rompe", func(t *testing.T) {
		if got := ResolveBranchDataURL(ruc, 999, "/uploads/tenants/"+ruc+"/company/branches/999/noexiste.png"); got != "" {
			t.Errorf("esperaba vacío, got %q", got)
		}
	})
}
