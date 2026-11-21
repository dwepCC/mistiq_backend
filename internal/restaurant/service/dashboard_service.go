package service

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"tukifac/pkg/database"
	"tukifac/pkg/money"
	"tukifac/pkg/paymentcondition"
	"tukifac/pkg/saas"
	"tukifac/pkg/salescope"

	"gorm.io/gorm"
)

// DashboardData respuesta agregada GET /api/restaurant/dashboard
type DashboardData struct {
	Summary              DashboardSummary       `json:"summary"`
	SalesByStatus        []DashboardStatusRow   `json:"sales_by_status"`
	SalesByPaymentMethod []DashboardPaymentRow  `json:"sales_by_payment_method"`
	TopProducts          []DashboardProductRow  `json:"top_products"`
	TopCategories        []DashboardCategoryRow `json:"top_categories"`
	SalesLast30Days      []DashboardDayRow      `json:"sales_last_30_days"`
	SalesByHour          []DashboardHourRow     `json:"sales_by_hour"`
	TablesSummary        DashboardTablesSummary `json:"tables_summary"`
	ScopedToUser         bool                   `json:"scoped_to_user"`
}

type DashboardSummary struct {
	TotalSales        float64 `json:"total_sales"`
	TotalOrders       int64   `json:"total_orders"`
	AverageTicket     float64 `json:"average_ticket"`
	ProductsSold      float64 `json:"products_sold"`
	ClientsServed     int64   `json:"clients_served"`
	HasClientData     bool    `json:"has_client_data"`
}

type DashboardStatusRow struct {
	Status string `json:"status"`
	Label  string `json:"label"`
	Count  int64  `json:"count"`
}

type DashboardPaymentRow struct {
	Method string  `json:"method"`
	Label  string  `json:"label"`
	Total  float64 `json:"total"`
	Count  int64   `json:"count"`
}

type DashboardProductRow struct {
	Position         int     `json:"position"`
	ProductID        uint    `json:"product_id"`
	ProductName      string  `json:"product_name"`
	QuantitySold     float64 `json:"quantity_sold"`
	TotalAmount      float64 `json:"total_amount"`
	ParticipationPct float64 `json:"participation_pct"`
}

type DashboardCategoryRow struct {
	CategoryName string  `json:"category_name"`
	QuantitySold float64 `json:"quantity_sold"`
	TotalAmount  float64 `json:"total_amount"`
}

type DashboardDayRow struct {
	Day   string  `json:"day"`
	Total float64 `json:"total"`
	Count int64   `json:"count"`
}

type DashboardHourRow struct {
	Hour    int   `json:"hour"`
	Label   string `json:"label"`
	Orders  int64  `json:"orders"`
}

type DashboardTablesSummary struct {
	Enabled   bool  `json:"enabled"`
	Occupied  int64 `json:"occupied"`
	Free      int64 `json:"free"`
	Reserved  int64 `json:"reserved"`
	Total     int64 `json:"total"`
}

type DashboardService struct {
	db *gorm.DB
}

func NewDashboardService(db *gorm.DB) *DashboardService {
	return &DashboardService{db: db}
}

func (s *DashboardService) GetDashboard(branchID uint, from, toExclusive time.Time, topN int, filter DashboardFilter) (*DashboardData, error) {
	if topN <= 0 {
		topN = 10
	}
	if topN > 100 {
		topN = 100
	}

	out := &DashboardData{ScopedToUser: filter.RestrictUser && filter.UserID > 0}

	if err := s.loadSummary(branchID, from, toExclusive, filter, out); err != nil {
		return nil, err
	}
	if err := s.loadSalesByStatus(branchID, from, toExclusive, filter, out); err != nil {
		return nil, err
	}
	if err := s.loadSalesByPayment(branchID, from, toExclusive, filter, out); err != nil {
		return nil, err
	}
	if err := s.loadTopProducts(branchID, from, toExclusive, topN, filter, out); err != nil {
		return nil, err
	}
	if err := s.loadTopCategories(branchID, from, toExclusive, filter, out); err != nil {
		return nil, err
	}
	if err := s.loadSalesLast30Days(branchID, filter, out); err != nil {
		return nil, err
	}
	if err := s.loadSalesByHour(branchID, from, toExclusive, filter, out); err != nil {
		return nil, err
	}
	if err := s.loadTablesSummary(branchID, out); err != nil {
		return nil, err
	}

	return out, nil
}

