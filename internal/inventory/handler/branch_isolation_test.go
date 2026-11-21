package handler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http/httptest"
	"testing"

	"tukifac/pkg/database"

	"github.com/gofiber/fiber/v3"
	"gorm.io/gorm"
)

// Usuarios del escenario de aislamiento (Fase 3A): el middleware real deja en Locals active_branch_id,
// allowed_branch_ids e is_branch_admin; aquí se simulan por cabecera X-U.
//
//	s1: vendedor de la sucursal 1      c2: cajero de la sucursal 2      s3: usuario de la sucursal 3 (ajena a ambas)
//	admin: Administrador (ve todo) con la sucursal 1 activa      adminall: Administrador sin sucursal activa
func newIsolationApp(db *gorm.DB) *fiber.App {
	h := &InventoryHandler{}
	app := fiber.New()
	app.Use(func(c fiber.Ctx) error {
		c.Locals("tenantDB", db)
		c.Locals("user_id", uint(1))
		switch c.Get("X-U") {
		case "s1":
			c.Locals("active_branch_id", uint(1))
			c.Locals("allowed_branch_ids", []uint{1})
		case "c2":
			c.Locals("active_branch_id", uint(2))
			c.Locals("allowed_branch_ids", []uint{2})
		case "s3":
			c.Locals("active_branch_id", uint(3))
			c.Locals("allowed_branch_ids", []uint{3})
		case "admin":
			c.Locals("active_branch_id", uint(1))
			c.Locals("allowed_branch_ids", []uint{1})
			c.Locals("is_branch_admin", true)
		case "adminall":
			c.Locals("active_branch_id", uint(0))
			c.Locals("is_branch_admin", true)
		}
		return c.Next()
	})
	app.Get("/inventory/documents/:id", h.DocumentGetAPI)
	app.Put("/inventory/documents/:id", h.DocumentUpdateAPI)
	app.Post("/inventory/documents/:id/confirm", h.DocumentConfirmAPI)
	app.Post("/inventory/documents/:id/void", h.DocumentVoidAPI)
	app.Get("/inventory/stock-summary", h.StockSummaryAPI)
	app.Get("/inventory/stock/:productId", h.StockAPI)
	app.Get("/inventory/transfers", h.TransfersListAPI)
	app.Post("/inventory/transfer", h.TransferAPI)
	app.Post("/inventory/transfers/:id/confirm", h.TransferConfirmAPI)
	app.Post("/inventory/transfers/:id/cancel", h.TransferCancelAPI)
	app.Post("/inventory/transfers/:id/reverse", h.TransferReverseAPI)
	return app
}

func doReq(t *testing.T, app *fiber.App, user, method, path string, body any) (int, string) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req := httptest.NewRequest(method, path, rd)
	req.Header.Set("X-U", user)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := app.Test(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(out)
}

type isoFixture struct {
	app          *fiber.App
	db           *gorm.DB
	productID    uint
	docS1, docS2 uint
}

func setupIsolation(t *testing.T) isoFixture {
	t.Helper()
	db := setupInventoryMovementsTestDB(t)
	for i, name := range []string{"Principal", "Sucursal 2", "Sucursal 3"} {
		if err := db.Create(&database.TenantBranch{Name: name, Active: true, IsMain: i == 0}).Error; err != nil {
			t.Fatal(err)
		}
	}
	// Sucursal inactiva (id 4): no puede ser destino.
	inactive := database.TenantBranch{Name: "Cerrada", Active: true}
	if err := db.Create(&inactive).Error; err != nil {
		t.Fatal(err)
	}
	db.Model(&inactive).Update("active", false)

	p := database.TenantProduct{Code: "P1", Name: "Producto", Type: "product", Unit: "NIU", SalePrice: 5, ManageStock: true, Active: true}
	if err := db.Create(&p).Error; err != nil {
		t.Fatal(err)
	}
	for _, bid := range []uint{1, 2} {
		if err := db.Create(&database.TenantProductStock{ProductID: p.ID, BranchID: bid, Quantity: 100}).Error; err != nil {
			t.Fatal(err)
		}
	}
	d1 := database.TenantInventoryDocument{Number: "DRAFT-A", Direction: "IN", OperationTypeID: 1, BranchID: 1, Status: "draft", Source: "MANUAL", CreatedBy: 1}
	d2 := database.TenantInventoryDocument{Number: "DRAFT-B", Direction: "IN", OperationTypeID: 1, BranchID: 2, Status: "draft", Source: "MANUAL", CreatedBy: 1}
	if err := db.Create(&d1).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&d2).Error; err != nil {
		t.Fatal(err)
	}
	return isoFixture{app: newIsolationApp(db), db: db, productID: p.ID, docS1: d1.ID, docS2: d2.ID}
}

