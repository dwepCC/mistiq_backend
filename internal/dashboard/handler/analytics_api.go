package handler

import (
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"tukifac/pkg/branch"
	"tukifac/pkg/productbranch"
	"tukifac/pkg/database"
	"tukifac/pkg/money"
	"tukifac/pkg/paymentcondition"
	"tukifac/pkg/salescope"

	"github.com/gofiber/fiber/v3"
	"gorm.io/gorm"
)

// dashboardTopProductRow es una fila del ranking "Productos más vendidos" del dashboard — Fase
// 7J.2, mismo criterio que SalesByProductRow (7J.1, internal/sales/service/sale_service.go): cada
// fila es una combinación INEQUÍVOCA producto+SaleUnit, nunca cantidades comerciales mezcladas de
// distintas unidades. SaleUnitID nil = venta en unidad base/legacy — combinación propia, nunca se
// funde con una SaleUnit real del mismo producto. Qty es la cantidad COMERCIAL de esa combinación
// (nunca convertida a base); Total (dinero) sí puede compararse/ordenarse entre combinaciones
// porque es una magnitud común. El "top N" se aplica DESPUÉS del desglose, sobre combinaciones,
// no sobre productos — un mismo producto puede ocupar más de una posición del ranking si se
// vendió con más de una SaleUnit. Ver FASE_7J_REPORTS_AUDIT_REPORT.md.
type dashboardTopProductRow struct {
	ProductID  uint    `json:"product_id"`
	Name       string  `json:"name"`
	SaleUnitID *uint   `json:"sale_unit_id,omitempty"`
	Qty        float64 `json:"quantity"`
	Total      float64 `json:"total"`
}

