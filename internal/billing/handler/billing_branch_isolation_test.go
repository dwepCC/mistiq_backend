package handler

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"tukifac/internal/sales/branchguard/branchtest"
	"tukifac/pkg/database"

	"github.com/gofiber/fiber/v3"
	"gorm.io/gorm"
)

// Aislamiento entre sucursales en facturación (Fase 3A): vendedor de S1, cajero de S2 y
// administrador con S1 activa.

type billFixture struct {
	db                   *gorm.DB
	sale1, sale2         uint // boleta de S1 / S2 (con registro fiscal)
	desp1, desp2         uint
	ret1, ret2           uint // retención de S1 (compra de S1) / de S2 (serie de S2)
	per1, per2           uint
	voided1, voided2     uint
	rev1, rev2           uint
	summary              uint
	purchase1, purchase2 uint
}

func u(n uint) string { return strconv.FormatUint(uint64(n), 10) }

func newBillFixture(t *testing.T) *billFixture {
	t.Helper()
	db := branchtest.OpenDB(t,
		&database.TenantBranch{}, &database.TenantDocumentSeries{}, &database.TenantSale{},
		&database.TenantSaleItem{}, &database.TenantInvoice{}, &database.TenantDespatch{},
		&database.TenantRetention{}, &database.TenantPerception{}, &database.TenantSunatVoided{},
		&database.TenantSunatReversion{}, &database.TenantSunatSummary{}, &database.TenantPurchase{},
		&database.TenantCompanyConfig{}, &database.TenantContact{},
	)
	f := &billFixture{db: db}
	for _, b := range []database.TenantBranch{{ID: 1, Name: "S1", Active: true, IsMain: true}, {ID: 2, Name: "S2", Active: true}} {
		b := b
		if err := db.Create(&b).Error; err != nil {
			t.Fatal(err)
		}
	}
	mustCreate := func(v interface{}) {
		if err := db.Create(v).Error; err != nil {
			t.Fatal(err)
		}
	}
	// Series por sucursal (las de retención/percepción identifican la sucursal de esos documentos).
	for _, s := range []database.TenantDocumentSeries{
		{BranchID: 1, DocType: "Boleta", SunatCode: "03", Category: "venta", Series: "B001", Active: true},
		{BranchID: 2, DocType: "Boleta", SunatCode: "03", Category: "venta", Series: "B002", Active: true},
		{BranchID: 1, DocType: "Retención", SunatCode: "20", Category: "retencion", Series: "R001", Active: true},
		{BranchID: 2, DocType: "Retención", SunatCode: "20", Category: "retencion", Series: "R002", Active: true},
		{BranchID: 1, DocType: "Percepción", SunatCode: "40", Category: "percepcion", Series: "P001", Active: true},
		{BranchID: 2, DocType: "Percepción", SunatCode: "40", Category: "percepcion", Series: "P002", Active: true},
	} {
		s := s
		mustCreate(&s)
	}
	mkSale := func(branchID uint, series string, corr uint) uint {
		s := database.TenantSale{BranchID: branchID, UserID: 1, SeriesID: 1, DocType: "BOLETA", Series: series, Correlative: corr,
			Number: series + "-" + u(corr), IssueDate: time.Now(), Total: 50, Status: "paid", BillingStatus: "pending"}
		mustCreate(&s)
		mustCreate(&database.TenantInvoice{SaleID: s.ID, SunatStatus: "pending", JobStatus: "pending"})
		return s.ID
	}
	f.sale1, f.sale2 = mkSale(1, "B001", 1), mkSale(2, "B002", 1)
	// Boletas aceptadas (para baja) y pendientes (campanita): una extra por sucursal.
	mkSale(1, "B001", 2)
	mkSale(2, "B002", 2)

	mkDesp := func(branchID uint, series string, corr uint) uint {
		d := database.TenantDespatch{BranchID: branchID, SeriesID: 1, Series: series, Correlative: corr, IssueDate: time.Now(), Status: "pending"}
		mustCreate(&d)
		return d.ID
	}
	f.desp1, f.desp2 = mkDesp(1, "T001", 7), mkDesp(2, "T002", 7)

	mkPurchase := func(branchID uint) uint {
		p := database.TenantPurchase{BranchID: branchID, UserID: 1, DocType: "FACTURA", Series: "F1", Number: "1", IssueDate: time.Now(), Status: "paid"}
		mustCreate(&p)
		return p.ID
	}
	f.purchase1, f.purchase2 = mkPurchase(1), mkPurchase(2)

	mkRet := func(series string, purchase *uint) uint {
		r := database.TenantRetention{Series: series, Correlative: "1", FechaEmision: time.Now(), PurchaseID: purchase, Status: "pending"}
		mustCreate(&r)
		return r.ID
	}
	f.ret1 = mkRet("R001", &f.purchase1) // sucursal por compra origen
	f.ret2 = mkRet("R002", nil)          // sucursal por serie
	mkPer := func(series string) uint {
		p := database.TenantPerception{Series: series, Correlative: "1", FechaEmision: time.Now(), Status: "pending"}
		mustCreate(&p)
		return p.ID
	}
	f.per1, f.per2 = mkPer("P001"), mkPer("P002")

	mkVoided := func(serie string) uint {
		v := database.TenantSunatVoided{FecComunicacion: time.Now(), Correlativo: "1", Status: "pending",
			PayloadJSON: `{"details":[{"tipoDoc":"03","serie":"` + serie + `","correlativo":"1","desMotivoBaja":"x"}]}`}
		mustCreate(&v)
		return v.ID
	}
	f.voided1, f.voided2 = mkVoided("B001"), mkVoided("B002")
	mkRev := func(serie string) uint {
		v := database.TenantSunatReversion{FecComunicacion: time.Now(), Correlativo: "1", Status: "pending",
			PayloadJSON: `{"details":[{"tipoDoc":"20","serie":"` + serie + `","correlativo":"1","desMotivoBaja":"x"}]}`}
		mustCreate(&v)
		return v.ID
	}
	f.rev1, f.rev2 = mkRev("R001"), mkRev("R002")
	sum := database.TenantSunatSummary{FecResumen: time.Now(), Correlativo: "1", Status: "pending"}
	mustCreate(&sum)
	f.summary = sum.ID
	return f
}

