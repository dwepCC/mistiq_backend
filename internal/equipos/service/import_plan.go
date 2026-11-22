package service

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"

	"tukifac/pkg/database"
)

// Claves de las hojas dentro del payload (las asigna el frontend según el nombre de la hoja).
const (
	sheetCatalog = "catalogo"
	sheetCombos  = "combos"
	sheetDetail  = "detalle"
	sheetOrders  = "envios"
	sheetReturns = "retornos"
	sheetStock   = "stock"
)

// ImportPayload libro mensual leído en el navegador: las filas de cada hoja.
type ImportPayload struct {
	FileName string             `json:"file_name"`
	Sheets   map[string][][]any `json:"sheets"`
}

// Severidades de un hallazgo.
const (
	SevError   = "error"   // bloquea la importación
	SevWarning = "warning" // se importa, pero conviene revisar
	SevInfo    = "info"    // normalización aplicada
)

// ImportIssue hallazgo de la lectura del libro.
type ImportIssue struct {
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Sheet    string `json:"sheet"`
	Row      int    `json:"row,omitempty"`   // fila del Excel (1 = primera)
	Order    int    `json:"order,omitempty"` // N° de orden afectado
	Message  string `json:"message"`
}

// ── Estructuras del plan (lo que se va a guardar) ──────────────────────────

type planProduct struct {
	Code, Name, Kind, Notes string
	Yellow, Green           int
	FromStockOnly           bool
}

type planComboItem struct {
	ProductCode string
	Quantity    int
}

type planCombo struct {
	Code  string
	Items []planComboItem
}

type planItem struct {
	Line        int
	Type        string // producto | combo | plan | otro
	Code        string // código del producto/combo
	Description string
	PlanMonths  int
	Quantity    float64
	UnitPrice   float64
	Subtotal    float64
	IsCourtesy  bool
	Notes       string
}

type planPayment struct {
	Amount    float64
	PaidAt    time.Time
	Moment    string // anticipado | al_recoger
	Method    string
	DocType   string
	Series    string
	Number    string
	Notes     string
	Approx    bool
	Reference string
}

type planShipment struct {
	Guide                          string
	Agency                         string
	Department, Province, District string
	Mode                           string
	ScheduledDispatch              *time.Time
	DispatchedAt                   *time.Time
	ArrivedAt                      *time.Time
	Status                         string
}

type planOrder struct {
	Number       int
	Row          int
	SaleType     string
	OrderDate    time.Time
	CustomerName string
	Doc          docInfo
	Phone        string
	PhoneKind    string
	BillingDoc   string
	Total        float64
	IsGift       bool
	Items        []planItem
	Payments     []planPayment
	Shipment     planShipment
	ExcelPayment string
	Notes        string
}

type planReturn struct {
	Number        int
	Row           int
	CustomerName  string
	Guide         string
	RequestedAt   *time.Time
	ReceivedAt    *time.Time
	ItemsText     string
	Items         []planItem // producto + cantidad
	Unpaid, Cost  float64
	Status        string
	Condition     string
	Notes         string
	LinkedOrderNo int
}

type planMovement struct {
	ProductCode string
	At          time.Time
	Type        string
	Quantity    int
	OrderNumber int
	ReturnNo    int
	ComboCode   string
	SaleType    string
	Note        string
}

type expectedStock struct {
	Code                             string
	Name                             string
	Opening                          int
	DirectI, DirectP, ComboI, ComboP int
	TotalOut, Reentries, Current     int
	Yellow, Green                    int
	Row                              int
}

// ImportReconRow compara, por producto, el Stock Maestro del Excel contra lo que calcula el sistema.
type ImportReconRow struct {
	Code   string          `json:"code"`
	Excel  ImportStockCols `json:"excel"`
	System ImportStockCols `json:"system"`
	Match  bool            `json:"match"`
}

type ImportStockCols struct {
	Opening   int `json:"opening"`
	DirectI   int `json:"direct_independiente"`
	DirectP   int `json:"direct_promo_tk"`
	ComboI    int `json:"combo_independiente"`
	ComboP    int `json:"combo_promo_tk"`
	TotalOut  int `json:"total_out"`
	Reentries int `json:"reingresos"`
	Current   int `json:"current"`
}

type importPlan struct {
	// periodMonth: mes (1-12) cuando el libro dice solo «OCTUBRE» sin año; el año se infiere de las fechas
	// del libro en analyze.
	periodMonth  int
	Period       string
	PeriodStart  time.Time
	StockManager string
	FileName     string
	Products     []planProduct
	Combos       []planCombo
	Orders       []planOrder
	Returns      []planReturn
	Movements    []planMovement
	Expected     []expectedStock
	Issues       []ImportIssue
}

func (p *importPlan) add(sev, code, sheet string, row, order int, format string, args ...any) {
	p.Issues = append(p.Issues, ImportIssue{Severity: sev, Code: code, Sheet: sheet, Row: row, Order: order, Message: fmt.Sprintf(format, args...)})
}

func (p *importPlan) errors() int {
	n := 0
	for _, i := range p.Issues {
		if i.Severity == SevError {
			n++
		}
	}
	return n
}

// ── Lectura de las hojas ───────────────────────────────────────────────────

