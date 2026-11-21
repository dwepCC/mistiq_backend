package service

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"tukifac/pkg/database"
	"tukifac/pkg/database/tenantmigrations"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func testDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&database.TenantSale{}, &database.TenantSaleItem{}, &database.TenantContact{}, &database.TenantPurchase{},
		&database.TenantPurchaseItem{}, &database.TenantCompanyConfig{}, &database.TenantCashMovement{},
		&database.TenantBankAccount{}, &database.TenantBankMovement{}, &database.TenantSalePayment{},
		&database.TenantPurchasePayable{}, &database.TenantPurchasePayment{},
		&database.TenantAccountingSettings{}, &database.TenantAccountingAccount{}, &database.TenantSireSettings{}, &database.TenantSireTicket{},
	); err != nil {
		t.Fatal(err)
	}
	db.Create(&database.TenantCompanyConfig{BusinessName: "EMPRESA DEMO SAC", RUC: "20123456789"})
	for _, a := range []database.TenantAccountingAccount{
		{Code: "1", Name: "Activos", Kind: "asset", Active: true}, {Code: "1.1", Name: "Activos corrientes", Kind: "asset", Active: true},
		{Code: "1.1.1", Name: "Efectivo", Kind: "asset", Active: true}, {Code: "1.1.1.1", Name: "Caja", Kind: "asset", Active: true},
		{Code: "1.1.1.2", Name: "Bancos", Kind: "asset", Active: true}, {Code: "1.1.2", Name: "CxC", Kind: "asset", Active: true},
		{Code: "2", Name: "Pasivos", Kind: "liability", Active: true}, {Code: "2.1", Name: "Pasivos corrientes", Kind: "liability", Active: true},
		{Code: "2.1.1", Name: "CxP", Kind: "liability", Active: true}, {Code: "2.1.2", Name: "Tributos", Kind: "liability", Active: true},
		{Code: "2.1.2.1", Name: "IGV por pagar", Kind: "liability", Active: true},
		{Code: "4", Name: "Ingresos", Kind: "income", Active: true}, {Code: "4.1", Name: "Ventas", Kind: "income", Active: true},
		{Code: "4.1.1", Name: "Ventas netas", Kind: "income", Active: true},
	} {
		db.Create(&a)
	}
	return db
}

var march = time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)

type saleOpts struct {
	docType, series string
	corr            uint
	status, billing string
	currency        string
	rate            *float64
	subtotal, tax   float64
	items           []database.TenantSaleItem
	original        *uint
	cond            string
	contact         *uint
}

func mkSale(t *testing.T, db *gorm.DB, o saleOpts) database.TenantSale {
	t.Helper()
	if o.status == "" {
		o.status = "paid"
	}
	if o.billing == "" {
		o.billing = "accepted"
	}
	if o.currency == "" {
		o.currency = "PEN"
	}
	s := database.TenantSale{
		BranchID: 1, UserID: 1, SeriesID: 1, DocType: o.docType, Series: o.series, Correlative: o.corr,
		Number: fmt.Sprintf("%s-%08d", o.series, o.corr), IssueDate: march, Subtotal: o.subtotal, TaxAmount: o.tax,
		Total: o.subtotal + o.tax, Currency: o.currency, ExchangeRate: o.rate, Status: o.status, BillingStatus: o.billing,
		OriginalSaleID: o.original, PaymentConditionCode: o.cond, ContactID: o.contact,
	}
	if s.PaymentConditionCode == "" {
		s.PaymentConditionCode = "cash"
	}
	if err := db.Create(&s).Error; err != nil {
		t.Fatal(err)
	}
	for _, it := range o.items {
		it.SaleID = s.ID
		if err := db.Create(&it).Error; err != nil {
			t.Fatal(err)
		}
	}
	return s
}

func item(aff string, sub, tax float64) database.TenantSaleItem {
	return database.TenantSaleItem{Description: "x", Quantity: 1, UnitPrice: sub, IgvAffectationType: aff, Subtotal: sub, TaxAmount: tax, Total: sub + tax}
}