// restaurantSalesScope: ventas de la sucursal en el rango (mismo criterio que /api/sales).
// No exige restaurant_session_id: NV/FE, ventas legacy y emisiones sin sesión también cuentan.
func restaurantSalesScope(db *gorm.DB, branchID uint, from, toExclusive time.Time, filter DashboardFilter) *gorm.DB {
	q := salescope.EmittedSalesNoNotes(db.Model(&database.TenantSale{})).
		Where("issue_date >= ? AND issue_date < ?", from, toExclusive).
		Where("status != ?", "cancelled")
	if branchID > 0 {
		q = q.Where("branch_id = ?", branchID)
	}
	if filter.RestrictUser && filter.UserID > 0 {
		q = q.Where("user_id = ?", filter.UserID)
	}
	return q
}

func dashboardDayKey(raw string) string {
	raw = strings.TrimSpace(raw)
	if i := strings.IndexByte(raw, 'T'); i >= 0 {
		raw = raw[:i]
	}
	if len(raw) >= 10 {
		return raw[:10]
	}
	return raw
}

func restaurantSessionsScope(db *gorm.DB, branchID uint, from, toExclusive time.Time, filter DashboardFilter) *gorm.DB {
	q := db.Model(&database.TenantTableSession{}).
		Where("opened_at >= ? AND opened_at < ?", from, toExclusive)
	if branchID > 0 {
		q = q.Where("branch_id = ?", branchID)
	}
	if filter.RestrictUser && filter.UserID > 0 {
		q = q.Where("user_id = ?", filter.UserID)
	}
	return q
}

func userScopeSaleJoin(filter DashboardFilter) func(*gorm.DB) *gorm.DB {
	return func(db *gorm.DB) *gorm.DB {
		if filter.RestrictUser && filter.UserID > 0 {
			return db.Where("s.user_id = ?", filter.UserID)
		}
		return db
	}
}

func (s *DashboardService) loadSummary(branchID uint, from, toExclusive time.Time, filter DashboardFilter, out *DashboardData) error {
	type salesAgg struct {
		TotalSales    float64 `gorm:"column:total_sales"`
		TotalOrders   int64   `gorm:"column:total_orders"`
		ClientsServed int64   `gorm:"column:clients_served"`
	}
	var agg salesAgg
	err := restaurantSalesScope(s.db, branchID, from, toExclusive, filter).
		Select(`
			COALESCE(SUM(CASE WHEN currency = 'USD' AND exchange_rate > 0 THEN (total - COALESCE(service_charge_amount, 0)) * exchange_rate ELSE (total - COALESCE(service_charge_amount, 0)) END), 0) AS total_sales,
			COUNT(*) AS total_orders,
			COUNT(DISTINCT CASE WHEN contact_id IS NOT NULL AND contact_id > 0 THEN contact_id END) AS clients_served
		`).
		Scan(&agg).Error
	if err != nil {
		return err
	}

	var productsSold float64
	err = s.db.Table("tenant_sale_items si").
		Select("COALESCE(SUM(si.quantity), 0)").
		Joins("INNER JOIN tenant_sales s ON s.id = si.sale_id").
		Scopes(salescope.ScopeEmittedNoNotes("s")).
		Where("s.issue_date >= ? AND s.issue_date < ?", from, toExclusive).
		Where("s.status != ?", "cancelled").
		Scopes(branchScopeSales(branchID)).
		Scopes(userScopeSaleJoin(filter)).
		Scan(&productsSold).Error
	if err != nil {
		return err
	}

	avg := 0.0
	if agg.TotalOrders > 0 {
		avg = agg.TotalSales / float64(agg.TotalOrders)
	}

	out.Summary = DashboardSummary{
		TotalSales:    agg.TotalSales,
		TotalOrders:   agg.TotalOrders,
		AverageTicket: avg,
		ProductsSold:  productsSold,
		ClientsServed: agg.ClientsServed,
		HasClientData: agg.ClientsServed > 0,
	}
	return nil
}