func (f *billFixture) app(a branchtest.Actor) *fiber.App {
	h := NewBillingHandler()
	app := branchtest.NewApp(f.db, a)
	app.Post("/billing/send/:saleId", h.SendToSUNAT)
	app.Get("/billing/status/:saleId", h.GetBillingStatus)
	app.Get("/billing/job/:saleId", h.GetBillingJobStatus)
	app.Post("/billing/resend/:saleId", h.ResendToSUNAT)
	app.Post("/billing/void-with-credit-note/:saleId", h.VoidWithCreditNoteAPI)
	app.Post("/billing/debit-notes/:saleId", h.CreateDebitNoteAPI)
	app.Post("/billing/notes/independent", h.CreateIndependentNoteAPI)
	app.Get("/billing/invoice/:saleId", h.GetInvoiceAPI)
	app.Get("/billing/invoice/:saleId/document/:kind", h.GetInvoiceDocumentAPI)
	app.Get("/billing/summaries", h.ListSummariesAPI)
	app.Post("/billing/summaries", h.CreateSummaryAPI)
	app.Get("/billing/summaries/:id/status", h.GetSummaryStatusAPI)
	app.Get("/billing/voided", h.ListVoidedAPI)
	app.Post("/billing/voided", h.CreateVoidedAPI)
	app.Get("/billing/voided/:id/status", h.GetVoidedStatusAPI)
	app.Get("/billing/notification-counts", h.NotificationCountsAPI)
	app.Get("/billing/invoice-status", h.ConsultInvoiceStatusAPI)
	app.Get("/billing/despatches", h.ListDespatchesAPI)
	app.Get("/billing/despatches/lookup", h.LookupDespatchAPI)
	app.Post("/billing/despatches", h.CreateDespatchAPI)
	app.Get("/billing/despatches/:id/status", h.GetDespatchStatusAPI)
	app.Get("/billing/retentions", h.ListRetentionsAPI)
	app.Post("/billing/retentions", h.CreateRetentionAPI)
	app.Get("/billing/retentions/:id/status", h.GetRetentionStatusAPI)
	app.Get("/billing/perceptions", h.ListPerceptionsAPI)
	app.Post("/billing/perceptions", h.CreatePerceptionAPI)
	app.Get("/billing/perceptions/:id/status", h.GetPerceptionStatusAPI)
	app.Get("/billing/reversions", h.ListReversionsAPI)
	app.Post("/billing/reversions", h.CreateReversionAPI)
	app.Get("/billing/reversions/:id/status", h.GetReversionStatusAPI)
	return app
}