func seedMonth(t *testing.T, db *gorm.DB) (factura, nc database.TenantSale) {
	t.Helper()
	cli := database.TenantContact{DocType: "RUC", DocNumber: "20555555551", BusinessName: "CLIENTE SAC", Type: "customer"}
	db.Create(&cli)
	factura = mkSale(t, db, saleOpts{docType: "FACTURA", series: "F001", corr: 1, subtotal: 100, tax: 18, contact: &cli.ID,
		items: []database.TenantSaleItem{item("10", 100, 18)}})
	// Factura mixta: gravado 100 + exonerado 50
	mkSale(t, db, saleOpts{docType: "FACTURA", series: "F001", corr: 2, subtotal: 150, tax: 18, contact: &cli.ID,
		items: []database.TenantSaleItem{item("10", 100, 18), item("20", 50, 0)}})
	// Boleta en dólares con TC 3.5
	rate := 3.5
	mkSale(t, db, saleOpts{docType: "BOLETA", series: "B001", corr: 1, subtotal: 10, tax: 1.8, currency: "USD", rate: &rate,
		items: []database.TenantSaleItem{item("10", 10, 1.8)}})
	// Anulada
	mkSale(t, db, saleOpts{docType: "BOLETA", series: "B001", corr: 3, status: "cancelled", subtotal: 20, tax: 3.6,
		items: []database.TenantSaleItem{item("10", 20, 3.6)}})
	// Pendiente de envío (no aceptada) y rechazada (excluida) y borrador (excluido)
	mkSale(t, db, saleOpts{docType: "BOLETA", series: "B001", corr: 4, billing: "pending", subtotal: 30, tax: 5.4, items: []database.TenantSaleItem{item("10", 30, 5.4)}})
	mkSale(t, db, saleOpts{docType: "BOLETA", series: "B001", corr: 5, billing: "rejected", subtotal: 30, tax: 5.4, items: []database.TenantSaleItem{item("10", 30, 5.4)}})
	mkSale(t, db, saleOpts{docType: "FACTURA", series: "F001", corr: 9, status: "draft", subtotal: 30, tax: 5.4})
	// Nota de venta (no fiscal): nunca entra
	mkSale(t, db, saleOpts{docType: "NOTA_VENTA", series: "NV01", corr: 1, subtotal: 100, tax: 0})
	// Nota de crédito de la factura 1
	nc = mkSale(t, db, saleOpts{docType: "NOTA_CREDITO", series: "FC01", corr: 1, subtotal: 100, tax: 18, original: &factura.ID,
		items: []database.TenantSaleItem{item("10", 100, 18)}})
	return
}

func TestSalesRegister_SignsCurrencyAndVoided(t *testing.T) {
	db := testDB(t)
	seedMonth(t, db)
	s := New(db)
	tb, err := s.SalesRegister("2026-03", true)
	if err != nil {
		t.Fatal(err)
	}
	// 01 ×2, 03 ×3 (aceptada USD, anulada, pendiente) y la NC; sin NV, rechazada ni borrador.
	if len(tb.Rows) != 6 {
		t.Fatalf("filas: %d\n%v", len(tb.Rows), tb.Rows)
	}
	byNum := map[string][]any{}
	for _, r := range tb.Rows {
		byNum[fmt.Sprintf("%v-%v", r[4], r[5])] = r
	}
	col := func(name string) int {
		for i, c := range tb.Columns {
			if c == name {
				return i
			}
		}
		t.Fatalf("columna %q", name)
		return -1
	}
	// Mixta: base gravada 100, exonerada 50, IGV 18, total 168.
	mix := byNum["F001-2"]
	if mix[col("Base imponible gravada")] != 100.0 || mix[col("Exonerada")] != 50.0 || mix[col("Importe total")] != 168.0 {
		t.Errorf("mixta: %v", mix)
	}
	// USD convertida: 11.80 × 3.5 = 41.30; total en moneda original 11.80.
	usd := byNum["B001-1"]
	if usd[col("Importe total")] != 41.3 || usd[col("Total en moneda original")] != 11.8 || usd[col("Moneda")] != "USD" {
		t.Errorf("usd: %v", usd)
	}
	// Anulada: fila presente con importes en cero.
	void := byNum["B001-3"]
	if void[col("Importe total")] != 0.0 || void[col("Estado")] != "ANULADO" {
		t.Errorf("anulada: %v", void)
	}
	// Nota de crédito: negativa y con su documento de referencia.
	nc := byNum["FC01-1"]
	if nc[col("Importe total")] != -118.0 || nc[col("Serie referencia")] != "F001" || nc[col("Número referencia")] != "1" || nc[col("Tipo doc. referencia")] != "01" {
		t.Errorf("nc: %v", nc)
	}
	// Totales: 118 + 168 + 41.3 + 0 + 35.4 (pendiente) − 118 = 244.7
	if got := tb.Footer[0][col("Importe total")]; got != 244.7 {
		t.Errorf("total pie: %v", got)
	}
}