func branchScopeSales(branchID uint) func(*gorm.DB) *gorm.DB {
	return func(db *gorm.DB) *gorm.DB {
		if branchID > 0 {
			return db.Where("s.branch_id = ?", branchID)
		}
		return db
	}
}

func (s *DashboardService) loadSalesByStatus(branchID uint, from, toExclusive time.Time, filter DashboardFilter, out *DashboardData) error {
	type row struct {
		StatusKey string `gorm:"column:status_key"`
		Count     int64  `gorm:"column:count"`
	}
	var rows []row
	err := restaurantSessionsScope(s.db, branchID, from, toExclusive, filter).
		Select(`
			CASE
				WHEN order_status IN ('draft','pending') THEN 'pending'
				WHEN order_status IN ('sent_to_kitchen','preparing') THEN 'preparing'
				WHEN order_status IN ('ready','on_the_way') THEN 'ready'
				WHEN order_status IN ('delivered','paid') THEN 'delivered'
				WHEN order_status = 'cancelled' THEN 'cancelled'
				ELSE 'pending'
			END AS status_key,
			COUNT(*) AS count
		`).
		Group("status_key").
		Scan(&rows).Error
	if err != nil {
		return err
	}

	labelByKey := map[string]string{
		"pending":    "Pendientes",
		"preparing":  "En preparación",
		"ready":      "Listos",
		"delivered":  "Entregados",
		"cancelled":  "Cancelados",
	}
	order := []string{"pending", "preparing", "ready", "delivered", "cancelled"}
	byKey := make(map[string]int64, len(rows))
	for _, r := range rows {
		byKey[r.StatusKey] = r.Count
	}
	out.SalesByStatus = make([]DashboardStatusRow, 0, len(order))
	for _, k := range order {
		if c, ok := byKey[k]; ok && c > 0 {
			out.SalesByStatus = append(out.SalesByStatus, DashboardStatusRow{
				Status: k,
				Label:  labelByKey[k],
				Count:  c,
			})
		}
	}
	return nil
}

func paymentLabel(bucket string) string {
	switch bucket {
	case "efectivo":
		return "Efectivo"
	case "tarjeta":
		return "Tarjeta"
	case "yape":
		return "Yape"
	case "plin":
		return "Plin"
	case "transferencia":
		return "Transferencia"
	default:
		return "Otros"
	}
}

// paymentBucket agrupa el código de método de pago en los cubos del dashboard.
func paymentBucket(method string) string {
	switch strings.ToLower(strings.TrimSpace(method)) {
	case "cash", "efectivo":
		return "efectivo"
	case "card", "tarjeta":
		return "tarjeta"
	case "yape":
		return "yape"
	case "plin":
		return "plin"
	case "transfer", "transferencia":
		return "transferencia"
	default:
		return "otros"
	}
}

