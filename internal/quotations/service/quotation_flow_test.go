package service

import (
	"errors"
	"strings"
	"testing"
	"time"

	"tukifac/pkg/database"
	"tukifac/pkg/tax"

	"gorm.io/gorm"
)

// quotationEnv monta BD + series de cotización y nota de venta + un cliente real.
type quotationEnv struct {
	db       *gorm.DB
	svc      *QuotationService
	quoteSer uint
	nvSer    uint
	contact  uint
}

func newQuotationEnv(t *testing.T) quotationEnv {
	t.Helper()
	db := setupQuotationConvertDB(t)
	db.Create(&database.TenantBranch{ID: 1, Name: "Principal", IsMain: true, Active: true})
	qs := database.TenantDocumentSeries{BranchID: 1, DocType: "Cotización", Category: "cotizacion", Series: "COT1", Correlative: 1, Active: true}
	nv := database.TenantDocumentSeries{BranchID: 1, DocType: "Nota de Venta", SunatCode: "00", Series: "NV01", Correlative: 1, Active: true}
	if err := db.Create(&qs).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&nv).Error; err != nil {
		t.Fatal(err)
	}
	c := database.TenantContact{Type: "customer", DocType: "6", DocNumber: "10726187938", BusinessName: "Cliente Real SAC", Active: true}
	if err := db.Create(&c).Error; err != nil {
		t.Fatal(err)
	}
	return quotationEnv{db: db, svc: NewQuotationService(db), quoteSer: qs.ID, nvSer: nv.ID, contact: c.ID}
}

func manualItem(o func(*QuotationItemInput)) QuotationItemInput {
	it := QuotationItemInput{
		Code: "MANUAL", Description: "Servicio", Unit: "NIU", Quantity: 1, UnitPrice: 20,
		IgvAffectationType: "10", PriceIncludesIgv: true,
	}
	if o != nil {
		o(&it)
	}
	return it
}

func (e quotationEnv) create(t *testing.T, items []QuotationItemInput, mutate func(*CreateQuotationInput)) (*database.TenantQuotation, error) {
	t.Helper()
	in := CreateQuotationInput{
		BranchID: 1, UserID: 1, SeriesID: e.quoteSer, IssueDate: time.Now(), Currency: "PEN",
		Items: items, TaxConfig: tax.DefaultConfig(),
	}
	if mutate != nil {
		mutate(&in)
	}
	return e.svc.Create(in)
}

func (e quotationEnv) convert(t *testing.T, id uint) (*database.TenantSale, error) {
	t.Helper()
	return e.svc.ConvertToSale(id, ConvertInput{
		Target: "nota_venta", SeriesID: e.nvSer, IssueDate: time.Now(), UserID: 1, TaxConfig: tax.DefaultConfig(),
	})
}

func (e quotationEnv) paymentsTotal(t *testing.T, saleID uint) float64 {
	t.Helper()
	var sum float64
	if err := e.db.Model(&database.TenantSalePayment{}).Where("sale_id = ?", saleID).Select("COALESCE(SUM(amount),0)").Scan(&sum).Error; err != nil {
		t.Fatal(err)
	}
	return sum
}

func near(a, b float64) bool { d := a - b; return d < 0.005 && d > -0.005 }

// ── Validaciones ───────────────────────────────────────────────────────────────────────────