func TestInventoryDocuments_CrossBranchAccess(t *testing.T) {
	f := setupIsolation(t)
	get := func(u string, id uint) int {
		code, _ := doReq(t, f.app, u, "GET", fmt.Sprintf("/inventory/documents/%d", id), nil)
		return code
	}
	if c := get("s1", f.docS1); c != 200 {
		t.Errorf("vendedor S1 leyendo su documento: %d, quiere 200", c)
	}
	if c := get("s1", f.docS2); c != 404 {
		t.Errorf("vendedor S1 leyendo documento de S2: %d, quiere 404", c)
	}
	if c := get("c2", f.docS2); c != 200 {
		t.Errorf("cajero S2 leyendo su documento: %d, quiere 200", c)
	}
	if c := get("c2", f.docS1); c != 404 {
		t.Errorf("cajero S2 leyendo documento de S1: %d, quiere 404", c)
	}
	if c := get("admin", f.docS2); c != 200 {
		t.Errorf("admin (ve todo) leyendo documento de S2: %d, quiere 200", c)
	}
	// Escritura cruzada: 404 para quien no ve la sucursal.
	for _, path := range []string{"/inventory/documents/%d/confirm", "/inventory/documents/%d/void"} {
		if c, _ := doReq(t, f.app, "s1", "POST", fmt.Sprintf(path, f.docS2), nil); c != 404 {
			t.Errorf("vendedor S1 POST %s sobre doc de S2: %d, quiere 404", path, c)
		}
	}
	if c, _ := doReq(t, f.app, "s1", "PUT", fmt.Sprintf("/inventory/documents/%d", f.docS2), map[string]any{"notes": "x"}); c != 404 {
		t.Errorf("vendedor S1 PUT doc de S2: %d, quiere 404", c)
	}
	// El admin VE el documento de S2 pero no lo modifica con S1 activa.
	for _, path := range []string{"/inventory/documents/%d/confirm", "/inventory/documents/%d/void"} {
		if c, _ := doReq(t, f.app, "admin", "POST", fmt.Sprintf(path, f.docS2), nil); c != 403 {
			t.Errorf("admin con S1 activa POST %s sobre doc de S2: %d, quiere 403", path, c)
		}
	}
	if c, _ := doReq(t, f.app, "admin", "PUT", fmt.Sprintf("/inventory/documents/%d", f.docS2), map[string]any{"notes": "x"}); c != 403 {
		t.Errorf("admin con S1 activa PUT doc de S2: %d, quiere 403", c)
	}
	// Modo "todas" (sin sucursal activa): solo lectura.
	if c, _ := doReq(t, f.app, "adminall", "POST", fmt.Sprintf("/inventory/documents/%d/confirm", f.docS1), nil); c != 403 {
		t.Errorf("admin sin sucursal activa confirmando: %d, quiere 403", c)
	}
	// Caso propio sigue funcionando: la guarda deja pasar (el servicio puede responder 400 por falta de líneas, no 404/403).
	if c, _ := doReq(t, f.app, "s1", "POST", fmt.Sprintf("/inventory/documents/%d/confirm", f.docS1), nil); c == 404 || c == 403 {
		t.Errorf("vendedor S1 confirmando su propio documento: %d, la guarda no debe bloquearlo", c)
	}
	if c, _ := doReq(t, f.app, "s1", "GET", "/inventory/documents/9999", nil); c != 404 {
		t.Errorf("documento inexistente: %d, quiere 404", c)
	}
}

func createTransfer(t *testing.T, f isoFixture, user string, from, to uint) (int, uint) {
	t.Helper()
	code, body := doReq(t, f.app, user, "POST", "/inventory/transfer", map[string]any{
		"from_branch_id": from, "to_branch_id": to,
		"items": []map[string]any{{"product_id": f.productID, "quantity": 1}},
	})
	var out struct {
		TransferID uint `json:"transfer_id"`
	}
	_ = json.Unmarshal([]byte(body), &out)
	return code, out.TransferID
}