// computeTopProducts calcula el ranking "Productos más vendidos" — extraído a función propia
// (antes vivía inline en AnalyticsAPI) para poder probarlo sin montar todo el handler.
func computeTopProducts(tdb *gorm.DB, from, toExclusive time.Time, branchID uint, userID uint, restrictUser bool, limit int) ([]dashboardTopProductRow, error) {
	var rows []dashboardTopProductRow
	err := tdb.Table("tenant_sale_items si").
		Select(`si.product_id, p.name, si.sale_unit_id as sale_unit_id,
			COALESCE(SUM(si.quantity),0) as qty, COALESCE(SUM(` + saleLineTotalPEN + `),0) as total`).
		Joins("JOIN tenant_sales s ON s.id = si.sale_id").
		Scopes(salescope.ScopeEmittedNoNotes("s")).
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
		Group("si.product_id, si.sale_unit_id, p.name").
		Order("total DESC").
		Limit(limit).
		Scan(&rows).Error
	return rows, err
}

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

	// Misma regla de sucursal que los reportes de ventas (branch.ResolveReportBranchFilter): quien
	// puede cambiar de sucursal ve todas si no elige una; el resto queda en la suya. Antes el
	// dashboard sumaba TODAS las sucursales para cualquiera y los reportes solo la activa, y los
	// totales no coincidían.
	var requestedBranch uint
	if v, e := strconv.ParseUint(c.Query("branch_id"), 10, 32); e == nil {
		requestedBranch = uint(v)
	}
	branchID := branch.ResolveReportBranchFilter(c, requestedBranch, true)

	// Usuario: el administrador ve a todos o elige uno (?user_id=); cualquier otro rol ve SOLO lo
	// suyo, siempre (ver resolveDashboardUser). Es el mismo criterio que usa Inicio.
	us := resolveDashboardUser(c)
	userID, restrictUser := us.UserID, us.Restrict

	now := time.Now()
	duration := toExclusive.Sub(from)
	// Comparación justa: un mes completo se compara con los mismos días del mes anterior.
	cmp := comparisonWindow(from, toExclusive, now)
	prevFrom, prevToExclusive := cmp.From, cmp.ToExclusive

	// --- Resumen período actual (ventas no anuladas)
	var salesTotal float64
	var salesCount int64
	analyticsSaleScope(tdb, from, toExclusive, branchID, userID, restrictUser).Select(sumSalesPENSelect()).Scan(&salesTotal)
	analyticsSaleScope(tdb, from, toExclusive, branchID, userID, restrictUser).Count(&salesCount)

	avgTicket := float64(0)
	if salesCount > 0 {
		avgTicket = salesTotal / float64(salesCount)
	}

	var prevSalesTotal float64
	qPrev := analyticsSaleScope(tdb, prevFrom, prevToExclusive, branchID, userID, restrictUser)
	qPrev.Select(sumSalesPENSelect()).Scan(&prevSalesTotal)

	changePct := float64(0)
	if prevSalesTotal > 0 {
		changePct = (salesTotal - prevSalesTotal) / prevSalesTotal * 100
	}

	// Ventas del día (hoy) y del mes calendario actual (para KPIs fijos en tarjetas)
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	todayEnd := todayStart.AddDate(0, 0, 1)
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.Local)
	monthEnd := monthStart.AddDate(0, 1, 0)

	var salesToday float64
	var salesMonthCalendar float64
	var salesTodayCount int64
	analyticsSaleScope(tdb, todayStart, todayEnd, branchID, userID, restrictUser).Select(sumSalesPENSelect()).Scan(&salesToday)
	analyticsSaleScope(tdb, todayStart, todayEnd, branchID, userID, restrictUser).Count(&salesTodayCount)

	analyticsSaleScope(tdb, monthStart, monthEnd, branchID, userID, restrictUser).Select(sumSalesPENSelect()).Scan(&salesMonthCalendar)

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

	// Lo que está pendiente HOY, sin límite de fecha y con la misma definición que la campanita del
	// encabezado: los contadores de arriba son solo del período y daban otra cifra sin explicarlo.
	pendingSunatAll := countElectronicBilling(tdb, "pending", branchID, userID, restrictUser)
	errorSunatAll := countElectronicBilling(tdb, "error", branchID, userID, restrictUser)
	rejectedSunatAll := countElectronicBilling(tdb, "rejected", branchID, userID, restrictUser)

	// Ventas con total <= 0 (datos inconsistentes: distorsionan totales y ticket promedio).
	var invalidTotalSales int64
	analyticsSaleScope(tdb, from, toExclusive, branchID, userID, restrictUser).Where("tenant_sales.total <= 0").Count(&invalidTotalSales)

	// Ventas anuladas en el período
	var cancelledCount int64
	salescope.EmittedSalesNoNotes(tdb.Model(&database.TenantSale{})).
		Where("issue_date >= ? AND issue_date < ?", from, toExclusive).
		Where("status = ?", "cancelled").
		Scopes(branchScope(branchID)).
		Scopes(userScope(restrictUser, userID)).
		Count(&cancelledCount)

	// Serie diaria: ventas + cantidad documentos (no anulados)
	var daily []dailyRow
	salescope.EmittedSalesNoNotes(tdb.Model(&database.TenantSale{})).
		Select("DATE(issue_date) as day, " + sumSalesPENSelect() + " as sales, COUNT(*) as docs").
		Where("issue_date >= ? AND issue_date < ?", from, toExclusive).
		Where("status != ?", "cancelled").
		Scopes(branchScope(branchID)).
		Scopes(userScope(restrictUser, userID)).
		Group("DATE(issue_date)").
		Order("day").
		Scan(&daily)
	// Días sin ventas con 0 (y fecha "YYYY-MM-DD"): el gráfico no debe saltar días ni curvarse.
	daily = fillDailySeries(daily, from, toExclusive, now)

	// Comparación mes vs mes anterior calendario (ventas totales)
	monthCmp := comparisonWindow(monthStart, monthEnd, now)
	prevMonthStart, prevMonthEnd := monthCmp.From, monthCmp.ToExclusive
	var salesPrevMonth float64
	qPM := analyticsSaleScope(tdb, prevMonthStart, prevMonthEnd, branchID, userID, restrictUser)
	qPM.Select(sumSalesPENSelect()).Scan(&salesPrevMonth)
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
	salescope.EmittedSalesNoNotes(tdb.Model(&database.TenantSale{})).
		Select("tenant_branches.id, tenant_branches.name, " + sumSalesPENSelect() + " as total").
		Joins("JOIN tenant_branches ON tenant_branches.id = tenant_sales.branch_id").
		Where("tenant_sales.issue_date >= ? AND tenant_sales.issue_date < ?", from, toExclusive).
		Where("tenant_sales.status != ?", "cancelled").
		Scopes(branchScope(branchID)).
		Scopes(userScope(restrictUser, userID)).
		Group("tenant_branches.id, tenant_branches.name").
		Order("total DESC").
		Scan(&byBranch)

	// Por vendedor
	var bySeller []namedTotal
	salescope.EmittedSalesNoNotes(tdb.Model(&database.TenantSale{})).
		Select("tenant_users.id, tenant_users.name, " + sumSalesPENSelect() + " as total").
		Joins("JOIN tenant_users ON tenant_users.id = tenant_sales.user_id").
		Where("tenant_sales.issue_date >= ? AND tenant_sales.issue_date < ?", from, toExclusive).
		Where("tenant_sales.status != ?", "cancelled").
		Scopes(branchScope(branchID)).
		Scopes(userScope(restrictUser, userID)).
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
	salescope.EmittedSalesNoNotes(tdb.Model(&database.TenantSale{})).
		Select("tenant_contacts.id, COALESCE(NULLIF(TRIM(tenant_contacts.trade_name),''), tenant_contacts.business_name) as name, " + sumSalesPENSelect() + " as total, COUNT(tenant_sales.id) as sales_count").
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
	salescope.EmittedSalesNoNotes(tdb.Model(&database.TenantSale{})).
		Select("tenant_sales.doc_type as `key`, " + sumSalesPENSelect() + " as total, COUNT(*) as count").
		Where("tenant_sales.issue_date >= ? AND tenant_sales.issue_date < ?", from, toExclusive).
		Where("tenant_sales.status != ?", "cancelled").
		Scopes(branchScope(branchID)).
		Scopes(userScope(restrictUser, userID)).
		Group("tenant_sales.doc_type").
		Scan(&byDocType)

	// Por método de pago: según las líneas de pago reales (una venta dividida reparte su total
	// entre los métodos usados), igual que el dashboard de restaurante y el listado de ventas.
	byPayment, err := computeSalesByPaymentMethod(tdb, from, toExclusive, branchID, userID, restrictUser)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "no se pudo calcular ventas por método de pago"})
	}

	// Estado operativo de venta (paid, draft, credit — cancelled excluido arriba en métricas principales)
	type kvInt struct {
		Key   string `json:"key"`
		Count int64  `json:"count"`
	}
	var bySaleStatus []kvInt
	salescope.EmittedSalesNoNotes(tdb.Model(&database.TenantSale{})).
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
		// Adjustment: fila que concilia el gráfico con el total de ventas (otras categorías,
		// descuentos globales, líneas sin producto).
		Adjustment bool `json:"adjustment,omitempty"`
	}
	var byCategory []catRow
	tdb.Table("tenant_sale_items si").
		Select("COALESCE(tc.name, 'Sin categoría') as name, COALESCE(SUM(" + saleLineTotalPEN + "),0) as total").
		Joins("JOIN tenant_sales s ON s.id = si.sale_id").
		Scopes(salescope.ScopeEmittedNoNotes("s")).
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

	// Conciliación: el gráfico suma líneas con producto (top 12); el resto hasta el total de ventas
	// se muestra como filas de ajuste en vez de dejar una diferencia sin explicar.
	var linesAll float64
	tdb.Table("tenant_sale_items si").
		Select("COALESCE(SUM(" + saleLineTotalPEN + "),0)").
		Joins("JOIN tenant_sales s ON s.id = si.sale_id").
		Scopes(salescope.ScopeEmittedNoNotes("s")).
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
		Scan(&linesAll)
	var catTop float64
	for _, r := range byCategory {
		catTop += r.Total
	}
	if other := linesAll - catTop; other > 0.009 {
		byCategory = append(byCategory, catRow{Name: "Otras categorías", Total: money.RoundDisplay(other), Adjustment: true})
	}
	if adj := salesTotal - linesAll; adj > 0.009 || adj < -0.009 {
		byCategory = append(byCategory, catRow{Name: "Descuentos, líneas sin producto y ajustes", Total: money.RoundDisplay(adj), Adjustment: true})
	}

	// Top productos — Fase 7J.2: computeTopProducts agrupa por producto+SaleUnit (ver su doc).
	topProducts, _ := computeTopProducts(tdb, from, toExclusive, branchID, userID, restrictUser, 10)

	// Stock bajo (actual global, no depende del rango de fechas del dashboard)
	lowStock := queryLowStock(tdb, branchID, 8)

	// Productos con vencimiento próximo (30 días) o ya vencidos
	expiringProducts := make([]struct {
		ProductID       uint      `json:"product_id"`
		ProductName     string    `json:"product_name"`
		ExpiryDate      time.Time `json:"expiry_date"`
		DaysUntilExpiry int       `json:"days_until_expiry"`
	}, 0)
	horizon := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local).AddDate(0, 0, 30)
	expQ := tdb.Table("tenant_products p").
		Select(`p.id as product_id, p.name as product_name, p.expiry_date,
			DATEDIFF(p.expiry_date, CURDATE()) as days_until_expiry`).
		Where("p.active = ? AND p.has_expiry_date = ? AND p.expiry_date IS NOT NULL AND p.expiry_date <= ?", true, true, horizon)
	// Productos compartidos con visibilidad por sucursal: con una sucursal elegida, solo los vendibles allí.
	if branchID > 0 && productbranch.Ready(tdb) {
		expQ = expQ.Where(productbranch.ListFilterSQL("p.id", "(p.branch_id IS NULL OR p.branch_id = 0 OR p.branch_id = ?)"), branchID, branchID)
	}
	expQ.
		Order("p.expiry_date ASC").
		Limit(8).
		Scan(&expiringProducts)

	// Últimos comprobantes (sin notas de crédito/débito: son el reverso de un comprobante, no una venta)
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
	salescope.EmittedSalesNoNotes(tdb.Model(&database.TenantSale{})).
		Select(`tenant_sales.id,
			tenant_sales.doc_type as doc_type,
			tenant_sales.number as number,
			tenant_sales.issue_date, tenant_sales.total,
			tenant_sales.status,
			CASE WHEN tenant_sales.doc_type IN ('FACTURA','BOLETA')
					THEN tenant_sales.billing_status ELSE '' END as billing_status,
			tenant_branches.name as branch_name,
			COALESCE(NULLIF(TRIM(tenant_contacts.trade_name),''), tenant_contacts.business_name,'—') as contact_display`).
		Joins("LEFT JOIN tenant_branches ON tenant_branches.id = tenant_sales.branch_id").
		Joins("LEFT JOIN tenant_contacts ON tenant_contacts.id = tenant_sales.contact_id").
		Where("tenant_sales.issue_date >= ? AND tenant_sales.issue_date < ?", from, toExclusive).
		// Una venta anulada no es un comprobante vigente: la tabla no muestra el estado, así que
		// aparecería con su total como si fuera una venta normal.
		Where("tenant_sales.status != ?", "cancelled").
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
			Where("m.created_at >= ? AND m.created_at < ?", from, toExclusive).
			Where("COALESCE(m.category, '') NOT IN ('Transferencia', 'Anulación transferencia')")
		if branchID > 0 {
			q = q.Where("cs.branch_id = ?", branchID)
		}
		if restrictUser && userID != 0 {
			q = q.Where("cs.user_id = ?", userID)
		}
		return q
	}
	cashBase().Select("COALESCE(SUM(CASE WHEN m.type = 'income' THEN COALESCE(m.amount_pen, m.amount) ELSE 0 END),0)").Scan(&cashIncome)
	cashBase().Select("COALESCE(SUM(CASE WHEN m.type = 'expense' THEN COALESCE(m.amount_pen, m.amount) ELSE 0 END),0)").Scan(&cashExpense)

	// Sesiones de caja abiertas: con el mismo filtro de sucursal/usuario que el resto del dashboard
	// (antes contaba todas las sucursales y no coincidía con el módulo Caja, que lista la activa).
	var openCashSessions, staleCashSessions int64
	openCashQ := func() *gorm.DB {
		q := tdb.Model(&database.TenantCashSession{}).Where("status = ?", "open")
		if branchID > 0 {
			q = q.Where("branch_id = ?", branchID)
		}
		if restrictUser && userID != 0 {
			q = q.Where("user_id = ?", userID)
		}
		return q
	}
	openCashQ().Count(&openCashSessions)
	// Abiertas hace más de 24 h: casi siempre un cierre olvidado.
	openCashQ().Where("opened_at < ?", now.Add(-24*time.Hour)).Count(&staleCashSessions)

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
		Scopes(salescope.ScopeEmittedNoNotes("s")).
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
			"compare_mode":     cmp.Mode,
			"compare_label":    cmp.Label,
			"compare_from":     prevFrom.Format("2006-01-02"),
			"compare_to":       prevToExclusive.Add(-time.Nanosecond).Format("2006-01-02"),
		},
		// Quién ve qué: el administrador recibe la lista de usuarios para el selector.
		"scope": fiber.Map{
			"is_admin":         us.IsAdmin,
			"restricted":       us.Restrict,
			"user_id":          us.UserID,
			"month_compare":    monthCmp.Label,
			"users":            scopeUsers(us, tdb),
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
			"stale_cash_sessions":   staleCashSessions,
			"pending_sunat_all":     pendingSunatAll,
			"error_sunat_all":       errorSunatAll,
			"rejected_sunat_all":    rejectedSunatAll,
			"invalid_total_sales":   invalidTotalSales,
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

// paymentMethodTotal es una fila de "ventas por método de pago" (key = código del método).
type paymentMethodTotal struct {
	Key   string  `json:"key"`
	Total float64 `json:"total"`
	Count int64   `json:"count"`
}

// computeSalesByPaymentMethod agrupa el dinero de las ventas vigentes del período por MÉTODO DE
// PAGO usando tenant_sale_payments. Antes se agrupaba por tenant_sales.payment_method (un solo
// campo por venta), así que una venta dividida (p. ej. Efectivo + Yape + Plin) caía ENTERA en un
// único método y el gráfico no coincidía con el dashboard de restaurante ni con el listado de
// ventas.
//   - El vuelto se descuenta del efectivo (money.AllocateSalePaymentReportAmounts), igual que el
//     listado de ventas, para que ningún método supere lo que realmente cubrió el total.
//   - El marcador "credito" (venta a crédito sin cobrar) se informa como "credito" y la
//     detracción SPOT con su propio código: ambos forman parte del total de la venta.
//   - Una venta sin ninguna línea de pago (histórico) se atribuye por tenant_sales.payment_method.
//
// Count es la cantidad de ventas distintas que usaron cada método.
func computeSalesByPaymentMethod(tdb *gorm.DB, from, toExclusive time.Time, branchID uint, userID uint, restrictUser bool) ([]paymentMethodTotal, error) {
	var sales []struct {
		ID            uint
		Total         float64
		PaymentMethod string
		Currency      string
		ExchangeRate  *float64
	}
	if err := analyticsSaleScope(tdb, from, toExclusive, branchID, userID, restrictUser).
		Select("id, total, payment_method, currency, exchange_rate").Scan(&sales).Error; err != nil {
		return nil, err
	}

	type payRow struct {
		ID     uint
		SaleID uint
		Method string
		Amount float64
	}
	var pays []payRow
	q := tdb.Table("tenant_sale_payments tsp").
		Select("tsp.id AS id, tsp.sale_id AS sale_id, LOWER(TRIM(tsp.method)) AS method, tsp.amount AS amount").
		Joins("JOIN tenant_sales ON tenant_sales.id = tsp.sale_id").
		Scopes(salescope.ScopeEmittedNoNotes("tenant_sales")).
		Where("tenant_sales.issue_date >= ? AND tenant_sales.issue_date < ?", from, toExclusive).
		Where("tenant_sales.status != ?", "cancelled")
	q = branchScope(branchID)(q)
	q = userScope(restrictUser, userID)(q)
	if err := q.Order("tsp.id ASC").Scan(&pays).Error; err != nil {
		return nil, err
	}

	paysBySale := make(map[uint][]payRow, len(sales))
	for _, p := range pays {
		paysBySale[p.SaleID] = append(paysBySale[p.SaleID], p)
	}

	totals := make(map[string]float64)
	counts := make(map[string]int64)
	for _, sale := range sales {
		// Todo se agrega en SOLES: una venta en USD con TC aporta sus importes × TC (mismo criterio que las tarjetas de ventas).
		factor := 1.0
		if strings.EqualFold(strings.TrimSpace(sale.Currency), "USD") && sale.ExchangeRate != nil && *sale.ExchangeRate > 0 {
			factor = *sale.ExchangeRate
		}
		rows := paysBySale[sale.ID]
		if len(rows) == 0 {
			method := strings.ToLower(strings.TrimSpace(sale.PaymentMethod))
			if method == "" {
				method = "sin_definir"
			}
			totals[method] += sale.Total * factor
			counts[method]++
			continue
		}
		lines := make([]money.SalePaymentLine, 0, len(rows))
		methodByID := make(map[uint]string, len(rows))
		for _, r := range rows {
			methodByID[r.ID] = r.Method
			// El marcador "credito" no es dinero recibido: no entra a la base del vuelto.
			if paymentcondition.IsCreditCode(r.Method) {
				continue
			}
			lines = append(lines, money.SalePaymentLine{ID: r.ID, Amount: r.Amount, IsCash: money.IsCashMethod(r.Method)})
		}
		seen := make(map[string]struct{}, len(rows))
		add := func(method string, amount float64) {
			if method == "" {
				method = "sin_definir"
			}
			totals[method] += amount * factor
			if _, ok := seen[method]; !ok {
				seen[method] = struct{}{}
				counts[method]++
			}
		}
		for id, amt := range money.AllocateSalePaymentReportAmounts(sale.Total, lines) {
			add(methodByID[id], amt)
		}
		for _, r := range rows {
			if paymentcondition.IsCreditCode(r.Method) {
				add(r.Method, r.Amount)
			}
		}
	}

	out := make([]paymentMethodTotal, 0, len(totals))
	for k, v := range totals {
		out = append(out, paymentMethodTotal{Key: k, Total: money.RoundDisplay(v), Count: counts[k]})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Total != out[j].Total {
			return out[i].Total > out[j].Total
		}
		return out[i].Key < out[j].Key
	})
	return out, nil
}

