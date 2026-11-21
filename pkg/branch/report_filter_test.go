package branch

import (
	"io"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/gofiber/fiber/v3"
)

// resolveReport ejecuta ResolveReportBranchFilter dentro de una request real, con las mismas
// Locals que inyecta el middleware de sucursal.
func resolveReport(t *testing.T, active uint, admin bool, requested uint, allWhenEmpty bool) uint {
	t.Helper()
	app := fiber.New()
	app.Get("/x", func(c fiber.Ctx) error {
		c.Locals("active_branch_id", active)
		c.Locals("is_branch_admin", admin)
		return c.SendString(strconv.FormatUint(uint64(ResolveReportBranchFilter(c, requested, allWhenEmpty)), 10))
	})
	resp, err := app.Test(httptest.NewRequest("GET", "/x", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	n, err := strconv.ParseUint(string(b), 10, 32)
	if err != nil {
		t.Fatalf("respuesta inesperada %q", b)
	}
	return uint(n)
}

// "Todas las sucursales" del reporte y del dashboard debe ser todas (0) para quien puede cambiar de
// sucursal; antes el reporte caía en la sucursal activa y los totales no coincidían.
func TestResolveReportBranchFilter(t *testing.T) {
	cases := []struct {
		name      string
		active    uint
		admin     bool
		requested uint
		allEmpty  bool
		want      uint
	}{
		{"admin sin elegir y pide todas", 3, true, 0, true, 0},
		{"admin elige una", 3, true, 7, true, 7},
		{"admin sin elegir, sin pedir todas: su sucursal activa", 3, true, 0, false, 3},
		{"no admin queda en la suya aunque pida todas", 3, false, 0, true, 3},
		{"no admin no puede ver otra sucursal", 3, false, 7, true, 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveReport(t, tc.active, tc.admin, tc.requested, tc.allEmpty); got != tc.want {
				t.Fatalf("got %d, want %d", got, tc.want)
			}
		})
	}
}