var markerRe = regexp.MustCompile(`^[➕📌💡⚠️✅]`)

func isMarker(s string) bool { return markerRe.MatchString(strings.TrimSpace(s)) }

func (p *importPlan) parseCatalog(rows [][]any) {
	seen := map[string]int{}
	inCombos := false
	for i, r := range rows {
		c0, c1 := cellStr(r, 0), cellStr(r, 1)
		if foldText(c1) == "combo" { // encabezado de la sección de combos
			inCombos = true
			continue
		}
		if inCombos || c1 == "" || isMarker(c0) || !onlyDigits(c0) {
			continue
		}
		code := collapse(c1)
		if prev, dup := seen[key(code)]; dup {
			p.add(SevError, "producto_duplicado", sheetCatalog, i+1, 0, "El producto %q está repetido (también en la fila %d)", code, prev)
			continue
		}
		seen[key(code)] = i + 1
		p.Products = append(p.Products, planProduct{Code: code, Name: code, Kind: inferKind(code, code), Notes: cellStr(r, 3)})
	}
	if len(p.Products) == 0 {
		p.add(SevError, "sin_catalogo", sheetCatalog, 0, 0, "No se encontraron productos en la hoja Catálogo")
	}
}

func (p *importPlan) productByKey(k string) *planProduct {
	for i := range p.Products {
		if key(p.Products[i].Code) == k {
			return &p.Products[i]
		}
	}
	return nil
}

// matchProduct resuelve un nombre del libro a un producto del catálogo: exacto, o único por contenido
// («E803» → «POS-E803»). El segundo valor indica que fue una suposición.
func (p *importPlan) matchProduct(name string) (*planProduct, bool) {
	k := key(name)
	if k == "" {
		return nil, false
	}
	if pr := p.productByKey(k); pr != nil {
		return pr, false
	}
	var found *planProduct
	for i := range p.Products {
		pk := key(p.Products[i].Code)
		if strings.HasSuffix(pk, k) || strings.Contains(pk, k) {
			if found != nil {
				return nil, false // ambiguo
			}
			found = &p.Products[i]
		}
	}
	if found != nil && len(k) >= 3 {
		return found, true
	}
	return nil, false
}

func (p *importPlan) parseCombos(rows [][]any) {
	header := -1
	for i, r := range rows {
		if foldText(cellStr(r, 0)) == "combo" {
			header = i
			break
		}
	}
	if header < 0 {
		return
	}
	for i := header + 1; i < len(rows); i++ {
		r := rows[i]
		code := collapse(cellStr(r, 0))
		if code == "" || isMarker(code) {
			if code == "" {
				break
			}
			continue
		}
		combo := planCombo{Code: code}
		for c := 1; c+1 < len(r)+1 && c <= 7; c += 2 {
			comp := collapse(cellStr(r, c))
			if comp == "" {
				continue
			}
			qty, ok := cellInt(r, c+1)
			if !ok || qty < 1 {
				p.add(SevError, "combo_cantidad", sheetCombos, i+1, 0, "Combo %q: cantidad inválida para el componente %q", code, comp)
				continue
			}
			pr, assumed := p.matchProduct(comp)
			if pr == nil {
				p.add(SevError, "combo_componente", sheetCombos, i+1, 0, "Combo %q: el componente %q no existe en el catálogo", code, comp)
				continue
			}
			if assumed {
				p.add(SevInfo, "producto_asumido", sheetCombos, i+1, 0, "Combo %q: «%s» se tomó como %q", code, comp, pr.Code)
			}
			combo.Items = append(combo.Items, planComboItem{ProductCode: pr.Code, Quantity: qty})
		}
		if len(combo.Items) == 0 {
			p.add(SevError, "combo_vacio", sheetCombos, i+1, 0, "El combo %q no tiene componentes válidos", code)
			continue
		}
		p.Combos = append(p.Combos, combo)
	}
}

func (p *importPlan) comboByKey(k string) *planCombo {
	for i := range p.Combos {
		if key(p.Combos[i].Code) == k {
			return &p.Combos[i]
		}
	}
	return nil
}

