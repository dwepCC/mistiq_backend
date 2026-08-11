package handler

import (
	"time"

	"tukifac/config"
	"tukifac/pkg/database"
	"tukifac/pkg/salescope"

	"github.com/gofiber/fiber/v3"
	"gorm.io/gorm"
)

type DashboardHandler struct{}

func NewDashboardHandler() *DashboardHandler { return &DashboardHandler{} }

func db(c fiber.Ctx) *gorm.DB {
	v, _ := c.Locals("tenantDB").(*gorm.DB)
	return v
}

func (h *DashboardHandler) Home(c fiber.Ctx) error {
	tdb := db(c)
	userEmail, _ := c.Locals("user_email").(string)
	tenant, _ := c.Locals("tenant").(*database.Tenant)

	tenantName := ""
	if tenant != nil {
		tenantName = tenant.Name
	}

	now := time.Now()
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.Local)
	monthEnd := monthStart.AddDate(0, 1, 0)

	// Contadores generales
	var contactsCount, productsCount int64
	var salesCount, purchasesCount int64
	tdb.Model(&database.TenantContact{}).Where("active = ?", true).Count(&contactsCount)
	tdb.Model(&database.TenantProduct{}).Where("active = ?", true).Count(&productsCount)
	salescope.CommercialSales(tdb.Model(&database.TenantSale{})).Where("status != ?", "cancelled").Count(&salesCount)
	tdb.Model(&database.TenantPurchase{}).Count(&purchasesCount)

	// Ventas del mes
	var monthSalesTotal float64
	var monthSalesCount int64
	salescope.CommercialSales(tdb.Model(&database.TenantSale{})).
		Where("issue_date >= ? AND issue_date < ? AND status != ?", monthStart, monthEnd, "cancelled").
		Count(&monthSalesCount)
	salescope.CommercialSales(tdb.Model(&database.TenantSale{})).
		Where("issue_date >= ? AND issue_date < ? AND status != ?", monthStart, monthEnd, "cancelled").
		Select("COALESCE(SUM(total), 0)").Scan(&monthSalesTotal)

	// Compras del mes
	var monthPurchasesTotal float64
	tdb.Model(&database.TenantPurchase{}).
		Where("issue_date >= ? AND issue_date < ?", monthStart, monthEnd).
		Select("COALESCE(SUM(total), 0)").Scan(&monthPurchasesTotal)

	// Ventas por mes (últimos 6 meses)
	type MonthStat struct {
		Label  string
		Amount float64
		Height int
	}
	labels := []string{"E", "F", "M", "A", "M", "J", "J", "A", "S", "O", "N", "D"}
	monthly := make([]MonthStat, 12)
	var maxAmount float64

	for i := 1; i <= 12; i++ {
		start := time.Date(now.Year(), time.Month(i), 1, 0, 0, 0, 0, time.Local)
		end := start.AddDate(0, 1, 0)
		var sum float64
		salescope.CommercialSales(tdb.Model(&database.TenantSale{})).
			Where("issue_date >= ? AND issue_date < ? AND status != ?", start, end, "cancelled").
			Select("COALESCE(SUM(total), 0)").Scan(&sum)
		monthly[i-1] = MonthStat{Label: labels[i-1], Amount: sum}
		if sum > maxAmount {
			maxAmount = sum
		}
	}

	for i := range monthly {
		if maxAmount > 0 {
			ratio := monthly[i].Amount / maxAmount
			h := int(20 + ratio*80)
			if h < 10 {
				h = 10
			}
			monthly[i].Height = h
		} else {
			monthly[i].Height = 10
		}
	}

	// Productos con stock bajo
	var lowStock []struct {
		ProductID   uint
		ProductName string
		Quantity    float64
		MinStock    float64
	}
	tdb.Table("tenant_product_stocks ps").
		Select("ps.product_id, p.name as product_name, ps.quantity, p.min_stock").
		Joins("JOIN tenant_products p ON p.id = ps.product_id").
		Where("p.manage_stock = ? AND p.active = ? AND ps.quantity <= p.min_stock", true, true).
		Limit(5).Scan(&lowStock)

	// Últimas ventas
	var recentSales []database.TenantSale
	tdb.Order("created_at DESC").Limit(5).Find(&recentSales)

	// Sesiones de caja abiertas
	var openCashSessions int64
	tdb.Model(&database.TenantCashSession{}).Where("status = ?", "open").Count(&openCashSessions)

	// Facturas pendientes de envío SUNAT
	var pendingBilling int64
	tdb.Model(&database.TenantSale{}).
		Where("billing_status = ? AND doc_type IN (?, ?)", "pending", "FACTURA", "BOLETA").
		Count(&pendingBilling)

	return c.Render("dashboard/home", fiber.Map{
		"Title":               "Dashboard",
		"UserEmail":           userEmail,
		"TenantName":          tenantName,
		"IsDev":               config.AppConfig.IsDev(),
		"ContactsCount":       contactsCount,
		"ProductsCount":       productsCount,
		"SalesCount":          salesCount,
		"PurchasesCount":      purchasesCount,
		"MonthSalesCount":     monthSalesCount,
		"MonthSalesTotal":     monthSalesTotal,
		"MonthPurchasesTotal": monthPurchasesTotal,
		"MonthlyStats":        monthly,
		"LowStockProducts":    lowStock,
		"RecentSales":         recentSales,
		"OpenCashSessions":    openCashSessions,
		"PendingBilling":      pendingBilling,
		"CurrentMonth":        now.Format("January 2006"),
		"CurrentYear":         now.Year(),
	}, "layouts/base")
}
