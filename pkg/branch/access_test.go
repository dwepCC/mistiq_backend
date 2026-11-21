package branch

import (
	"fmt"
	"io"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/gofiber/fiber/v3"
)

type ctxSetup struct {
	active  uint
	viewAll bool
	allowed []uint
}

// run ejecuta fn dentro de una request real con las mismas Locals que inyecta el middleware.
func run(t *testing.T, s ctxSetup, fn func(c fiber.Ctx) string) string {
	t.Helper()
	app := fiber.New()
	app.Get("/x", func(c fiber.Ctx) error {
		c.Locals("active_branch_id", s.active)
		c.Locals("is_branch_admin", s.viewAll)
		c.Locals("allowed_branch_ids", s.allowed)
		return c.SendString(fn(c))
	})
	resp, err := app.Test(httptest.NewRequest("GET", "/x", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

func u(n uint) string { return strconv.FormatUint(uint64(n), 10) }

func TestCanAccessBranch(t *testing.T) {
	seller := ctxSetup{active: 1, allowed: []uint{1}}
	multi := ctxSetup{active: 1, allowed: []uint{1, 2}} // 2 sucursales: NO es acceso global
	admin := ctxSetup{active: 1, viewAll: true, allowed: []uint{1}}
	cases := []struct {
		name string
		s    ctxSetup
		id   uint
		want bool
	}{
		{"vendedor su sucursal", seller, 1, true},
		{"vendedor otra sucursal", seller, 2, false},
		{"id 0 nunca", seller, 0, false},
		{"multi: asignada", multi, 2, true},
		{"multi: no asignada", multi, 3, false},
		{"admin ve cualquiera", admin, 9, true},
	}
	for _, tc := range cases {
		got := run(t, tc.s, func(c fiber.Ctx) string { return strconv.FormatBool(CanAccessBranch(c, tc.id)) })
		if got != strconv.FormatBool(tc.want) {
			t.Errorf("%s: got %s want %v", tc.name, got, tc.want)
		}
	}
}

func TestRecordReadableWritable(t *testing.T) {
	admin := ctxSetup{active: 1, viewAll: true, allowed: []uint{1}}
	seller := ctxSetup{active: 1, allowed: []uint{1}}
	check := func(name string, s ctxSetup, rec uint, read, write bool) {
		r := run(t, s, func(c fiber.Ctx) string { return strconv.FormatBool(RecordReadable(c, rec)) })
		w := run(t, s, func(c fiber.Ctx) string { return strconv.FormatBool(RecordWritable(c, rec)) })
		if r != strconv.FormatBool(read) || w != strconv.FormatBool(write) {
			t.Errorf("%s: read=%s write=%s, want %v/%v", name, r, w, read, write)
		}
	}
	check("vendedor registro propio", seller, 1, true, true)
	check("vendedor registro ajeno", seller, 2, false, false)
	check("admin lee ajeno pero NO escribe", admin, 2, true, false)
	check("registro legacy sin sucursal", seller, 0, true, true)
}

func TestResolveFiltersFailClosed(t *testing.T) {
	// Sin sucursal activa ni acceso total jamás debe resolverse a 0 ("todas").
	noCtx := ctxSetup{}
	if got := run(t, noCtx, func(c fiber.Ctx) string { return u(ResolveReadBranchFilter(c, 0)) }); got != u(NoBranchFilter) {
		t.Errorf("read sin sucursal: got %s", got)
	}
	if got := run(t, noCtx, func(c fiber.Ctx) string { return u(ResolveReportBranchFilter(c, 0, true)) }); got != u(NoBranchFilter) {
		t.Errorf("report sin sucursal: got %s", got)
	}
	// Un vendedor no puede pedir otra sucursal: vuelve a la suya.
	seller := ctxSetup{active: 1, allowed: []uint{1}}
	if got := run(t, seller, func(c fiber.Ctx) string { return u(ResolveReadBranchFilter(c, 2)) }); got != "1" {
		t.Errorf("vendedor pide ajena: got %s", got)
	}
	// Multi-sucursal sin acceso global: "todas" no existe para él.
	multi := ctxSetup{active: 1, allowed: []uint{1, 2}}
	if got := run(t, multi, func(c fiber.Ctx) string { return u(ResolveReportBranchFilter(c, 0, true)) }); got != "1" {
		t.Errorf("multi pide todas: got %s", got)
	}
	if got := run(t, multi, func(c fiber.Ctx) string { return u(ResolveReportBranchFilter(c, 2, true)) }); got != "2" {
		t.Errorf("multi pide asignada: got %s", got)
	}
}

func TestResolveWriteBranchID(t *testing.T) {
	seller := ctxSetup{active: 1, allowed: []uint{1}}
	got := run(t, seller, func(c fiber.Ctx) string {
		id, err := ResolveWriteBranchID(c, 2)
		if err == nil {
			return "ok" + u(id)
		}
		return "err"
	})
	if got != "err" {
		t.Errorf("vendedor escribe en ajena: got %s", got)
	}
	got = run(t, seller, func(c fiber.Ctx) string { id, _ := ResolveWriteBranchID(c, 0); return u(id) })
	if got != "1" {
		t.Errorf("sin pedir: got %s", got)
	}
}

// Ni siquiera quien ve todas las sucursales escribe en otra por el branch_id del body.
func TestResolveWriteBranchIDAdminMustUseActive(t *testing.T) {
	admin := ctxSetup{active: 1, viewAll: true, allowed: []uint{1, 2}}
	got := run(t, admin, func(c fiber.Ctx) string {
		id, err := ResolveWriteBranchID(c, 2)
		if err != nil {
			return "err"
		}
		return u(id)
	})
	if got != "err" {
		t.Errorf("admin con branch_id=2 y activa 1: %s, quiere err", got)
	}
	if got := run(t, admin, func(c fiber.Ctx) string { id, _ := ResolveWriteBranchID(c, 1); return u(id) }); got != "1" {
		t.Errorf("admin con su sucursal activa: %s", got)
	}
	if got := run(t, ctxSetup{viewAll: true}, func(c fiber.Ctx) string { _, err := ResolveWriteBranchID(c, 1); return fmt.Sprint(err != nil) }); got != "true" {
		t.Errorf("sin sucursal activa no se escribe: %s", got)
	}
}