func (p *importPlan) parseStock(rows [][]any) {
	header := -1
	for i, r := range rows {
		if foldText(cellStr(r, 1)) == "producto" && strings.Contains(foldText(cellStr(r, 2)), "stock inicial") {
			header = i
			break
		}
	}
	for i := 0; i < len(rows) && i < 8; i++ {
		for c := range rows[i] {
			if per, ok := parsePeriod(cellStr(rows[i], c)); ok && p.Period == "" {
				p.Period = per
			}
			if p.periodMonth == 0 {
				if mo, ok := parseMonthOnly(cellStr(rows[i], c)); ok {
					p.periodMonth = mo
				}
			}
			if foldText(cellStr(rows[i], c)) == "responsable:" {
				for n := c + 1; n < len(rows[i]); n++ {
					if v := cellStr(rows[i], n); v != "" {
						p.StockManager = v
						break
					}
				}
			}
		}
	}
	if header < 0 {
		p.add(SevError, "sin_stock", sheetStock, 0, 0, "No se encontró la tabla del Stock Maestro: no se puede conciliar el stock")
		return
	}
	// Algunos libros traen una fila de subencabezado (Independiente / Promo TK) entre el encabezado y los
	// productos; otros no. Se salta solo si existe: antes se saltaba siempre y el primer producto del libro
	// (con su stock inicial) quedaba fuera de la importación sin ningún aviso.
	start := header + 1
	for start < len(rows) && start <= header+2 && !onlyDigits(cellStr(rows[start], 0)) && collapse(cellStr(rows[start], 1)) == "" {
		start++
	}
	for i := start; i < len(rows); i++ {
		r := rows[i]
		name := collapse(cellStr(r, 1))
		if name == "" || !onlyDigits(cellStr(r, 0)) {
			break
		}
		get := func(c int) int { n, _ := cellInt(r, c); return n }
		p.Expected = append(p.Expected, expectedStock{
			Code: name, Name: name, Opening: get(2), DirectI: get(3), DirectP: get(4), ComboI: get(5), ComboP: get(6),
			TotalOut: get(7), Reentries: get(8), Current: get(9), Yellow: get(11), Green: get(12), Row: i + 1,
		})
	}
	if len(p.Expected) == 0 {
		p.add(SevError, "sin_stock", sheetStock, 0, 0, "El Stock Maestro no tiene filas de productos")
	}
}

type rawDetailLine struct {
	Row   int
	Order int
	Name  string
	Qty   float64
	Price float64
	Sub   float64
	Notes string
}

func (p *importPlan) parseDetail(rows [][]any) map[int][]rawDetailLine {
	out := map[int][]rawDetailLine{}
	header := -1
	for i, r := range rows {
		if strings.Contains(foldText(cellStr(r, 0)), "n° orden") || strings.Contains(foldText(cellStr(r, 0)), "n orden") {
			header = i
			break
		}
	}
	if header < 0 {
		p.add(SevError, "sin_detalle", sheetDetail, 0, 0, "No se encontró el encabezado de la hoja Detalle de Pedidos")
		return out
	}
	for i := header + 1; i < len(rows); i++ {
		r := rows[i]
		ord, ok := cellInt(r, 0)
		name := collapse(cellStr(r, 1))
		if !ok || name == "" {
			continue
		}
		qty, _ := cellNum(r, 2)
		price, _ := cellNum(r, 3)
		sub, subOK := cellNum(r, 4)
		if !subOK {
			sub = qty * price
		}
		if qty <= 0 {
			p.add(SevError, "cantidad_invalida", sheetDetail, i+1, ord, "Pedido %d: cantidad inválida para %q", ord, name)
			continue
		}
		out[ord] = append(out[ord], rawDetailLine{Row: i + 1, Order: ord, Name: name, Qty: qty, Price: price, Sub: sub, Notes: cellStr(r, 5)})
	}
	return out
}

// ── Pedidos ────────────────────────────────────────────────────────────────

func atTime(date *time.Time, hh, mm int) time.Time {
	y, m, d := date.Date()
	return time.Date(y, m, d, hh, mm, 0, 0, limaLoc())
}

