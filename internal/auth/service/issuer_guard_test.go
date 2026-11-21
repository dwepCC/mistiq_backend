package service

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Test guardián: los tokens JWT de tenant (access y refresh) se firman SOLO en internal/auth/service/issuer.go.
// Recorre el árbol de producción y falla si aparece SignedString( o jwt.NewWithClaims( en otro archivo.
// Excluidos: .claude/worktrees (copia vieja), cualquier *_test.go, y el login del superadmin, que tiene su propio
// JWT y secreto (internal/superadmin/handler/auth_sa_handler.go).
func TestJWTSigningOnlyInIssuer(t *testing.T) {
	root := moduleRoot(t)
	allowed := map[string]bool{
		"internal/auth/service/issuer.go":                true,
		"internal/superadmin/handler/auth_sa_handler.go": true,
		// Token de la CUENTA DEL CLIENTE de la tienda virtual (no es una sesión de personal del negocio).
		"pkg/middleware/ecommerce_customer_auth.go": true,
	}
	forbidden := []string{"SignedString(", "jwt.NewWithClaims("}

	sawIssuer := false
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if rel == ".claude" || strings.HasPrefix(rel, ".claude/") || rel == ".git" || rel == "vendor" || rel == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(rel, ".go") || strings.HasSuffix(rel, "_test.go") {
			return nil
		}
		if rel == "internal/auth/service/issuer.go" {
			sawIssuer = true
		}
		if allowed[rel] {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for n, line := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			for _, f := range forbidden {
				if strings.Contains(line, f) {
					t.Errorf("%s:%d firma JWT fuera del emisor único (%s); usa internal/auth/service/issuer.go", rel, n+1, f)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !sawIssuer {
		t.Fatal("el recorrido no encontró issuer.go: la raíz del módulo es incorrecta")
	}
}

func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod no encontrado")
		}
		dir = parent
	}
}
