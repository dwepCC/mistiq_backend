package handler

import (
	"tukifac/pkg/salecurrency"
	"strconv"
	"time"

	"tukifac/pkg/branch"
	"tukifac/pkg/database"
	"tukifac/pkg/salescope"

	"github.com/gofiber/fiber/v3"
)

// GET /api/dashboard/stats
func (h *DashboardHandler) StatsAPI(c fiber.Ctx) error {
	tdb := db(c)
	if tdb == nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "sin contexto de empresa"})
	}

	// Sucursal: quien ve todas y no elige ninguna (branch_id vacío) ve la empresa entera; el resto, solo la suya.
	// Antes los KPIs de Inicio sumaban SIEMPRE todas las sucursales, para cualquier usuario.
	var requestedBranch uint
	if v, e := strconv.ParseUint(c.Query("branch_id"), 10, 32); e == nil {
		requestedBranch = uint(v)
	}
	branchID := branch.ResolveReportBranchFilter(c, requestedBranch, true)

	now := time.Now()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	todayEnd := todayStart.AddDate(0, 0, 1)
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.Local)
	monthEnd := monthStart.AddDate(0, 1, 0)

	// Mismo criterio que el Dashboard: el administrador ve todo; los demás roles, solo lo suyo.
	us := resolveDashboardUser(c)
	userID := us.UserID
	isAdmin := !us.Restrict

	// KPIs para la página de inicio: ventas hoy, ventas del mes, compras hoy, compras del mes (filtro por usuario si no es Administrador)
	var salesTodayTotal, salesMonthTotal, purchasesTodayTotal, purchasesMonthTotal float64
	salesCond := "status != ?"
	salesArgs := []interface{}{"cancelled"}
	purchasesCond := "1 = 1"
	purchasesArgs := []interface{}{}
	if !isAdmin && userID != 0 {
		salesCond += " AND user_id = ?"
		salesArgs = append(salesArgs, userID)
		purchasesCond = "user_id = ?"
		purchasesArgs = append(purchasesArgs, userID)
	}