func TestQuotation_Create_RejectsInvalidInput(t *testing.T) {
	e := newQuotationEnv(t)
	inactive := database.TenantProduct{Code: "OFF", Name: "Inactivo", Type: "product", Unit: "NIU", SalePrice: 10, IgvAffectationType: "10", BranchID: 1, Active: true}
	if err := e.db.Create(&inactive).Error; err != nil {
		t.Fatal(err)
	}
	if err := e.db.Model(&inactive).Update("active", false).Error; err != nil {
		t.Fatal(err)
	}
	missing := uint(999999)
	inactiveID := inactive.ID
	past := time.Now().AddDate(-1, 0, 0)
	supplier := database.TenantContact{Type: "supplier", DocType: "6", DocNumber: "20100070970", BusinessName: "Proveedor", Active: true}
	e.db.Create(&supplier)
	supplierID := supplier.ID
	badContact := uint(888888)

	cases := []struct {
		name    string
		items   []QuotationItemInput
		mutate  func(*CreateQuotationInput)
		wantErr string
	}{
		{"cantidad negativa", []QuotationItemInput{manualItem(func(i *QuotationItemInput) { i.Quantity = -5 })}, nil, "cantidad inválida"},
		{"cantidad cero", []QuotationItemInput{manualItem(func(i *QuotationItemInput) { i.Quantity = 0 })}, nil, "cantidad inválida"},
		{"precio cero", []QuotationItemInput{manualItem(func(i *QuotationItemInput) { i.UnitPrice = 0 })}, nil, "precio de venta válido"},
		{"descripción vacía en línea manual", []QuotationItemInput{manualItem(func(i *QuotationItemInput) { i.Description = " " })}, nil, "no tiene descripción"},
		{"descuento negativo (monto bruto)", []QuotationItemInput{manualItem(func(i *QuotationItemInput) { i.Discount = -10 })}, nil, "negativo"},
		{"descuento mayor al importe", []QuotationItemInput{manualItem(func(i *QuotationItemInput) { i.LineDiscountMode = "amount"; i.LineDiscountValue = 500 })}, nil, "supera el importe"},
		{"descuento legado mayor al bruto", []QuotationItemInput{manualItem(func(i *QuotationItemInput) { i.Discount = 999 })}, nil, "supera el importe"},
		{"porcentaje > 100", []QuotationItemInput{manualItem(func(i *QuotationItemInput) { i.LineDiscountMode = "percent"; i.LineDiscountValue = 150 })}, nil, "100%"},
		{"porcentaje negativo", []QuotationItemInput{manualItem(func(i *QuotationItemInput) { i.LineDiscountMode = "percent"; i.LineDiscountValue = -10 })}, nil, "negativo"},
		{"modo de descuento desconocido", []QuotationItemInput{manualItem(func(i *QuotationItemInput) { i.LineDiscountMode = "xx"; i.LineDiscountValue = 1 })}, nil, "modo de descuento"},
		{"afectación fuera de catálogo", []QuotationItemInput{manualItem(func(i *QuotationItemInput) { i.IgvAffectationType = "99" })}, nil, "afectación IGV inválido"},
		{"producto inexistente", []QuotationItemInput{manualItem(func(i *QuotationItemInput) { i.ProductID = &missing })}, nil, "no existe"},
		{"producto inactivo", []QuotationItemInput{manualItem(func(i *QuotationItemInput) { i.ProductID = &inactiveID })}, nil, "inactivo"},
		{"cliente inexistente", []QuotationItemInput{manualItem(nil)}, func(in *CreateQuotationInput) { in.ContactID = &badContact }, "cliente seleccionado no existe"},
		{"contacto que no es cliente", []QuotationItemInput{manualItem(nil)}, func(in *CreateQuotationInput) { in.ContactID = &supplierID }, "no es un cliente válido"},
		{"USD sin tipo de cambio", []QuotationItemInput{manualItem(nil)}, func(in *CreateQuotationInput) { in.Currency = "USD" }, "tipo de cambio"},
		{"vigencia anterior a la emisión", []QuotationItemInput{manualItem(nil)}, func(in *CreateQuotationInput) { in.ValidUntil = &past }, "vigencia"},
		{"descuento global mayor al subtotal", []QuotationItemInput{manualItem(nil)}, func(in *CreateQuotationInput) { in.GlobalDiscountMode = "amount"; in.GlobalDiscountValue = 500 }, "descuento global"},
		{"descuento global > 100%", []QuotationItemInput{manualItem(nil)}, func(in *CreateQuotationInput) { in.GlobalDiscountMode = "percent"; in.GlobalDiscountValue = 120 }, "100%"},
		{"descuento global negativo", []QuotationItemInput{manualItem(nil)}, func(in *CreateQuotationInput) { in.GlobalDiscountMode = "amount"; in.GlobalDiscountValue = -5 }, "negativo"},
		{"sin ítems", nil, nil, "al menos un ítem"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q, err := e.create(t, tc.items, tc.mutate)
			if err == nil {
				t.Fatalf("esperaba error (%q) y se guardó la cotización %+v", tc.wantErr, q)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %q, debía contener %q", err.Error(), tc.wantErr)
			}
		})
	}
	var n int64
	e.db.Model(&database.TenantQuotation{}).Count(&n)
	if n != 0 {
		t.Fatalf("ninguna cotización inválida debe persistirse, hay %d", n)
	}
	var ser database.TenantDocumentSeries
	e.db.First(&ser, e.quoteSer)
	if ser.Correlative != 1 {
		t.Fatalf("las cotizaciones rechazadas no deben consumir correlativo: serie en %d", ser.Correlative)
	}
}

