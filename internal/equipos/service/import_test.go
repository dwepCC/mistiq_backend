package service

import (
	"encoding/json"
	"strings"
	"testing"

	"tukifac/pkg/database"
)

// miniWorkbook arma un libro pequeño con la MISMA estructura que «CONTROL DE EQUIPOS»: sin datos reales de clientes.
func miniWorkbook(t *testing.T) ImportPayload {
	t.Helper()
	row := func(v ...any) []any { return v }
	sheets := map[string][][]any{
		sheetCatalog: {
			row("CATÁLOGO DE PRODUCTOS"), row("nota"), row(),
			row("N°", "Producto / Equipo", "Tipo", "Notas"),
			row(1, "PRINT-A", "Individual", "Portátil 80mm"),
			row(2, "ROLL", "Individual", ""),
			row("➕ AGREGAR NUEVOS PRODUCTOS", nil, nil, nil), row(),
			row("N°", "Combo", "Tipo", "Composición"),
			row(1, "COMBO X", "Combo", "Ver hoja"),
		},
		sheetCombos: {
			row("COMPOSICIÓN DE COMBOS"), row("nota"), row(),
			row("Combo", "Componente 1", "Cant.", "Componente 2", "Cant."),
			row("COMBO X", "PRINT-A", 1, "ROLL", 10),
			row(),
		},
		sheetDetail: {
			row("DETALLE DE PEDIDOS"), row("nota"),
			row("N° Orden Envío", "Equipo / Producto", "Cant.", "Precio Unit. (S/)", "Subtotal (S/)", "Observaciones"),
			row(1, "PRINT-A", 1, 100, 100, ""),
			row(1, "ROLL", 5, 2, 10, ""),
			row(2, "COMBO X", 1, 150, 150, ""),
			row(2, "Plan Semestral ", 1, 177, 177, ""),
			row(2, "PRINT-A", 1, 0, nil, ""),
			row("💡 nota al pie"),
		},
		sheetOrders: {
			row("CONTROL DE ENVÍOS"), row("Transportista: SHALOM"), row(),
			row("N° Orden", "Tipo de Salida", "Fecha Despacho", "Cliente", "Boleta o Factura", "DNI / RUC", "Teléfono WhatsApp", "Resumen", "Total Ítems",
				"N° Guía", "Agencia Destino", "Provincia / Depto.", "Total Venta", "Abono Inicial", "Hora Depósito", "Factura", "Saldo Pendiente",
				"Hora Depósito", "Factura", "Fecha Llegada Real", "Días Llegada", "Estado Pago", "Estado Envío", "Validación", "Obs"),
			row(1, "Independiente", "2026-08-03T00:00:00.000Z", "Juan Pérez", "Boleta", 12345678, "999 888 777", "", 6, nil, "Arequipa", "Entregado en Oficina",
				110, 110, "1899-12-31T10:30:00.000Z", "B001-5", 0, nil, nil, nil, "", "Pagado", nil, nil, nil),
			row(2, "Promo TK", "2026-08-05T00:00:00.000Z", "EMPRESA SAC", 20123456789, 87654321, "Magu_hen", "", 4, 12345, "SJL", "Lima / Lima / San Juan De Lurigancho / Sjl",
				327, 50, "1899-12-31T09:00:00.000Z", "F002 - 8", 277, "1899-12-31T18:05:00.000Z", "F002-9", "2026-08-09T00:00:00.000Z", 3, "Pagado", "Entregado", nil, nil),
			row(3, nil, nil, nil, nil, nil, nil, "—", nil, nil, nil, nil, 0, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil),
		},
		sheetReturns: {
			row("REGISTRO DE RETORNOS"),
			row("N° Orden", "Fecha Solicitud", "Cliente", "N° Guía", "Equipos (detalle)", "Saldo No Cobrado", "Costo Retorno", "Fecha Retorno Rec.", "Estado", "Acción", "Obs"),
			row(1, "2026-08-02T00:00:00.000Z", "ANA LÓPEZ", "999", "print-a", 0, 12, "2026-08-10T00:00:00.000Z", "Recibido", "Retorno", "Equipo en buen estado"),
			row(2, nil, "LUIS", "998", "PRINT-A", 0, 0, "2026-08-11T00:00:00.000Z", "Recibido", "Retorno", "Equipo no funciona"),
			row("TOTALES"),
		},
		sheetStock: {
			row("STOCK"), row("nota"), row(),
			row("Mes de Control:", nil, "AGOSTO 2026", nil, "Responsable:", nil, "Ana"),
			row("N°", "Producto", "Stock Inicial", "SALIDAS DIRECTAS (sin combo)", nil, "SALIDAS POR COMBOS", nil, "Total Salidas", "Reingresos", "Stock Actual", "Semáforo", "Umbral Amarillo", "Umbral Verde"),
			row(nil, nil, nil, "Independiente", "Promo TK", "Independiente", "Promo TK"),
			row(1, "PRINT-A", 20, 1, 1, 0, 1, 3, 1, 18, "✅", 5, 10),
			row(2, "ROLL", 100, 5, 0, 0, 10, 15, 0, 85, "✅", 20, 40),
			row(nil, "TOTALES GENERALES"),
		},
	}
	raw, err := json.Marshal(ImportPayload{FileName: "mini.xlsx", Sheets: sheets})
	if err != nil {
		t.Fatal(err)
	}
	var p ImportPayload
	if err := json.Unmarshal(raw, &p); err != nil { // mismo camino que el JSON real: números como float64
		t.Fatal(err)
	}
	return p
}