salescope.EmittedSalesNoNotes(tdb.Model(&database.TenantSale{})).Scopes(branchScope(branchID)).Where(salesCond, salesArgs...).Where("issue_date >= ? AND issue_date < ?", todayStart, todayEnd).Select(sumSalesPENSelect()).Scan(&salesTodayTotal)
	salescope.EmittedSalesNoNotes(tdb.Model(&database.TenantSale{})).Scopes(branchScope(branchID)).Where(salesCond, salesArgs...).Where("issue_date >= ? AND issue_date < ?", monthStart, monthEnd).Select(sumSalesPENSelect()).Scan(&salesMonthTotal)
	tdb.Model(&database.TenantPurchase{}).Scopes(purchaseBranchScope(branchID)).Where(purchasesCond, purchasesArgs...).Where("status != ?", "cancelled").Where("issue_date >= ? AND issue_date < ?", todayStart, todayEnd).Select("COALESCE(SUM(" + salecurrency.TotalPENSQL("", "total") + "), 0)").Scan(&purchasesTodayTotal)
	tdb.Model(&database.TenantPurchase{}).Scopes(purchaseBranchScope(branchID)).Where(purchasesCond, purchasesArgs...).Where("status != ?", "cancelled").Where("issue_date >= ? AND issue_date < ?", monthStart, monthEnd).Select("COALESCE(SUM(" + salecurrency.TotalPENSQL("", "total") + "), 0)").Scan(&purchasesMonthTotal)

	// Ventas del mes en curso vs los MISMOS días del mes anterior (no contra el mes anterior completo,
	// que a inicios de mes mostraba una caída falsa).
	monthCmpHome := comparisonWindow(monthStart, monthEnd, now)
	var salesPrevSameDays float64
	salescope.EmittedSalesNoNotes(tdb.Model(&database.TenantSale{})).Scopes(branchScope(branchID)).Where(salesCond, salesArgs...).Where("issue_date >= ? AND issue_date < ?", monthCmpHome.From, monthCmpHome.ToExclusive).Select(sumSalesPENSelect()).Scan(&salesPrevSameDays)

	homeKPIs := fiber.Map{
		"sales_prev_month_same_days": salesPrevSameDays,
		"month_compare_label":        monthCmpHome.Label,
		"sales_today":      salesTodayTotal,
		"sales_month":      salesMonthTotal,
		"purchases_today":  purchasesTodayTotal,
		"purchases_month":  purchasesMonthTotal,
	}

	// Totales generales (dashboard completo, sin filtro usuario para no romper otras vistas)
	var contactsCount, productsCount, salesCount, purchasesCount int64
	tdb.Model(&database.TenantContact{}).Where("active = ?", true).Count(&contactsCount)
	tdb.Model(&database.TenantProduct{}).Where("active = ?", true).Count(&productsCount)
	salescope.EmittedSalesNoNotes(tdb.Model(&database.TenantSale{})).Scopes(branchScope(branchID)).Where("status != ?", "cancelled").Count(&salesCount)
	tdb.Model(&database.TenantPurchase{}).Scopes(purchaseBranchScope(branchID)).Where("status != ?", "cancelled").Count(&purchasesCount)

	var monthSalesTotal, monthPurchasesTotal float64
	var monthSalesCount int64
	salescope.EmittedSalesNoNotes(tdb.Model(&database.TenantSale{})).Scopes(branchScope(branchID)).
		Where("issue_date >= ? AND issue_date < ? AND status != ?", monthStart, monthEnd, "cancelled").
		Count(&monthSalesCount)
	salescope.EmittedSalesNoNotes(tdb.Model(&database.TenantSale{})).Scopes(branchScope(branchID)).
		Where("issue_date >= ? AND issue_date < ? AND status != ?", monthStart, monthEnd, "cancelled").
		Select(sumSalesPENSelect()).Scan(&monthSalesTotal)
	tdb.Model(&database.TenantPurchase{}).Scopes(purchaseBranchScope(branchID)).
		Where("status != ? AND issue_date >= ? AND issue_date < ?", "cancelled", monthStart, monthEnd).
		Select("COALESCE(SUM(" + salecurrency.TotalPENSQL("", "total") + "), 0)").Scan(&monthPurchasesTotal)

	type MonthAmount struct {
		Month  int     `json:"month"`
		Year   int     `json:"year"`
		Amount float64 `json:"amount"`
	}
	monthly := make([]MonthAmount, 12)
	for i := 1; i <= 12; i++ {
		s := time.Date(now.Year(), time.Month(i), 1, 0, 0, 0, 0, time.Local)
		e := s.AddDate(0, 1, 0)
		var sum float64
		salescope.EmittedSalesNoNotes(tdb.Model(&database.TenantSale{})).Scopes(branchScope(branchID)).
			Where("issue_date >= ? AND issue_date < ? AND status != ?", s, e, "cancelled").
			Select(sumSalesPENSelect()).Scan(&sum)
		monthly[i-1] = MonthAmount{Month: i, Year: now.Year(), Amount: sum}
	}

	lowStock := queryLowStock(tdb, branchID, 10)

	var openCashSessions int64
	openCashQ := tdb.Model(&database.TenantCashSession{}).Where("status = ?", "open")
	if branchID > 0 {
		openCashQ = openCashQ.Where("branch_id = ?", branchID)
	}
	if us.Restrict && userID != 0 {
		openCashQ = openCashQ.Where("user_id = ?", userID)
	}
	openCashQ.Count(&openCashSessions)
	// Pendientes de envío SUNAT: misma definición que la campanita del encabezado y que el
	// Dashboard (por serie electrónica, sin límite de fecha), acotada al usuario si no es admin.
	pendingBilling := countElectronicBilling(tdb, "pending", branchID, userID, us.Restrict)

	return c.JSON(fiber.Map{
		"home": homeKPIs,
		"totals": fiber.Map{
			"contacts":  contactsCount,
			"products":  productsCount,
			"sales":     salesCount,
			"purchases": purchasesCount,
		},
		"current_month": fiber.Map{
			"sales_count":     monthSalesCount,
			"sales_total":     monthSalesTotal,
			"purchases_total": monthPurchasesTotal,
			"month":           int(now.Month()),
			"year":            now.Year(),
		},
		"monthly_sales":       monthly,
		"low_stock_products":  lowStock,
		"open_cash_sessions":  openCashSessions,
		"pending_billing":     pendingBilling,
		"scope":               fiber.Map{"is_admin": us.IsAdmin, "restricted": us.Restrict},
	})
}