func (f *billFixture) expect(t *testing.T, a branchtest.Actor, method, path, body string, want int) string {
	t.Helper()
	code, resp := branchtest.Do(t, f.app(a), method, path, body)
	if code != want {
		t.Errorf("%s %s %s %s: got %d want %d (%s)", a.Name, method, path, body, code, want, resp)
	}
	return resp
}

// notNotFound: el caso propio debe pasar la guarda (el servicio puede responder otros errores,
// p. ej. 400 por facturador no configurado en el test, pero nunca 404/403 de sucursal).
func (f *billFixture) notNotFound(t *testing.T, a branchtest.Actor, method, path, body string) {
	t.Helper()
	code, resp := branchtest.Do(t, f.app(a), method, path, body)
	if code == 404 || code == 403 {
		t.Errorf("%s %s %s: la guarda bloqueó un caso propio: %d (%s)", a.Name, method, path, code, resp)
	}
}

func TestBillingBySaleIDIsolation(t *testing.T) {
	f := newBillFixture(t)
	v, c, ad := branchtest.Vendedor1, branchtest.Cajero2, branchtest.Admin

	writes := []struct{ method, path, body string }{
		{"POST", "/billing/send/", ""},
		{"POST", "/billing/resend/", ""},
		{"POST", "/billing/void-with-credit-note/", `{"reason":"x"}`},
		{"POST", "/billing/debit-notes/", `{"reason":"x"}`},
	}
	reads := []struct{ method, path string }{
		{"GET", "/billing/status/"},
		{"GET", "/billing/job/"},
		{"GET", "/billing/invoice/"},
	}
	for _, w := range writes {
		f.expect(t, v, w.method, w.path+u(f.sale2), w.body, 404)  // S1 -> S2
		f.expect(t, c, w.method, w.path+u(f.sale1), w.body, 404)  // S2 -> S1
		f.expect(t, ad, w.method, w.path+u(f.sale2), w.body, 404) // admin (S1 activa) NO escribe en S2
		f.notNotFound(t, v, w.method, w.path+u(f.sale1), w.body)  // lo propio sigue
		f.notNotFound(t, ad, w.method, w.path+u(f.sale1), w.body) // admin en su sucursal activa
	}
	for _, r := range reads {
		f.expect(t, v, r.method, r.path+u(f.sale2), "", 404)
		f.expect(t, c, r.method, r.path+u(f.sale1), "", 404)
		f.notNotFound(t, ad, r.method, r.path+u(f.sale2), "") // admin SÍ lee otras sucursales
		f.notNotFound(t, v, r.method, r.path+u(f.sale1), "")
	}
	// Documentos (XML/CDR/PDF)
	for _, kind := range []string{"xml", "cdr", "pdf", "xml-generated"} {
		f.expect(t, v, "GET", "/billing/invoice/"+u(f.sale2)+"/document/"+kind, "", 404)
		f.expect(t, c, "GET", "/billing/invoice/"+u(f.sale1)+"/document/"+kind, "", 404)
	}
	// Lectura propia del registro fiscal devuelve 200.
	f.expect(t, v, "GET", "/billing/invoice/"+u(f.sale1), "", 200)
	f.expect(t, ad, "GET", "/billing/invoice/"+u(f.sale2), "", 200)
}

func TestIndependentNoteBranchFromBody(t *testing.T) {
	f := newBillFixture(t)
	// branch_id del body de otra sucursal -> 403 con código de sucursal.
	resp := f.expect(t, branchtestVendedor(), "POST", "/billing/notes/independent", `{"branch_id":2,"doc_type":"07"}`, 403)
	if !strings.Contains(resp, "BRANCH_FORBIDDEN") {
		t.Errorf("debía incluir el código BRANCH_FORBIDDEN: %s", resp)
	}
	// Propia (o vacía = activa): pasa la guarda; el servicio la rechaza después por otras reglas (400).
	f.notNotFound(t, branchtestVendedor(), "POST", "/billing/notes/independent", `{"branch_id":1,"doc_type":"07"}`)
	f.notNotFound(t, branchtestVendedor(), "POST", "/billing/notes/independent", `{"doc_type":"07"}`)
}

