package handler

import (
	"strings"
	"testing"
	"time"

	"tukifac/internal/sales/branchguard/branchtest"
	"tukifac/pkg/database"

	"github.com/gofiber/fiber/v3"
	"gorm.io/gorm"
)

// Aislamiento entre sucursales en ventas (Fase 3A): vendedor de S1, cajero de S2 y administrador
// con S1 activa. Cada caso intenta tocar un registro de la OTRA sucursal y exige 404; el caso
// propio debe seguir funcionando.

type saleFixture struct {
	db                     *gorm.DB
	sale1, sale2           uint // nota de venta pagada de S1 / S2
	note2, note1           uint // nota de crédito aceptada de S2 / S1
	sess1, sess2           uint // caja abierta de S1 / S2
	mov1, mov2             uint // cobro de una venta anulada, sin devolver (S1 / S2)
	cancelled1, cancelled2 uint
	quotation1, quotation2 uint
	series1                uint
	_                      struct{}
}

func newSaleFixture(t *testing.T) *saleFixture {
	t.Helper()
	db := branchtest.OpenDB(t,
		&database.TenantBranch{}, &database.TenantDocumentSeries{}, &database.TenantSale{},
		&database.TenantSaleItem{}, &database.TenantSalePayment{}, &database.TenantInvoice{},
		&database.TenantCashSession{}, &database.TenantCashMovement{}, &database.TenantQuotation{},
		&database.TenantContact{}, &database.TenantCompanyConfig{}, &database.TenantPaymentMethod{},
		&database.TenantProduct{}, &database.TenantProductStock{}, &database.TenantStockMovement{},
		&database.TenantInventoryOperationType{}, &database.TenantBankMovement{}, &database.TenantBankAccount{},
		&database.TenantProductSerial{}, &database.TenantSalePrepaymentApplication{}, &database.TenantSalePrepaymentVoucher{},
	)
	if err := database.SeedInventoryOperationTypes(db); err != nil {
		t.Fatal(err)
	}
	f := &saleFixture{db: db}
	for _, b := range []database.TenantBranch{{ID: 1, Name: "S1", Active: true, IsMain: true}, {ID: 2, Name: "S2", Active: true}} {
		b := b
		if err := db.Create(&b).Error; err != nil {
			t.Fatal(err)
		}
	}
	ser := database.TenantDocumentSeries{BranchID: 1, DocType: "Nota de venta", SunatCode: "00", Category: "venta", Series: "NV01", Active: true}
	db.Create(&ser)
	f.series1 = ser.ID
	mkSale := func(branchID uint, num, docType, status string) uint {
		s := database.TenantSale{BranchID: branchID, UserID: 1, SeriesID: ser.ID, DocType: docType, Series: "NV01",
			Number: num, IssueDate: time.Now(), Total: 100, Status: status, BillingStatus: "pending"}
		if err := db.Create(&s).Error; err != nil {
			t.Fatal(err)
		}
		return s.ID
	}
	f.sale1 = mkSale(1, "NV01-1", "NOTA_VENTA", "paid")
	f.sale2 = mkSale(2, "NV01-2", "NOTA_VENTA", "paid")
	f.cancelled1 = mkSale(1, "NV01-3", "NOTA_VENTA", "cancelled")
	f.cancelled2 = mkSale(2, "NV01-4", "NOTA_VENTA", "cancelled")
	f.note1 = mkSale(1, "NC01-1", "NOTA_CREDITO", "paid")
	f.note2 = mkSale(2, "NC01-2", "NOTA_CREDITO", "paid")
	mkSess := func(branchID uint) uint {
		s := database.TenantCashSession{BranchID: branchID, UserID: 1, OpenedBy: 1, Status: "open", OpenedAt: time.Now()}
		if err := db.Create(&s).Error; err != nil {
			t.Fatal(err)
		}
		return s.ID
	}
	f.sess1, f.sess2 = mkSess(1), mkSess(2)
	mkMov := func(sess, sale uint) uint {
		m := database.TenantCashMovement{CashSessionID: sess, Type: "income", Amount: 100, PaymentMethod: "cash",
			Category: "Venta", SaleID: &sale, UserID: 1, CreatedAt: time.Now()}
		if err := db.Create(&m).Error; err != nil {
			t.Fatal(err)
		}
		return m.ID
	}
	f.mov1, f.mov2 = mkMov(f.sess1, f.cancelled1), mkMov(f.sess2, f.cancelled2)
	mkQuo := func(branchID uint) uint {
		q := database.TenantQuotation{BranchID: branchID, UserID: 1, SeriesID: ser.ID, Series: "C001", Status: "draft", IssueDate: time.Now()}
		if err := db.Create(&q).Error; err != nil {
			t.Fatal(err)
		}
		return q.ID
	}
	f.quotation1, f.quotation2 = mkQuo(1), mkQuo(2)
	return f
}

