package handler

import (
	"math"
	"strconv"
	"time"

	"github.com/gofiber/fiber/v3"
	"gorm.io/gorm"

	"tukifac/pkg/branch"
	"tukifac/pkg/database"
)

// dashUser describe sobre QUÉ usuario(s) se calculan las cifras del dashboard y de Inicio.
//
//   - Administrador: ve todos los usuarios por defecto y puede elegir uno (?user_id=).
//   - Cualquier otro rol: SIEMPRE solo lo suyo, aunque mande ?user_id= de otro (se ignora). Antes
//     Inicio filtraba por usuario a quien no era Administrador pero el Dashboard mostraba todo, así
//     que las dos pantallas no coincidían.
type dashUser struct {
	IsAdmin  bool
	UserID   uint // usuario al que se restringe (si Restrict)
	Restrict bool
	SelfID   uint
}

func resolveDashboardUser(c fiber.Ctx) dashUser {
	self, _ := c.Locals("user_id").(uint)
	if !branch.IsBranchAdmin(c) {
		uid := self
		if uid == 0 {
			// Sin identidad no se puede acotar: el valor imposible hace que no devuelva filas en vez
			// de mostrar la empresa entera.
			uid = math.MaxUint32
		}
		return dashUser{IsAdmin: false, UserID: uid, Restrict: true, SelfID: self}
	}
	if v, err := strconv.ParseUint(c.Query("user_id"), 10, 32); err == nil && v > 0 {
		return dashUser{IsAdmin: true, UserID: uint(v), Restrict: true, SelfID: self}
	}
	return dashUser{IsAdmin: true, SelfID: self}
}

// dashboardUserOption fila del selector "Usuario" (solo se envía al administrador).
type dashboardUserOption struct {
	ID   uint   `json:"id"`
	Name string `json:"name"`
}

func listDashboardUsers(tdb *gorm.DB) []dashboardUserOption {
	out := make([]dashboardUserOption, 0)
	tdb.Model(&database.TenantUser{}).Where("active = ?", true).Select("id, name").Order("name ASC").Scan(&out)
	return out
}

// compareWindow ventana contra la que se compara el período elegido.
type compareWindow struct {
	From        time.Time
	ToExclusive time.Time
	// Mode: "month_to_date" (mes completo elegido → mismos días del mes anterior) o
	// "previous_period" (rango cualquiera → ventana del mismo largo justo antes).
	Mode  string
	Label string
}

// comparisonWindow evita comparar un mes EN CURSO contra un mes completo (a inicios de mes salía
// una caída falsa): si el rango es un mes calendario completo, se compara con los MISMOS DÍAS
// transcurridos del mes anterior. Cualquier otro rango se compara con la ventana inmediata anterior
// de igual duración.
func comparisonWindow(from, toExclusive, now time.Time) compareWindow {
	loc := from.Location()
	isFullMonth := from.Day() == 1 && toExclusive.Equal(from.AddDate(0, 1, 0))
	if !isFullMonth {
		d := toExclusive.Sub(from)
		return compareWindow{From: from.Add(-d), ToExclusive: from, Mode: "previous_period", Label: "período anterior de igual duración"}
	}
	prevStart := from.AddDate(0, -1, 0)
	tomorrow := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, 1)
	effectiveEnd := toExclusive
	if tomorrow.After(from) && tomorrow.Before(effectiveEnd) {
		effectiveEnd = tomorrow
	}
	days := int(math.Round(effectiveEnd.Sub(from).Hours() / 24))
	if days < 1 {
		days = 1
	}
	prevEnd := prevStart.AddDate(0, 0, days)
	if prevEnd.After(from) {
		prevEnd = from
	}
	fullDays := int(math.Round(from.Sub(prevStart).Hours() / 24))
	label := "mes anterior"
	if days < fullDays {
		label = "mismos " + strconv.Itoa(days) + " días del mes anterior"
	}
	return compareWindow{From: prevStart, ToExclusive: prevEnd, Mode: "month_to_date", Label: label}
}

type dailyRow struct {
	Day   string  `json:"day"`
	Sales float64 `json:"sales"`
	Docs  int64   `json:"documents"`
}