func branchtestVendedor() branchtest.Actor { return branchtest.Vendedor1 }

type idList struct {
	Despatches  []struct{ ID uint } `json:"despatches"`
	Retentions  []struct{ ID uint } `json:"retentions"`
	Perceptions []struct{ ID uint } `json:"perceptions"`
	Reversions  []struct{ ID uint } `json:"reversions"`
	Voided      []struct{ ID uint } `json:"voided"`
	Summaries   []struct{ ID uint } `json:"summaries"`
}

func ids(t *testing.T, body, key string) []uint {
	t.Helper()
	var l idList
	if err := json.Unmarshal([]byte(body), &l); err != nil {
		t.Fatalf("respuesta no es JSON: %v (%s)", err, body)
	}
	var out []uint
	switch key {
	case "despatches":
		for _, x := range l.Despatches {
			out = append(out, x.ID)
		}
	case "retentions":
		for _, x := range l.Retentions {
			out = append(out, x.ID)
		}
	case "perceptions":
		for _, x := range l.Perceptions {
			out = append(out, x.ID)
		}
	case "reversions":
		for _, x := range l.Reversions {
			out = append(out, x.ID)
		}
	case "voided":
		for _, x := range l.Voided {
			out = append(out, x.ID)
		}
	case "summaries":
		for _, x := range l.Summaries {
			out = append(out, x.ID)
		}
	}
	return out
}

func same(got []uint, want ...uint) bool {
	if len(got) != len(want) {
		return false
	}
	m := map[uint]bool{}
	for _, g := range got {
		m[g] = true
	}
	for _, w := range want {
		if !m[w] {
			return false
		}
	}
	return true
}

func TestBillingListsAreFilteredByBranch(t *testing.T) {
	f := newBillFixture(t)
	v, c, ad := branchtest.Vendedor1, branchtest.Cajero2, branchtest.Admin
	for _, tc := range []struct {
		path, key string
		s1, s2    uint
	}{
		{"/billing/despatches", "despatches", f.desp1, f.desp2},
		{"/billing/retentions", "retentions", f.ret1, f.ret2},
		{"/billing/perceptions", "perceptions", f.per1, f.per2},
		{"/billing/reversions", "reversions", f.rev1, f.rev2},
		{"/billing/voided", "voided", f.voided1, f.voided2},
	} {
		if got := ids(t, f.expect(t, v, "GET", tc.path, "", 200), tc.key); !same(got, tc.s1) {
			t.Errorf("%s vendedor S1: got %v want [%d]", tc.path, got, tc.s1)
		}
		if got := ids(t, f.expect(t, c, "GET", tc.path, "", 200), tc.key); !same(got, tc.s2) {
			t.Errorf("%s cajero S2: got %v want [%d]", tc.path, got, tc.s2)
		}
		if got := ids(t, f.expect(t, ad, "GET", tc.path, "", 200), tc.key); !same(got, tc.s1, tc.s2) {
			t.Errorf("%s admin: got %v want [%d %d]", tc.path, got, tc.s1, tc.s2)
		}
		// branch_id ajeno pedido por un no-admin NO amplía el alcance.
		if got := ids(t, f.expect(t, v, "GET", tc.path+"?branch_id=2", "", 200), tc.key); !same(got, tc.s1) {
			t.Errorf("%s vendedor S1 con branch_id=2: got %v want [%d]", tc.path, got, tc.s1)
		}
		if got := ids(t, f.expect(t, v, "GET", tc.path+"?branch_id=all", "", 200), tc.key); !same(got, tc.s1) {
			t.Errorf("%s vendedor S1 con branch_id=all: got %v want [%d]", tc.path, got, tc.s1)
		}
	}
	// El admin puede acotar a una sucursal concreta.
	if got := ids(t, f.expect(t, ad, "GET", "/billing/despatches?branch_id=2", "", 200), "despatches"); !same(got, f.desp2) {
		t.Errorf("admin con branch_id=2: got %v", got)
	}
}