func TestSummaryReport_ConfirmedVoidedAndGaps(t *testing.T) {
	db := testDB(t)
	seedMonth(t, db)
	// Hueco: la factura F001-3 no existe entre la 2 y la 4.
	mkSale(t, db, saleOpts{docType: "FACTURA", series: "F001", corr: 4, subtotal: 10, tax: 1.8, items: []database.TenantSaleItem{item("10", 10, 1.8)}})
	rep, err := New(db).SummaryReport(time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	var f001 *SummaryRow
	for i := range rep.Confirmed {
		if rep.Confirmed[i].Series == "F001" {
			f001 = &rep.Confirmed[i]
		}
	}
	if f001 == nil || f001.Quantity != 3 || f001.FirstNumber != "1" || f001.LastNumber != "4" || f001.Gaps != "3" {
		t.Fatalf("F001: %+v", f001)
	}
	if rep.NotAccepted != 1 { // la boleta pendiente
		t.Errorf("no aceptados: %d", rep.NotAccepted)
	}
	if len(rep.Voided) != 1 || rep.Voided[0].Numbers != "3" || rep.Voided[0].Total != 23.6 {
		t.Errorf("anulados: %+v", rep.Voided)
	}
	// La nota de crédito RESTA: se informa en su propia fila NC.
	foundNC := false
	for _, r := range rep.Confirmed {
		if r.Type == "NC" && r.Total == -118 {
			foundNC = true
		}
	}
	if !foundNC {
		t.Errorf("falta la fila NC negativa: %+v", rep.Confirmed)
	}
}

func TestVouchers_BalancedAndCreditNoteInverted(t *testing.T) {
	db := testDB(t)
	seedMonth(t, db)
	s := New(db)
	docs, _, _ := s.SalesDocs(time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC))
	vs, warns := SalesVouchers(docs, DefaultAccounts)
	if len(warns) != 0 {
		t.Fatalf("advertencias: %v", warns)
	}
	for _, v := range vs {
		if !VoucherBalanced(v) {
			t.Errorf("%s no cuadra: %+v", v.Doc.FullNumber, v.Lines)
		}
		if v.Doc.IsCreditNote() {
			if v.Lines[0].Side != "H" || v.Lines[1].Side != "D" {
				t.Errorf("NC debe invertir lados: %+v", v.Lines)
			}
		} else if v.Lines[0].Side != "D" || v.Lines[1].Side != "H" {
			t.Errorf("lados: %+v", v.Lines)
		}
		if v.Doc.Voided {
			for _, l := range v.Lines {
				if l.Amount != 0 {
					t.Errorf("anulado con importe: %+v", l)
				}
			}
		}
	}
	// La factura mixta reparte gravado y exonerado en cuentas distintas.
	for _, v := range vs {
		if v.Doc.FullNumber == "F001-2" {
			got := map[Concept]float64{}
			for _, l := range v.Lines {
				got[l.Concept] = l.Amount
			}
			if got[ConceptTotal] != 168 || got[ConceptIgv] != 18 || got[ConceptTaxed] != 100 || got[ConceptExonerated] != 50 {
				t.Errorf("mixta: %v", got)
			}
		}
	}
}