func TestImportPreview_miniWorkbookReconciles(t *testing.T) {
	svc := New(setupEquiposDB(t))
	pv, err := svc.ImportPreview(miniWorkbook(t))
	if err != nil {
		t.Fatal(err)
	}
	if pv.Period != "2026-08" || pv.StockManager != "Ana" {
		t.Fatalf("período/responsable = %q/%q", pv.Period, pv.StockManager)
	}
	if pv.Counts.Orders != 2 || pv.Counts.Items != 5 || pv.Counts.Returns != 2 || pv.Counts.Products != 2 || pv.Counts.Combos != 1 {
		t.Fatalf("conteos inesperados: %+v", pv.Counts)
	}
	if pv.CodeCounts["pedido_vacio"] != 1 {
		t.Errorf("el pedido 3 (vacío) debe omitirse con aviso: %v", pv.CodeCounts)
	}
	if !pv.ReconOK || !pv.CanCommit {
		t.Fatalf("debe conciliar: recon=%v blocked=%v rows=%+v", pv.ReconOK, pv.BlockedBy, pv.Reconciliation)
	}
	if pv.SalesTotal != 437 { // 110 + 327
		t.Errorf("ventas = %.2f, want 437", pv.SalesTotal)
	}
}

func TestImportCommit_persistsEverythingAndStockMatches(t *testing.T) {
	db := setupEquiposDB(t)
	svc := New(db)
	res, err := svc.ImportCommit(miniWorkbook(t), 7)
	if err != nil {
		t.Fatalf("ImportCommit: %v", err)
	}
	if res.Batch.Orders != 2 || res.Batch.Payments != 3 || res.Batch.Movements == 0 {
		t.Fatalf("lote: %+v", res.Batch)
	}

	// Pedido 2: promo, plan normalizado, cliente con RUC y contacto DNI, cobros con comprobantes normalizados.
	var o database.EquipOrder
	if err := db.Where("order_number = ?", 2).First(&o).Error; err != nil {
		t.Fatal(err)
	}
	if o.SaleType != database.EquipSalePromoTK || o.CustomerDocType != "RUC" || o.CustomerDocNumber != "20123456789" ||
		o.ContactDNI != "87654321" || o.BillingDocType != "factura" || o.PaymentStatus != "pagado" || o.TotalAmount != 327 {
		t.Fatalf("pedido 2 mal importado: %+v", o)
	}
	var items []database.EquipOrderItem
	db.Where("order_id = ?", o.ID).Order("line_no").Find(&items)
	if len(items) != 3 || items[1].LineType != "plan" || items[1].PlanMonths != 6 || items[1].Description != "Plan semestral" {
		t.Fatalf("ítems del pedido 2: %+v", items)
	}
	if items[0].LineType != "combo" || items[0].ComboSnapshotJSON == "" || !items[2].IsCourtesy {
		t.Fatalf("combo/cortesía: %+v", items)
	}
	var pays []database.EquipPayment
	db.Joins("JOIN equip_payment_allocations a ON a.payment_id = equip_payments.id").Where("a.order_id = ?", o.ID).Order("paid_at").Find(&pays)
	if len(pays) != 2 || pays[0].InvoiceSeries != "F002" || pays[0].InvoiceNumber != "8" || pays[1].Moment != "al_recoger" || pays[0].CustomerID == nil {
		t.Fatalf("cobros del pedido 2: %+v", pays)
	}
	var phone database.EquipCustomer
	db.Where("doc_number = ?", "20123456789").First(&phone)
	if phone.PhoneKind != "usuario" {
		t.Errorf("«Magu_hen» debe quedar como usuario, no teléfono: %+v", phone)
	}

	// Pedido 1 sin cliente identificado en documento largo: DNI propio, entregado en oficina.
	var sh database.EquipShipment
	var o1 database.EquipOrder
	db.Where("order_number = ?", 1).First(&o1)
	db.Where("order_id = ?", o1.ID).First(&sh)
	if sh.DeliveryMode != "oficina" || sh.CarrierID == nil {
		t.Fatalf("envío del pedido 1: %+v", sh)
	}
	// Pedido 2: guía, destino separado, llegada y vencimiento a 15 días.
	var sh2 database.EquipShipment
	db.Where("order_id = ?", o.ID).First(&sh2)
	sh = sh2
	if sh.GuideNumber != "12345" || sh.DestinationDepartment != "Lima" || sh.DestinationDistrict != "San Juan De Lurigancho" ||
		sh.Status != "entregado" || sh.PickupDeadline == nil || sh.PickupDeadline.Sub(*sh.ArrivedAt).Hours() != 15*24 {
		t.Fatalf("envío del pedido 2: %+v", sh)
	}

	// Retornos: el de buen estado reingresa; el dañado no.
	var rets []database.EquipReturn
	db.Order("return_number").Find(&rets)
	if len(rets) != 2 || rets[0].Condition != "buen_estado" || rets[1].Condition != "danado" {
		t.Fatalf("retornos: %+v", rets)
	}

	// El Stock Maestro calculado desde el kardex guardado coincide con el del libro.
	report, err := svc.StockReport("2026-08")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]StockRow{}
	for _, r := range report {
		got[r.Code] = r
	}
	a := got["PRINT-A"]
	if a.Opening != 20 || a.DirectIndep != 1 || a.DirectPromo != 1 || a.ComboPromo != 1 || a.TotalOut != 3 || a.Reentries != 1 || a.Current != 18 {
		t.Errorf("PRINT-A: %+v", a)
	}
	r := got["ROLL"]
	if r.Opening != 100 || r.DirectIndep != 5 || r.ComboPromo != 10 || r.Current != 85 {
		t.Errorf("ROLL: %+v", r)
	}
	if a.Yellow != 5 || a.Green != 10 || a.Semaphore != "suficiente" {
		t.Errorf("umbrales/semáforo PRINT-A: %+v", a)
	}

	// Siguiente número de pedido y responsable quedan configurados; el mismo mes no se importa dos veces.
	st, _ := svc.GetSettings()
	if st.NextOrderNumber != 3 || st.StockManager != "Ana" || st.DefaultCarrierID == nil {
		t.Errorf("settings: %+v", st)
	}
	if _, err := svc.ImportCommit(miniWorkbook(t), 7); err == nil || !strings.Contains(err.Error(), "ya fue importado") {
		t.Fatalf("reimportar el mismo período debe rechazarse, err=%v", err)
	}
}