// loadSalesByPayment reparte el dinero de las ventas del período por método de pago.
//
// tenant_sale_payments.amount guarda lo que el cliente ENTREGÓ (puede superar el total de la venta
// cuando hay vuelto). Sumarlo tal cual inflaba el efectivo (y el total del gráfico) con el vuelto. Se
// reparte con money.AllocateSalePaymentReportAmounts, igual que el dashboard del ERP y el listado de
// ventas: el vuelto se descuenta únicamente del efectivo y ningún método supera lo que cubrió la venta.
// Una venta sin líneas de pago (histórico) se atribuye por tenant_sales.payment_method con su total.
// Count = ventas distintas que usaron cada método.
func (s *DashboardService) loadSalesByPayment(branchID uint, from, toExclusive time.Time, filter DashboardFilter, out *DashboardData) error {
	var sales []struct {
		ID            uint
		Total         float64
		PaymentMethod string
	}
	if err := restaurantSalesScope(s.db, branchID, from, toExclusive, filter).
		Select("id, total, payment_method").Scan(&sales).Error; err != nil {
		return err
	}

	type payRow struct {
		ID     uint
		SaleID uint
		Method string
		Amount float64
	}
	var pays []payRow
	if err := s.db.Table("tenant_sale_payments tsp").
		Select("tsp.id AS id, tsp.sale_id AS sale_id, LOWER(TRIM(tsp.method)) AS method, tsp.amount AS amount").
		Joins("INNER JOIN tenant_sales s ON s.id = tsp.sale_id").
		Scopes(salescope.ScopeEmittedNoNotes("s")).
		Where("s.issue_date >= ? AND s.issue_date < ?", from, toExclusive).
		Where("s.status != ?", "cancelled").
		Scopes(branchScopeSales(branchID)).
		Scopes(userScopeSaleJoin(filter)).
		Order("tsp.id ASC").
		Scan(&pays).Error; err != nil {
		return err
	}
	paysBySale := make(map[uint][]payRow, len(sales))
	for _, p := range pays {
		paysBySale[p.SaleID] = append(paysBySale[p.SaleID], p)
	}

	totals := map[string]float64{}
	counts := map[string]int64{}
	for _, sale := range sales {
		rows := paysBySale[sale.ID]
		if len(rows) == 0 {
			b := paymentBucket(sale.PaymentMethod)
			totals[b] += sale.Total
			counts[b]++
			continue
		}
		lines := make([]money.SalePaymentLine, 0, len(rows))
		bucketByID := make(map[uint]string, len(rows))
		for _, r := range rows {
			bucketByID[r.ID] = paymentBucket(r.Method)
			// El marcador de crédito no es dinero recibido: no entra a la base del vuelto.
			if paymentcondition.IsCreditCode(r.Method) {
				continue
			}
			lines = append(lines, money.SalePaymentLine{ID: r.ID, Amount: r.Amount, IsCash: money.IsCashMethod(r.Method)})
		}
		seen := map[string]struct{}{}
		add := func(bucket string, amount float64) {
			totals[bucket] += amount
			if _, ok := seen[bucket]; !ok {
				seen[bucket] = struct{}{}
				counts[bucket]++
			}
		}
		for id, amt := range money.AllocateSalePaymentReportAmounts(sale.Total, lines) {
			add(bucketByID[id], amt)
		}
		for _, r := range rows {
			if paymentcondition.IsCreditCode(r.Method) {
				add(bucketByID[r.ID], r.Amount)
			}
		}
	}

	out.SalesByPaymentMethod = make([]DashboardPaymentRow, 0, len(totals))
	for b, total := range totals {
		out.SalesByPaymentMethod = append(out.SalesByPaymentMethod, DashboardPaymentRow{
			Method: b,
			Label:  paymentLabel(b),
			Total:  money.RoundDisplay(total),
			Count:  counts[b],
		})
	}
	sort.Slice(out.SalesByPaymentMethod, func(i, j int) bool {
		a, c := out.SalesByPaymentMethod[i], out.SalesByPaymentMethod[j]
		if a.Total != c.Total {
			return a.Total > c.Total
		}
		return a.Method < c.Method
	})
	return nil
}

