package handler

import (
	"errors"
	"strconv"
	"time"

	"tukifac/pkg/database"
	"tukifac/pkg/salescope"

	"github.com/gofiber/fiber/v3"
	"gorm.io/gorm"
)

// GET /api/dashboard/analytics?date_from=YYYY-MM-DD&date_to=YYYY-MM-DD&branch_id=
// Resume KPIs, series temporales y desgloses para el dashboard analítico del tenant.
func (h *DashboardHandler) AnalyticsAPI(c fiber.Ctx) error {
	tdb := db(c)
	if tdb == nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "sin contexto de empresa"})
	}

	from, toExclusive, err := parseAnalyticsDateRange(c)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "fechas inválidas (use date_from y date_to como YYYY-MM-DD)"})
	}

	var branchID uint
	if v, e := strconv.ParseUint(c.Query("branch_id"), 10, 32); e == nil {
		branchID = uint(v)
	}

	userID, _ := c.Locals("user_id").(uint)
	userRole, _ := c.Locals("user_role").(string)
	restrictUser := userRole != "Administrador" && userID != 0

	duration := toExclusive.Sub(from)
	prevToExclusive := from
	prevFrom := from.Add(-duration)

	// --- Resumen período actual (ventas no anuladas)
	var salesTotal float64
	var salesCount int64
	analyticsSaleScope(tdb, from, toExclusive, branchID, userID, restrictUser).Select("COALESCE(SUM(total), 0)").Scan(&salesTotal)
	analyticsSaleScope(tdb, from, toExclusive, branchID, userID, restrictUser).Count(&salesCount)

	avgTicket := float64(0)
	if salesCount > 0 {
		avgTicket = salesTotal / float64(salesCount)
	}

	var prevSalesTotal float64
	qPrev := analyticsSaleScope(tdb, prevFrom, prevToExclusive, branchID, userID, restrictUser)
	qPrev.Select("COALESCE(SUM(total), 0)").Scan(&prevSalesTotal)

	changePct := float64(0)
	if prevSalesTotal > 0 {
		changePct = (salesTotal - prevSalesTotal) / prevSalesTotal * 100
	}

	// Ventas del día (hoy) y del mes calendario actual (para KPIs fijos en tarjetas)
	now := time.Now()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	todayEnd := todayStart.AddDate(0, 0, 1)
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.Local)
	monthEnd := monthStart.AddDate(0, 1, 0)

	var salesToday float64
	var salesMonthCalendar float64
	var salesTodayCount int64
	analyticsSaleScope(tdb, todayStart, todayEnd, branchID, userID, restrictUser).Select("COALESCE(SUM(total), 0)").Scan(&salesToday)
	analyticsSaleScope(tdb, todayStart, todayEnd, branchID, userID, restrictUser).Count(&salesTodayCount)

	analyticsSaleScope(tdb, monthStart, monthEnd, branchID, userID, restrictUser).Select("COALESCE(SUM(total), 0)").Scan(&salesMonthCalendar)

	// Clientes nuevos (clientes tipo customer/both creados en el rango)
	var newContacts int64
	tdb.Model(&database.TenantContact{}).
		Where("created_at >= ? AND created_at < ?", from, toExclusive).
		Where("active = ?", true).
		Where("type IN ?", []string{"customer", "both"}).
		Count(&newContacts)

	// Documentos electrónicos SUNAT (solo factura/boleta en el rango)
	eligibleSunat := "doc_type IN ('FACTURA','BOLETA')"
	var pendingSunat, sentSunat, acceptedSunat, rejectedSunat, errorSunat int64
	countBilling := func(status string, dest *int64) {
		q := tdb.Model(&database.TenantSale{}).
			Where("issue_date >= ? AND issue_date < ?", from, toExclusive).
			Where("status != ?", "cancelled").
			Where(eligibleSunat).
			Where("billing_status = ?", status)
		q = branchScope(branchID)(q)
		q = userScope(restrictUser, userID)(q)
		q.Count(dest)
	}
	countBilling("pending", &pendingSunat)
	countBilling("sent", &sentSunat)
	countBilling("accepted", &acceptedSunat)
	countBilling("rejected", &rejectedSunat)
	countBilling("error", &errorSunat)

	// Ventas anuladas en el período
	var cancelledCount int64
	salescope.CommercialSales(tdb.Model(&database.TenantSale{})).
		Where("issue_date >= ? AND issue_date < ?", from, toExclusive).
		Where("status = ?", "cancelled").
		Scopes(func(db *gorm.DB) *gorm.DB {
			if branchID > 0 {
				return db.Where("branch_id = ?", branchID)
			}
			return db
		}).
		Scopes(func(db *gorm.DB) *gorm.DB {
			if restrictUser && userID != 0 {
				return db.Where("user_id = ?", userID)
			}
			return db
		}).
		Count(&cancelledCount)

	// Serie diaria: ventas + cantidad documentos (no anulados)
	type dayRow struct {
		Day   string  `json:"day"`
		Sales float64 `json:"sales"`
		Docs  int64   `json:"documents"`
	}
	var daily []dayRow
	salescope.CommercialSales(tdb.Model(&database.TenantSale{})).
		Select("DATE(issue_date) as day, COALESCE(SUM(total),0) as sales, COUNT(*) as docs").
		Where("issue_date >= ? AND issue_date < ?", from, toExclusive).
		Where("status != ?", "cancelled").
		Scopes(branchScope(branchID)).
		Scopes(userScope(restrictUser, userID)).
		Group("DATE(issue_date)").
		Order("day").
		Scan(&daily)

	// Comparación mes vs mes anterior calendario (ventas totales)
	prevMonthStart := monthStart.AddDate(0, -1, 0)
	prevMonthEnd := monthStart
	var salesPrevMonth float64
	qPM := analyticsSaleScope(tdb, prevMonthStart, prevMonthEnd, branchID, userID, restrictUser)
	qPM.Select("COALESCE(SUM(total), 0)").Scan(&salesPrevMonth)
	monthOverMonthPct := float64(0)
	if salesPrevMonth > 0 {
		monthOverMonthPct = (salesMonthCalendar - salesPrevMonth) / salesPrevMonth * 100
	}

	// Por sucursal
	type namedTotal struct {
		ID    uint    `json:"id"`
		Name  string  `json:"name"`
		Total float64 `json:"total"`
	}
	var byBranch []namedTotal
	salescope.CommercialSales(tdb.Model(&database.TenantSale{})).
		Select("tenant_branches.id, tenant_branches.name, COALESCE(SUM(tenant_sales.total),0) as total").
		Joins("JOIN tenant_branches ON tenant_branches.id = tenant_sales.branch_id").
		Where("tenant_sales.issue_date >= ? AND tenant_sales.issue_date < ?", from, toExclusive).
		Where("tenant_sales.status != ?", "cancelled").
		Scopes(userScope(restrictUser, userID)).
		Group("tenant_branches.id, tenant_branches.name").
		Order("total DESC").
		Scan(&byBranch)

	// Por vendedor
	var bySeller []namedTotal
	salescope.CommercialSales(tdb.Model(&database.TenantSale{})).
		Select("tenant_users.id, tenant_users.name, COALESCE(SUM(tenant_sales.total),0) as total").
		Joins("JOIN tenant_users ON tenant_users.id = tenant_sales.user_id").
		Where("tenant_sales.issue_date >= ? AND tenant_sales.issue_date < ?", from, toExclusive).
		Where("tenant_sales.status != ?", "cancelled").
		Scopes(branchScope(branchID)).
		Group("tenant_users.id, tenant_users.name").
		Order("total DESC").
		Limit(12).
		Scan(&bySeller)

	// Top clientes
	type contactTotal struct {
		ID         uint    `json:"id"`
		Name       string  `json:"name"`
		Total      float64 `json:"total"`
		SalesCount int64   `json:"sales_count"`
	}
	var topContacts []contactTotal
	salescope.CommercialSales(tdb.Model(&database.TenantSale{})).
		Select("tenant_contacts.id, COALESCE(NULLIF(TRIM(tenant_contacts.trade_name),''), tenant_contacts.business_name) as name, COALESCE(SUM(tenant_sales.total),0) as total, COUNT(tenant_sales.id) as sales_count").
		Joins("JOIN tenant_contacts ON tenant_contacts.id = tenant_sales.contact_id").
		Where("tenant_sales.issue_date >= ? AND tenant_sales.issue_date < ?", from, toExclusive).
		Where("tenant_sales.status != ?", "cancelled").
		Where("tenant_sales.contact_id IS NOT NULL").
		Scopes(branchScope(branchID)).
		Scopes(userScope(restrictUser, userID)).
		Group("tenant_contacts.id, tenant_contacts.trade_name, tenant_contacts.business_name").
		Order("total DESC").
		Limit(10).
		Scan(&topContacts)

	// Por tipo de comprobante
	type kvFloat struct {
		Key   string  `json:"key"`
		Total float64 `json:"total"`
		Count int64   `json:"count"`
	}
	var byDocType []kvFloat
	salescope.CommercialSales(tdb.Model(&database.TenantSale{})).
		Select("COALESCE(fe.doc_type, tenant_sales.doc_type) as `key`, COALESCE(SUM(tenant_sales.total),0) as total, COUNT(*) as count").
		Joins("LEFT JOIN tenant_sales fe ON fe.issued_from_nota_sale_id = tenant_sales.id AND fe.deleted_at IS NULL").
		Where("tenant_sales.issue_date >= ? AND tenant_sales.issue_date < ?", from, toExclusive).
		Where("tenant_sales.status != ?", "cancelled").
		Scopes(branchScope(branchID)).
		Scopes(userScope(restrictUser, userID)).
		Group("COALESCE(fe.doc_type, tenant_sales.doc_type)").
		Scan(&byDocType)

	// Por método de pago (campo en venta)
	var byPayment []kvFloat
	salescope.CommercialSales(tdb.Model(&database.TenantSale{})).
		Select("COALESCE(NULLIF(TRIM(payment_method),''),'sin_definir') as `key`, COALESCE(SUM(total),0) as total, COUNT(*) as count").
		Where("issue_date >= ? AND issue_date < ?", from, toExclusive).
		Where("status != ?", "cancelled").
		Scopes(branchScope(branchID)).
		Scopes(userScope(restrictUser, userID)).
		Group("COALESCE(NULLIF(TRIM(payment_method),''),'sin_definir')").
		Scan(&byPayment)

	// Estado operativo de venta (paid, draft, credit — cancelled excluido arriba en métricas principales)
	type kvInt struct {
		Key   string `json:"key"`
		Count int64  `json:"count"`
	}
	var bySaleStatus []kvInt
	salescope.CommercialSales(tdb.Model(&database.TenantSale{})).
		Select("status as `key`, COUNT(*) as count").
		Where("issue_date >= ? AND issue_date < ?", from, toExclusive).
		Where("status != ?", "cancelled").
		Scopes(branchScope(branchID)).
		Scopes(userScope(restrictUser, userID)).
		Group("status").
		Scan(&bySaleStatus)

	// Por categoría de producto (líneas de venta)
	type catRow struct {
		Name  string  `json:"name"`
		Total float64 `json:"total"`
	}
	var byCategory []catRow
	tdb.Table("tenant_sale_items si").
		Select("COALESCE(tc.name, 'Sin categoría') as name, COALESCE(SUM(si.total),0) as total").
		Joins("JOIN tenant_sales s ON s.id = si.sale_id").
		Scopes(salescope.ScopeCommercial("s")).
		Joins("LEFT JOIN tenant_products p ON p.id = si.product_id").
		Joins("LEFT JOIN tenant_categories tc ON tc.id = p.category_id").
		Where("s.issue_date >= ? AND s.issue_date < ?", from, toExclusive).
		Where("s.status != ?", "cancelled").
		Where("si.product_id IS NOT NULL").
		Scopes(func(db *gorm.DB) *gorm.DB {
			if branchID > 0 {
				return db.Where("s.branch_id = ?", branchID)
			}
			return db
		}).
		Scopes(func(db *gorm.DB) *gorm.DB {
			if restrictUser && userID != 0 {
				return db.Where("s.user_id = ?", userID)
			}
			return db
		}).
		Group("COALESCE(tc.id, 0), COALESCE(tc.name, 'Sin categoría')").
		Order("total DESC").
		Limit(12).
		Scan(&byCategory)

	// Top productos
	type prodRow struct {
		ProductID uint    `json:"product_id"`
		Name      string  `json:"name"`
		Qty       float64 `json:"quantity"`
		Total     float64 `json:"total"`
	}
	var topProducts []prodRow
	tdb.Table("tenant_sale_items si").
		Select("si.product_id, p.name, COALESCE(SUM(si.quantity),0) as qty, COALESCE(SUM(si.total),0) as total").
		Joins("JOIN tenant_sales s ON s.id = si.sale_id").
		Scopes(salescope.ScopeCommercial("s")).
		Joins("JOIN tenant_products p ON p.id = si.product_id").
		Where("s.issue_date >= ? AND s.issue_date < ?", from, toExclusive).
		Where("s.status != ?", "cancelled").
		Scopes(func(db *gorm.DB) *gorm.DB {
			if branchID > 0 {
				return db.Where("s.branch_id = ?", branchID)
			}
			return db
		}).
		Scopes(func(db *gorm.DB) *gorm.DB {
			if restrictUser && userID != 0 {
				return db.Where("s.user_id = ?", userID)
			}
			return db
		}).
		Group("si.product_id, p.name").
		Order("total DESC").
		Limit(10).
		Scan(&topProducts)

	// Stock bajo (actual global, no depende del rango de fechas del dashboard)
	lowStock := make([]struct {
		ProductID   uint    `json:"product_id"`
		ProductName string  `json:"product_name"`
		Quantity    float64 `json:"quantity"`
		MinStock    float64 `json:"min_stock"`
	}, 0)
	tdb.Table("tenant_product_stocks ps").
		Select("ps.product_id, p.name as product_name, ps.quantity, p.min_stock").
		Joins("JOIN tenant_products p ON p.id = ps.product_id").
		Where("p.manage_stock = ? AND p.active = ? AND ps.quantity <= p.min_stock", true, true).
		Limit(8).
		Scan(&lowStock)

	// Productos con vencimiento próximo (30 días) o ya vencidos
	expiringProducts := make([]struct {
		ProductID       uint      `json:"product_id"`
		ProductName     string    `json:"product_name"`
		ExpiryDate      time.Time `json:"expiry_date"`
		DaysUntilExpiry int       `json:"days_until_expiry"`
	}, 0)
	horizon := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local).AddDate(0, 0, 30)
	tdb.Table("tenant_products p").
		Select(`p.id as product_id, p.name as product_name, p.expiry_date,
			DATEDIFF(p.expiry_date, CURDATE()) as days_until_expiry`).
		Where("p.active = ? AND p.has_expiry_date = ? AND p.expiry_date IS NOT NULL AND p.expiry_date <= ?", true, true, horizon).
		Order("p.expiry_date ASC").
		Limit(8).
		Scan(&expiringProducts)

	// Últimos comprobantes
	type recentDoc struct {
		ID             uint      `json:"id"`
		DocType        string    `json:"doc_type"`
		Number         string    `json:"number"`
		IssueDate      time.Time `json:"issue_date"`
		Total          float64   `json:"total"`
		Status         string    `json:"status"`
		BillingStatus  string    `json:"billing_status"`
		BranchName     string    `json:"branch_name"`
		ContactDisplay string    `json:"contact_name" gorm:"column:contact_display"`
	}
	var recentSales []recentDoc
	salescope.CommercialSales(tdb.Model(&database.TenantSale{})).
		Select(`tenant_sales.id,
			COALESCE(fe.doc_type, tenant_sales.doc_type) as doc_type,
			COALESCE(fe.number, tenant_sales.number) as number,
			tenant_sales.issue_date, tenant_sales.total,
			tenant_sales.status,
			COALESCE(fe.billing_status, tenant_sales.billing_status) as billing_status,
			tenant_branches.name as branch_name,
			COALESCE(NULLIF(TRIM(tenant_contacts.trade_name),''), tenant_contacts.business_name,'—') as contact_display`).
		Joins("LEFT JOIN tenant_sales fe ON fe.issued_from_nota_sale_id = tenant_sales.id AND fe.deleted_at IS NULL").
		Joins("LEFT JOIN tenant_branches ON tenant_branches.id = tenant_sales.branch_id").
		Joins("LEFT JOIN tenant_contacts ON tenant_contacts.id = tenant_sales.contact_id").
		Where("tenant_sales.issue_date >= ? AND tenant_sales.issue_date < ?", from, toExclusive).
		Scopes(branchScope(branchID)).
		Scopes(userScope(restrictUser, userID)).
		Order("tenant_sales.issue_date DESC, tenant_sales.id DESC").
		Limit(12).
		Scan(&recentSales)

	// Ingresos / egresos caja en el período
	var cashIncome, cashExpense float64
	cashBase := func() *gorm.DB {
		q := tdb.Table("tenant_cash_movements m").
			Joins("JOIN tenant_cash_sessions cs ON cs.id = m.cash_session_id").
			Where("m.created_at >= ? AND m.created_at < ?", from, toExclusive)
		if branchID > 0 {
			q = q.Where("cs.branch_id = ?", branchID)
		}
		return q
	}
	cashBase().Select("COALESCE(SUM(CASE WHEN m.type = 'income' THEN m.amount ELSE 0 END),0)").Scan(&cashIncome)
	cashBase().Select("COALESCE(SUM(CASE WHEN m.type = 'expense' THEN m.amount ELSE 0 END),0)").Scan(&cashExpense)

	var openCashSessions int64
	tdb.Model(&database.TenantCashSession{}).Where("status = ?", "open").Count(&openCashSessions)

	type detPeriodAgg struct {
		SumDetraccion   float64 `gorm:"column:sum_detraccion"`
		SumNetPayable   float64 `gorm:"column:sum_net_payable"`
		CountDetraccion int64   `gorm:"column:count_detraccion"`
	}
	var detPeriod detPeriodAgg
	tdb.Table("tenant_sale_detraccion d").
		Select(`
			COALESCE(SUM(d.detraction_amount_pen), 0) AS sum_detraccion,
			COALESCE(SUM(d.net_payable_pen), 0) AS sum_net_payable,
			COUNT(*) AS count_detraccion
		`).
		Joins("JOIN tenant_sales s ON s.id = d.sale_id").
		Scopes(salescope.ScopeCommercial("s")).
		Where("s.issue_date >= ? AND s.issue_date < ?", from, toExclusive).
		Where("s.status != ?", "cancelled").
		Scopes(func(db *gorm.DB) *gorm.DB {
			if branchID > 0 {
				return db.Where("s.branch_id = ?", branchID)
			}
			return db
		}).
		Scopes(func(db *gorm.DB) *gorm.DB {
			if restrictUser && userID != 0 {
				return db.Where("s.user_id = ?", userID)
			}
			return db
		}).
		Scan(&detPeriod)

	return c.JSON(fiber.Map{
		"period": fiber.Map{
			"date_from":       from.Format("2006-01-02"),
			"date_to":         toExclusive.Add(-time.Nanosecond).Format("2006-01-02"),
			"previous_from":   prevFrom.Format("2006-01-02"),
			"previous_to":     prevToExclusive.Add(-time.Nanosecond).Format("2006-01-02"),
			"duration_days":   int(duration.Hours() / 24),
			"sales_change_pct": changePct,
		},
		"summary": fiber.Map{
			"sales_total":           salesTotal,
			"sales_count":           salesCount,
			"avg_ticket":            avgTicket,
			"sales_previous_total":  prevSalesTotal,
			"sales_today":           salesToday,
			"sales_today_count":     salesTodayCount,
			"sales_month_calendar":  salesMonthCalendar,
			"sales_previous_month":  salesPrevMonth,
			"month_over_month_pct":  monthOverMonthPct,
			"new_contacts":          newContacts,
			"cancelled_sales":       cancelledCount,
			"pending_sunat":         pendingSunat,
			"sent_sunat":            sentSunat,
			"accepted_sunat":        acceptedSunat,
			"rejected_sunat":        rejectedSunat,
			"error_sunat":           errorSunat,
			"cash_income":           cashIncome,
			"cash_expense":          cashExpense,
			"cash_net":              cashIncome - cashExpense,
			"open_cash_sessions":    openCashSessions,
			"sum_detraccion":        detPeriod.SumDetraccion,
			"sum_net_payable":       detPeriod.SumNetPayable,
			"count_detraccion":      detPeriod.CountDetraccion,
		},
		"timeseries_daily": daily,
		"sales_by_branch":    byBranch,
		"sales_by_seller":    bySeller,
		"top_clients":        topContacts,
		"top_products":       topProducts,
		"by_doc_type":        byDocType,
		"by_payment_method":  byPayment,
		"by_sale_status":     bySaleStatus,
		"by_product_category": byCategory,
		"low_stock_products": lowStock,
		"expiring_products":  expiringProducts,
		"recent_sales":       recentSales,
	})
}