func TestInventoryTransfers_OriginDestinationRules(t *testing.T) {
	f := setupIsolation(t)

	// Origen ajeno / destino inválido.
	if c, _ := createTransfer(t, f, "s1", 2, 1); c != 403 {
		t.Errorf("vendedor S1 transfiriendo DESDE S2: %d, quiere 403", c)
	}
	if c, _ := createTransfer(t, f, "s3", 1, 2); c != 403 {
		t.Errorf("usuario S3 transfiriendo entre S1 y S2: %d, quiere 403", c)
	}
	if c, _ := createTransfer(t, f, "s1", 1, 1); c != 400 {
		t.Errorf("origen == destino: %d, quiere 400", c)
	}
	if c, _ := createTransfer(t, f, "s1", 1, 99); c != 400 {
		t.Errorf("destino inexistente: %d, quiere 400", c)
	}
	if c, _ := createTransfer(t, f, "s1", 1, 4); c != 400 {
		t.Errorf("destino inactivo: %d, quiere 400", c)
	}
	if c, _ := createTransfer(t, f, "adminall", 1, 2); c != 403 {
		t.Errorf("admin sin sucursal activa (modo todas) creando transferencia: %d, quiere 403", c)
	}
	// Ni el admin saca stock de una sucursal que no es la activa: debe cambiar de sucursal.
	if c, _ := createTransfer(t, f, "admin", 2, 1); c != 403 && c != 404 {
		t.Errorf("admin con S1 activa creando transferencia con origen S2: %d, quiere 403/404", c)
	}

	// Caso propio: S1 -> S2.
	code, tr1 := createTransfer(t, f, "s1", 1, 2)
	if code != 200 || tr1 == 0 {
		t.Fatalf("vendedor S1 transfiriendo desde su sucursal: %d, quiere 200", code)
	}
	_, tr2 := createTransfer(t, f, "s1", 1, 2)

	// Confirmar: solo el DESTINO (S2). El origen (S1) y un tercero no pueden.
	conf := func(u string, id uint) int {
		c, _ := doReq(t, f.app, u, "POST", fmt.Sprintf("/inventory/transfers/%d/confirm", id), nil)
		return c
	}
	if c := conf("s1", tr1); c != 403 {
		t.Errorf("el ORIGEN confirmando su propia transferencia: %d, quiere 403", c)
	}
	if c := conf("s3", tr1); c != 404 {
		t.Errorf("tercero confirmando: %d, quiere 404", c)
	}
	if c := conf("admin", tr1); c != 403 {
		t.Errorf("admin con S1 activa confirmando (destino S2): %d, quiere 403", c)
	}
	if c := conf("c2", tr1); c != 200 {
		t.Errorf("el DESTINO confirmando: %d, quiere 200", c)
	}

	// Cancelar: solo el ORIGEN (S1).
	canc := func(u string, id uint) int {
		c, _ := doReq(t, f.app, u, "POST", fmt.Sprintf("/inventory/transfers/%d/cancel", id), nil)
		return c
	}
	if c := canc("c2", tr2); c != 403 {
		t.Errorf("el destino cancelando: %d, quiere 403", c)
	}
	if c := canc("s3", tr2); c != 404 {
		t.Errorf("tercero cancelando: %d, quiere 404", c)
	}
	if c := canc("s1", tr2); c != 200 {
		t.Errorf("el origen cancelando: %d, quiere 200", c)
	}

	// Una transferencia ya confirmada no se puede confirmar otra vez (el servicio lo valida: 400, no 404/403 de guarda).
	if c := conf("c2", tr1); c != 400 {
		t.Errorf("confirmar dos veces: %d, quiere 400", c)
	}
}