func (p *importPlan) parseOrders(rows [][]any, detail map[int][]rawDetailLine) {
	header := -1
	for i, r := range rows {
		if strings.Contains(foldText(cellStr(r, 0)), "n° orden") || strings.Contains(foldText(cellStr(r, 0)), "n orden") {
			header = i
			break
		}
	}
	if header < 0 {
		p.add(SevError, "sin_envios", sheetOrders, 0, 0, "No se encontró el encabezado de la hoja Control de Envíos")
		return
	}
	seen := map[int]int{}
	used := map[int]bool{}
	noGuide := 0
	for i := header + 1; i < len(rows); i++ {
		r := rows[i]
		num, ok := cellInt(r, 0)
		if !ok || num <= 0 {
			continue
		}
		lines := detail[num]
		customer := collapse(cellStr(r, 3))
		if customer == "0" {
			customer = ""
		}
		total, _ := cellNum(r, 12)
		if len(lines) == 0 && customer == "" && total == 0 && cellStr(r, 1) == "" {
			p.add(SevInfo, "pedido_vacio", sheetOrders, i+1, num, "El pedido %d no tiene datos: se omite", num)
			continue
		}
		if prev, dup := seen[num]; dup {
			p.add(SevError, "pedido_duplicado", sheetOrders, i+1, num, "El pedido %d está repetido (también en la fila %d)", num, prev)
			continue
		}
		seen[num] = i + 1
		used[num] = true
		o := planOrder{Number: num, Row: i + 1, CustomerName: customer, Total: round2(total)}

		switch foldText(cellStr(r, 1)) {
		case "promo tk":
			o.SaleType = database.EquipSalePromoTK
		case "independiente":
			o.SaleType = database.EquipSaleIndependiente
		default:
			o.SaleType = database.EquipSaleIndependiente
			p.add(SevWarning, "sin_tipo_salida", sheetOrders, i+1, num, "El pedido %d no tiene tipo de salida: se tomó como Independiente", num)
		}

		disp := cellTime(r, 2)
		if disp.Date != nil {
			o.OrderDate = *disp.Date
		} else if !p.PeriodStart.IsZero() {
			o.OrderDate = noonLima(p.PeriodStart)
			p.add(SevWarning, "sin_fecha", sheetOrders, i+1, num, "El pedido %d no tiene fecha de despacho: se usó el inicio del período", num)
		}

		o.Doc = classifyDoc(cellStr(r, 4), cellStr(r, 5))
		if o.Doc.Weird != "" && o.Doc.DocType == "CE" {
			p.add(SevWarning, "documento_raro", sheetOrders, i+1, num, "Pedido %d: el documento %q no es DNI ni RUC (se guardó como CE)", num, o.Doc.Weird)
		}
		o.Phone, o.PhoneKind, _ = classifyPhone(cellStr(r, 6))
		if _, _, w := classifyPhone(cellStr(r, 6)); w != "" {
			p.add(SevWarning, "telefono", sheetOrders, i+1, num, "Pedido %d: %s (%q)", num, w, cellStr(r, 6))
		}
		if o.PhoneKind == "usuario" {
			p.add(SevInfo, "telefono_usuario", sheetOrders, i+1, num, "Pedido %d: el contacto %q es un usuario, no un teléfono", num, o.Phone)
		}
		o.ExcelPayment = foldText(cellStr(r, 21))

		// Ítems.
		sum := 0.0
		for n, l := range lines {
			it := p.resolveItem(l, n+1)
			sum += it.Subtotal
			o.Items = append(o.Items, it)
		}
		if len(o.Items) > 0 && math.Abs(sum-o.Total) > 0.015 {
			p.add(SevWarning, "total_distinto", sheetOrders, i+1, num, "Pedido %d: el total de Control de Envíos (%.2f) no coincide con la suma del detalle (%.2f)", num, o.Total, sum)
		}
		if len(o.Items) == 0 && o.Total > 0 {
			p.add(SevWarning, "sin_items", sheetOrders, i+1, num, "Pedido %d: tiene total %.2f pero ningún ítem en el detalle", num, o.Total)
		}
		o.IsGift = o.Total == 0 && len(o.Items) > 0

		// Envío.
		dest := parseDestination(cellStr(r, 11))
		o.Shipment = planShipment{
			Guide: cellStr(r, 9), Agency: collapse(cellStr(r, 10)), Mode: dest.Mode,
			Department: dest.Department, Province: dest.Province, District: dest.District,
		}
		if dest.Mode == "agencia" && dest.Department != "" && !dest.Known {
			p.add(SevWarning, "destino", sheetOrders, i+1, num, "Pedido %d: el destino %q no es un departamento reconocido", num, cellStr(r, 11))
		}
		o.Shipment.ScheduledDispatch = disp.Date
		if arr := cellTime(r, 19); arr.Date != nil {
			o.Shipment.ArrivedAt = arr.Date
		}
		switch foldText(cellStr(r, 22)) {
		case "entregado":
			o.Shipment.Status = "entregado"
		case "en transito":
			o.Shipment.Status = "en_transito"
		case "en agencia":
			o.Shipment.Status = "en_agencia"
		case "retorno":
			o.Shipment.Status = "retorno"
		default:
			switch {
			case o.Shipment.ArrivedAt != nil:
				o.Shipment.Status = "en_agencia"
			case disp.Date != nil && (o.Shipment.Guide != "" || o.Shipment.Mode == "oficina"):
				o.Shipment.Status = "en_transito"
			case disp.Date != nil:
				o.Shipment.Status = "en_transito"
			default:
				o.Shipment.Status = "pendiente_envio"
			}
		}
		if o.Shipment.Status != "pendiente_envio" && disp.Date != nil {
			o.Shipment.DispatchedAt = disp.Date
		}
		if o.Shipment.Guide == "" && o.Shipment.Mode == "agencia" && o.Total > 0 {
			noGuide++
		}

		p.parsePayments(&o, r)
		// Comprobante del pedido si el libro no lo dice.
		o.BillingDoc = o.Doc.BillingDoc
		if o.BillingDoc == "" {
			for _, pay := range o.Payments {
				if pay.DocType != "" {
					o.BillingDoc = pay.DocType
					break
				}
			}
		}
		if o.BillingDoc == "" {
			o.BillingDoc = "ninguno"
		}
		p.Orders = append(p.Orders, o)
	}
	for num, lines := range detail {
		if !used[num] {
			p.add(SevError, "detalle_sin_pedido", sheetDetail, lines[0].Row, num, "El detalle tiene %d línea(s) del pedido %d, que no está en Control de Envíos", len(lines), num)
		}
	}
	if noGuide > 0 {
		p.add(SevInfo, "sin_guia", sheetOrders, 0, 0, "%d pedido(s) enviados por agencia no tienen N° de guía en el libro", noGuide)
	}
	sort.Slice(p.Orders, func(i, j int) bool { return p.Orders[i].Number < p.Orders[j].Number })
}

func round2(f float64) float64 { return math.Round(f*100) / 100 }