func (s *DashboardService) loadTopProducts(branchID uint, from, toExclusive time.Time, topN int, filter DashboardFilter, out *DashboardData) error {
	branchClause := ""
	userClause := ""
	args := []interface{}{from, toExclusive}
	if branchID > 0 {
		branchClause = " AND s.branch_id = ?"
		args = append(args, branchID)
	}
	if filter.RestrictUser && filter.UserID > 0 {
		userClause = " AND s.user_id = ?"
		args = append(args, filter.UserID)
	}

	var totalAmountAll float64
	totalSQL := `
		SELECT COALESCE(SUM(CASE WHEN s.currency = 'USD' AND s.exchange_rate > 0 THEN si.total * s.exchange_rate ELSE si.total END), 0)
		FROM tenant_sale_items si
		INNER JOIN tenant_sales s ON s.id = si.sale_id
		WHERE s.issue_date >= ? AND s.issue_date < ?
			AND s.status != 'cancelled'
			AND ` + salescope.EmittedWhereNoNotes("s") + branchClause + userClause
	if err := s.db.Raw(totalSQL, args...).Scan(&totalAmountAll).Error; err != nil {
		return err
	}

	type prodRow struct {
		ProductID    uint    `gorm:"column:product_id"`
		ProductName  string  `gorm:"column:product_name"`
		QuantitySold float64 `gorm:"column:quantity_sold"`
		TotalAmount  float64 `gorm:"column:total_amount"`
	}
	productSQL := `
		SELECT
			COALESCE(si.product_id, 0) AS product_id,
			COALESCE(NULLIF(TRIM(p.name), ''), NULLIF(TRIM(si.description), ''), 'Sin nombre') AS product_name,
			COALESCE(SUM(si.quantity), 0) AS quantity_sold,
			COALESCE(SUM(CASE WHEN s.currency = 'USD' AND s.exchange_rate > 0 THEN si.total * s.exchange_rate ELSE si.total END), 0) AS total_amount
		FROM tenant_sale_items si
		INNER JOIN tenant_sales s ON s.id = si.sale_id
		LEFT JOIN tenant_products p ON p.id = si.product_id
		WHERE s.issue_date >= ? AND s.issue_date < ?
			AND s.status != 'cancelled'
			AND ` + salescope.EmittedWhereNoNotes("s") + branchClause + userClause + `
		GROUP BY COALESCE(si.product_id, 0), product_name
		ORDER BY quantity_sold DESC
		LIMIT ?
	`
	productArgs := append([]interface{}{}, args...)
	productArgs = append(productArgs, topN)

	var rows []prodRow
	if err := s.db.Raw(productSQL, productArgs...).Scan(&rows).Error; err != nil {
		return err
	}

	out.TopProducts = make([]DashboardProductRow, 0, len(rows))
	for i, r := range rows {
		pct := 0.0
		if totalAmountAll > 0 {
			pct = r.TotalAmount / totalAmountAll * 100
		}
		out.TopProducts = append(out.TopProducts, DashboardProductRow{
			Position:         i + 1,
			ProductID:        r.ProductID,
			ProductName:      r.ProductName,
			QuantitySold:     r.QuantitySold,
			TotalAmount:      r.TotalAmount,
			ParticipationPct: pct,
		})
	}
	return nil
}

func (s *DashboardService) loadTopCategories(branchID uint, from, toExclusive time.Time, filter DashboardFilter, out *DashboardData) error {
	branchClause := ""
	userClause := ""
	args := []interface{}{from, toExclusive}
	if branchID > 0 {
		branchClause = " AND s.branch_id = ?"
		args = append(args, branchID)
	}
	if filter.RestrictUser && filter.UserID > 0 {
		userClause = " AND s.user_id = ?"
		args = append(args, filter.UserID)
	}

	type catRow struct {
		CategoryName string  `gorm:"column:category_name"`
		QuantitySold float64 `gorm:"column:quantity_sold"`
		TotalAmount  float64 `gorm:"column:total_amount"`
	}
	sql := `
		SELECT
			COALESCE(NULLIF(TRIM(pc.name), ''), 'Sin categoría') AS category_name,
			COALESCE(SUM(si.quantity), 0) AS quantity_sold,
			COALESCE(SUM(CASE WHEN s.currency = 'USD' AND s.exchange_rate > 0 THEN si.total * s.exchange_rate ELSE si.total END), 0) AS total_amount
		FROM tenant_sale_items si
		INNER JOIN tenant_sales s ON s.id = si.sale_id
		LEFT JOIN tenant_products p ON p.id = si.product_id
		LEFT JOIN tenant_categories pc ON pc.id = p.category_id
		WHERE s.issue_date >= ? AND s.issue_date < ?
			AND s.status != 'cancelled'
			AND ` + salescope.EmittedWhereNoNotes("s") + branchClause + userClause + `
		GROUP BY category_name
		ORDER BY total_amount DESC
		LIMIT 20
	`
	var rows []catRow
	if err := s.db.Raw(sql, args...).Scan(&rows).Error; err != nil {
		return err
	}
	out.TopCategories = make([]DashboardCategoryRow, 0, len(rows))
	for _, r := range rows {
		out.TopCategories = append(out.TopCategories, DashboardCategoryRow{
			CategoryName: r.CategoryName,
			QuantitySold: r.QuantitySold,
			TotalAmount:  r.TotalAmount,
		})
	}
	return nil
}

