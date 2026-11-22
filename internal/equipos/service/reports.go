package service

import (
	"math"
	"sort"
	"strings"
	"time"

	"tukifac/pkg/database"
)

// ── Cierre mensual ─────────────────────────────────────────────────────────

// periodClosed devuelve el período (AAAA-MM) al que pertenece la fecha si ya fue cerrado; "" si sigue abierto.
func (s *Service) periodClosed(at time.Time) (string, error) {
	p := at.In(limaLoc()).Format("2006-01")
	var n int64
	if err := s.db.Model(&database.EquipStockPeriod{}).Where("period = ?", p).Count(&n).Error; err != nil {
		return "", err
	}
	if n > 0 {
		return p, nil
	}
	return "", nil
}

type ClosedPeriod struct {
	Period   string    `json:"period"`
	ClosedAt time.Time `json:"closed_at"`
	Products int       `json:"products"`
}

func (s *Service) ClosedPeriods() ([]ClosedPeriod, error) {
	out := []ClosedPeriod{}
	err := s.db.Model(&database.EquipStockPeriod{}).
		Select("period, MAX(closed_at) AS closed_at, COUNT(*) AS products").Group("period").Order("period DESC").Scan(&out).Error
	return out, err
}

// ClosePeriod congela el stock inicial y final de cada producto del mes. Solo se cierran meses terminados y, mientras
// esté cerrado, no se aceptan movimientos manuales con fecha dentro de ese mes.
func (s *Service) ClosePeriod(period string, userID uint) (*ClosedPeriod, error) {
	if _, _, err := periodBounds(period); err != nil {
		return nil, err
	}
	if period >= currentPeriod() {
		return nil, invalid("solo se cierran meses ya terminados")
	}
	var n int64
	if err := s.db.Model(&database.EquipStockPeriod{}).Where("period = ?", period).Count(&n).Error; err != nil {
		return nil, err
	}
	if n > 0 {
		return nil, invalid("el período %s ya está cerrado", period)
	}
	rows, err := s.StockReport(period)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	var uid *uint
	if userID > 0 {
		u := userID
		uid = &u
	}
	recs := make([]database.EquipStockPeriod, 0, len(rows))
	for _, r := range rows {
		recs = append(recs, database.EquipStockPeriod{Period: period, ProductID: r.ProductID, Opening: r.Opening, Closing: r.Current, ClosedAt: now, ClosedBy: uid})
	}
	if len(recs) == 0 {
		return nil, invalid("no hay productos para cerrar")
	}
	if err := s.db.Create(&recs).Error; err != nil {
		return nil, err
	}
	return &ClosedPeriod{Period: period, ClosedAt: now, Products: len(recs)}, nil
}

func (s *Service) ReopenPeriod(period string) error {
	res := s.db.Where("period = ?", period).Delete(&database.EquipStockPeriod{})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return invalid("el período %s no está cerrado", period)
	}
	return nil
}

// ── Resumen mensual ────────────────────────────────────────────────────────

type TypeSummary struct {
	SaleType string  `json:"sale_type"`
	Orders   int     `json:"orders"`
	Sales    float64 `json:"sales"`
	Paid     float64 `json:"paid"`
	Balance  float64 `json:"balance"`
}

type MethodSummary struct {
	Method string  `json:"method"`
	Count  int     `json:"count"`
	Total  float64 `json:"total"`
}

type Summary struct {
	Period           string          `json:"period"`
	ByType           []TypeSummary   `json:"by_type"`
	Total            TypeSummary     `json:"total"`
	GiftOrders       int             `json:"gift_orders"`
	Collected        float64         `json:"collected"` // cobros vigentes con fecha en el período
	PaymentsByMethod []MethodSummary `json:"payments_by_method"`
	Returns          int             `json:"returns"`
	ReturnCost       float64         `json:"return_cost"`
	ReturnUnpaid     float64         `json:"return_unpaid"`
}