func (p *importPlan) resolveItem(l rawDetailLine, line int) planItem {
	it := planItem{Line: line, Quantity: l.Qty, UnitPrice: round2(l.Price), Subtotal: round2(l.Sub), Notes: l.Notes}
	it.IsCourtesy = l.Price == 0
	k := key(l.Name)
	if pr := p.productByKey(k); pr != nil {
		it.Type, it.Code, it.Description = "producto", pr.Code, pr.Code
		return it
	}
	if c := p.comboByKey(k); c != nil {
		it.Type, it.Code, it.Description = "combo", c.Code, c.Code
		return it
	}
	if isPlan, months := planMonths(l.Name); isPlan {
		it.Type, it.PlanMonths, it.Description = "plan", months, canonicalPlanName(l.Name)
		if months == 0 {
			p.add(SevInfo, "plan_sin_meses", sheetDetail, l.Row, l.Order, "Pedido %d: no se pudo deducir los meses del plan %q", l.Order, l.Name)
		}
		return it
	}
	if pr, assumed := p.matchProduct(l.Name); pr != nil && assumed {
		it.Type, it.Code, it.Description = "producto", pr.Code, pr.Code
		p.add(SevInfo, "producto_asumido", sheetDetail, l.Row, l.Order, "Pedido %d: «%s» se tomó como %q", l.Order, l.Name, pr.Code)
		return it
	}
	it.Type, it.Description = "otro", l.Name
	p.add(SevWarning, "item_desconocido", sheetDetail, l.Row, l.Order, "Pedido %d: «%s» no está en el catálogo ni es un plan: se guarda como «otro» y no mueve stock", l.Order, l.Name)
	return it
}

// parsePayments traduce abono y saldo del libro en cobros del cliente.
func (p *importPlan) parsePayments(o *planOrder, r []any) {
	date := o.OrderDate
	base := &date
	mk := func(amount float64, hhmm parsedTime, invoice, moment string, when *time.Time) {
		var paid time.Time
		approx := false
		d := base
		if when != nil {
			d = when
		}
		if hhmm.HasTime {
			paid = atTime(d, hhmm.Hour, hhmm.Minute)
		} else {
			paid = atTime(d, 12, 0)
		}
		approx = true // el libro no guarda la fecha del depósito, solo la hora
		docType, series, number, okInv := parseInvoice(invoice)
		pay := planPayment{Amount: round2(amount), PaidAt: paid, Moment: moment, Method: "otro", DocType: docType, Series: series, Number: number, Approx: approx}
		if !okInv {
			pay.Notes = "Comprobante con formato no reconocido: " + invoice
			p.add(SevWarning, "comprobante", sheetOrders, o.Row, o.Number, "Pedido %d: el comprobante %q no tiene formato SERIE-NÚMERO", o.Number, invoice)
		}
		if approx {
			if pay.Notes != "" {
				pay.Notes += " · "
			}
			pay.Notes += "Importado: fecha aproximada (el libro solo guarda la hora)"
		}
		o.Payments = append(o.Payments, pay)
	}
	if o.Total <= 0 {
		return
	}
	abono, hasAbono := cellNum(r, 13)
	saldo, hasSaldo := cellNum(r, 16)
	if hasAbono && abono > 0.004 {
		mk(abono, cellTime(r, 14), cellStr(r, 15), "anticipado", nil)
	}
	paidFull := strings.Contains(o.ExcelPayment, "pagado")
	remaining := o.Total - abono
	if !hasAbono {
		remaining = o.Total
	}
	if hasSaldo && saldo > 0.004 {
		remaining = saldo
	}
	if remaining > 0.004 && paidFull {
		mk(remaining, cellTime(r, 17), cellStr(r, 18), "al_recoger", o.Shipment.ArrivedAt)
	}
	// Control: lo cobrado debe cuadrar con el total cuando el libro dice «Pagado».
	paid := 0.0
	for _, pay := range o.Payments {
		paid += pay.Amount
	}
	if paidFull && math.Abs(paid-o.Total) > 0.015 {
		p.add(SevWarning, "cobro_no_cuadra", sheetOrders, o.Row, o.Number, "Pedido %d: figura «Pagado» pero lo cobrado (%.2f) no cuadra con el total (%.2f)", o.Number, paid, o.Total)
	}
	if !paidFull && paid < o.Total-0.015 && o.ExcelPayment != "" && !strings.Contains(o.ExcelPayment, "pendiente") {
		p.add(SevInfo, "estado_pago", sheetOrders, o.Row, o.Number, "Pedido %d: el estado de pago del libro es %q; quedan %.2f por cobrar", o.Number, o.ExcelPayment, o.Total-paid)
	}
}

// ── Retornos ───────────────────────────────────────────────────────────────

var returnSplitRe = regexp.MustCompile(`\s*(?:,|\+|;| y |/)\s*`)
var returnQtyRe = regexp.MustCompile(`^(\d+)\s*[xX]\s*(.+)$|^(.+?)\s*[xX]\s*(\d+)$`)