// fillDailySeries completa con ceros los días sin ventas hasta hoy (o el fin del rango): sin eso el
// gráfico dibujaba una curva entre los pocos días con ventas y el eje saltaba días sin avisar.
func fillDailySeries(rows []dailyRow, from, toExclusive, now time.Time) []dailyRow {
	byDay := make(map[string]dailyRow, len(rows))
	for _, r := range rows {
		d := r.Day
		if len(d) >= 10 {
			d = d[:10]
		}
		cur := byDay[d]
		cur.Day = d
		cur.Sales += r.Sales
		cur.Docs += r.Docs
		byDay[d] = cur
	}
	loc := from.Location()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	end := toExclusive
	if t := today.AddDate(0, 0, 1); t.Before(end) {
		end = t
	}
	out := make([]dailyRow, 0, len(byDay))
	for d := from; d.Before(end); d = d.AddDate(0, 0, 1) {
		key := d.Format("2006-01-02")
		if r, ok := byDay[key]; ok {
			out = append(out, r)
		} else {
			out = append(out, dailyRow{Day: key})
		}
		delete(byDay, key)
	}
	// Ventas con fecha fuera de la ventana calculada (p. ej. futuras): se conservan.
	for _, r := range byDay {
		out = append(out, r)
	}
	return out
}

// Códigos SUNAT de comprobantes electrónicos que el tenant ve en la campanita del encabezado
// (billing_service.GetNotificationCounts): mismos que usa el dashboard para "pendientes/errores".
var electronicBillingCodes = []string{"01", "03", "07", "08", "09", "31"}

// countElectronicBilling cuenta comprobantes electrónicos en un estado de envío SIN límite de fecha
// (lo que de verdad está pendiente hoy), con la misma definición que la campanita. Las anuladas no
// se envían a SUNAT, así que no cuentan como pendientes.
func countElectronicBilling(tdb *gorm.DB, status string, branchID, userID uint, restrict bool) int64 {
	var n int64
	q := tdb.Model(&database.TenantSale{}).
		Joins("JOIN tenant_document_series ON tenant_document_series.id = tenant_sales.series_id").
		Where("tenant_document_series.sunat_code IN ?", electronicBillingCodes).
		Where("tenant_sales.billing_status = ?", status).
		Where("tenant_sales.status != ?", "cancelled")
	q = branchScope(branchID)(q)
	q = userScope(restrict, userID)(q)
	q.Count(&n)
	return n
}

type lowStockRow struct {
	ProductID   uint    `json:"product_id"`
	ProductName string  `json:"product_name"`
	Quantity    float64 `json:"quantity"`
	MinStock    float64 `json:"min_stock"`
	// Status: "out" = agotado (cantidad ≤ 0); "low" = por debajo del mínimo definido (> 0).
	Status string `json:"status"`
}

// queryLowStock: un producto SIN mínimo definido (min_stock = 0) ya no aparece como "stock bajo"
// con "0 / min 0": solo se muestra si está agotado, y se rotula como tal.
func queryLowStock(tdb *gorm.DB, branchID uint, limit int) []lowStockRow {
	rows := make([]lowStockRow, 0)
	q := tdb.Table("tenant_product_stocks ps").
		Select("ps.product_id, p.name AS product_name, ps.quantity, p.min_stock, CASE WHEN ps.quantity <= 0 THEN 'out' ELSE 'low' END AS status").
		Joins("JOIN tenant_products p ON p.id = ps.product_id").
		Where("p.manage_stock = ? AND p.active = ?", true, true).
		Where("(ps.quantity <= 0 OR (p.min_stock > 0 AND ps.quantity <= p.min_stock))")
	if branchID > 0 {
		q = q.Where("ps.branch_id = ?", branchID)
	}
	q.Order("ps.quantity ASC").Limit(limit).Scan(&rows)
	return rows
}

// purchaseBranchScope filtra compras por sucursal (0 = sin filtro; "todas" solo para quien ve todo).
func purchaseBranchScope(branchID uint) func(*gorm.DB) *gorm.DB {
	return func(db *gorm.DB) *gorm.DB {
		if branchID > 0 {
			return db.Where("branch_id = ?", branchID)
		}
		return db
	}
}