func TestExportFormats_Shapes(t *testing.T) {
	db := testDB(t)
	seedMonth(t, db)
	s := New(db)
	want := map[string]int{"concar": 39, "concar_simple": 40, "siscont_excel": 56, "foxcont": 14, "adsoft": 29, "sumerius": 27}
	for k, n := range want {
		ex, err := s.ExportFormat(k, "2026-03")
		if err != nil {
			t.Fatalf("%s: %v", k, err)
		}
		if ex.Table == nil || len(ex.Table.Columns) != n || len(ex.Table.Rows) == 0 {
			t.Errorf("%s: columnas=%d filas=%d", k, len(ex.Table.Columns), len(ex.Table.Rows))
			continue
		}
		for i, r := range ex.Table.Rows {
			if len(r) != n {
				t.Errorf("%s fila %d: %d celdas, quería %d", k, i, len(r), n)
				break
			}
		}
	}
	// Contasis: sin cabecera y 42 columnas.
	c, err := s.ExportFormat("contasis", "2026-03")
	if err != nil || !c.Table.NoHeader || len(c.Table.Rows[0]) != 42 {
		t.Fatalf("contasis: %v %d", err, len(c.Table.Rows[0]))
	}
	// Siscont txt: todas las líneas de 358 caracteres.
	tx, err := s.ExportFormat("siscont_txt", "2026-03")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(tx.Text, "\r\n"), "\r\n")
	if len(lines) < 6 {
		t.Fatalf("líneas: %d", len(lines))
	}
	for i, l := range lines {
		if len([]rune(l)) != 358 {
			t.Errorf("línea %d mide %d, quería 358: %q", i, len([]rune(l)), l)
		}
	}
	if _, err := s.ExportFormat("nada", "2026-03"); err == nil {
		t.Error("formato desconocido debe fallar")
	}
}

func TestExportConcar_UsesConfiguredAccountsAndInvertsCreditNote(t *testing.T) {
	db := testDB(t)
	seedMonth(t, db)
	s := New(db)
	if _, err := s.SaveSettings(database.TenantAccountingSettings{ReceivablePEN: "0121", TaxedPEN: "7011101", SubdiarioSales: "07"}); err != nil {
		t.Fatal(err)
	}
	ex, _ := s.ExportFormat("concar_simple", "2026-03")
	hasZeroPadded, ncInverted := false, false
	for _, r := range ex.Table.Rows {
		if r[9] == "0121" { // cuenta por cobrar con cero inicial conservado (texto)
			hasZeroPadded = true
			if r[16] == "NC" && r[12] == "H" {
				ncInverted = true
			}
		}
		if r[0] != "07" {
			t.Fatalf("subdiario: %v", r[0])
		}
	}
	if !hasZeroPadded || !ncInverted {
		t.Errorf("cuenta con cero inicial=%v, NC invertida=%v", hasZeroPadded, ncInverted)
	}
	if _, err := s.SaveSettings(database.TenantAccountingSettings{TaxedPEN: "70 11"}); err == nil {
		t.Error("una cuenta con espacios debe rechazarse")
	}
}

func TestLedger_SignsParentsAndPreviousBalance(t *testing.T) {
	db := testDB(t)
	// Caja: febrero +500 / −100; marzo +300 / −50.
	feb := time.Date(2026, 2, 5, 10, 0, 0, 0, time.UTC)
	db.Create(&database.TenantCashMovement{CashSessionID: 1, Type: "income", Amount: 500, UserID: 1, CreatedAt: feb})
	db.Create(&database.TenantCashMovement{CashSessionID: 1, Type: "expense", Amount: 100, UserID: 1, CreatedAt: feb})
	db.Create(&database.TenantCashMovement{CashSessionID: 1, Type: "income", Amount: 300, UserID: 1, CreatedAt: march})
	db.Create(&database.TenantCashMovement{CashSessionID: 1, Type: "expense", Amount: 50, UserID: 1, CreatedAt: march})
	// Banco: crédito (ingreso) 1000 en marzo.
	bank := database.TenantBankAccount{Name: "BCP Soles", Currency: "PEN", Active: true}
	db.Create(&bank)
	db.Create(&database.TenantBankMovement{BankAccountID: bank.ID, Type: "credit", Amount: 1000, Date: march, UserID: 1})
	// Venta de marzo: base 100 + IGV 18.
	mkSale(t, db, saleOpts{docType: "FACTURA", series: "F001", corr: 1, subtotal: 100, tax: 18, items: []database.TenantSaleItem{item("10", 100, 18)}})
	led, err := New(db).Ledger("2026-03")
	if err != nil {
		t.Fatal(err)
	}
	rows := map[string]LedgerRow{}
	for _, r := range led.Rows {
		rows[r.Code] = r
	}
	caja := rows["1.1.1.1"]
	if caja.Previous != 400 || caja.Debit != 300 || caja.Credit != 50 || caja.Final != 650 {
		t.Errorf("caja: %+v", caja)
	}
	bk := rows[fmt.Sprintf("1.1.1.2.%d", bank.ID)]
	if bk.Name != "BCP Soles" || bk.Final != 1000 {
		t.Errorf("banco: %+v", bk)
	}
	// Padre «1.1.1» suma caja + bancos del MISMO mes; «1» también.
	if rows["1.1.1"].Final != 1650 || rows["1"].Final != 1650 || rows["1.1.1"].Previous != 400 {
		t.Errorf("padres: %+v %+v", rows["1.1.1"], rows["1"])
	}
	// Ingresos y pasivos conservan signo positivo con saldo acreedor.
	if v := rows["4.1.1"]; v.Credit != 100 || v.Final != 100 {
		t.Errorf("ventas: %+v", v)
	}
	if v := rows["2.1.2.1"]; v.Credit != 18 || v.Final != 18 {
		t.Errorf("igv: %+v", v)
	}
	// Orden numérico: 1.1.1.2 antes que 1.1.2 (y 1.1.10 iría después de 1.1.2, no antes).
	idx := func(code string) int {
		for i, r := range led.Rows {
			if r.Code == code {
				return i
			}
		}
		return -1
	}
	if !(idx("1.1.1.2") < idx("1.1.2")) {
		t.Error("orden de códigos")
	}
	if !codeLess("1.1.2", "1.1.10") {
		t.Error("codeLess debe ser numérico")
	}
}

