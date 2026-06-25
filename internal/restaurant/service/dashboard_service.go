package service

import (
	"fmt"
	"strings"
	"time"

	"tukifac/pkg/database"
	"tukifac/pkg/saas"
	"tukifac/pkg/salescope"

	"gorm.io/gorm"
)

// DashboardData respuesta agregada GET /api/restaurant/dashboard
type DashboardData struct {
	Summary              DashboardSummary       `json:"summary"`
	SalesByStatus        []DashboardStatusRow `json:"sales_by_status"`
	SalesByPaymentMethod []DashboardPaymentRow `json:"sales_by_payment_method"`
	TopProducts          []DashboardProductRow `json:"top_products"`
	TopCategories        []DashboardCategoryRow `json:"top_categories"`
	SalesLast30Days      []DashboardDayRow      `json:"sales_last_30_days"`
	SalesByHour          []DashboardHourRow     `json:"sales_by_hour"`
	TablesSummary        DashboardTablesSummary `json:"tables_summary"`
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

func (s *DashboardService) GetDashboard(branchID uint, from, toExclusive time.Time, topN int) (*DashboardData, error) {
	if topN <= 0 {
		topN = 10
	}
	if topN > 100 {
		topN = 100
	}

	out := &DashboardData{}

	if err := s.loadSummary(branchID, from, toExclusive, out); err != nil {
		return nil, err
	}
	if err := s.loadSalesByStatus(branchID, from, toExclusive, out); err != nil {
		return nil, err
	}
	if err := s.loadSalesByPayment(branchID, from, toExclusive, out); err != nil {
		return nil, err
	}
	if err := s.loadTopProducts(branchID, from, toExclusive, topN, out); err != nil {
		return nil, err
	}
	if err := s.loadTopCategories(branchID, from, toExclusive, out); err != nil {
		return nil, err
	}
	if err := s.loadSalesLast30Days(branchID, out); err != nil {
		return nil, err
	}
	if err := s.loadSalesByHour(branchID, from, toExclusive, out); err != nil {
		return nil, err
	}
	if err := s.loadTablesSummary(branchID, out); err != nil {
		return nil, err
	}

	return out, nil
}

// restaurantSalesScope: ventas de la sucursal en el rango (mismo criterio que /api/sales).
// No exige restaurant_session_id: NV/FE, ventas legacy y emisiones sin sesión también cuentan.
func restaurantSalesScope(db *gorm.DB, branchID uint, from, toExclusive time.Time) *gorm.DB {
	q := salescope.CommercialSales(db.Model(&database.TenantSale{})).
		Where("issue_date >= ? AND issue_date < ?", from, toExclusive).
		Where("status != ?", "cancelled")
	if branchID > 0 {
		q = q.Where("branch_id = ?", branchID)
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

func restaurantSessionsScope(db *gorm.DB, branchID uint, from, toExclusive time.Time) *gorm.DB {
	q := db.Model(&database.TenantTableSession{}).
		Where("opened_at >= ? AND opened_at < ?", from, toExclusive)
	if branchID > 0 {
		q = q.Where("branch_id = ?", branchID)
	}
	return q
}

func (s *DashboardService) loadSummary(branchID uint, from, toExclusive time.Time, out *DashboardData) error {
	type salesAgg struct {
		TotalSales   float64 `gorm:"column:total_sales"`
		TotalOrders  int64   `gorm:"column:total_orders"`
		ClientsServed int64  `gorm:"column:clients_served"`
	}
	var agg salesAgg
	err := restaurantSalesScope(s.db, branchID, from, toExclusive).
		Select(`
			COALESCE(SUM(total), 0) AS total_sales,
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
		Scopes(salescope.ScopeCommercial("s")).
		Where("s.issue_date >= ? AND s.issue_date < ?", from, toExclusive).
		Where("s.status != ?", "cancelled").
		Scopes(branchScopeSales(branchID)).
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

func (s *DashboardService) loadSalesByStatus(branchID uint, from, toExclusive time.Time, out *DashboardData) error {
	type row struct {
		StatusKey string `gorm:"column:status_key"`
		Count     int64  `gorm:"column:count"`
	}
	var rows []row
	err := restaurantSessionsScope(s.db, branchID, from, toExclusive).
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

func (s *DashboardService) loadSalesByPayment(branchID uint, from, toExclusive time.Time, out *DashboardData) error {
	branchClause := ""
	argsPayments := []interface{}{from, toExclusive}
	argsHeader := []interface{}{from, toExclusive}
	if branchID > 0 {
		branchClause = " AND s.branch_id = ?"
		argsPayments = append(argsPayments, branchID)
		argsHeader = append(argsHeader, branchID)
	}

	sql := `
		SELECT bucket, COALESCE(SUM(amount), 0) AS total, COALESCE(SUM(cnt), 0) AS count FROM (
			SELECT
				CASE
					WHEN LOWER(TRIM(tsp.method)) IN ('cash','efectivo') THEN 'efectivo'
					WHEN LOWER(TRIM(tsp.method)) IN ('card','tarjeta') THEN 'tarjeta'
					WHEN LOWER(TRIM(tsp.method)) = 'yape' THEN 'yape'
					WHEN LOWER(TRIM(tsp.method)) = 'plin' THEN 'plin'
					WHEN LOWER(TRIM(tsp.method)) IN ('transfer','transferencia') THEN 'transferencia'
					ELSE 'otros'
				END AS bucket,
				tsp.amount AS amount,
				1 AS cnt
			FROM tenant_sale_payments tsp
			INNER JOIN tenant_sales s ON s.id = tsp.sale_id
			WHERE s.issue_date >= ? AND s.issue_date < ?
				AND s.status != 'cancelled'
				AND ` + salescope.CommercialWhere("s") + branchClause + `
			UNION ALL
			SELECT
				CASE
					WHEN LOWER(TRIM(s.payment_method)) IN ('cash','efectivo') THEN 'efectivo'
					WHEN LOWER(TRIM(s.payment_method)) IN ('card','tarjeta') THEN 'tarjeta'
					WHEN LOWER(TRIM(s.payment_method)) = 'yape' THEN 'yape'
					WHEN LOWER(TRIM(s.payment_method)) = 'plin' THEN 'plin'
					WHEN LOWER(TRIM(s.payment_method)) IN ('transfer','transferencia') THEN 'transferencia'
					ELSE 'otros'
				END AS bucket,
				s.total AS amount,
				1 AS cnt
			FROM tenant_sales s
			WHERE s.issue_date >= ? AND s.issue_date < ?
				AND s.status != 'cancelled'
				AND ` + salescope.CommercialWhere("s") + `
				AND NOT EXISTS (SELECT 1 FROM tenant_sale_payments tsp WHERE tsp.sale_id = s.id)` + branchClause + `
		) AS combined
		GROUP BY bucket
		ORDER BY total DESC
	`
	fullArgs := append(argsPayments, argsHeader...)

	type payRow struct {
		Bucket string  `gorm:"column:bucket"`
		Total  float64 `gorm:"column:total"`
		Count  int64   `gorm:"column:count"`
	}
	var rows []payRow
	if err := s.db.Raw(sql, fullArgs...).Scan(&rows).Error; err != nil {
		return err
	}

	out.SalesByPaymentMethod = make([]DashboardPaymentRow, 0, len(rows))
	for _, r := range rows {
		out.SalesByPaymentMethod = append(out.SalesByPaymentMethod, DashboardPaymentRow{
			Method: r.Bucket,
			Label:  paymentLabel(r.Bucket),
			Total:  r.Total,
			Count:  r.Count,
		})
	}
	return nil
}

func (s *DashboardService) loadTopProducts(branchID uint, from, toExclusive time.Time, topN int, out *DashboardData) error {
	branchClause := ""
	args := []interface{}{from, toExclusive}
	if branchID > 0 {
		branchClause = " AND s.branch_id = ?"
		args = append(args, branchID)
	}

	var totalAmountAll float64
	totalSQL := `
		SELECT COALESCE(SUM(si.total), 0)
		FROM tenant_sale_items si
		INNER JOIN tenant_sales s ON s.id = si.sale_id
		WHERE s.issue_date >= ? AND s.issue_date < ?
			AND s.status != 'cancelled'
			AND ` + salescope.CommercialWhere("s") + branchClause
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
			COALESCE(SUM(si.total), 0) AS total_amount
		FROM tenant_sale_items si
		INNER JOIN tenant_sales s ON s.id = si.sale_id
		LEFT JOIN tenant_products p ON p.id = si.product_id
		WHERE s.issue_date >= ? AND s.issue_date < ?
			AND s.status != 'cancelled'
			AND ` + salescope.CommercialWhere("s") + branchClause + `
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

func (s *DashboardService) loadTopCategories(branchID uint, from, toExclusive time.Time, out *DashboardData) error {
	branchClause := ""
	args := []interface{}{from, toExclusive}
	if branchID > 0 {
		branchClause = " AND s.branch_id = ?"
		args = append(args, branchID)
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
			COALESCE(SUM(si.total), 0) AS total_amount
		FROM tenant_sale_items si
		INNER JOIN tenant_sales s ON s.id = si.sale_id
		LEFT JOIN tenant_products p ON p.id = si.product_id
		LEFT JOIN tenant_categories pc ON pc.id = p.category_id
		WHERE s.issue_date >= ? AND s.issue_date < ?
			AND s.status != 'cancelled'
			AND ` + salescope.CommercialWhere("s") + branchClause + `
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

func (s *DashboardService) loadSalesLast30Days(branchID uint, out *DashboardData) error {
	now := saas.NowLima()
	todayStart := saas.CalendarDateLima(now)
	from30 := todayStart.AddDate(0, 0, -29)
	toExclusive := todayStart.AddDate(0, 0, 1)

	type dayRow struct {
		Day   string  `gorm:"column:day"`
		Total float64 `gorm:"column:total"`
		Count int64   `gorm:"column:count"`
	}
	q := restaurantSalesScope(s.db, branchID, from30, toExclusive).
		Select("DATE_FORMAT(issue_date, '%Y-%m-%d') AS day, COALESCE(SUM(total), 0) AS total, COUNT(*) AS count").
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

func (s *DashboardService) loadSalesByHour(branchID uint, from, toExclusive time.Time, out *DashboardData) error {
	type hourRow struct {
		Hour   int   `gorm:"column:hour"`
		Orders int64 `gorm:"column:orders"`
	}
	q := restaurantSessionsScope(s.db, branchID, from, toExclusive).
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
