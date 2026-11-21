package branchguard_test

import (
	"fmt"
	"io"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"tukifac/internal/sales/branchguard"
	"tukifac/pkg/branchscope"
	"tukifac/pkg/database"

	"github.com/glebarez/sqlite"
	"github.com/gofiber/fiber/v3"
	"gorm.io/gorm"
)

var seq atomic.Int64

// Las guardas infieren la sucursal de documentos relacionados (compra/venta origen, serie, comprobante por serie y
// correlativo). Con el scope global de sucursal instalado esas búsquedas no deben ejecutarse acotadas: si no ven el registro
// ajeno lo tomarían por "documento sin sucursal" y lo dejarían pasar (fail-open).
func TestGuardsInferBranchOutsideScope(t *testing.T) {
	raw, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s_%d?mode=memory&cache=shared", t.Name(), seq.Add(1))), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.ApplyBaselineSchema(raw); err != nil {
		t.Fatal(err)
	}
	if err := branchscope.Install(raw, database.BranchScopedModels()...); err != nil {
		t.Fatal(err)
	}
	sys := branchscope.System(raw, "test: sembrado")

	// Datos de la sucursal 2: compra, venta, serie y comprobantes derivados.
	purchase := database.TenantPurchase{BranchID: 2}
	sale := database.TenantSale{BranchID: 2, Series: "F002", Correlative: 7}
	series := database.TenantDocumentSeries{BranchID: 2, Series: "R002", DocType: "RETENCION", Active: true}
	for _, v := range []interface{}{&purchase, &sale, &series} {
		if err := sys.Create(v).Error; err != nil {
			t.Fatal(err)
		}
	}
	retByPurchase := database.TenantRetention{PurchaseID: &purchase.ID, Series: "R002", Correlative: "1", FechaEmision: time.Now()}
	retBySeries := database.TenantRetention{Series: "R002", Correlative: "2", FechaEmision: time.Now()}
	perByPurchaseSale := database.TenantPerception{SourceSaleID: &sale.ID, Series: "P002", Correlative: "1", FechaEmision: time.Now()}
	for _, v := range []interface{}{&retByPurchase, &retBySeries, &perByPurchaseSale} {
		if err := sys.Create(v).Error; err != nil {
			t.Fatal(err)
		}
	}

	// Solicitud de un vendedor de la sucursal 1: db con scope.
	scopedDB := branchscope.Attach(raw, &branchscope.Scope{ActiveBranchID: 1, AllowedBranchIDs: []uint{1}})
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		c.Locals("active_branch_id", uint(1))
		c.Locals("allowed_branch_ids", []uint{1})
		return c.Next()
	})
	check := func(name string, wantReadable bool, f func(c fiber.Ctx) bool) {
		t.Helper()
		app.Get("/"+name, func(c fiber.Ctx) error { return c.SendString(fmt.Sprint(f(c))) })
		resp, _ := app.Test(httptest.NewRequest("GET", "/"+name, nil))
		b, _ := io.ReadAll(resp.Body)
		if string(b) != fmt.Sprint(wantReadable) {
			t.Errorf("%s: legible=%s, quiere %v", name, b, wantReadable)
		}
	}

	check("ret_por_compra", false, func(c fiber.Ctx) bool { return branchguard.RetentionReadable(c, scopedDB, retByPurchase.ID) == nil })
	check("ret_por_serie", false, func(c fiber.Ctx) bool { return branchguard.RetentionReadable(c, scopedDB, retBySeries.ID) == nil })
	check("per_por_venta", false, func(c fiber.Ctx) bool {
		return branchguard.PerceptionReadable(c, scopedDB, perByPurchaseSale.ID) == nil
	})
	// Comprobante ajeno referenciado por serie/correlativo en una baja o reversión: no es "externo", es de otra sucursal.
	check("doc_venta_ajena_legible", false, func(c fiber.Ctx) bool {
		return branchguard.LinesReadable(c, scopedDB, `{"details":[{"tipoDoc":"01","serie":"F002","correlativo":"0000007"}]}`)
	})
	check("doc_venta_ajena_escribible", false, func(c fiber.Ctx) bool {
		return branchguard.LineWritable(c, scopedDB, "01", "F002", "0000007")
	})
	check("doc_retencion_ajena_escribible", false, func(c fiber.Ctx) bool {
		return branchguard.LineWritable(c, scopedDB, "20", "R002", "1")
	})
	// Un documento realmente externo (no existe localmente) sigue siendo del tenant.
	check("doc_externo_escribible", true, func(c fiber.Ctx) bool {
		return branchguard.LineWritable(c, scopedDB, "01", "F999", "0000001")
	})
}