func (f *saleFixture) app(a branchtest.Actor) *fiber.App {
	h := NewSaleHandler()
	app := branchtest.NewApp(f.db, a)
	app.Get("/sales/:id", h.GetAPI)
	app.Post("/sales", h.CreateAPI)
	app.Post("/sales/:id/cancel", h.CancelAPI)
	app.Post("/sales/:id/void-rejected", h.VoidRejectedAPI)
	app.Post("/sales/:id/issue-electronic", h.IssueElectronicFromNotaAPI)
	app.Post("/sales/:id/email-receipt", h.EmailReceiptAPI)
	app.Post("/sales/pending-refunds/apply", h.ApplyPendingRefundAPI)
	app.Post("/sales/pending-refunds/apply-note", h.ApplyPendingNoteRefundAPI)
	return app
}

func path(format string, id uint) string {
	return strings.Replace(format, ":id", itoa(id), 1)
}

func itoa(n uint) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}

func saleStatus(t *testing.T, db *gorm.DB, id uint) string {
	t.Helper()
	var s database.TenantSale
	if err := db.First(&s, id).Error; err != nil {
		t.Fatal(err)
	}
	return s.Status
}

func TestSaleGetByIDIsolation(t *testing.T) {
	f := newSaleFixture(t)
	cases := []struct {
		actor branchtest.Actor
		id    uint
		want  int
	}{
		{branchtest.Vendedor1, f.sale2, 404}, // S1 no ve la venta de S2
		{branchtest.Cajero2, f.sale1, 404},   // S2 no ve la venta de S1
		{branchtest.Vendedor1, f.sale1, 200}, // lo propio sigue funcionando
		{branchtest.Cajero2, f.sale2, 200},
		{branchtest.Admin, f.sale2, 200}, // el admin SÍ lee otras sucursales
		{branchtest.Vendedor1, 9999, 404},
	}
	for _, tc := range cases {
		code, body := branchtest.Do(t, f.app(tc.actor), "GET", path("/sales/:id", tc.id), "")
		if code != tc.want {
			t.Errorf("%s GET /sales/%d: got %d want %d (%s)", tc.actor.Name, tc.id, code, tc.want, body)
		}
	}
}

func TestSaleWritesIsolation(t *testing.T) {
	f := newSaleFixture(t)
	// Cada fila: endpoint de escritura sobre la venta de S2 (sale2) que debe responder 404.
	for _, ep := range []struct{ name, route, body string }{
		{"cancel", "/sales/:id/cancel", `{"reason":"x"}`},
		{"void-rejected", "/sales/:id/void-rejected", `{"reason":"x"}`},
		{"issue-electronic", "/sales/:id/issue-electronic", `{"series_id":1}`},
		{"email-receipt", "/sales/:id/email-receipt", `{"email":"a@b.c"}`},
	} {
		// vendedor de S1 sobre venta de S2
		if code, body := branchtest.Do(t, f.app(branchtest.Vendedor1), "POST", path(ep.route, f.sale2), ep.body); code != 404 {
			t.Errorf("%s: vendedor S1 sobre venta de S2: got %d want 404 (%s)", ep.name, code, body)
		}
		// cajero de S2 sobre venta de S1
		if code, body := branchtest.Do(t, f.app(branchtest.Cajero2), "POST", path(ep.route, f.sale1), ep.body); code != 404 {
			t.Errorf("%s: cajero S2 sobre venta de S1: got %d want 404 (%s)", ep.name, code, body)
		}
	}
	// El administrador con S1 activa lee S2 pero NO la escribe (cancel/void/issue/email-receipt de S2).
	for _, ep := range []struct{ name, route, body string }{
		{"cancel", "/sales/:id/cancel", `{"reason":"x"}`},
		{"void-rejected", "/sales/:id/void-rejected", `{"reason":"x"}`},
		{"issue-electronic", "/sales/:id/issue-electronic", `{"series_id":1}`},
	} {
		if code, body := branchtest.Do(t, f.app(branchtest.Admin), "POST", path(ep.route, f.sale2), ep.body); code != 404 {
			t.Errorf("%s: admin(S1 activa) sobre venta de S2: got %d want 404 (%s)", ep.name, code, body)
		}
	}
	if st := saleStatus(t, f.db, f.sale2); st != "paid" {
		t.Fatalf("la venta de S2 no debía cambiar: status=%s", st)
	}
}

func TestSaleCancelOwnStillWorks(t *testing.T) {
	f := newSaleFixture(t)
	code, body := branchtest.Do(t, f.app(branchtest.Vendedor1), "POST", path("/sales/:id/cancel", f.sale1), `{"reason":"prueba"}`)
	if code == 404 {
		t.Fatalf("la guarda bloqueó una venta propia: %d %s", code, body)
	}
	if code != 200 {
		t.Fatalf("anular una venta propia debe seguir funcionando: %d %s", code, body)
	}
	if st := saleStatus(t, f.db, f.sale1); st != "cancelled" {
		t.Fatalf("la venta propia debía quedar anulada: %s", st)
	}
	// El admin con S1 activa también anula lo de su sucursal activa.
	t.Run("admin", func(t *testing.T) {
		f2 := newSaleFixture(t)
		if code, body := branchtest.Do(t, f2.app(branchtest.Admin), "POST", path("/sales/:id/cancel", f2.sale1), `{"reason":"prueba"}`); code != 200 {
			t.Fatalf("admin sobre su sucursal activa debe poder anular: %d %s", code, body)
		}
	})
}