// ── SIRE ─────────────────────────────────────────────────────────────────────────────────────────

func sireZip(t *testing.T, lines ...string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("RVIE_20123456789_202603.txt")
	w.Write([]byte("CABECERA\r\n" + strings.Join(lines, "\r\n") + "\r\n"))
	zw.Close()
	return buf.Bytes()
}

// sireLineSale arma una línea del RVIE con los campos que usamos: fecha[4], tipo[6], serie[7], número[8], total[25].
func sireLineSale(date, typ, serie, num string, total float64) string {
	f := make([]string, 30)
	f[4], f[6], f[7], f[8], f[25] = date, typ, serie, num, fmt.Sprintf("%.2f", total)
	return strings.Join(f, "|")
}

func TestSire_TicketFlowAndComparison(t *testing.T) {
	t.Setenv("SECRETS_ENCRYPTION_KEY", strings.Repeat("ab", 32))
	db := testDB(t)
	s := New(db)
	seedMonth(t, db)

	tokenCalls := 0
	var accepted bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.Contains(r.URL.Path, "/oauth2/token"):
			tokenCalls++
			if r.FormValue("client_secret") != "SECRETO" || r.FormValue("password") != "CLAVE" || r.FormValue("username") != "20123456789USER" {
				w.WriteHeader(401)
				w.Write([]byte(`{"error":"invalid_client","error_description":"credenciales"}`))
				return
			}
			w.Write([]byte(`{"access_token":"TOK","expires_in":3600}`))
		case strings.HasSuffix(r.URL.Path, "/periodos"):
			if r.Header.Get("Authorization") != "Bearer TOK" {
				w.WriteHeader(401)
				return
			}
			w.Write([]byte(`[{"numEjercicio":"2026","desEstado":"Abierto","lisPeriodos":[{"perTributario":"202603","desEstado":"Abierto"}]}]`))
		case strings.HasSuffix(r.URL.Path, "/exportapropuesta"):
			w.Write([]byte(`{"numTicket":"T-123"}`))
		case strings.HasSuffix(r.URL.Path, "/consultaestadotickets"):
			w.Write([]byte(`{"registros":[{"numTicket":"T-123","codEstadoProceso":"06","desEstadoProceso":"Terminado","archivoReporte":[{"nomArchivoReporte":"RVIE.zip"}]}]}`))
		case strings.HasSuffix(r.URL.Path, "/archivoreporte"):
			if r.URL.Query().Get("codLibro") != "140000" || r.URL.Query().Get("codTipoArchivoReporte") != "01" || r.Header.Get("Content-Type") != "application/json" {
				w.WriteHeader(422)
				return
			}
			w.Write(sireZip(t,
				sireLineSale("10/03/2026", "01", "F001", "00000001", 118), // coincide
				sireLineSale("10/03/2026", "01", "F001", "00000002", 200), // difiere (168 local)
				sireLineSale("10/03/2026", "01", "F001", "00000077", 59),  // solo SUNAT
			))
		case strings.HasSuffix(r.URL.Path, "/aceptapropuesta"):
			accepted = true
			w.Write([]byte(`{}`))
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	c := &SireClient{AuthURL: srv.URL, APIURL: srv.URL, HTTP: srv.Client()}

	// Sin credenciales: mensaje claro, sin llamar a SUNAT.
	if _, err := s.SirePeriods(c, SireBookSale); err == nil || !strings.Contains(err.Error(), "credenciales SIRE") {
		t.Fatalf("sin credenciales: %v", err)
	}
	if err := s.SaveSireSettings("CID", "SECRETO", "20123456789USER", "CLAVE"); err != nil {
		t.Fatal(err)
	}
	// Los secretos se guardan cifrados y la vista nunca los trae.
	raw, _ := s.SireSettings()
	if raw.ClientSecretEnc == "SECRETO" || raw.SolPasswordEnc == "CLAVE" || !strings.HasPrefix(raw.ClientSecretEnc, "v1:") {
		t.Fatalf("secretos sin cifrar: %+v", raw)
	}
	view, _ := s.SireSettingsView()
	vj, _ := json.Marshal(view)
	if strings.Contains(string(vj), "SECRETO") || strings.Contains(string(vj), "CLAVE") || !view.HasClientSecret || !view.HasSolPassword {
		t.Fatalf("vista: %s", vj)
	}
	// Un secreto vacío conserva el anterior.
	if err := s.SaveSireSettings("CID", "", "20123456789USER", ""); err != nil {
		t.Fatal(err)
	}

	ps, err := s.SirePeriods(c, SireBookSale)
	if err != nil || len(ps) != 1 || ps[0].Periods[0].Period != "202603" {
		t.Fatalf("periodos: %v %v", ps, err)
	}
	tk, err := s.SireRequestTicket(c, SireBookSale, "202603", 7)
	if err != nil || tk.Ticket != "T-123" {
		t.Fatalf("ticket: %v %v", tk, err)
	}
	res, err := s.SireQueryTicket(c, SireBookSale, "202603", "T-123")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Ready || res.Ticket.StatusCode != "06" {
		t.Fatalf("estado: %+v", res.Ticket)
	}
	if res.Summary["ok"] != 1 || res.Summary["diff"] != 1 || res.Summary["only_sunat"] != 1 {
		t.Errorf("resumen SUNAT: %v", res.Summary)
	}
	// Lo que está en el sistema pero no vino de SUNAT (boleta, NC, etc.) se marca «only_system».
	if res.Summary["only_system"] < 3 {
		t.Errorf("solo sistema: %v", res.Summary)
	}
	// El token se reutiliza: 1 solo pedido para periodos+ticket+consulta+descarga.
	if tokenCalls != 1 {
		t.Errorf("el token debía cachearse, llamadas=%d", tokenCalls)
	}
	hist, _ := s.SireTickets(SireBookSale, 10)
	if len(hist) != 1 || hist[0].StatusCode != "06" || hist[0].FileName != "RVIE.zip" {
		t.Errorf("historial: %+v", hist)
	}
	if err := s.SireAccept(c, "202603"); err != nil || !accepted {
		t.Fatalf("aceptar: %v %v", err, accepted)
	}
	hist, _ = s.SireTickets(SireBookSale, 10)
	if hist[0].AcceptedAt == nil {
		t.Error("debe registrar la aceptación")
	}
	if err := s.SireAccept(c, "2026"); err == nil {
		t.Error("periodo inválido debe fallar")
	}
}

