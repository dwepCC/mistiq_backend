package handler

import (
	"time"

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

	now := time.Now()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	todayEnd := todayStart.AddDate(0, 0, 1)
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.Local)
	monthEnd := monthStart.AddDate(0, 1, 0)

	userID, _ := c.Locals("user_id").(uint)
	userRole, _ := c.Locals("user_role").(string)
	isAdmin := userRole == "Administrador"

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
	salescope.CommercialSales(tdb.Model(&database.TenantSale{})).Where(salesCond, salesArgs...).Where("issue_date >= ? AND issue_date < ?", todayStart, todayEnd).Select("COALESCE(SUM(total), 0)").Scan(&salesTodayTotal)
	salescope.CommercialSales(tdb.Model(&database.TenantSale{})).Where(salesCond, salesArgs...).Where("issue_date >= ? AND issue_date < ?", monthStart, monthEnd).Select("COALESCE(SUM(total), 0)").Scan(&salesMonthTotal)
	tdb.Model(&database.TenantPurchase{}).Where(purchasesCond, purchasesArgs...).Where("issue_date >= ? AND issue_date < ?", todayStart, todayEnd).Select("COALESCE(SUM(total), 0)").Scan(&purchasesTodayTotal)
	tdb.Model(&database.TenantPurchase{}).Where(purchasesCond, purchasesArgs...).Where("issue_date >= ? AND issue_date < ?", monthStart, monthEnd).Select("COALESCE(SUM(total), 0)").Scan(&purchasesMonthTotal)

	homeKPIs := fiber.Map{
		"sales_today":      salesTodayTotal,
		"sales_month":      salesMonthTotal,
		"purchases_today":  purchasesTodayTotal,
		"purchases_month":  purchasesMonthTotal,
	}

	// Totales generales (dashboard completo, sin filtro usuario para no romper otras vistas)
	var contactsCount, productsCount, salesCount, purchasesCount int64
	tdb.Model(&database.TenantContact{}).Where("active = ?", true).Count(&contactsCount)
	tdb.Model(&database.TenantProduct{}).Where("active = ?", true).Count(&productsCount)
	salescope.CommercialSales(tdb.Model(&database.TenantSale{})).Where("status != ?", "cancelled").Count(&salesCount)
	tdb.Model(&database.TenantPurchase{}).Count(&purchasesCount)

	var monthSalesTotal, monthPurchasesTotal float64
	var monthSalesCount int64
	salescope.CommercialSales(tdb.Model(&database.TenantSale{})).
		Where("issue_date >= ? AND issue_date < ? AND status != ?", monthStart, monthEnd, "cancelled").
		Count(&monthSalesCount)
	salescope.CommercialSales(tdb.Model(&database.TenantSale{})).
		Where("issue_date >= ? AND issue_date < ? AND status != ?", monthStart, monthEnd, "cancelled").
		Select("COALESCE(SUM(total), 0)").Scan(&monthSalesTotal)
	tdb.Model(&database.TenantPurchase{}).
		Where("issue_date >= ? AND issue_date < ?", monthStart, monthEnd).
		Select("COALESCE(SUM(total), 0)").Scan(&monthPurchasesTotal)

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
		salescope.CommercialSales(tdb.Model(&database.TenantSale{})).
			Where("issue_date >= ? AND issue_date < ? AND status != ?", s, e, "cancelled").
			Select("COALESCE(SUM(total), 0)").Scan(&sum)
		monthly[i-1] = MonthAmount{Month: i, Year: now.Year(), Amount: sum}
	}

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
		Limit(10).Scan(&lowStock)

	var openCashSessions, pendingBilling int64
	tdb.Model(&database.TenantCashSession{}).Where("status = ?", "open").Count(&openCashSessions)
	tdb.Model(&database.TenantSale{}).
		Where("billing_status = ? AND doc_type IN (?, ?)", "pending", "FACTURA", "BOLETA").
		Count(&pendingBilling)

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
	})
}