func (p *importPlan) parseReturns(rows [][]any) {
	header := -1
	for i, r := range rows {
		if strings.Contains(foldText(cellStr(r, 1)), "fecha solicitud") {
			header = i
			break
		}
	}
	if header < 0 {
		return
	}
	for i := header + 1; i < len(rows); i++ {
		r := rows[i]
		c0 := cellStr(r, 0)
		if foldText(c0) == "totales" || c0 == "" {
			break
		}
		num, ok := cellInt(r, 0)
		if !ok {
			continue
		}
		ret := planReturn{Number: num, Row: i + 1, CustomerName: collapse(cellStr(r, 2)), Guide: cellStr(r, 3), ItemsText: collapse(cellStr(r, 4))}
		if t := cellTime(r, 1); t.Date != nil {
			ret.RequestedAt = t.Date
		}
		ret.Unpaid, _ = cellNum(r, 5)
		ret.Cost, _ = cellNum(r, 6)
		if t := cellTime(r, 7); t.Date != nil {
			ret.ReceivedAt = t.Date
		}
		switch foldText(cellStr(r, 8)) {
		case "recibido":
			ret.Status = "recibido"
		case "en camino":
			ret.Status = "en_camino"
		case "desechado por shalom", "desechado por agencia":
			ret.Status = "desechado_por_agencia"
		default:
			ret.Status = "solicitado"
		}
		notes := cellStr(r, 10)
		f := foldText(notes)
		ret.Condition = "buen_estado"
		if strings.Contains(f, "no funciona") || strings.Contains(f, "danado") || strings.Contains(f, "malogr") || strings.Contains(f, "defectuos") {
			ret.Condition = "danado"
		}
		ret.Notes = notes
		if act := cellStr(r, 9); act != "" {
			if ret.Notes != "" {
				ret.Notes += " · "
			}
			ret.Notes += "Acción: " + act
		}
		for _, tok := range returnSplitRe.Split(ret.ItemsText, -1) {
			tok = collapse(tok)
			if tok == "" {
				continue
			}
			for _, ri := range p.resolveReturnToken(tok) {
				if ri.pr == nil {
					p.add(SevWarning, "retorno_producto", sheetReturns, i+1, 0, "Retorno %d: no se reconoció el equipo %q", num, ri.name)
					continue
				}
				if ri.assumed {
					p.add(SevInfo, "producto_asumido", sheetReturns, i+1, 0, "Retorno %d: «%s» se tomó como %q", num, ri.name, ri.pr.Code)
				}
				ret.Items = append(ret.Items, planItem{Type: "producto", Code: ri.pr.Code, Quantity: float64(ri.qty)})
			}
		}
		p.Returns = append(p.Returns, ret)
	}
}

type returnItemGuess struct {
	name    string
	qty     int
	pr      *planProduct
	assumed bool
}

var returnLeadQtyRe = regexp.MustCompile(`^(\d+)\s*([A-Za-z].*)$`)
var returnGluedQtyWordRe = regexp.MustCompile(`^\d+[A-Za-z]`)

// guessReturnItem interpreta un fragmento como «cantidad + equipo»: «2x PRINT», «PRINT x2», o «2etiq50*30» (cantidad pegada).
func (p *importPlan) guessReturnItem(tok string) returnItemGuess {
	qty, name := 1, tok
	if m := returnQtyRe.FindStringSubmatch(tok); m != nil {
		if m[1] != "" {
			fmt.Sscanf(m[1], "%d", &qty)
			name = m[2]
		} else {
			fmt.Sscanf(m[4], "%d", &qty)
			name = m[3]
		}
	}
	pr, assumed := p.matchProduct(name)
	if pr == nil {
		// «2etiq50*30»: la cantidad va pegada al nombre.
		if m := returnLeadQtyRe.FindStringSubmatch(tok); m != nil {
			q := 1
			fmt.Sscanf(m[1], "%d", &q)
			if pr2, as2 := p.matchProduct(m[2]); pr2 != nil {
				return returnItemGuess{name: m[2], qty: q, pr: pr2, assumed: as2}
			}
		}
	}
	return returnItemGuess{name: name, qty: qty, pr: pr, assumed: assumed}
}

// resolveReturnToken reconoce uno o varios equipos en un fragmento del detalle de un retorno. Si el fragmento
// entero no se reconoce y trae varias palabras («H-Q300 2etiq50*30», sin coma entre los equipos), lo parte donde
// empieza una cantidad pegada a un nombre y reconoce cada parte.
func (p *importPlan) resolveReturnToken(tok string) []returnItemGuess {
	whole := p.guessReturnItem(tok)
	if whole.pr != nil {
		return []returnItemGuess{whole}
	}
	fields := strings.Fields(tok)
	if len(fields) < 2 {
		return []returnItemGuess{whole}
	}
	var pieces []string
	cur := []string{}
	for _, f := range fields {
		if len(cur) > 0 && returnGluedQtyWordRe.MatchString(f) {
			pieces = append(pieces, strings.Join(cur, " "))
			cur = nil
		}
		cur = append(cur, f)
	}
	pieces = append(pieces, strings.Join(cur, " "))
	if len(pieces) < 2 {
		return []returnItemGuess{whole}
	}
	out := make([]returnItemGuess, 0, len(pieces))
	for _, piece := range pieces {
		out = append(out, p.guessReturnItem(piece))
	}
	return out
}

// ── Movimientos y conciliación ─────────────────────────────────────────────

// clampToPeriod ubica un movimiento dentro del período del libro: un pedido despachado a inicios del mes
// siguiente igual se descontó en el libro de este mes, y el Stock Maestro lo cuenta aquí.
func (p *importPlan) clampToPeriod(t time.Time) time.Time {
	if p.PeriodStart.IsZero() {
		return t
	}
	end := p.PeriodStart.AddDate(0, 1, 0).Add(-time.Hour)
	if t.Before(p.PeriodStart) {
		return p.PeriodStart.Add(time.Hour)
	}
	if t.After(end) {
		return end
	}
	return t
}