func TestInventoryTransfers_ListFiltersByBranch(t *testing.T) {
	f := setupIsolation(t)
	_, _ = createTransfer(t, f, "s1", 1, 2) // S1 -> S2
	_, _ = createTransfer(t, f, "c2", 2, 1) // S2 -> S1
	// Transferencia S2 -> S3: se inserta directo (ningún usuario del escenario tiene S2 activa y S3 como destino a la vez).
	if err := f.db.Create(&database.TenantTransfer{FromBranchID: 2, ToBranchID: 3, Status: "pending", CreatedBy: 1}).Error; err != nil {
		t.Fatal(err)
	}
	count := func(u string) int {
		code, body := doReq(t, f.app, u, "GET", "/inventory/transfers", nil)
		if code != 200 {
			t.Fatalf("listado %s: %d %s", u, code, body)
		}
		var out struct {
			Data []json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal([]byte(body), &out); err != nil {
			t.Fatal(err)
		}
		return len(out.Data)
	}
	if n := count("s1"); n != 2 {
		t.Errorf("S1 ve %d transferencias, quiere 2 (origen o destino = S1)", n)
	}
	if n := count("c2"); n != 3 {
		t.Errorf("S2 ve %d transferencias, quiere 3", n)
	}
	if n := count("s3"); n != 1 {
		t.Errorf("S3 ve %d transferencias, quiere 1 (solo S2->S3)", n)
	}
	if n := count("admin"); n != 3 {
		t.Errorf("admin ve %d transferencias, quiere 3 (todas)", n)
	}
}

func TestInventoryTransfers_ReverseLegacyNeedsBothBranches(t *testing.T) {
	f := setupIsolation(t)
	row := database.TenantTransferLog{ProductID: f.productID, FromBranchID: 1, ToBranchID: 2, Quantity: 1, UserID: 1}
	if err := f.db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/inventory/transfers/%d/reverse", row.ID)
	if c, _ := doReq(t, f.app, "s1", "POST", path, nil); c != 404 {
		t.Errorf("S1 (solo origen) anulando transferencia legacy S1->S2: %d, quiere 404", c)
	}
	if c, _ := doReq(t, f.app, "s3", "POST", path, nil); c != 404 {
		t.Errorf("tercero anulando: %d, quiere 404", c)
	}
	// Quien accede a ambas (admin) pasa la guarda; el servicio decide el resto.
	if c, _ := doReq(t, f.app, "admin", "POST", path, nil); c == 404 || c == 403 {
		t.Errorf("admin (acceso a ambas) anulando: %d, la guarda no debe bloquearlo", c)
	}
}

func TestInventoryStock_BranchIDNotTrusted(t *testing.T) {
	f := setupIsolation(t)
	type stockResp struct {
		Data []struct {
			BranchID uint `json:"branch_id"`
		} `json:"data"`
	}
	stockOf := func(u, q string) []uint {
		code, body := doReq(t, f.app, u, "GET", fmt.Sprintf("/inventory/stock/%d%s", f.productID, q), nil)
		if code != 200 {
			t.Fatalf("stock: %d %s", code, body)
		}
		var out stockResp
		_ = json.Unmarshal([]byte(body), &out)
		ids := []uint{}
		for _, r := range out.Data {
			ids = append(ids, r.BranchID)
		}
		return ids
	}
	if got := stockOf("s1", "?branch_id=2"); len(got) != 1 || got[0] != 1 {
		t.Errorf("S1 pidiendo stock de S2 recibió sucursales %v, quiere solo [1]", got)
	}
	if got := stockOf("s1", ""); len(got) != 1 || got[0] != 1 {
		t.Errorf("S1 sin branch_id recibió sucursales %v, quiere solo [1]", got)
	}
	if got := stockOf("admin", "?branch_id=2"); len(got) != 1 || got[0] != 2 {
		t.Errorf("admin pidiendo S2 recibió %v, quiere [2]", got)
	}
	if got := stockOf("admin", ""); len(got) != 2 {
		t.Errorf("admin sin branch_id recibió %v, quiere ambas sucursales", got)
	}

	// stock-summary: S1 no puede leer los totales de S2 ni sumando "todas". S2 tiene más stock para distinguirlas.
	f.db.Model(&database.TenantProductStock{}).Where("branch_id = ?", 2).Update("quantity", 150)
	totals := func(u, q string) float64 {
		code, body := doReq(t, f.app, u, "GET", fmt.Sprintf("/inventory/stock-summary?product_ids=%d%s", f.productID, q), nil)
		if code != 200 {
			t.Fatalf("stock-summary: %d %s", code, body)
		}
		var o struct {
			Data map[string]float64 `json:"data"`
		}
		_ = json.Unmarshal([]byte(body), &o)
		return o.Data[fmt.Sprint(f.productID)]
	}
	if v := totals("s1", ""); v != 100 {
		t.Errorf("S1 sin branch_id vio total %v, quiere 100 (solo su sucursal)", v)
	}
	if v := totals("s1", "&branch_id=2"); v != 100 {
		t.Errorf("S1 con branch_id=2 vio %v, quiere 100 (no el de S2)", v)
	}
	if v := totals("admin", "&branch_id=2"); v != 150 {
		t.Errorf("admin con branch_id=2 vio %v, quiere 150", v)
	}
}
