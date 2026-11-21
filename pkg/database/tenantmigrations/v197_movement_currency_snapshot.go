package tenantmigrations

import (
	"strings"
	"time"

	"tukifac/pkg/branchscope"
	"tukifac/pkg/dailyfx"
	"tukifac/pkg/datespe"

	"gorm.io/gorm"
)

// V197MovementCurrencySnapshot agrega currency / exchange_rate / amount_pen a los movimientos de caja y de banco y rellena los
// existentes a partir de su venta o compra de origen:
//   - sin documento, o con documento en soles: moneda PEN y amount_pen = amount (una sola sentencia por tabla);
//   - con documento en USD: TC del documento (o, si no lo guardó, el TC del día de emisión) y amount_pen = amount × TC; si no hay
//     ningún TC queda NULL (los lectores derivan el valor como antes).
//
// Idempotente: solo toca filas con amount_pen NULL. No cambia amount ni ningún otro dato.
type V197MovementCurrencySnapshot struct{}

func (V197MovementCurrencySnapshot) Version() int { return 197 }
func (V197MovementCurrencySnapshot) Name() string { return "movement_currency_snapshot" }

func (V197MovementCurrencySnapshot) Up(db *gorm.DB) error {
	if err := db.AutoMigrate(&v197CashMovement{}, &v197BankMovement{}); err != nil {
		return err
	}
	sys := branchscope.System(db, "V197 backfill de moneda de movimientos")
	for _, table := range []string{"tenant_cash_movements", "tenant_bank_movements"} {
		// 1) Soles: sin documento o con documento en PEN/sin moneda. Por tramos de id: un único UPDATE sobre toda la tabla tomaría
		// bloqueos de fila/hueco sobre millones de filas y detendría los cobros concurrentes del tenant.
		var maxID uint64
		if err := sys.Table(table).Select("COALESCE(MAX(id), 0)").Scan(&maxID).Error; err != nil {
			return err
		}
		const chunk = 5000
		for lo := uint64(1); lo <= maxID; lo += chunk {
			if err := sys.Exec(`UPDATE `+table+` SET currency = 'PEN', amount_pen = amount
				WHERE id BETWEEN ? AND ? AND amount_pen IS NULL
				  AND (sale_id IS NULL OR sale_id = 0 OR sale_id IN (SELECT id FROM tenant_sales WHERE currency IS NULL OR TRIM(currency) = '' OR UPPER(TRIM(currency)) <> 'USD'))
				  AND (purchase_id IS NULL OR purchase_id = 0 OR purchase_id IN (SELECT id FROM tenant_purchases WHERE currency IS NULL OR TRIM(currency) = '' OR UPPER(TRIM(currency)) <> 'USD'))`, lo, lo+chunk-1).Error; err != nil {
				return err
			}
		}
		// 2) Dólares: pocas filas; una por una con el TC del documento o el del día.
		if err := backfillUSDMovements(sys, table); err != nil {
			return err
		}
	}
	return nil
}

type usdMovRow struct {
	ID         uint
	Amount     float64
	SaleID     *uint
	PurchaseID *uint
}

func backfillUSDMovements(sys *gorm.DB, table string) error {
	var rows []usdMovRow
	if err := sys.Table(table).Select("id, amount, sale_id, purchase_id").Where("amount_pen IS NULL").Find(&rows).Error; err != nil {
		return err
	}
	for _, r := range rows {
		var doc struct {
			Currency     string
			ExchangeRate *float64
			IssueDate    time.Time
		}
		var err error
		if r.SaleID != nil && *r.SaleID > 0 {
			err = sys.Table("tenant_sales").Select("currency, exchange_rate, issue_date").Where("id = ?", *r.SaleID).Take(&doc).Error
		} else if r.PurchaseID != nil && *r.PurchaseID > 0 {
			err = sys.Table("tenant_purchases").Select("currency, exchange_rate, issue_date").Where("id = ?", *r.PurchaseID).Take(&doc).Error
		} else {
			continue
		}
		if err != nil || strings.ToUpper(strings.TrimSpace(doc.Currency)) != "USD" {
			continue
		}
		rate := doc.ExchangeRate
		if rate == nil || *rate <= 0 {
			day := doc.IssueDate.In(datespe.Location()).Format("2006-01-02")
			if v, _, ok := dailyfx.SaleRate(day); ok && v > 0 {
				rate = &v
			} else {
				rate = nil
			}
		}
		upd := map[string]interface{}{"currency": "USD"}
		if rate != nil {
			upd["exchange_rate"] = *rate
			upd["amount_pen"] = float64(int64(r.Amount**rate*100+0.5)) / 100
		}
		if err := sys.Table(table).Where("id = ?", r.ID).Updates(upd).Error; err != nil {
			return err
		}
	}
	return nil
}