func (s *Service) Summary(period string) (*Summary, error) {
	if period == "" {
		period = currentPeriod()
	}
	start, end, err := periodBounds(period)
	if err != nil {
		return nil, err
	}
	out := &Summary{Period: period, ByType: []TypeSummary{}, PaymentsByMethod: []MethodSummary{}}
	if err := s.db.Model(&database.EquipOrder{}).
		Select("sale_type, COUNT(*) AS orders, COALESCE(SUM(total_amount),0) AS sales, COALESCE(SUM(paid_amount),0) AS paid, COALESCE(SUM(balance_amount),0) AS balance").
		Where("status = ? AND order_date >= ? AND order_date < ?", "registrado", start, end).Group("sale_type").Order("sale_type").Scan(&out.ByType).Error; err != nil {
		return nil, err
	}
	for i := range out.ByType {
		t := &out.ByType[i]
		t.Sales, t.Paid, t.Balance = round2(t.Sales), round2(t.Paid), round2(t.Balance)
		out.Total.Orders += t.Orders
		out.Total.Sales += t.Sales
		out.Total.Paid += t.Paid
		out.Total.Balance += t.Balance
	}
	out.Total.SaleType = "total"
	out.Total.Sales, out.Total.Paid, out.Total.Balance = round2(out.Total.Sales), round2(out.Total.Paid), round2(out.Total.Balance)
	var gifts int64
	if err := s.db.Model(&database.EquipOrder{}).Where("status = ? AND is_gift = ? AND order_date >= ? AND order_date < ?", "registrado", true, start, end).Count(&gifts).Error; err != nil {
		return nil, err
	}
	out.GiftOrders = int(gifts)
	if err := s.db.Model(&database.EquipPayment{}).Select("method, COUNT(*) AS count, COALESCE(SUM(amount),0) AS total").
		Where("status = ? AND paid_at >= ? AND paid_at < ?", "vigente", start, end).Group("method").Order("total DESC").Scan(&out.PaymentsByMethod).Error; err != nil {
		return nil, err
	}
	for i := range out.PaymentsByMethod {
		out.PaymentsByMethod[i].Total = round2(out.PaymentsByMethod[i].Total)
		out.Collected += out.PaymentsByMethod[i].Total
	}
	out.Collected = round2(out.Collected)
	var rc struct {
		N            int
		Cost, Unpaid float64
	}
	if err := s.db.Model(&database.EquipReturn{}).Select("COUNT(*) AS n, COALESCE(SUM(return_cost),0) AS cost, COALESCE(SUM(unpaid_balance),0) AS unpaid").
		Where("requested_at >= ? AND requested_at < ?", start, end).Scan(&rc).Error; err != nil {
		return nil, err
	}
	out.Returns, out.ReturnCost, out.ReturnUnpaid = rc.N, round2(rc.Cost), round2(rc.Unpaid)
	return out, nil
}

// ── Ventas por producto / combo / plan ─────────────────────────────────────

type SalesLine struct {
	Kind     string  `json:"kind"` // producto | combo | plan | otro
	Code     string  `json:"code"`
	Name     string  `json:"name"`
	Quantity float64 `json:"quantity"`
	Amount   float64 `json:"amount"`
}

type ProductUnits struct {
	Code        string `json:"code"`
	Name        string `json:"name"`
	DirectIndep int    `json:"direct_independiente"`
	DirectPromo int    `json:"direct_promo_tk"`
	ComboIndep  int    `json:"combo_independiente"`
	ComboPromo  int    `json:"combo_promo_tk"`
	Total       int    `json:"total"`
}

type SalesReport struct {
	Period string         `json:"period"`
	Lines  []SalesLine    `json:"lines"`
	Units  []ProductUnits `json:"units"`
}

