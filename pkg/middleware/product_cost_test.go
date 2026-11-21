package middleware

import (
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"tukifac/pkg/restaurantperm"

	"github.com/gofiber/fiber/v3"
)

func costApp(claims *TenantClaims, restaurantPerms []string, path, body string) *fiber.App {
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		c.Locals("tenant_claims", claims)
		if restaurantPerms != nil {
			set := map[string]struct{}{}
			for _, p := range restaurantPerms {
				set[p] = struct{}{}
			}
			c.Locals(restaurantPermsLocal, set)
		}
		return c.Next()
	}, StripProductCost())
	app.Get(path, func(c fiber.Ctx) error {
		c.Set("Content-Type", "application/json")
		return c.SendString(body)
	})
	return app
}

func getBody(t *testing.T, app *fiber.App, path string) string {
	t.Helper()
	resp, err := app.Test(httptest.NewRequest("GET", path, nil))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

const costBody = `{"data":[{"id":12345678901234567,"sale_price":10.5,"purchase_price":7.25,"items":[{"purchase_price":3.1}]}],"total":1}`

func TestStripProductCost_OmiteCostoSinPermiso(t *testing.T) {
	got := getBody(t, costApp(&TenantClaims{Permissions: []string{"products.view"}}, nil, "/api/products", costBody), "/api/products")
	if strings.Contains(got, "7.25") || strings.Contains(got, "3.1") {
		t.Fatalf("el costo no debió viajar: %s", got)
	}
	if !strings.Contains(got, `"purchase_price":0`) {
		t.Fatalf("el costo debe quedar en 0: %s", got)
	}
	if !strings.Contains(got, "12345678901234567") || !strings.Contains(got, "10.5") {
		t.Fatalf("el resto del documento debe quedar intacto: %s", got)
	}
}

func TestStripProductCost_ConservaCostoConPermiso(t *testing.T) {
	cases := map[string]struct {
		claims *TenantClaims
		rest   []string
	}{
		"products.view_cost": {&TenantClaims{Permissions: []string{"products.view_cost"}}, nil},
		"products.manage":    {&TenantClaims{Permissions: []string{"products.manage"}}, nil},
		"gestion Tukichef":   {&TenantClaims{Permissions: []string{"products.view"}}, []string{restaurantperm.ProductsManage}},
	}
	for name, tc := range cases {
		got := getBody(t, costApp(tc.claims, tc.rest, "/api/products", costBody), "/api/products")
		if got != costBody {
			t.Errorf("%s: la respuesta no debe tocarse: %s", name, got)
		}
	}
}

func TestStripProductCost_ReporteDeUtilidadesExento(t *testing.T) {
	got := getBody(t, costApp(&TenantClaims{Permissions: []string{"sales.view"}}, nil, "/api/sales/profit-detail", costBody), "/api/sales/profit-detail")
	if got != costBody {
		t.Fatalf("profit-detail muestra costo por diseño: %s", got)
	}
}

func TestStripProductCost_PermisoCostoNoLoDaProductsView(t *testing.T) {
	// Tukichef de caja/mozo (sin g.p) tampoco ve el costo.
	got := getBody(t, costApp(&TenantClaims{Permissions: []string{"products.view"}}, []string{restaurantperm.POSUse}, "/api/products", costBody), "/api/products")
	if strings.Contains(got, "7.25") {
		t.Fatalf("POSUse no concede el costo: %s", got)
	}
}

func TestRequireStockViewAccess(t *testing.T) {
	cases := []struct {
		name   string
		claims *TenantClaims
		rest   []string
		want   int
	}{
		{"con pos.view_stock", &TenantClaims{Permissions: []string{"pos.view_stock"}}, nil, fiber.StatusOK},
		{"inventory.manage no lo da (vistas separadas)", &TenantClaims{Permissions: []string{"inventory.manage"}}, nil, fiber.StatusForbidden},
		{"inventory.view a secas no lo da", &TenantClaims{Permissions: []string{"inventory.view"}}, nil, fiber.StatusForbidden},
		{"sin permisos", &TenantClaims{Permissions: []string{"sales.pos"}}, nil, fiber.StatusForbidden},
		{"POS de PIN (puente)", &TenantClaims{}, []string{restaurantperm.POSUse}, fiber.StatusOK},
	}
	for _, tc := range cases {
		if got := statusForInventoryAccess(t, tc.claims, tc.rest, RequireStockViewAccess()); got != tc.want {
			t.Errorf("%s: status %d, quería %d", tc.name, got, tc.want)
		}
	}
}

func TestProductsManageSigueImplicandoElCosto(t *testing.T) {
	if !tenantHasPermission([]string{"products.manage"}, PermProductsViewCost) {
		t.Fatal("products.manage debe seguir concediendo todas las acciones de products")
	}
	if !tenantHasPermission([]string{"inventory.manage"}, "inventory.view") {
		t.Fatal("inventory.manage debe seguir implicando inventory.view")
	}
	if tenantHasPermission([]string{"products.view_cost"}, PermPOSViewCost) || tenantHasPermission([]string{"pos.view_cost"}, PermProductsViewCost) {
		t.Fatal("el costo del POS y el de Productos son permisos independientes")
	}
	if tenantHasPermission([]string{"products.view", "products.edit"}, PermProductsViewCost) {
		t.Fatal("editar productos no debe dar el costo")
	}
}