// buildMovements genera el kardex del período a partir de los pedidos y retornos leídos.
func (p *importPlan) buildMovements() {
	for _, e := range p.Expected {
		if e.Opening != 0 {
			p.Movements = append(p.Movements, planMovement{
				ProductCode: e.Code, At: p.PeriodStart, Type: database.EquipMoveApertura, Quantity: e.Opening,
				Note: "Stock inicial " + p.Period + " (importado del Excel)",
			})
		}
	}
	for _, o := range p.Orders {
		at := p.clampToPeriod(noonLima(o.OrderDate))
		for _, it := range o.Items {
			switch it.Type {
			case "producto":
				p.Movements = append(p.Movements, planMovement{
					ProductCode: it.Code, At: at, Type: database.EquipMoveSalida, Quantity: -int(math.Round(it.Quantity)),
					OrderNumber: o.Number, SaleType: o.SaleType,
				})
			case "combo":
				if c := p.comboByKey(key(it.Code)); c != nil {
					for _, ci := range c.Items {
						p.Movements = append(p.Movements, planMovement{
							ProductCode: ci.ProductCode, At: at, Type: database.EquipMoveSalida,
							Quantity: -ci.Quantity * int(math.Round(it.Quantity)), OrderNumber: o.Number, ComboCode: c.Code, SaleType: o.SaleType,
						})
					}
				}
			}
		}
	}
	for _, ret := range p.Returns {
		if ret.Status != "recibido" || ret.Condition != "buen_estado" {
			continue
		}
		when := ret.ReceivedAt
		if when == nil {
			when = ret.RequestedAt
		}
		if when == nil {
			d := noonLima(p.PeriodStart)
			when = &d
		}
		for _, it := range ret.Items {
			p.Movements = append(p.Movements, planMovement{
				ProductCode: it.Code, At: p.clampToPeriod(noonLima(*when)), Type: database.EquipMoveReingreso,
				Quantity: int(math.Round(it.Quantity)), ReturnNo: ret.Number,
				Note: "Retorno " + fmt.Sprint(ret.Number) + " en buen estado",
			})
		}
	}
}

// explainReingresos agrega un aviso por cada producto cuyo Stock Maestro cuenta más reingresos de los que el
// sistema puede respaldar: la columna «Reingresos» del libro es manual, y el sistema solo suma retornos en
// estado «Recibido» y en buen estado. Es la causa más común de un descuadre de 1-2 unidades.
func (p *importPlan) explainReingresos(rows []ImportReconRow) {
	for _, r := range rows {
		if r.Match || r.Excel.Reentries <= r.System.Reentries {
			continue
		}
		for _, ret := range p.Returns {
			if ret.Status == "recibido" && ret.Condition == "buen_estado" {
				continue
			}
			for _, it := range ret.Items {
				if key(it.Code) != key(r.Code) {
					continue
				}
				why := "está en estado «" + strings.ReplaceAll(ret.Status, "_", " ") + "», no «Recibido»"
				if ret.Status == "recibido" {
					why = "está marcado como no apto (equipo dañado)"
				}
				p.add(SevWarning, "reingreso_sin_recibir", sheetReturns, ret.Row, 0,
					"El Stock Maestro cuenta %d reingreso(s) de «%s», pero el retorno %d %s y el sistema solo suma retornos Recibidos en buen estado. Si el equipo ya llegó, cambia su estado a «Recibido» en la hoja Retornos; si no, pon 0 en Reingresos del Stock Maestro",
					r.Excel.Reentries, r.Code, ret.Number, why)
			}
		}
	}
}