func TestSire_BadCredentialsGiveClearError(t *testing.T) {
	t.Setenv("SECRETS_ENCRYPTION_KEY", strings.Repeat("cd", 32))
	db := testDB(t)
	s := New(db)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		w.Write([]byte(`{"error":"invalid_client","error_description":"Credenciales inválidas"}`))
	}))
	defer srv.Close()
	s.SaveSireSettings("CID", "S", "U", "P")
	_, err := s.SirePeriods(&SireClient{AuthURL: srv.URL, APIURL: srv.URL, HTTP: srv.Client()}, SireBookSale)
	if err == nil || !strings.Contains(err.Error(), "401") || !strings.Contains(err.Error(), "Credenciales inválidas") {
		t.Fatalf("error: %v", err)
	}
}

// Una venta anulada que tiene nota de crédito vigente conserva sus importes (la nota la revierte en
// negativo): ponerla en cero y además restar la nota duplicaría la reversión.
func TestSalesRegister_CancelledWithCreditNoteIsNotDoubleReversed(t *testing.T) {
	db := testDB(t)
	f := mkSale(t, db, saleOpts{docType: "FACTURA", series: "F001", corr: 1, status: "cancelled", subtotal: 100, tax: 18,
		items: []database.TenantSaleItem{item("10", 100, 18)}})
	mkSale(t, db, saleOpts{docType: "NOTA_CREDITO", series: "FC01", corr: 1, subtotal: 100, tax: 18, original: &f.ID,
		items: []database.TenantSaleItem{item("10", 100, 18)}})
	tb, err := New(db).SalesRegister("2026-03", false)
	if err != nil {
		t.Fatal(err)
	}
	var sum float64
	for _, r := range tb.Rows {
		sum += r[17].(float64)
	}
	if len(tb.Rows) != 2 || sum != 0 || tb.Rows[0][17] != 118.0 || tb.Rows[1][17] != -118.0 {
		t.Fatalf("esperaba +118 y −118 (neto 0): %v", tb.Rows)
	}
	rep, _ := New(db).SummaryReport(time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC))
	if len(rep.Voided) != 0 || rep.ConfirmedTotals.Total != 0 {
		t.Errorf("resumen: anulados=%v total=%v", rep.Voided, rep.ConfirmedTotals.Total)
	}
}