func TestSummariesAreRucWide(t *testing.T) {
	f := newBillFixture(t)
	// El resumen diario incluye boletas de todas las sucursales: un usuario de una sola sucursal
	// no lo ve, no lo consulta y no lo emite.
	if got := ids(t, f.expect(t, branchtest.Vendedor1, "GET", "/billing/summaries", "", 200), "summaries"); len(got) != 0 {
		t.Errorf("vendedor S1 no debe ver resúmenes del RUC: %v", got)
	}
	f.expect(t, branchtest.Vendedor1, "GET", "/billing/summaries/"+u(f.summary)+"/status", "", 404)
	f.expect(t, branchtest.Vendedor1, "POST", "/billing/summaries", `{"fec_resumen":"2026-11-10"}`, 403)
	if got := ids(t, f.expect(t, branchtest.Admin, "GET", "/billing/summaries", "", 200), "summaries"); !same(got, f.summary) {
		t.Errorf("admin debe ver el resumen: %v", got)
	}
	// Admin: pasa la guarda (el servicio responde 400 por facturador no configurado).
	f.notNotFound(t, branchtest.Admin, "POST", "/billing/summaries", `{"fec_resumen":"2026-11-10"}`)
}

func TestVoidedAndReversionWritesIsolation(t *testing.T) {
	f := newBillFixture(t)
	v, ad := branchtest.Vendedor1, branchtest.Admin
	byID := `{"details":[{"sale_id":` + u(f.sale2) + `,"des_motivo_baja":"x"}]}`
	byRef := `{"details":[{"tipo_doc":"03","serie":"B002","correlativo":"1","des_motivo_baja":"x"}]}`
	for _, a := range []branchtest.Actor{v, ad} {
		f.expect(t, a, "POST", "/billing/voided", byID, 404)
		f.expect(t, a, "POST", "/billing/voided", byRef, 404)
	}
	own := `{"details":[{"sale_id":` + u(f.sale1) + `,"des_motivo_baja":"x"}]}`
	f.notNotFound(t, v, "POST", "/billing/voided", own)

	revForeign := `{"details":[{"tipo_doc":"20","serie":"R002","correlativo":"1","des_motivo_baja":"x"}]}`
	for _, a := range []branchtest.Actor{v, ad} {
		f.expect(t, a, "POST", "/billing/reversions", revForeign, 404)
	}
	revOwn := `{"details":[{"tipo_doc":"20","serie":"R001","correlativo":"1","des_motivo_baja":"x"}]}`
	f.notNotFound(t, v, "POST", "/billing/reversions", revOwn)

	// Estado por ID
	f.expect(t, v, "GET", "/billing/voided/"+u(f.voided2)+"/status", "", 404)
	f.expect(t, branchtest.Cajero2, "GET", "/billing/voided/"+u(f.voided1)+"/status", "", 404)
	f.expect(t, v, "GET", "/billing/reversions/"+u(f.rev2)+"/status", "", 404)
	f.expect(t, branchtest.Cajero2, "GET", "/billing/reversions/"+u(f.rev1)+"/status", "", 404)
	f.notNotFound(t, v, "GET", "/billing/voided/"+u(f.voided1)+"/status", "")
	f.notNotFound(t, v, "GET", "/billing/reversions/"+u(f.rev1)+"/status", "")
	f.notNotFound(t, ad, "GET", "/billing/voided/"+u(f.voided2)+"/status", "") // admin lee
}

