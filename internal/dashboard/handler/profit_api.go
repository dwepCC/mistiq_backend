package handler

import (
	"strconv"
	"time"

	"github.com/gofiber/fiber/v3"
	"gorm.io/gorm"

	"tukifac/pkg/branch"
	"tukifac/pkg/money"

	salessvc "tukifac/internal/sales/service"
)

// profitResult cifras del gráfico "Utilidades / Ganancias" del dashboard.
type profitResult struct {
	// Income: ventas del período (igual que la tarjeta "Ventas del período"); con filtro de producto,
	// lo vendido de ese producto.
	Income float64 `json:"income"`
	// Cost: costo de compra de lo vendido (precio de compra × cantidad de cada línea), con la misma
	// base que el reporte Reportes → Utilidades.
	Cost float64 `json:"cost"`
	// Expenses: gastos manuales de caja del período (categoría "gasto"), no anulados ni revertidos.
	Expenses         float64 `json:"expenses"`
	ExpensesIncluded bool    `json:"expenses_included"`
	// Expense: egreso total que se resta del ingreso (costo + gastos si se pidió considerarlos).
	Expense float64 `json:"expense"`
	Profit  float64 `json:"profit"`
	// Líneas vendidas sin precio de compra registrado: la utilidad puede estar sobreestimada.
	Lines            int64 `json:"lines"`
	LinesWithoutCost int64 `json:"lines_without_cost"`
	// ProductID != 0: el cálculo es solo de ese producto (los gastos no aplican).
	ProductID uint `json:"product_id,omitempty"`
}

// GET /api/dashboard/profit?date_from=&date_to=&branch_id=&user_id=&include_expenses=1&product_id=
// Mismo alcance de sucursal y usuario que /dashboard/analytics (el administrador puede elegir
// usuario; los demás roles, solo lo suyo).
func (h *DashboardHandler) ProfitAPI(c fiber.Ctx) error {
	tdb := db(c)
	if tdb == nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "sin contexto de empresa"})
	}
	from, toExclusive, err := parseAnalyticsDateRange(c)
	if err != nil {
		return c.Status(fiber.StatusBadRequest).JSON(fiber.Map{"error": "fechas inválidas (use date_from y date_to como YYYY-MM-DD)"})
	}
	var requestedBranch uint
	if v, e := strconv.ParseUint(c.Query("branch_id"), 10, 32); e == nil {
		requestedBranch = uint(v)
	}
	branchID := branch.ResolveReportBranchFilter(c, requestedBranch, true)
	us := resolveDashboardUser(c)

	var productID uint
	if v, e := strconv.ParseUint(c.Query("product_id"), 10, 32); e == nil {
		productID = uint(v)
	}
	includeExpenses, _ := strconv.ParseBool(c.Query("include_expenses"))

	res, err := computeProfit(tdb, from, toExclusive, branchID, us, productID, includeExpenses)
	if err != nil {
		return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{"error": "no se pudo calcular la utilidad"})
	}
	return c.JSON(res)
}

func computeProfit(tdb *gorm.DB, from, toExclusive time.Time, branchID uint, us dashUser, productID uint, includeExpenses bool) (profitResult, error) {
	dateTo := toExclusive.Add(-time.Nanosecond)
	params := salessvc.ProfitDetailParams{DateFrom: &from, DateTo: &dateTo, BranchID: branchID, ProductID: productID}
	if us.Restrict {
		params.UserID = us.UserID
	}
	_, sm, err := salessvc.NewSaleService(tdb).ProfitDetail(params)
	if err != nil {
		return profitResult{}, err
	}

	res := profitResult{
		Income:           sm.TotalSales,
		Cost:             sm.TotalCost,
		Lines:            sm.LineItems,
		LinesWithoutCost: sm.LinesWithoutCost,
		ProductID:        productID,
	}
	if productID == 0 {
		// Sin filtro de producto el ingreso es el de la tarjeta "Ventas del período" (total de las
		// ventas vigentes): así las dos cifras de la misma pantalla coinciden. Con producto, lo vendido
		// de ese producto (no hay total de venta por producto).
		var sales float64
		analyticsSaleScope(tdb, from, toExclusive, branchID, us.UserID, us.Restrict).Select(sumSalesPENSelect()).Scan(&sales)
		res.Income = sales
		res.Expenses = sumManualExpenses(tdb, from, toExclusive, branchID, us.UserID, us.Restrict)
		res.ExpensesIncluded = includeExpenses
	}

	res.Expense = res.Cost
	if res.ExpensesIncluded {
		res.Expense += res.Expenses
	}
	res.Income = money.RoundDisplay(res.Income)
	res.Cost = money.RoundDisplay(res.Cost)
	res.Expenses = money.RoundDisplay(res.Expenses)
	res.Expense = money.RoundDisplay(res.Expense)
	res.Profit = money.RoundDisplay(res.Income - res.Expense)
	return res, nil
}

// sumManualExpenses suma los gastos manuales de caja del período: egresos con categoría "gasto" que no
// vienen de una venta ni de una compra y que no fueron revertidos (ni son la reversión de otro).
func sumManualExpenses(tdb *gorm.DB, from, toExclusive time.Time, branchID uint, userID uint, restrict bool) float64 {
	var total float64
	q := tdb.Table("tenant_cash_movements m").
		Joins("JOIN tenant_cash_sessions cs ON cs.id = m.cash_session_id").
		Where("m.type = ? AND LOWER(m.category) = ?", "expense", "gasto").
		Where("m.sale_id IS NULL AND m.purchase_id IS NULL AND m.reversal_of_id IS NULL").
		Where("NOT EXISTS (SELECT 1 FROM tenant_cash_movements r WHERE r.reversal_of_id = m.id)").
		Where("m.created_at >= ? AND m.created_at < ?", from, toExclusive)
	if branchID > 0 {
		q = q.Where("cs.branch_id = ?", branchID)
	}
	if restrict && userID != 0 {
		q = q.Where("cs.user_id = ?", userID)
	}
	q.Select("COALESCE(SUM(m.amount), 0)").Scan(&total)
	return total
}