func (s *Service) SalesByProduct(period string) (*SalesReport, error) {
	if period == "" {
		period = currentPeriod()
	}
	start, end, err := periodBounds(period)
	if err != nil {
		return nil, err
	}
	rep := &SalesReport{Period: period, Lines: []SalesLine{}, Units: []ProductUnits{}}
	var items []struct {
		LineType    string
		ProductID   *uint
		ComboID     *uint
		Description string
		PlanMonths  int
		Qty         float64
		Amount      float64
	}
	if err := s.db.Table("equip_order_items i").
		Select("i.line_type, i.product_id, i.combo_id, i.description, i.plan_months, COALESCE(SUM(i.quantity),0) AS qty, COALESCE(SUM(i.subtotal),0) AS amount").
		Joins("JOIN equip_orders o ON o.id = i.order_id").
		Where("o.status = ? AND o.order_date >= ? AND o.order_date < ?", "registrado", start, end).
		Group("i.line_type, i.product_id, i.combo_id, i.description, i.plan_months").Scan(&items).Error; err != nil {
		return nil, err
	}
	prodName, comboName := map[uint][2]string{}, map[uint][2]string{}
	var prods []database.EquipProduct
	_ = s.db.Find(&prods).Error
	for _, p := range prods {
		prodName[p.ID] = [2]string{p.Code, p.Name}
	}
	var combos []database.EquipCombo
	_ = s.db.Find(&combos).Error
	for _, c := range combos {
		comboName[c.ID] = [2]string{c.Code, c.Name}
	}
	agg := map[string]*SalesLine{}
	for _, it := range items {
		key, code, name := it.LineType+"|", "", it.Description
		switch it.LineType {
		case "producto":
			if it.ProductID != nil {
				key += string(rune(*it.ProductID))
				code, name = prodName[*it.ProductID][0], prodName[*it.ProductID][1]
			}
		case "combo":
			if it.ComboID != nil {
				key += string(rune(*it.ComboID))
				code, name = comboName[*it.ComboID][0], comboName[*it.ComboID][1]
			}
		default:
			label := strings.TrimSpace(it.Description)
			if it.LineType == "plan" && it.PlanMonths > 0 {
				label += " · " + itoa(it.PlanMonths) + " m"
			}
			key += strings.ToLower(label)
			name = label
		}
		l := agg[key]
		if l == nil {
			l = &SalesLine{Kind: it.LineType, Code: code, Name: name}
			agg[key] = l
		}
		l.Quantity += it.Qty
		l.Amount += it.Amount
	}
	for _, l := range agg {
		l.Amount = round2(l.Amount)
		rep.Lines = append(rep.Lines, *l)
	}
	sort.Slice(rep.Lines, func(i, j int) bool { return rep.Lines[i].Amount > rep.Lines[j].Amount })

	var moves []struct {
		ProductID uint
		ViaCombo  bool
		SaleType  string
		Qty       int
	}
	if err := s.db.Model(&database.EquipStockMovement{}).
		Select("product_id, (via_combo_id IS NOT NULL) AS via_combo, sale_type_snapshot AS sale_type, -SUM(quantity) AS qty").
		Where("movement_type IN ? AND occurred_at >= ? AND occurred_at < ?", []string{database.EquipMoveSalida, database.EquipMoveReenvio}, start, end).
		Group("product_id, (via_combo_id IS NOT NULL), sale_type_snapshot").Scan(&moves).Error; err != nil {
		return nil, err
	}
	units := map[uint]*ProductUnits{}
	for _, m := range moves {
		u := units[m.ProductID]
		if u == nil {
			u = &ProductUnits{Code: prodName[m.ProductID][0], Name: prodName[m.ProductID][1]}
			units[m.ProductID] = u
		}
		promo := m.SaleType == database.EquipSalePromoTK
		switch {
		case m.ViaCombo && promo:
			u.ComboPromo += m.Qty
		case m.ViaCombo:
			u.ComboIndep += m.Qty
		case promo:
			u.DirectPromo += m.Qty
		default:
			u.DirectIndep += m.Qty
		}
		u.Total += m.Qty
	}
	for _, u := range units {
		rep.Units = append(rep.Units, *u)
	}
	sort.Slice(rep.Units, func(i, j int) bool { return rep.Units[i].Total > rep.Units[j].Total })
	return rep, nil
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

// ── Cobranza por antigüedad ────────────────────────────────────────────────

type DebtOrder struct {
	OrderID     uint      `json:"order_id"`
	OrderNumber int       `json:"order_number"`
	OrderDate   time.Time `json:"order_date"`
	Total       float64   `json:"total_amount"`
	Balance     float64   `json:"balance_amount"`
	Days        int       `json:"days"`
	Shipment    string    `json:"shipment_status"`
}

type DebtCustomer struct {
	CustomerID *uint       `json:"customer_id"`
	Name       string      `json:"customer_name"`
	Phone      string      `json:"customer_phone"`
	Balance    float64     `json:"balance"`
	B0         float64     `json:"b0_15"`
	B1         float64     `json:"b16_30"`
	B2         float64     `json:"b31_60"`
	B3         float64     `json:"b60_plus"`
	Oldest     int         `json:"oldest_days"`
	Orders     []DebtOrder `json:"orders"`
}

type Collections struct {
	Total     float64        `json:"total"`
	B0        float64        `json:"b0_15"`
	B1        float64        `json:"b16_30"`
	B2        float64        `json:"b31_60"`
	B3        float64        `json:"b60_plus"`
	Customers []DebtCustomer `json:"customers"`
}

func (s *Service) Collections() (*Collections, error) {
	var rows []struct {
		ID            uint
		OrderNumber   int
		OrderDate     time.Time
		CustomerID    *uint
		CustomerName  string
		CustomerPhone string
		TotalAmount   float64
		BalanceAmount float64
		ShipStatus    string
	}
	if err := s.db.Table("equip_orders o").
		Select("o.id, o.order_number, o.order_date, o.customer_id, o.customer_name, o.customer_phone, o.total_amount, o.balance_amount, COALESCE(sh.status,'') AS ship_status").
		Joins("LEFT JOIN equip_shipments sh ON sh.order_id = o.id AND sh.is_current = ?", true).
		Where("o.status = ? AND o.balance_amount > 0 AND o.payment_status <> ?", "registrado", "no_pago").
		Order("o.order_date ASC").Scan(&rows).Error; err != nil {
		return nil, err
	}
	now := todayNoon()
	out := &Collections{Customers: []DebtCustomer{}}
	byKey := map[string]*DebtCustomer{}
	var order []string
	for _, r := range rows {
		key := strings.ToLower(strings.TrimSpace(r.CustomerName))
		if r.CustomerID != nil {
			key = "id:" + itoa(int(*r.CustomerID))
		}
		c := byKey[key]
		if c == nil {
			c = &DebtCustomer{CustomerID: r.CustomerID, Name: r.CustomerName, Phone: r.CustomerPhone}
			byKey[key] = c
			order = append(order, key)
		}
		days := int(now.Sub(noonLima(r.OrderDate)).Hours() / 24)
		if days < 0 {
			days = 0
		}
		bal := round2(r.BalanceAmount)
		switch {
		case days <= 15:
			c.B0 += bal
			out.B0 += bal
		case days <= 30:
			c.B1 += bal
			out.B1 += bal
		case days <= 60:
			c.B2 += bal
			out.B2 += bal
		default:
			c.B3 += bal
			out.B3 += bal
		}
		c.Balance += bal
		out.Total += bal
		if days > c.Oldest {
			c.Oldest = days
		}
		c.Orders = append(c.Orders, DebtOrder{OrderID: r.ID, OrderNumber: r.OrderNumber, OrderDate: r.OrderDate, Total: r.TotalAmount, Balance: bal, Days: days, Shipment: r.ShipStatus})
	}
	for _, k := range order {
		c := byKey[k]
		c.Balance, c.B0, c.B1, c.B2, c.B3 = round2(c.Balance), round2(c.B0), round2(c.B1), round2(c.B2), round2(c.B3)
		out.Customers = append(out.Customers, *c)
	}
	sort.Slice(out.Customers, func(i, j int) bool { return out.Customers[i].Balance > out.Customers[j].Balance })
	out.Total, out.B0, out.B1, out.B2, out.B3 = round2(out.Total), round2(out.B0), round2(out.B1), round2(out.B2), round2(out.B3)
	return out, nil
}

// ── Reposición sugerida ────────────────────────────────────────────────────

type ReplenishRow struct {
	ProductID    uint    `json:"product_id"`
	Code         string  `json:"code"`
	Name         string  `json:"name"`
	Stock        int     `json:"stock"`
	MonthlyAvg   float64 `json:"monthly_avg"`
	CoverDays    *int    `json:"cover_days"` // nil = sin consumo
	Target       int     `json:"target"`
	Suggested    int     `json:"suggested"`
	Urgency      string  `json:"urgency"` // alta | media | ok
	MonthsOfData int     `json:"months_of_data"`
}

// Replenishment compara el consumo de los últimos tres meses cerrados con el stock actual. Objetivo: cubrir seis semanas
// (consumo mensual × 1.5), nunca menos que el umbral «verde» del producto.
func (s *Service) Replenishment() ([]ReplenishRow, error) {
	curStart, _, err := periodBounds(currentPeriod())
	if err != nil {
		return nil, err
	}
	from := curStart.AddDate(0, -3, 0)
	var products []database.EquipProduct
	if err := s.db.Where("active = ?", true).Order("code").Find(&products).Error; err != nil {
		return nil, err
	}
	var cons []struct {
		ProductID uint
		Month     string
		Qty       int
	}
	if err := s.db.Model(&database.EquipStockMovement{}).
		Select("product_id, "+monthExpr(s)+" AS month, -SUM(quantity) AS qty").
		Where("movement_type IN ? AND occurred_at >= ? AND occurred_at < ?", []string{database.EquipMoveSalida, database.EquipMoveReenvio, database.EquipMoveReingreso}, from, curStart).
		Group("product_id, " + monthExpr(s)).Scan(&cons).Error; err != nil {
		return nil, err
	}
	total, months := map[uint]int{}, map[uint]map[string]bool{}
	for _, c := range cons {
		total[c.ProductID] += c.Qty
		if months[c.ProductID] == nil {
			months[c.ProductID] = map[string]bool{}
		}
		months[c.ProductID][c.Month] = true
	}
	var stocks []struct {
		ProductID uint
		Stock     int
	}
	if err := s.db.Model(&database.EquipStockMovement{}).Select("product_id, COALESCE(SUM(quantity),0) AS stock").Group("product_id").Scan(&stocks).Error; err != nil {
		return nil, err
	}
	stock := map[uint]int{}
	for _, r := range stocks {
		stock[r.ProductID] = r.Stock
	}
	out := make([]ReplenishRow, 0, len(products))
	for _, p := range products {
		n := len(months[p.ID])
		avg := 0.0
		if n > 0 {
			avg = float64(total[p.ID]) / float64(n)
		}
		if avg < 0 {
			avg = 0
		}
		row := ReplenishRow{ProductID: p.ID, Code: p.Code, Name: p.Name, Stock: stock[p.ID], MonthlyAvg: math.Round(avg*10) / 10, MonthsOfData: n, Urgency: "ok"}
		target := int(math.Ceil(avg * 1.5))
		if p.GreenThreshold > target {
			target = p.GreenThreshold
		}
		row.Target = target
		if avg > 0 {
			d := int(math.Floor(float64(row.Stock) / (avg / 30)))
			if d < 0 {
				d = 0
			}
			row.CoverDays = &d
			switch {
			case row.Stock <= 0 || d < 15:
				row.Urgency = "alta"
			case d < 30:
				row.Urgency = "media"
			}
		} else if row.Stock <= p.YellowThreshold && p.YellowThreshold > 0 {
			row.Urgency = "media"
		}
		if row.Stock < target {
			row.Suggested = target - row.Stock
		}
		out = append(out, row)
	}
	rank := map[string]int{"alta": 0, "media": 1, "ok": 2}
	sort.SliceStable(out, func(i, j int) bool {
		if rank[out[i].Urgency] != rank[out[j].Urgency] {
			return rank[out[i].Urgency] < rank[out[j].Urgency]
		}
		return out[i].Suggested > out[j].Suggested
	})
	return out, nil
}

// monthExpr expresión SQL AAAA-MM de occurred_at (MySQL en producción; SQLite en las pruebas).
func monthExpr(s *Service) string {
	if s.db.Dialector.Name() == "sqlite" {
		return "strftime('%Y-%m', occurred_at)"
	}
	return "DATE_FORMAT(occurred_at, '%Y-%m')"
}

// ── Utilidad (costos opcionales) ───────────────────────────────────────────

type ProfitRow struct {
	SaleType string  `json:"sale_type"`
	Revenue  float64 `json:"revenue"`
	Cogs     float64 `json:"cogs"`
	Freight  float64 `json:"freight"`
	Profit   float64 `json:"profit"`
	Margin   float64 `json:"margin"` // fracción (0.25 = 25 %)
}

type ProfitReport struct {
	Period        string      `json:"period"`
	Rows          []ProfitRow `json:"rows"`
	Total         ProfitRow   `json:"total"`
	ReturnCost    float64     `json:"return_cost"`
	CostCoverage  float64     `json:"cost_coverage"` // fracción de las unidades vendidas con costo conocido
	MissingCostBy []string    `json:"missing_cost_products"`
}

// Profitability utilidad del período por tipo de salida: ventas − costo de lo vendido (costo promedio ponderado de los
// ingresos que traen costo) − flete − costo de retornos. Es informativa: sin costos cargados, el costo queda en 0 y
// `cost_coverage` lo dice.
func (s *Service) Profitability(period string) (*ProfitReport, error) {
	if period == "" {
		period = currentPeriod()
	}
	start, end, err := periodBounds(period)
	if err != nil {
		return nil, err
	}
	var avgs []struct {
		ProductID uint
		Avg       float64
	}
	if err := s.db.Model(&database.EquipStockMovement{}).
		Select("product_id, SUM(quantity * unit_cost) / SUM(quantity) AS avg").
		Where("movement_type = ? AND unit_cost IS NOT NULL AND quantity > 0", database.EquipMoveIngreso).Group("product_id").Scan(&avgs).Error; err != nil {
		return nil, err
	}
	cost := map[uint]float64{}
	for _, a := range avgs {
		cost[a.ProductID] = a.Avg
	}
	rep := &ProfitReport{Period: period, Rows: []ProfitRow{}, MissingCostBy: []string{}}
	by := map[string]*ProfitRow{}
	row := func(t string) *ProfitRow {
		if by[t] == nil {
			by[t] = &ProfitRow{SaleType: t}
		}
		return by[t]
	}
	var rev []struct {
		SaleType string
		Total    float64
	}
	if err := s.db.Model(&database.EquipOrder{}).Select("sale_type, COALESCE(SUM(total_amount),0) AS total").
		Where("status = ? AND order_date >= ? AND order_date < ?", "registrado", start, end).Group("sale_type").Scan(&rev).Error; err != nil {
		return nil, err
	}
	for _, r := range rev {
		row(r.SaleType).Revenue += r.Total
	}
	var fr []struct {
		SaleType string
		Total    float64
	}
	if err := s.db.Table("equip_shipments sh").Select("o.sale_type, COALESCE(SUM(sh.freight_cost),0) AS total").
		Joins("JOIN equip_orders o ON o.id = sh.order_id").
		Where("o.status = ? AND o.order_date >= ? AND o.order_date < ?", "registrado", start, end).Group("o.sale_type").Scan(&fr).Error; err != nil {
		return nil, err
	}
	for _, r := range fr {
		row(r.SaleType).Freight += r.Total
	}
	var mv []struct {
		ProductID uint
		SaleType  string
		Qty       int
	}
	if err := s.db.Model(&database.EquipStockMovement{}).Select("product_id, sale_type_snapshot AS sale_type, -SUM(quantity) AS qty").
		Where("movement_type IN ? AND occurred_at >= ? AND occurred_at < ?", []string{database.EquipMoveSalida, database.EquipMoveReenvio}, start, end).
		Group("product_id, sale_type_snapshot").Scan(&mv).Error; err != nil {
		return nil, err
	}
	known, all := 0, 0
	missing := map[uint]bool{}
	for _, m := range mv {
		all += m.Qty
		if c, ok := cost[m.ProductID]; ok {
			known += m.Qty
			row(m.SaleType).Cogs += float64(m.Qty) * c
		} else if m.Qty > 0 {
			missing[m.ProductID] = true
		}
	}
	if all > 0 {
		rep.CostCoverage = float64(known) / float64(all)
	}
	if len(missing) > 0 {
		var ps []database.EquipProduct
		ids := make([]uint, 0, len(missing))
		for id := range missing {
			ids = append(ids, id)
		}
		_ = s.db.Where("id IN ?", ids).Order("code").Find(&ps).Error
		for _, p := range ps {
			rep.MissingCostBy = append(rep.MissingCostBy, p.Code)
		}
	}
	var rc struct{ Cost float64 }
	_ = s.db.Model(&database.EquipReturn{}).Select("COALESCE(SUM(return_cost),0) AS cost").Where("requested_at >= ? AND requested_at < ?", start, end).Scan(&rc).Error
	rep.ReturnCost = round2(rc.Cost)
	types := make([]string, 0, len(by))
	for t := range by {
		types = append(types, t)
	}
	sort.Strings(types)
	for _, t := range types {
		r := by[t]
		r.Profit = r.Revenue - r.Cogs - r.Freight
		rep.Total.Revenue += r.Revenue
		rep.Total.Cogs += r.Cogs
		rep.Total.Freight += r.Freight
		fin(r)
		rep.Rows = append(rep.Rows, *r)
	}
	rep.Total.SaleType = "total"
	rep.Total.Profit = rep.Total.Revenue - rep.Total.Cogs - rep.Total.Freight - rep.ReturnCost
	fin(&rep.Total)
	return rep, nil
}

func fin(r *ProfitRow) {
	r.Revenue, r.Cogs, r.Freight, r.Profit = round2(r.Revenue), round2(r.Cogs), round2(r.Freight), round2(r.Profit)
	if r.Revenue > 0 {
		r.Margin = math.Round(r.Profit/r.Revenue*1000) / 1000
	}
}