// Tukifac marca como «cancelled» tanto la venta anulada como su nota de crédito: la nota aceptada sigue
// siendo un documento válido (en negativo), nunca una fila «anulada».
func TestSalesRegister_CancelledCreditNoteStaysValid(t *testing.T) {
	db := testDB(t)
	f := mkSale(t, db, saleOpts{docType: "FACTURA", series: "F001", corr: 1, status: "cancelled", subtotal: 100, tax: 18,
		items: []database.TenantSaleItem{item("10", 100, 18)}})
	mkSale(t, db, saleOpts{docType: "NOTA_CREDITO", series: "FC01", corr: 1, status: "cancelled", subtotal: 100, tax: 18, original: &f.ID,
		items: []database.TenantSaleItem{item("10", 100, 18)}})
	rep, _ := New(db).SummaryReport(time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 3, 31, 0, 0, 0, 0, time.UTC))
	if len(rep.Voided) != 0 || rep.ConfirmedTotals.Total != 0 || len(rep.Confirmed) != 2 {
		t.Errorf("anulados=%v confirmados=%+v", rep.Voided, rep.Confirmed)
	}
}

// Las cuentas sembradas por la migración V175 deben ser las mismas que usa el servicio por defecto.
func TestSeedDefaultsMatchServiceDefaults(t *testing.T) {
	d, s := DefaultAccounts, tenantmigrations.AccountingSeedDefaults
	got := Accounts{
		ReceivablePEN: s.ReceivablePEN, ReceivableUSD: s.ReceivableUSD, IgvSalesPEN: s.IgvSalesPEN, IgvSalesUSD: s.IgvSalesUSD,
		TaxedPEN: s.TaxedPEN, TaxedUSD: s.TaxedUSD, Exonerated: s.Exonerated, Unaffected: s.Unaffected,
		Exportation: s.Exportation, Icbper: s.Icbper, SubdiarioSales: s.SubdiarioSales,
	}
	if got != d {
		t.Errorf("la migración V175 y DefaultAccounts difieren:\n migración: %+v\n servicio:  %+v", got, d)
	}
}

// Guardar con campos vacíos devuelve cada cuenta de ventas a su valor por defecto (nunca queda vacía).
func TestSaveSettingsEmptyRestoresDefaults(t *testing.T) {
	s := New(testDB(t))
	st, err := s.SaveSettings(database.TenantAccountingSettings{ReceivablePEN: "0121"})
	if err != nil {
		t.Fatal(err)
	}
	if st.ReceivablePEN != "0121" || st.TaxedPEN != "70111" || st.IgvSalesPEN != "40111" || st.SubdiarioSales != "05" {
		t.Errorf("guardado: %+v", st)
	}
}