func TestQuotation_Create_AcceptsValidEdgeCases(t *testing.T) {
	e := newQuotationEnv(t)
	cases := map[string]struct {
		items  []QuotationItemInput
		mutate func(*CreateQuotationInput)
		total  float64
	}{
		"100% de descuento → total 0": {[]QuotationItemInput{manualItem(func(i *QuotationItemInput) { i.LineDiscountMode = "percent"; i.LineDiscountValue = 100 })}, nil, 0},
		"USD con tipo de cambio":      {[]QuotationItemInput{manualItem(nil)}, func(in *CreateQuotationInput) { r := 3.8; in.Currency = "USD"; in.ExchangeRate = &r }, 20},
		"vigencia = emisión":          {[]QuotationItemInput{manualItem(nil)}, func(in *CreateQuotationInput) { v := in.IssueDate; in.ValidUntil = &v }, 20},
		"sin cliente":                 {[]QuotationItemInput{manualItem(nil)}, nil, 20},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			q, err := e.create(t, tc.items, tc.mutate)
			if err != nil {
				t.Fatalf("debía aceptarse: %v", err)
			}
			if !near(q.Total, tc.total) {
				t.Fatalf("total = %.2f, want %.2f", q.Total, tc.total)
			}
		})
	}
}

// ── Descuentos: se guardan como se teclearon y sobreviven editar ──────────────────────────