func parseAnalyticsDateRange(c fiber.Ctx) (from, toExclusive time.Time, err error) {
	df := c.Query("date_from")
	dt := c.Query("date_to")
	now := time.Now()
	if df == "" || dt == "" {
		from = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.Local)
		toExclusive = time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local).AddDate(0, 0, 1)
		return from, toExclusive, nil
	}
	f, e1 := time.ParseInLocation("2006-01-02", df, time.Local)
	t, e2 := time.ParseInLocation("2006-01-02", dt, time.Local)
	if e1 != nil || e2 != nil {
		return time.Time{}, time.Time{}, e1
	}
	from = time.Date(f.Year(), f.Month(), f.Day(), 0, 0, 0, 0, time.Local)
	toDay := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.Local)
	toExclusive = toDay.AddDate(0, 0, 1)
	if !toExclusive.After(from) {
		return time.Time{}, time.Time{}, errors.New("date_to debe ser >= date_from")
	}
	return from, toExclusive, nil
}

func analyticsSaleScope(tdb *gorm.DB, from, toExclusive time.Time, branchID uint, userID uint, restrictUser bool) *gorm.DB {
	q := salescope.CommercialSales(tdb.Model(&database.TenantSale{})).
		Where("issue_date >= ? AND issue_date < ?", from, toExclusive).
		Where("status != ?", "cancelled")
	q = branchScope(branchID)(q)
	q = userScope(restrictUser, userID)(q)
	return q
}

func branchScope(branchID uint) func(*gorm.DB) *gorm.DB {
	return func(db *gorm.DB) *gorm.DB {
		if branchID > 0 {
			return db.Where("branch_id = ?", branchID)
		}
		return db
	}
}

func userScope(restrict bool, userID uint) func(*gorm.DB) *gorm.DB {
	return func(db *gorm.DB) *gorm.DB {
		if restrict && userID != 0 {
			return db.Where("user_id = ?", userID)
		}
		return db
	}
}