func (s *DashboardService) loadSalesLast30Days(branchID uint, filter DashboardFilter, out *DashboardData) error {
	now := saas.NowLima()
	todayStart := saas.CalendarDateLima(now)
	from30 := todayStart.AddDate(0, 0, -29)
	toExclusive := todayStart.AddDate(0, 0, 1)

	type dayRow struct {
		Day   string  `gorm:"column:day"`
		Total float64 `gorm:"column:total"`
		Count int64   `gorm:"column:count"`
	}
	q := restaurantSalesScope(s.db, branchID, from30, toExclusive, filter).
		Select("DATE_FORMAT(issue_date, '%Y-%m-%d') AS day, COALESCE(SUM(CASE WHEN currency = 'USD' AND exchange_rate > 0 THEN total * exchange_rate ELSE total END), 0) AS total, COUNT(*) AS count").
		Group("DATE_FORMAT(issue_date, '%Y-%m-%d')").
		Order("day")
	var rows []dayRow
	if err := q.Scan(&rows).Error; err != nil {
		return err
	}

	// Rellenar días sin ventas para gráfico continuo
	byDay := make(map[string]dayRow, len(rows))
	for _, r := range rows {
		byDay[dashboardDayKey(r.Day)] = r
	}
	out.SalesLast30Days = make([]DashboardDayRow, 0, 30)
	for d := from30; d.Before(toExclusive); d = d.AddDate(0, 0, 1) {
		key := d.Format("2006-01-02")
		if r, ok := byDay[key]; ok {
			out.SalesLast30Days = append(out.SalesLast30Days, DashboardDayRow{
				Day:   key,
				Total: r.Total,
				Count: r.Count,
			})
		} else {
			out.SalesLast30Days = append(out.SalesLast30Days, DashboardDayRow{Day: key, Total: 0, Count: 0})
		}
	}
	return nil
}

func (s *DashboardService) loadSalesByHour(branchID uint, from, toExclusive time.Time, filter DashboardFilter, out *DashboardData) error {
	type hourRow struct {
		Hour   int   `gorm:"column:hour"`
		Orders int64 `gorm:"column:orders"`
	}
	q := restaurantSessionsScope(s.db, branchID, from, toExclusive, filter).
		Select("HOUR(opened_at) AS hour, COUNT(*) AS orders").
		Group("HOUR(opened_at)").
		Order("hour")
	var rows []hourRow
	if err := q.Scan(&rows).Error; err != nil {
		return err
	}
	byHour := make(map[int]int64, len(rows))
	for _, r := range rows {
		byHour[r.Hour] = r.Orders
	}
	out.SalesByHour = make([]DashboardHourRow, 0, 24)
	for h := 0; h < 24; h++ {
		out.SalesByHour = append(out.SalesByHour, DashboardHourRow{
			Hour:   h,
			Label:  fmt.Sprintf("%02d:00", h),
			Orders: byHour[h],
		})
	}
	return nil
}

func (s *DashboardService) loadTablesSummary(branchID uint, out *DashboardData) error {
	type statusRow struct {
		Status string `gorm:"column:status"`
		Count  int64  `gorm:"column:count"`
	}
	q := s.db.Model(&database.TenantRestaurantTable{}).
		Select("status, COUNT(*) AS count").
		Where("active = ?", true)
	if branchID > 0 {
		q = q.Where("branch_id = ?", branchID)
	}
	var rows []statusRow
	if err := q.Group("status").Scan(&rows).Error; err != nil {
		return err
	}

	summary := DashboardTablesSummary{Enabled: true}
	for _, r := range rows {
		summary.Total += r.Count
		switch strings.ToLower(strings.TrimSpace(r.Status)) {
		case "libre":
			summary.Free += r.Count
		case "ocupada", "en_consumo":
			summary.Occupied += r.Count
		default:
			summary.Reserved += r.Count
		}
	}
	out.TablesSummary = summary
	return nil
}