// Si el stock calculado no coincide con el Stock Maestro del libro, NO se importa nada.
func TestImportCommit_blocksWhenStockDoesNotReconcile(t *testing.T) {
	db := setupEquiposDB(t)
	svc := New(db)
	p := miniWorkbook(t)
	p.Sheets[sheetStock][6][9] = 19.0 // el libro dice 19 y el cálculo da 18
	pv, err := svc.ImportPreview(p)
	if err != nil {
		t.Fatal(err)
	}
	if pv.ReconOK || pv.CanCommit {
		t.Fatalf("debe bloquear: %+v", pv.BlockedBy)
	}
	if _, err := svc.ImportCommit(p, 1); err == nil || !strings.Contains(err.Error(), "No se puede importar") {
		t.Fatalf("ImportCommit debe fallar, err=%v", err)
	}
	var n int64
	db.Model(&database.EquipOrder{}).Count(&n)
	if n != 0 {
		t.Fatalf("no debe guardarse nada (hay %d pedidos)", n)
	}
}

func TestImportPreview_reportsErrorsAndNormalizations(t *testing.T) {
	svc := New(setupEquiposDB(t))
	p := miniWorkbook(t)
	// Producto desconocido, pedido duplicado, combo con componente inexistente.
	p.Sheets[sheetDetail] = append(p.Sheets[sheetDetail][:len(p.Sheets[sheetDetail])-1], []any{1.0, "Cable raro", 1.0, 5.0, 5.0, ""})
	p.Sheets[sheetOrders] = append(p.Sheets[sheetOrders], p.Sheets[sheetOrders][4])
	cb := p.Sheets[sheetCombos]
	p.Sheets[sheetCombos] = append(cb[:len(cb)-1], []any{"COMBO Y", "NO-EXISTE", 1.0}, []any{})
	pv, err := svc.ImportPreview(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, code := range []string{"item_desconocido", "pedido_duplicado", "combo_componente", "total_distinto"} {
		if pv.CodeCounts[code] == 0 {
			t.Errorf("falta el hallazgo %q: %v", code, pv.CodeCounts)
		}
	}
	if pv.CanCommit {
		t.Fatal("con errores no se puede importar")
	}
}

func TestImportPreview_requiresStockSheet(t *testing.T) {
	svc := New(setupEquiposDB(t))
	p := miniWorkbook(t)
	delete(p.Sheets, sheetStock)
	pv, err := svc.ImportPreview(p)
	if err != nil {
		t.Fatal(err)
	}
	if pv.CanCommit || pv.CodeCounts["sin_stock"] == 0 || pv.CodeCounts["sin_periodo"] == 0 {
		t.Fatalf("sin Stock Maestro no puede conciliar: %v / %v", pv.CodeCounts, pv.BlockedBy)
	}
}

func TestParseHelpers(t *testing.T) {
	d := classifyDoc("Boleta", "12345678")
	if d.DocType != "DNI" || d.DocNumber != "12345678" || d.BillingDoc != "boleta" {
		t.Errorf("DNI: %+v", d)
	}
	d = classifyDoc("20123456789", "87654321")
	if d.DocType != "RUC" || d.ContactDNI != "87654321" || d.BillingDoc != "factura" {
		t.Errorf("RUC: %+v", d)
	}
	if d = classifyDoc("Boleta", "-"); d.DocType != "SIN_DOC" {
		t.Errorf("sin documento: %+v", d)
	}
	if p, k, _ := classifyPhone("999 888 777"); p != "999888777" || k != "whatsapp" {
		t.Errorf("teléfono: %q %q", p, k)
	}
	if _, k, _ := classifyPhone("NPZAMORA"); k != "usuario" {
		t.Errorf("usuario: %q", k)
	}
	if _, _, w := classifyPhone("72807342"); w == "" {
		t.Error("8 dígitos debe advertir")
	}
	if dt, s, n, ok := parseInvoice(" f002 - 0059 "); !ok || dt != "factura" || s != "F002" || n != "59" {
		t.Errorf("comprobante: %q %q %q %v", dt, s, n, ok)
	}
	for in, want := range map[string]int{"Plan semestral": 6, "PLAN ANUAL ": 12, "plan semestral emprendedor": 6, "Plan mensual": 1, "PROMOCION SEMESTRAL": 6, "Plan emprendedor": 0} {
		if is, m := planMonths(in); !is || m != want {
			t.Errorf("planMonths(%q) = %v,%d want %d", in, is, m, want)
		}
	}
	if is, _ := planMonths("TK-E583"); is {
		t.Error("un equipo no es un plan")
	}
	if d := parseDestination("Ica / Nazca / Vista Alegre / Vista Alegre Co"); d.Department != "Ica" || d.Province != "Nazca" || d.District != "Vista Alegre" || !d.Known {
		t.Errorf("destino: %+v", d)
	}
	if d := parseDestination("Entregado en Oficina"); d.Mode != "oficina" {
		t.Errorf("oficina: %+v", d)
	}
	if per, ok := parsePeriod("SETIEMBRE 2026"); !ok || per != "2026-09" {
		t.Errorf("período: %q", per)
	}
	if k := inferKind("CONT. 58MM", "CONT. 58MM"); k != "consumible" {
		t.Errorf("kind: %q", k)
	}
	if pt := cellTime([]any{"1899-12-31T18:05:00.000Z"}, 0); !pt.HasTime || pt.Hour != 18 || pt.Minute != 5 || pt.Date != nil {
		t.Errorf("hora suelta: %+v", pt)
	}
	if pt := cellTime([]any{"2026-08-03T00:00:00.000Z"}, 0); pt.Date == nil || pt.Date.Format("2006-01-02") != "2026-08-03" {
		t.Errorf("fecha: %+v", pt)
	}
}

// El libro dice solo «OCTUBRE» (sin año): el año se toma de las fechas del libro, con un aviso, en vez de bloquear.
func TestImportPreview_mesDeControlSinAnioSeInfiereDeLasFechas(t *testing.T) {
	p := miniWorkbook(t)
	for _, r := range p.Sheets[sheetStock] {
		if len(r) > 2 && r[2] == "AGOSTO 2026" {
			r[2] = "AGOSTO"
		}
	}
	pv, err := New(setupEquiposDB(t)).ImportPreview(p)
	if err != nil {
		t.Fatal(err)
	}
	if pv.Period != "2026-08" {
		t.Fatalf("período = %q, want 2026-08 (año inferido de los pedidos de agosto 2026)", pv.Period)
	}
	if pv.CodeCounts["sin_periodo"] != 0 || pv.CodeCounts["periodo_sin_anio"] != 1 {
		t.Fatalf("debe avisar y no bloquear: %v", pv.CodeCounts)
	}
	if !pv.CanCommit {
		t.Fatalf("debe poder importarse: %v", pv.BlockedBy)
	}
}

func TestParseMonthOnly(t *testing.T) {
	for in, want := range map[string]int{"OCTUBRE": 10, " Setiembre ": 9, "agosto.": 8} {
		if got, ok := parseMonthOnly(in); !ok || got != want {
			t.Errorf("parseMonthOnly(%q) = %d,%v want %d", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "AGOSTO 2026", "Ingresar stock al inicio del mes", "Mes de Control:"} {
		if _, ok := parseMonthOnly(in); ok {
			t.Errorf("parseMonthOnly(%q) no debería reconocerse como solo-mes", in)
		}
	}
}

// «H-Q300 2etiq50*30»: dos equipos sin separador (cantidad pegada al nombre) en el detalle de un retorno.
func TestImportPreview_retornoConEquiposSinSeparadorYReingresoSinRecibir(t *testing.T) {
	p := miniWorkbook(t)
	// Retorno 1 en camino con dos equipos pegados; el Stock Maestro ya cuenta el reingreso a mano.
	ret := p.Sheets[sheetReturns][2]
	ret[4] = "print-a    2roll"
	ret[8] = "En camino"
	pv, err := New(setupEquiposDB(t)).ImportPreview(p)
	if err != nil {
		t.Fatal(err)
	}
	if pv.CodeCounts["retorno_producto"] != 0 {
		t.Fatalf("debe reconocer ambos equipos: %v", pv.CodeCounts)
	}
	if pv.CodeCounts["reingreso_sin_recibir"] == 0 {
		t.Fatalf("debe explicar que el Excel cuenta reingresos de un retorno no recibido: %v", pv.CodeCounts)
	}
	if pv.ReconOK {
		t.Fatal("el Stock Maestro cuenta un reingreso que el sistema no puede respaldar: no debe conciliar")
	}
}

// Regresión: sin la fila de subencabezado (Independiente / Promo TK) tras el encabezado del Stock Maestro, el
// primer producto del libro se perdía con su stock inicial y la conciliación no lo notaba.
func TestImportPreview_stockMaestroSinSubencabezadoNoPierdeElPrimerProducto(t *testing.T) {
	p := miniWorkbook(t)
	var kept [][]any
	for _, r := range p.Sheets[sheetStock] {
		if len(r) > 3 && r[3] == "Independiente" && r[0] == nil { // la fila de subencabezado
			continue
		}
		kept = append(kept, r)
	}
	if len(kept) != len(p.Sheets[sheetStock])-1 {
		t.Fatal("el libro de prueba debía tener una fila de subencabezado")
	}
	p.Sheets[sheetStock] = kept

	pv, err := New(setupEquiposDB(t)).ImportPreview(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(pv.Reconciliation) != 2 {
		t.Fatalf("deben conciliarse los 2 productos del Stock Maestro, hubo %d: %+v", len(pv.Reconciliation), pv.Reconciliation)
	}
	if pv.Reconciliation[0].Code != "PRINT-A" || pv.Reconciliation[0].Excel.Opening != 20 {
		t.Fatalf("el primer producto debe leerse con su stock inicial: %+v", pv.Reconciliation[0])
	}
	if !pv.ReconOK {
		t.Fatalf("debe conciliar: %+v", pv.Reconciliation)
	}
}