func analyticsSaleScope(tdb *gorm.DB, from, toExclusive time.Time, branchID uint, userID uint, restrictUser bool) *gorm.DB {
	q := salescope.EmittedSalesNoNotes(tdb.Model(&database.TenantSale{})).
		Where("issue_date >= ? AND issue_date < ?", from, toExclusive).
		Where("status != ?", "cancelled")
	q = branchScope(branchID)(q)
	q = userScope(restrictUser, userID)(q)
	return q
}

// branchScope/userScope siempre filtran sobre `tenant_sales` (toda esta base la usa vía
// tdb.Model(&database.TenantSale{})) — se califica el nombre de columna explícitamente porque
// byDocType y recentSales hacen self-join de tenant_sales (alias `fe`, para resolver el
// comprobante electrónico emitido desde una nota de venta), y ese segundo `tenant_sales` también
// tiene branch_id/user_id: sin calificar, MySQL devuelve "Column 'user_id' in where clause is
// ambiguous" (Error 1052) y la consulta entera falla, en vez de solo filtrar mal.
func branchScope(branchID uint) func(*gorm.DB) *gorm.DB {
	return func(db *gorm.DB) *gorm.DB {
		if branchID > 0 {
			return db.Where("tenant_sales.branch_id = ?", branchID)
		}
		return db
	}
}

func userScope(restrict bool, userID uint) func(*gorm.DB) *gorm.DB {
	return func(db *gorm.DB) *gorm.DB {
		if restrict && userID != 0 {
			return db.Where("tenant_sales.user_id = ?", userID)
		}
		return db
	}
}

// scopeUsers: lista del selector de usuarios, solo para el administrador.
func scopeUsers(us dashUser, tdb *gorm.DB) []dashboardUserOption {
	if !us.IsAdmin {
		return []dashboardUserOption{}
	}
	return listDashboardUsers(tdb)
}