// reconcile compara el stock calculado por el plan contra el Stock Maestro del libro.
func (p *importPlan) reconcile() ([]ImportReconRow, bool) {
	type agg struct{ opening, di, dp, ci, cp, re int }
	calc := map[string]*agg{}
	get := func(code string) *agg {
		k := key(code)
		if calc[k] == nil {
			calc[k] = &agg{}
		}
		return calc[k]
	}
	for _, m := range p.Movements {
		a := get(m.ProductCode)
		switch m.Type {
		case database.EquipMoveApertura:
			a.opening += m.Quantity
		case database.EquipMoveReingreso:
			a.re += m.Quantity
		case database.EquipMoveSalida:
			q := -m.Quantity
			promo := m.SaleType == database.EquipSalePromoTK
			switch {
			case m.ComboCode != "" && promo:
				a.cp += q
			case m.ComboCode != "":
				a.ci += q
			case promo:
				a.dp += q
			default:
				a.di += q
			}
		}
	}
	var rows []ImportReconRow
	allMatch := true
	seen := map[string]bool{}
	for _, e := range p.Expected {
		k := key(e.Code)
		seen[k] = true
		a := calc[k]
		if a == nil {
			a = &agg{}
		}
		sys := ImportStockCols{
			Opening: a.opening, DirectI: a.di, DirectP: a.dp, ComboI: a.ci, ComboP: a.cp,
			TotalOut: a.di + a.dp + a.ci + a.cp, Reentries: a.re,
		}
		sys.Current = sys.Opening - sys.TotalOut + sys.Reentries
		ex := ImportStockCols{Opening: e.Opening, DirectI: e.DirectI, DirectP: e.DirectP, ComboI: e.ComboI, ComboP: e.ComboP,
			TotalOut: e.TotalOut, Reentries: e.Reentries, Current: e.Current}
		match := ex == sys
		if !match {
			allMatch = false
		}
		rows = append(rows, ImportReconRow{Code: e.Code, Excel: ex, System: sys, Match: match})
	}
	// Productos con movimientos que el Stock Maestro no lista.
	var extra []string
	for k := range calc {
		if !seen[k] {
			extra = append(extra, k)
		}
	}
	sort.Strings(extra)
	for _, k := range extra {
		a := calc[k]
		tot := a.di + a.dp + a.ci + a.cp
		if tot == 0 && a.re == 0 && a.opening == 0 {
			continue
		}
		code := k
		for _, m := range p.Movements {
			if key(m.ProductCode) == k {
				code = m.ProductCode
				break
			}
		}
		sys := ImportStockCols{Opening: a.opening, DirectI: a.di, DirectP: a.dp, ComboI: a.ci, ComboP: a.cp, TotalOut: tot, Reentries: a.re, Current: a.opening - tot + a.re}
		rows = append(rows, ImportReconRow{Code: code, System: sys, Match: false})
		allMatch = false
	}
	return rows, allMatch
}

// analyze lee el libro completo y devuelve el plan (sin tocar la base de datos).
func analyze(payload ImportPayload) *importPlan {
	p := &importPlan{FileName: payload.FileName}
	sheets := payload.Sheets
	p.parseCatalog(sheets[sheetCatalog])
	p.parseCombos(sheets[sheetCombos])
	p.parseStock(sheets[sheetStock])

	// Productos que están en el Stock Maestro pero no en el catálogo.
	for _, e := range p.Expected {
		if p.productByKey(key(e.Code)) == nil {
			p.Products = append(p.Products, planProduct{Code: e.Code, Name: e.Code, Kind: inferKind(e.Code, e.Code), FromStockOnly: true})
			p.add(SevWarning, "producto_solo_stock", sheetStock, e.Row, 0, "El producto %q está en el Stock Maestro pero no en el Catálogo: se creará", e.Code)
		}
	}
	for i := range p.Products {
		for _, e := range p.Expected {
			if key(e.Code) == key(p.Products[i].Code) {
				p.Products[i].Yellow, p.Products[i].Green = e.Yellow, e.Green
				if e.Yellow > e.Green {
					p.add(SevWarning, "umbrales", sheetStock, e.Row, 0, "El producto %q tiene el umbral amarillo (%d) mayor que el verde (%d)", e.Code, e.Yellow, e.Green)
					p.Products[i].Yellow = e.Green
				}
			}
		}
	}

	// Período: del Stock Maestro; si no, del primer despacho.
	detail := p.parseDetail(sheets[sheetDetail])
	// El libro dice solo «OCTUBRE» (sin año): se toma el año de las fechas de los pedidos y retornos.
	if p.Period == "" && p.periodMonth > 0 {
		year := inferPeriodYear(p.periodMonth, sheets[sheetOrders], sheets[sheetReturns])
		p.Period = fmt.Sprintf("%04d-%02d", year, p.periodMonth)
		p.add(SevWarning, "periodo_sin_anio", sheetStock, 0, 0,
			"El mes de control dice solo el mes, sin año: se tomó %s según las fechas del libro. Escribe el año (ej. «%s %d») para evitar confusiones",
			p.Period, strings.ToUpper(monthLabel(p.periodMonth)), year)
	}
	if p.Period == "" {
		p.add(SevError, "sin_periodo", sheetStock, 0, 0, "No se pudo determinar el mes de control (ej. «AGOSTO 2026») en el Stock Maestro")
	} else if start, _, err := periodBounds(p.Period); err == nil {
		p.PeriodStart = start
	}
	p.parseOrders(sheets[sheetOrders], detail)
	p.parseReturns(sheets[sheetReturns])
	p.buildMovements()

	// Los pedidos deben caer dentro del período del libro (un despacho de la primera semana del mes siguiente es normal).
	if !p.PeriodStart.IsZero() {
		end := p.PeriodStart.AddDate(0, 1, 0)
		for _, o := range p.Orders {
			switch {
			case o.OrderDate.Before(p.PeriodStart) || !o.OrderDate.Before(end.AddDate(0, 0, 7)):
				p.add(SevWarning, "fecha_fuera_periodo", sheetOrders, o.Row, o.Number, "El pedido %d tiene fecha %s, lejos del período %s", o.Number, o.OrderDate.Format("02/01/2006"), p.Period)
			case !o.OrderDate.Before(end):
				p.add(SevInfo, "fecha_siguiente_mes", sheetOrders, o.Row, o.Number, "El pedido %d tiene fecha %s (mes siguiente): su stock se registra en %s, como en el libro", o.Number, o.OrderDate.Format("02/01/2006"), p.Period)
			}
		}
	}
	return p
}