func TestDespatchRetentionPerceptionIsolation(t *testing.T) {
	f := newBillFixture(t)
	v, c, ad := branchtest.Vendedor1, branchtest.Cajero2, branchtest.Admin

	// Estado por ID
	f.expect(t, v, "GET", "/billing/despatches/"+u(f.desp2)+"/status", "", 404)
	f.expect(t, c, "GET", "/billing/despatches/"+u(f.desp1)+"/status", "", 404)
	f.expect(t, v, "GET", "/billing/retentions/"+u(f.ret2)+"/status", "", 404)
	f.expect(t, c, "GET", "/billing/retentions/"+u(f.ret1)+"/status", "", 404)
	f.expect(t, v, "GET", "/billing/perceptions/"+u(f.per2)+"/status", "", 404)
	f.expect(t, c, "GET", "/billing/perceptions/"+u(f.per1)+"/status", "", 404)
	f.expect(t, v, "GET", "/billing/despatches/"+u(f.desp1)+"/status", "", 200)
	f.expect(t, ad, "GET", "/billing/despatches/"+u(f.desp2)+"/status", "", 200)
	f.expect(t, v, "GET", "/billing/retentions/"+u(f.ret1)+"/status", "", 200)
	f.expect(t, v, "GET", "/billing/perceptions/"+u(f.per1)+"/status", "", 200)

	// Búsqueda de guía propia por número ("T002-7" es de S2)
	f.expect(t, v, "GET", "/billing/despatches/lookup?number=T002-7", "", 404)

	// Creación: branch_id del body de otra sucursal -> 403
	for _, ep := range []string{"/billing/despatches", "/billing/retentions", "/billing/perceptions"} {
		resp := f.expect(t, v, "POST", ep, `{"branch_id":2}`, 403)
		if !strings.Contains(resp, "BRANCH_FORBIDDEN") {
			t.Errorf("%s: falta el código BRANCH_FORBIDDEN: %s", ep, resp)
		}
		f.notNotFound(t, v, "POST", ep, `{"branch_id":1}`) // propia: pasa la guarda (400 del servicio)
	}
	// Documentos origen de otra sucursal -> 404
	f.expect(t, v, "POST", "/billing/despatches", `{"source_sale_id":`+u(f.sale2)+`}`, 404)
	f.expect(t, ad, "POST", "/billing/despatches", `{"source_sale_id":`+u(f.sale2)+`}`, 404) // admin (S1 activa)
	f.expect(t, v, "POST", "/billing/retentions", `{"source_purchase_id":`+u(f.purchase2)+`}`, 404)
	f.expect(t, v, "POST", "/billing/perceptions", `{"source_sale_id":`+u(f.sale2)+`}`, 404)
	f.notNotFound(t, v, "POST", "/billing/despatches", `{"source_sale_id":`+u(f.sale1)+`}`)
	f.notNotFound(t, v, "POST", "/billing/retentions", `{"source_purchase_id":`+u(f.purchase1)+`}`)
	f.notNotFound(t, v, "POST", "/billing/perceptions", `{"source_sale_id":`+u(f.sale1)+`}`)
}

func TestNotificationCountsAndInvoiceStatusIsolation(t *testing.T) {
	f := newBillFixture(t)
	count := func(a branchtest.Actor) int {
		var out struct{ Pending int }
		resp := f.expect(t, a, "GET", "/billing/notification-counts", "", 200)
		if err := json.Unmarshal([]byte(resp), &out); err != nil {
			t.Fatal(err)
		}
		return out.Pending
	}
	// Cada sucursal tiene 2 boletas pendientes; el administrador ve las 4.
	if n := count(branchtest.Vendedor1); n != 2 {
		t.Errorf("vendedor S1 debe contar solo su sucursal: %d", n)
	}
	if n := count(branchtest.Cajero2); n != 2 {
		t.Errorf("cajero S2 debe contar solo su sucursal: %d", n)
	}
	if n := count(branchtest.Admin); n != 4 {
		t.Errorf("admin debe contar todo el tenant: %d", n)
	}
	// Consulta de estado SUNAT por serie/número de un comprobante de otra sucursal.
	f.expect(t, branchtest.Vendedor1, "GET", "/billing/invoice-status?tipo=03&serie=B002&numero=1", "", 404)
	f.notNotFound(t, branchtest.Vendedor1, "GET", "/billing/invoice-status?tipo=03&serie=B001&numero=1", "")
}

// Con un tenant con una sola sucursal, un usuario sin rol de administrador sigue emitiendo y
// viendo resúmenes (cubre todas las sucursales activas).
func TestSummariesSingleBranchTenantStillWorks(t *testing.T) {
	db := branchtest.OpenDB(t, &database.TenantBranch{}, &database.TenantSunatSummary{}, &database.TenantCompanyConfig{})
	db.Create(&database.TenantBranch{ID: 1, Name: "Única", Active: true, IsMain: true})
	db.Create(&database.TenantSunatSummary{FecResumen: time.Now(), Correlativo: "1", Status: "pending"})
	f := &billFixture{db: db}
	got := ids(t, f.expect(t, branchtest.Vendedor1, "GET", "/billing/summaries", "", 200), "summaries")
	if len(got) != 1 {
		t.Errorf("tenant de una sola sucursal: debe ver su resumen, got %v", got)
	}
}