func TestPendingRefundsApplyIsolation(t *testing.T) {
	f := newSaleFixture(t)
	apply := func(a branchtest.Actor, mov, sess uint) int {
		code, _ := branchtest.Do(t, f.app(a), "POST", "/sales/pending-refunds/apply",
			`{"cash_movement_id":`+itoa(mov)+`,"cash_session_id":`+itoa(sess)+`,"reason":"x"}`)
		return code
	}
	if c := apply(branchtest.Vendedor1, f.mov2, f.sess2); c != 404 { // movimiento y caja ajenos
		t.Errorf("movimiento+caja de S2 desde S1: got %d want 404", c)
	}
	if c := apply(branchtest.Vendedor1, f.mov1, f.sess2); c != 404 { // devolución propia, caja ajena
		t.Errorf("caja destino de S2 desde S1: got %d want 404", c)
	}
	if c := apply(branchtest.Vendedor1, f.mov2, f.sess1); c != 404 { // devolución ajena, caja propia
		t.Errorf("movimiento de S2 en caja de S1: got %d want 404", c)
	}
	if c := apply(branchtest.Admin, f.mov2, f.sess2); c != 404 { // admin con S1 activa no escribe en S2
		t.Errorf("admin(S1) sobre S2: got %d want 404", c)
	}
	var n int64
	f.db.Model(&database.TenantCashMovement{}).Where("type = ?", "expense").Count(&n)
	if n != 0 {
		t.Fatalf("no debía crearse ningún egreso, hay %d", n)
	}
	// Caso propio: movimiento y caja de S1 -> crea el egreso de reversión.
	if c := apply(branchtest.Vendedor1, f.mov1, f.sess1); c != 200 {
		t.Errorf("devolución propia: got %d want 200", c)
	}
	f.db.Model(&database.TenantCashMovement{}).Where("type = ? AND cash_session_id = ?", "expense", f.sess1).Count(&n)
	if n != 1 {
		t.Errorf("debía crearse 1 egreso en la caja de S1, hay %d", n)
	}
}

func TestPendingNoteRefundApplyIsolation(t *testing.T) {
	f := newSaleFixture(t)
	apply := func(a branchtest.Actor, note, sess uint) int {
		code, _ := branchtest.Do(t, f.app(a), "POST", "/sales/pending-refunds/apply-note",
			`{"note_sale_id":`+itoa(note)+`,"cash_session_id":`+itoa(sess)+`,"reason":"x"}`)
		return code
	}
	if c := apply(branchtest.Vendedor1, f.note2, f.sess2); c != 404 {
		t.Errorf("nota+caja de S2 desde S1: got %d want 404", c)
	}
	if c := apply(branchtest.Vendedor1, f.note1, f.sess2); c != 404 {
		t.Errorf("nota propia con caja de S2: got %d want 404", c)
	}
	if c := apply(branchtest.Admin, f.note2, f.sess2); c != 404 {
		t.Errorf("admin(S1) sobre S2: got %d want 404", c)
	}
	// Caso propio: la nota de crédito y la caja son de S1. Debe pasar las guardas (el servicio
	// puede rechazar por otras reglas, pero nunca con 404).
	if c := apply(branchtest.Vendedor1, f.note1, f.sess1); c == 404 {
		t.Errorf("caso propio bloqueado por la guarda: %d", c)
	}
}

func TestCreateSaleFromForeignQuotationIsolation(t *testing.T) {
	f := newSaleFixture(t)
	body := `{"series_id":` + itoa(f.series1) + `,"doc_type":"NOTA_VENTA","from_quotation_id":` + itoa(f.quotation2) + `,"items":[]}`
	for _, a := range []branchtest.Actor{branchtest.Vendedor1, branchtest.Admin} {
		code, resp := branchtest.Do(t, f.app(a), "POST", "/sales", body)
		if code != 404 {
			t.Errorf("%s: venta desde cotización de S2: got %d want 404 (%s)", a.Name, code, resp)
		}
	}
	var q database.TenantQuotation
	f.db.First(&q, f.quotation2)
	if q.Status != "draft" {
		t.Errorf("la cotización de S2 no debía consumirse: %s", q.Status)
	}
	// Cotización propia: pasa la guarda (después puede fallar por otras validaciones, nunca 404).
	own := `{"series_id":` + itoa(f.series1) + `,"doc_type":"NOTA_VENTA","from_quotation_id":` + itoa(f.quotation1) + `,"items":[]}`
	if code, resp := branchtest.Do(t, f.app(branchtest.Vendedor1), "POST", "/sales", own); code == 404 {
		t.Errorf("cotización propia bloqueada: %d %s", code, resp)
	}
}