// Exportación de servicios (0201/0202/0208): la línea con afectación 40 va a la columna «Exportación», sin IGV; el hospedaje (0202) muestra al
// huésped (nombre y pasaporte) como adquirente, y la nota de crédito la revierte en negativo.
func TestSalesRegister_ExportacionDeServicios(t *testing.T) {
	db := testDB(t)
	if err := db.AutoMigrate(&database.TenantSaleExport{}); err != nil {
		t.Fatal(err)
	}
	varios := database.TenantContact{DocType: "0", DocNumber: "99999999", BusinessName: "CLIENTES VARIOS", Type: "customer"}
	db.Create(&varios)
	exp := mkSale(t, db, saleOpts{docType: "FACTURA", series: "F001", corr: 20, subtotal: 500, tax: 0, contact: &varios.ID,
		items: []database.TenantSaleItem{item("40", 500, 0)}})
	hosp := mkSale(t, db, saleOpts{docType: "FACTURA", series: "F001", corr: 21, subtotal: 300, tax: 0, contact: &varios.ID,
		items: []database.TenantSaleItem{item("40", 300, 0)}})
	db.Create(&database.TenantSaleExport{SaleID: exp.ID, OperationTypeCode: "0201", CountryUse: "US"})
	db.Create(&database.TenantSaleExport{SaleID: hosp.ID, OperationTypeCode: "0202", GuestName: "John Smith", GuestDocType: "7", GuestDocNumber: "X1234567", PassportCountry: "US", ResidenceCountry: "US"})
	mkSale(t, db, saleOpts{docType: "NOTA_CREDITO", series: "FC01", corr: 7, subtotal: 500, tax: 0, original: &exp.ID,
		items: []database.TenantSaleItem{item("40", 500, 0)}})

	tb, err := New(db).SalesRegister("2026-03", false)
	if err != nil {
		t.Fatal(err)
	}
	col := func(name string) int {
		for i, c := range tb.Columns {
			if c == name {
				return i
			}
		}
		t.Fatalf("columna %q", name)
		return -1
	}
	byNum := map[string][]any{}
	for _, r := range tb.Rows {
		byNum[fmt.Sprintf("%v-%v", r[4], r[5])] = r
	}
	e := byNum["F001-20"]
	if e[col("Exportación")] != 500.0 || e[col("Base imponible gravada")] != 0.0 || e[col("IGV")] != 0.0 || e[col("Importe total")] != 500.0 {
		t.Errorf("0201: %v", e)
	}
	if e[col("Cliente")] != "CLIENTES VARIOS" {
		t.Errorf("0201 conserva al cliente de la venta: %v", e[col("Cliente")])
	}
	h := byNum["F001-21"]
	if h[col("Cliente")] != "John Smith" || h[col("Tipo doc. identidad")] != "7" || h[col("N° doc. identidad")] != "X1234567" || h[col("Exportación")] != 300.0 {
		t.Errorf("0202 debe mostrar al huésped como adquirente: %v", h)
	}
	n := byNum["FC01-7"]
	if n[col("Exportación")] != -500.0 || n[col("Importe total")] != -500.0 {
		t.Errorf("nota de crédito de exportación en negativo: %v", n)
	}
}

func TestSire_422ShowsDetailAndRUCHint(t *testing.T) {
	t.Setenv("SECRETS_ENCRYPTION_KEY", strings.Repeat("cd", 32))
	db := testDB(t)
	s := New(db)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "oauth2/token") {
			w.Write([]byte(`{"access_token":"T","expires_in":3600}`))
			return
		}
		w.WriteHeader(422)
		w.Write([]byte(`{"cod":"422","msg":"Unprocessable Entity - Se presentaron errores de validacion","errors":[{"cod":"1001","msg":"El campo numRuc no enviado o es vacio"}]}`))
	}))
	defer srv.Close()
	c := &SireClient{AuthURL: srv.URL, APIURL: srv.URL, HTTP: srv.Client()}
	s.SaveSireSettings("CID", "S", "MODDATOS", "P")
	_, err := s.SirePeriods(c, SireBookSale)
	if err == nil || !strings.Contains(err.Error(), "1001") || !strings.Contains(err.Error(), "RUC por delante") {
		t.Fatalf("sin RUC: %v", err)
	}
	s.SaveSireSettings("CID", "", "20612257320MODDATOS", "")
	tokenMu.Lock()
	tokenCache = map[string]cachedToken{}
	tokenMu.Unlock()
	_, err = s.SirePeriods(c, SireBookSale)
	if err == nil || !strings.Contains(err.Error(), "1001") || strings.Contains(err.Error(), "RUC por delante") {
		t.Fatalf("con RUC: %v", err)
	}
}