func TestQuotation_Discounts_RoundTripThroughEdit(t *testing.T) {
	e := newQuotationEnv(t)
	cid := e.contact

	// Línea 10 % sobre S/ 20 con IGV incluido = S/ 18 (el caso que antes pasaba a S/ 20 al editar).
	q, err := e.create(t, []QuotationItemInput{manualItem(func(i *QuotationItemInput) {
		i.LineDiscountMode = "percent"
		i.LineDiscountValue = 10
	})}, func(in *CreateQuotationInput) { in.ContactID = &cid })
	if err != nil {
		t.Fatal(err)
	}
	if !near(q.Total, 18) {
		t.Fatalf("total inicial = %.2f, want 18", q.Total)
	}
	_, items, _ := e.svc.GetByID(q.ID)
	if items[0].LineDiscountMode != "percent" || !near(items[0].LineDiscountValue, 10) {
		t.Fatalf("el descuento debe guardarse como se tecleó: %q %.2f", items[0].LineDiscountMode, items[0].LineDiscountValue)
	}

	// Editar re-enviando lo que devuelve GetByID (modo+valor) → el total no cambia.
	upd, err := e.svc.Update(q.ID, UpdateQuotationInput{
		ContactID: &cid, SeriesID: e.quoteSer, IssueDate: time.Now(), Currency: "PEN", TaxConfig: tax.DefaultConfig(),
		Items: []QuotationItemInput{manualItem(func(i *QuotationItemInput) {
			i.LineDiscountMode = items[0].LineDiscountMode
			i.LineDiscountValue = items[0].LineDiscountValue
		})},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !near(upd.Total, 18) {
		t.Fatalf("tras editar sin cambios el total debe seguir en 18, quedó %.2f", upd.Total)
	}
	if upd.ContactID == nil || *upd.ContactID != cid {
		t.Fatalf("el cliente debe conservarse al editar: %v", upd.ContactID)
	}

	// Descuento global 15 % sobre S/ 100 con IGV incluido = S/ 85, y se guarda el "15 %".
	g, err := e.create(t, []QuotationItemInput{manualItem(func(i *QuotationItemInput) { i.UnitPrice = 100 })},
		func(in *CreateQuotationInput) { in.GlobalDiscountMode = "percent"; in.GlobalDiscountValue = 15 })
	if err != nil {
		t.Fatal(err)
	}
	if !near(g.Total, 85) || g.GlobalDiscountMode != "percent" || !near(g.GlobalDiscountValue, 15) || g.GlobalDiscountAmount <= 0 {
		t.Fatalf("global 15%%: total=%.2f modo=%q valor=%.2f monto=%.2f", g.Total, g.GlobalDiscountMode, g.GlobalDiscountValue, g.GlobalDiscountAmount)
	}
}

// Un cliente anterior que solo manda `discount` (monto bruto) sigue funcionando.
func TestQuotation_LegacyDiscountOnlyStillWorks(t *testing.T) {
	e := newQuotationEnv(t)
	q, err := e.create(t, []QuotationItemInput{manualItem(func(i *QuotationItemInput) { i.Discount = 2 })}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !near(q.Total, 18) {
		t.Fatalf("total con discount legado = %.2f, want 18", q.Total)
	}
}

// price_includes_igv=false debe persistirse (antes `default:true` lo pisaba y la conversión
// cambiaba el total).
func TestQuotation_PriceWithoutIgvIsPersisted(t *testing.T) {
	e := newQuotationEnv(t)
	q, err := e.create(t, []QuotationItemInput{manualItem(func(i *QuotationItemInput) {
		i.Quantity = 2
		i.UnitPrice = 50
		i.PriceIncludesIgv = false
	})}, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, items, _ := e.svc.GetByID(q.ID)
	if items[0].PriceIncludesIgv {
		t.Fatal("price_includes_igv=false se guardó como true")
	}
	if !near(q.Total, 118) {
		t.Fatalf("2×50 sin IGV = 118, got %.2f", q.Total)
	}
}

// ── La cotización y la venta convertida valen lo mismo, al céntimo, y el pago es el de la venta ──

func TestQuotation_TotalMatchesConvertedSale(t *testing.T) {
	scenarios := map[string]struct {
		items  []QuotationItemInput
		mutate func(*CreateQuotationInput)
		total  float64
	}{
		"descuento % de línea, IGV incluido": {
			[]QuotationItemInput{manualItem(func(i *QuotationItemInput) { i.LineDiscountMode = "percent"; i.LineDiscountValue = 10 })}, nil, 18},
		"monto fijo, IGV NO incluido": {
			[]QuotationItemInput{manualItem(func(i *QuotationItemInput) {
				i.Quantity = 2
				i.UnitPrice = 50
				i.PriceIncludesIgv = false
				i.LineDiscountMode = "amount"
				i.LineDiscountValue = 5
			})}, nil, 112.10},
		"redondeo 9.31 x3 con 15 %": {
			[]QuotationItemInput{manualItem(func(i *QuotationItemInput) {
				i.Quantity = 3
				i.UnitPrice = 9.31
				i.LineDiscountMode = "percent"
				i.LineDiscountValue = 15
			})}, nil, 23.74},
		"global % sobre dos líneas": {
			[]QuotationItemInput{manualItem(func(i *QuotationItemInput) { i.UnitPrice = 30 }), manualItem(func(i *QuotationItemInput) { i.UnitPrice = 70 })},
			func(in *CreateQuotationInput) { in.GlobalDiscountMode = "percent"; in.GlobalDiscountValue = 15 }, 85},
		"global monto fijo": {
			[]QuotationItemInput{manualItem(func(i *QuotationItemInput) { i.UnitPrice = 100 })},
			func(in *CreateQuotationInput) { in.GlobalDiscountMode = "amount"; in.GlobalDiscountValue = 5 }, 94.10},
		"línea + global combinados": {
			[]QuotationItemInput{manualItem(func(i *QuotationItemInput) { i.UnitPrice = 100; i.LineDiscountMode = "percent"; i.LineDiscountValue = 10 })},
			func(in *CreateQuotationInput) { in.GlobalDiscountMode = "percent"; in.GlobalDiscountValue = 5 }, 85.5},
		"gravado + exonerado + inafecto": {
			[]QuotationItemInput{
				manualItem(func(i *QuotationItemInput) { i.UnitPrice = 100; i.IgvAffectationType = "20" }),
				manualItem(func(i *QuotationItemInput) { i.UnitPrice = 100; i.IgvAffectationType = "30" }),
				manualItem(func(i *QuotationItemInput) { i.UnitPrice = 118 }),
			}, nil, 318},
		"bonificación (15) no suma al total": {
			[]QuotationItemInput{manualItem(func(i *QuotationItemInput) { i.UnitPrice = 100 }),
				manualItem(func(i *QuotationItemInput) { i.UnitPrice = 50; i.IgvAffectationType = "15" })}, nil, 100},
		"100 % de descuento": {
			[]QuotationItemInput{manualItem(func(i *QuotationItemInput) { i.LineDiscountMode = "percent"; i.LineDiscountValue = 100 })}, nil, 0},
	}
	for name, sc := range scenarios {
		t.Run(name, func(t *testing.T) {
			e := newQuotationEnv(t)
			q, err := e.create(t, sc.items, sc.mutate)
			if err != nil {
				t.Fatal(err)
			}
			if !near(q.Total, sc.total) {
				t.Fatalf("total de la cotización = %.2f, want %.2f", q.Total, sc.total)
			}
			sale, err := e.convert(t, q.ID)
			if err != nil {
				t.Fatalf("convertir: %v", err)
			}
			if !near(sale.Total, q.Total) {
				t.Fatalf("venta = %.2f ≠ cotización = %.2f", sale.Total, q.Total)
			}
			if got := e.paymentsTotal(t, sale.ID); !near(got, sale.Total) {
				t.Fatalf("pago registrado = %.2f ≠ total de la venta = %.2f", got, sale.Total)
			}
		})
	}
}

// ── Conversión atómica ──────────────────────────────────────────────────────────────────────

func TestQuotation_ConvertTwiceCreatesOneSale(t *testing.T) {
	e := newQuotationEnv(t)
	q, err := e.create(t, []QuotationItemInput{manualItem(nil)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.convert(t, q.ID); err != nil {
		t.Fatalf("primera conversión: %v", err)
	}
	if _, err := e.convert(t, q.ID); err == nil {
		t.Fatal("la segunda conversión debe fallar")
	}
	var sales int64
	e.db.Model(&database.TenantSale{}).Count(&sales)
	if sales != 1 {
		t.Fatalf("debe existir 1 venta, hay %d", sales)
	}
}

// Si otra conversión ganó entre el chequeo y el commit, el UPDATE condicional falla, la
// transacción hace rollback y NO queda venta ni correlativo consumido.
func TestClaimForConversionTx_RollsBackSaleWhenAlreadyClaimed(t *testing.T) {
	e := newQuotationEnv(t)
	q, err := e.create(t, []QuotationItemInput{manualItem(nil)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Otra petición ya reclamó la cotización.
	if err := e.db.Model(&database.TenantQuotation{}).Where("id = ?", q.ID).Update("status", "converted").Error; err != nil {
		t.Fatal(err)
	}
	var before database.TenantDocumentSeries
	e.db.First(&before, e.nvSer)

	// Se salta el chequeo temprano de ConvertToSale llamando directo al reclamo dentro de una venta.
	err = e.db.Transaction(func(tx *gorm.DB) error {
		return ClaimForConversionTx(tx, q.ID, 1, "nota_venta")
	})
	if !errors.Is(err, ErrQuotationAlreadyConverted) {
		t.Fatalf("esperaba ErrQuotationAlreadyConverted, obtuve %v", err)
	}
	var after database.TenantDocumentSeries
	e.db.First(&after, e.nvSer)
	if after.Correlative != before.Correlative {
		t.Fatalf("el correlativo no debe consumirse: %d → %d", before.Correlative, after.Correlative)
	}
}

func TestQuotation_EditRules(t *testing.T) {
	e := newQuotationEnv(t)
	cid := e.contact
	q, err := e.create(t, []QuotationItemInput{manualItem(nil)}, func(in *CreateQuotationInput) { in.ContactID = &cid })
	if err != nil {
		t.Fatal(err)
	}
	// Cambiar de serie no está permitido (la cotización ya está numerada).
	other := database.TenantDocumentSeries{BranchID: 1, DocType: "Cotización", Category: "cotizacion", Series: "COT2", Correlative: 1, Active: true}
	e.db.Create(&other)
	_, err = e.svc.Update(q.ID, UpdateQuotationInput{
		ContactID: &cid, SeriesID: other.ID, IssueDate: time.Now(), Currency: "PEN", TaxConfig: tax.DefaultConfig(),
		Items: []QuotationItemInput{manualItem(nil)},
	})
	if err == nil || !strings.Contains(err.Error(), "serie") {
		t.Fatalf("cambiar la serie debe rechazarse: %v", err)
	}
	// Convertida: no se edita ni se elimina.
	if _, err := e.convert(t, q.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Update(q.ID, UpdateQuotationInput{
		ContactID: &cid, SeriesID: e.quoteSer, IssueDate: time.Now(), Currency: "PEN", TaxConfig: tax.DefaultConfig(),
		Items: []QuotationItemInput{manualItem(nil)},
	}); err == nil {
		t.Fatal("una cotización convertida no se edita")
	}
	if err := e.svc.Delete(q.ID); err == nil {
		t.Fatal("una cotización convertida no se elimina")
	}
}

// ── Precios: misma regla que la venta (sales.override_price) ──────────────────────────────

func TestQuotation_CatalogPriceMustMatchUnlessUserCanOverride(t *testing.T) {
	e := newQuotationEnv(t)
	p := database.TenantProduct{Code: "SILLA", Name: "Silla", Type: "product", Unit: "NIU", SalePrice: 25,
		IgvAffectationType: "10", PriceIncludesIgv: true, BranchID: 1, Active: true}
	if err := e.db.Create(&p).Error; err != nil {
		t.Fatal(err)
	}
	pid := p.ID
	line := func(price float64) []QuotationItemInput {
		return []QuotationItemInput{{ProductID: &pid, Quantity: 1, UnitPrice: price, Unit: "NIU", IgvAffectationType: "10", PriceIncludesIgv: true}}
	}

	// Sin override: el precio de catálogo pasa; uno alterado se rechaza (igual que en una venta).
	if _, err := e.create(t, line(25), nil); err != nil {
		t.Fatalf("precio de catálogo debe aceptarse: %v", err)
	}
	if _, err := e.create(t, line(0.01), nil); err == nil || !strings.Contains(err.Error(), "precio autorizado") {
		t.Fatalf("precio alterado sin override debe rechazarse con 'precio autorizado': %v", err)
	}
	// Con override: aceptado (precio pactado).
	q, err := e.create(t, line(0.01), func(in *CreateQuotationInput) { in.UserCanOverridePrice = true })
	if err != nil {
		t.Fatalf("con sales.override_price el precio pactado debe aceptarse: %v", err)
	}

	// Un usuario SIN override edita esa cotización sin tocar el precio (pactado por otro): permitido.
	cid := e.contact
	if _, err := e.svc.Update(q.ID, UpdateQuotationInput{
		ContactID: &cid, SeriesID: e.quoteSer, IssueDate: time.Now(), Currency: "PEN", Notes: "solo cambia la nota",
		TaxConfig: tax.DefaultConfig(), Items: line(0.01),
	}); err != nil {
		t.Fatalf("editar sin cambiar el precio pactado no debe exigir override: %v", err)
	}
	// Pero si ese mismo usuario CAMBIA el precio a otro distinto del catálogo, se rechaza.
	if _, err := e.svc.Update(q.ID, UpdateQuotationInput{
		ContactID: &cid, SeriesID: e.quoteSer, IssueDate: time.Now(), Currency: "PEN",
		TaxConfig: tax.DefaultConfig(), Items: line(0.02),
	}); err == nil {
		t.Fatal("cambiar el precio a uno no autorizado sin override debe rechazarse")
	}
}

// ── Unidad de venta, series y combos se conservan hasta la venta ──────────────────────────

func (e quotationEnv) withInventoryTables(t *testing.T) {
	t.Helper()
	for _, m := range []interface{}{
		&database.TenantProductSerial{}, &database.TenantProductSaleUnit{}, &database.TenantProductSaleUnitBranchPrice{},
		&database.TenantProductAttribute{}, &database.TenantCashMovement{}, &database.TenantBankMovement{}, &database.TenantBankAccount{},
		&database.TenantModifierGroup{}, &database.TenantModifierOption{}, &database.TenantProductModifierGroup{},
		&database.TenantComboGroup{}, &database.TenantComboGroupItem{},
	} {
		if err := e.db.AutoMigrate(m); err != nil {
			t.Fatal(err)
		}
	}
}

func (e quotationEnv) stockOf(t *testing.T, productID uint) float64 {
	t.Helper()
	var st database.TenantProductStock
	if err := e.db.Where("product_id = ? AND branch_id = 1", productID).First(&st).Error; err != nil {
		t.Fatal(err)
	}
	return st.Quantity
}

// Cotizar 2 «Saco 100 KG» y convertir debe descontar 200 KG, igual que una venta directa
// (antes descontaba 2: la venta salía bien y el inventario quedaba mal, sin ningún error).
func TestQuotation_SaleUnitIsPreservedAndStockMatchesDirectSale(t *testing.T) {
	e := newQuotationEnv(t)
	e.withInventoryTables(t)
	p := database.TenantProduct{Code: "ARR", Name: "Arroz", Type: "product", Unit: "KGM", SalePrice: 4.5,
		IgvAffectationType: "10", PriceIncludesIgv: true, ManageStock: true, BranchID: 1, Active: true}
	e.db.Create(&p)
	e.db.Create(&database.TenantProductStock{ProductID: p.ID, BranchID: 1, Quantity: 500})
	su := database.TenantProductSaleUnit{ProductID: p.ID, Name: "Saco 100 KG", ConversionFactor: 100, AllowFraction: true, Price1: 450, Active: true}
	e.db.Create(&su)
	pid, suid := p.ID, su.ID

	q, err := e.create(t, []QuotationItemInput{{
		ProductID: &pid, SaleUnitID: &suid, Quantity: 2, UnitPrice: 450, Unit: "KGM", IgvAffectationType: "10", PriceIncludesIgv: true,
	}}, nil)
	if err != nil {
		t.Fatalf("crear: %v", err)
	}
	_, items, _ := e.svc.GetByID(q.ID)
	if items[0].SaleUnitID == nil || *items[0].SaleUnitID != suid {
		t.Fatalf("la unidad de venta debe guardarse en la línea: %v", items[0].SaleUnitID)
	}
	if _, err := e.convert(t, q.ID); err != nil {
		t.Fatalf("convertir: %v", err)
	}
	if got := e.stockOf(t, pid); !near(got, 300) {
		t.Fatalf("stock tras vender 2 sacos = %.2f, want 300 (500 − 2×100)", got)
	}

	// Unidad de venta inexistente / de otro producto: se rechaza al cotizar, no al convertir.
	bad := uint(9999)
	if _, err := e.create(t, []QuotationItemInput{{
		ProductID: &pid, SaleUnitID: &bad, Quantity: 1, UnitPrice: 450, Unit: "KGM", IgvAffectationType: "10", PriceIncludesIgv: true,
	}}, nil); err == nil {
		t.Fatal("una unidad de venta inexistente debe rechazarse al cotizar")
	}
}

// Las series elegidas al cotizar son las que se venden.
func TestQuotation_SerialsAreCarriedToTheSale(t *testing.T) {
	e := newQuotationEnv(t)
	e.withInventoryTables(t)
	p := database.TenantProduct{Code: "LAP", Name: "Laptop", Type: "product", Unit: "NIU", SalePrice: 100,
		IgvAffectationType: "10", PriceIncludesIgv: true, ManageStock: true, ManageSeries: true, BranchID: 1, Active: true}
	e.db.Create(&p)
	e.db.Create(&database.TenantProductStock{ProductID: p.ID, BranchID: 1, Quantity: 3})
	for _, s := range []string{"S1", "S2", "S3"} {
		e.db.Create(&database.TenantProductSerial{ProductID: p.ID, BranchID: 1, Serial: s, Status: "available"})
	}
	pid := p.ID
	q, err := e.create(t, []QuotationItemInput{{
		ProductID: &pid, Quantity: 1, UnitPrice: 100, Unit: "NIU", IgvAffectationType: "10", PriceIncludesIgv: true, Serials: []string{"S2"},
	}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.convert(t, q.ID); err != nil {
		t.Fatalf("convertir: %v", err)
	}
	status := func(serial string) string {
		var s database.TenantProductSerial
		e.db.Where("product_id = ? AND serial = ?", pid, serial).First(&s)
		return s.Status
	}
	if status("S2") != "sold" || status("S1") != "available" || status("S3") != "available" {
		t.Fatalf("debía venderse la serie elegida (S2): S1=%s S2=%s S3=%s", status("S1"), status("S2"), status("S3"))
	}
}

// Un combo cotizado se vende como combo: precio del grupo y salida de stock de sus componentes
// (antes la cotización ignoraba combo_json y la venta convertida no resolvía los componentes
// elegidos).
func TestQuotation_ComboConvertsWithComponentStock(t *testing.T) {
	e := newQuotationEnv(t)
	e.withInventoryTables(t)
	newProduct := func(name, code string, price float64) database.TenantProduct {
		p := database.TenantProduct{Code: code, Name: name, Type: "product", Unit: "NIU", SalePrice: price,
			IgvAffectationType: "10", PriceIncludesIgv: true, ManageStock: true, BranchID: 1, Active: true}
		if err := e.db.Create(&p).Error; err != nil {
			t.Fatal(err)
		}
		e.db.Create(&database.TenantProductStock{ProductID: p.ID, BranchID: 1, Quantity: 10})
		return p
	}
	polo := newProduct("Polo", "POLO", 10)
	pant := newProduct("Pantalón", "PANT", 30)
	combo := database.TenantProduct{Code: "PROMO", Name: "Promoción verano", Type: "product", Unit: "NIU", SalePrice: 20,
		IgvAffectationType: "10", PriceIncludesIgv: true, BranchID: 1, HasCombo: true, Active: true}
	if err := e.db.Create(&combo).Error; err != nil {
		t.Fatal(err)
	}
	for i, comp := range []database.TenantProduct{polo, pant} {
		g := database.TenantComboGroup{ProductID: combo.ID, Name: comp.Name, SelectionType: database.ComboSelectionFixed,
			MinSelect: 1, MaxSelect: 1, SortOrder: i, Active: true}
		e.db.Create(&g)
		e.db.Create(&database.TenantComboGroupItem{GroupID: g.ID, ProductID: comp.ID, DefaultQuantity: 1, MaxQuantity: 1, Active: true})
	}
	cid := combo.ID
	q, err := e.create(t, []QuotationItemInput{{
		ProductID: &cid, Quantity: 1, UnitPrice: 20, Unit: "NIU", IgvAffectationType: "10", PriceIncludesIgv: true,
	}}, nil)
	if err != nil {
		t.Fatalf("cotizar combo: %v", err)
	}
	if !near(q.Total, 20) {
		t.Fatalf("el combo cuesta 20 (no 40 = suma de componentes): %.2f", q.Total)
	}
	sale, err := e.convert(t, q.ID)
	if err != nil {
		t.Fatalf("convertir combo: %v", err)
	}
	if !near(sale.Total, 20) {
		t.Fatalf("venta del combo = %.2f, want 20", sale.Total)
	}
	if e.stockOf(t, polo.ID) != 9 || e.stockOf(t, pant.ID) != 9 {
		t.Fatalf("deben salir los componentes: polo=%.0f pantalón=%.0f (want 9 y 9)", e.stockOf(t, polo.ID), e.stockOf(t, pant.ID))
	}
}

// El documento impreso de la cotización trae el mismo desglose de descuentos que el de la venta
// (por línea y global), para que el PDF los muestre igual y no los reconstruya con una heurística.
func TestQuotation_PrintDataHasSameDiscountBreakdownAsSale(t *testing.T) {
	e := newQuotationEnv(t)
	q, err := e.create(t, []QuotationItemInput{manualItem(func(i *QuotationItemInput) {
		i.UnitPrice = 100
		i.LineDiscountMode = "percent"
		i.LineDiscountValue = 10
	})}, func(in *CreateQuotationInput) { in.GlobalDiscountMode = "amount"; in.GlobalDiscountValue = 5 })
	if err != nil {
		t.Fatal(err)
	}
	pd, err := BuildPrintDataForQuotation(e.db, q.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !near(pd.LineDiscountTotal, 8.47) || !near(pd.GlobalDiscountAmount, 5) {
		t.Fatalf("desglose del documento: línea=%.2f (want 8.47) global=%.2f (want 5.00)", pd.LineDiscountTotal, pd.GlobalDiscountAmount)
	}
	it := pd.Items[0]
	if !near(it.LineDiscountSubtotal, 8.47) || !near(it.GlobalDiscountSubtotal, 5) {
		t.Fatalf("desglose de la línea: %.2f / %.2f", it.LineDiscountSubtotal, it.GlobalDiscountSubtotal)
	}

	// Es el mismo desglose que registra la venta al convertir.
	sale, err := e.convert(t, q.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !near(sale.GlobalDiscountAmount, pd.GlobalDiscountAmount) || !near(sale.Total, q.Total) {
		t.Fatalf("venta: global=%.2f total=%.2f vs cotización: global=%.2f total=%.2f",
			sale.GlobalDiscountAmount, sale.Total, pd.GlobalDiscountAmount, q.Total)
	}

	// Sin descuentos: no se inventa desglose.
	plain, _ := e.create(t, []QuotationItemInput{manualItem(nil)}, nil)
	pp, _ := BuildPrintDataForQuotation(e.db, plain.ID)
	if pp.LineDiscountTotal != 0 || pp.GlobalDiscountAmount != 0 {
		t.Fatalf("sin descuentos no debe haber desglose: %.2f / %.2f", pp.LineDiscountTotal, pp.GlobalDiscountAmount)
	}
}

// El PDF de la cotización muestra Subtotal/descuentos/IGV igual que una venta (régimen general).
func TestQuotation_PrintDataShowsIgvBreakdownLikeSale(t *testing.T) {
	e := newQuotationEnv(t)
	q, err := e.create(t, []QuotationItemInput{manualItem(nil)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	pd, err := BuildPrintDataForQuotation(e.db, q.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !pd.Company.ShowIgvBreakdown {
		t.Fatal("en régimen general el documento debe discriminar subtotal e IGV, como una venta")
	}
}

// Regresión: el formulario manda en CADA línea `discount` = descuento bruto ya calculado, que
// incluye la parte repartida del descuento global. Una línea sin descuento propio no debe
// interpretarlo como descuento de línea (aplicaba el global dos veces: 216.13 en vez de 218.50).
func TestQuotation_FormPayloadWithGlobalDiscountIsNotAppliedTwice(t *testing.T) {
	e := newQuotationEnv(t)
	q, err := e.create(t, []QuotationItemInput{
		manualItem(func(i *QuotationItemInput) {
			i.Description, i.UnitPrice = "Servicio de consultoria", 200
			i.LineDiscountMode, i.LineDiscountValue = "percent", 10
			i.Discount = 29 // 10 % de línea + su parte del 5 % global, en bruto (como lo calcula el formulario)
		}),
		manualItem(func(i *QuotationItemInput) {
			i.Description, i.UnitPrice = "Soporte mensual", 50
			i.Discount = 2.5 // SOLO su parte del global; no es un descuento de línea
		}),
	}, func(in *CreateQuotationInput) { in.GlobalDiscountMode, in.GlobalDiscountValue = "percent", 5 })
	if err != nil {
		t.Fatal(err)
	}
	if !near(q.Total, 218.50) {
		t.Fatalf("total = %.2f, want 218.50 (el global no debe aplicarse dos veces)", q.Total)
	}
	_, items, _ := e.svc.GetByID(q.ID)
	if items[1].LineDiscountMode != "" || items[1].LineDiscountValue != 0 {
		t.Fatalf("la línea sin descuento propio no debe quedar con descuento de línea: %q %.4f", items[1].LineDiscountMode, items[1].LineDiscountValue)
	}
	if !near(items[0].Total, 171.00) || !near(items[1].Total, 47.50) {
		t.Fatalf("totales por línea = %.2f / %.2f, want 171.00 / 47.50", items[0].Total, items[1].Total)
	}
	// Y la venta convertida vale lo mismo.
	sale, err := e.convert(t, q.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !near(sale.Total, 218.50) {
		t.Fatalf("venta = %.2f, want 218.50", sale.Total)
	}
}
